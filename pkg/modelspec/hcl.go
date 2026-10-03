package modelspec

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseHCL reads a ModelSpec HCL source. file is used in findings and, without
// its .modelspec.hcl suffix, as the module short name. The source is parsed with
// the real HCL parser; a syntax error yields a Broken model.
func ParseHCL(file string, src []byte) (*Model, []Finding) {
	m := &Model{File: file, Form: FormHCL, Name: moduleNameFromFile(file), Group: file}
	if f, bad := precheck(file, src); bad {
		m.Broken = true
		return m, []Finding{f}
	}
	tokens := lexHCL(file, src)
	if found := hclLiterals(file, tokens); len(found) > 0 {
		m.Broken = true
		return m, found
	}
	input, heredocs := parserInput(src, tokens)
	p := &hclReader{m: m, heredocs: heredocs}
	parsed, diags := hclsyntax.ParseConfig(input, file, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		m.Broken = true
		shown := 0
		for _, d := range diags {
			// The syntax parser reports errors only.
			if shown++; shown > MaxSyntaxFindings {
				p.add(diagLine(d), RuleSyntax, fmt.Sprintf("more syntax errors follow; only the first %d are shown", MaxSyntaxFindings))
				break
			}
			p.add(diagLine(d), RuleSyntax, d.Summary)
		}
		SortFindings(p.findings)
		return m, p.findings
	}
	// ParseConfig always yields an *hclsyntax.Body.
	body := parsed.Body.(*hclsyntax.Body)
	for _, name := range sortedAttrNames(body) {
		a := body.Attributes[name]
		p.add(a.SrcRange.Start.Line, RuleShape, fmt.Sprintf("top-level attribute %q is not allowed; a ModelSpec file contains only blocks (decision 0009)", name))
	}
	for _, blk := range body.Blocks {
		p.topBlock(blk)
	}
	SortFindings(p.findings)
	return m, p.findings
}

func diagLine(d *hcl.Diagnostic) int {
	if d.Subject == nil {
		return 0
	}
	return d.Subject.Start.Line
}

type hclReader struct {
	m        *Model
	findings []Finding
	heredocs map[int]bool // where, in the parsed source, a heredoc begins: see parserInput
}

func (p *hclReader) add(line int, rule, msg string) {
	p.findings = append(p.findings, Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityError, Message: msg})
}

// sortedAttrNames returns attribute names in source order.
func sortedAttrNames(body *hclsyntax.Body) []string {
	names := make([]string, 0, len(body.Attributes))
	for n := range body.Attributes {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return body.Attributes[names[i]].SrcRange.Start.Byte < body.Attributes[names[j]].SrcRange.Start.Byte
	})
	return names
}

// label returns the block's single label, reporting a shape problem when it
// does not have exactly one.
func (p *hclReader) label(blk *hclsyntax.Block, line int) (string, bool) {
	if len(blk.Labels) != 1 {
		p.add(line, RuleShape, fmt.Sprintf("%s block needs exactly one name label, as in %s \"Name\" { ... }; found %d labels", blk.Type, blk.Type, len(blk.Labels)))
		return "", false
	}
	return blk.Labels[0], true
}

func (p *hclReader) topBlock(blk *hclsyntax.Block) {
	line := blk.DefRange().Start.Line
	switch blk.Type {
	case "entity", "component", "enum", "collection", "recordset":
		name, ok := p.label(blk, line)
		if !ok {
			return
		}
		p.concept(Kind(blk.Type), name, line, blk.Body)
	case "projection", "migration":
		name, ok := p.label(blk, line)
		if !ok {
			return
		}
		// Decision 0009 lists `projection` blocks and spec/migration-metadata.md
		// shows `migration` blocks, but no document defines their HCL attributes
		// or how they map to the JSON "projections" and "migrations" objects.
		p.m.Unmapped = append(p.m.Unmapped, Unmapped{What: fmt.Sprintf("%s %q", blk.Type, name), Line: line})
	default:
		p.add(line, RuleShape, fmt.Sprintf("unknown block type %q; ModelSpec declares entity, component, enum, collection, recordset, projection and migration blocks", blk.Type))
	}
}

// memberBlock is the block type a concept kind uses for its members.
var memberBlock = map[Kind]string{
	KindEntity:     "property",
	KindComponent:  "field",
	KindCollection: "field",
	KindRecordset:  "column",
}

func (p *hclReader) concept(kind Kind, name string, line int, body *hclsyntax.Body) {
	c := &Concept{Kind: kind, Name: name, Line: line}
	c.Attrs = p.attrs(body)
	for _, blk := range body.Blocks {
		bline := blk.DefRange().Start.Line
		switch {
		case blk.Type == memberBlock[kind]:
			mname, ok := p.label(blk, bline)
			if !ok {
				continue
			}
			p.checkNoBlocks(blk)
			c.Members = append(c.Members, Member{Name: mname, Line: bline, Attrs: p.attrs(blk.Body)})
		case kind == KindEntity && blk.Type == "index":
			iname, ok := p.label(blk, bline)
			if !ok {
				continue
			}
			// Core-model "Indexes" shows `index` blocks, but the JSON format
			// defines no place for them outside "projections".
			p.m.Unmapped = append(p.m.Unmapped, Unmapped{What: fmt.Sprintf("entity %q index %q", name, iname), Line: bline})
		default:
			p.add(bline, RuleShape, fmt.Sprintf("%s %q cannot contain a %q block", kind, name, blk.Type))
		}
	}
	p.m.Concepts = append(p.m.Concepts, c)
}

func (p *hclReader) checkNoBlocks(blk *hclsyntax.Block) {
	for _, inner := range blk.Body.Blocks {
		p.add(inner.DefRange().Start.Line, RuleShape, fmt.Sprintf("%s %q cannot contain a %q block", blk.Type, blk.Labels[0], inner.Type))
	}
}

// attrs reads a body's attributes as literals, in source order.
func (p *hclReader) attrs(body *hclsyntax.Body) []Attr {
	var out []Attr
	for _, name := range sortedAttrNames(body) {
		a := body.Attributes[name]
		line := a.SrcRange.Start.Line
		n, msg := p.literalNode(a.Expr, line)
		if msg != "" {
			p.add(line, RuleLiteral, fmt.Sprintf("attribute %q: %s", name, msg))
			continue
		}
		out = append(out, Attr{Name: name, Value: n, Line: line})
	}
	return out
}

// literalNode reads an expression that must be a literal (decision 0009:
// strings, numbers, booleans and lists, no expressions, references or
// functions). Only syntax a literal can contain reaches the parser (hclLiterals),
// so what is left to refuse is a bare word (a reference) and an object.
func (p *hclReader) literalNode(expr hclsyntax.Expression, line int) (*Node, string) {
	if _, isObject := expr.(*hclsyntax.ObjectConsExpr); isObject {
		return nil, "map-style values are not ModelSpec v0 syntax; declare members with named blocks (decision 0007)"
	}
	if !isLiteralExpr(expr) {
		return nil, "must be a literal string, number, boolean or list; expressions, references and functions are not ModelSpec v0 (decision 0009)"
	}
	tuple, isList := expr.(*hclsyntax.TupleConsExpr)
	if !isList {
		return p.scalarNode(expr, line)
	}
	// The items are read from the syntax tree, not through a list value of the
	// HCL library: that builds a value of the whole list first, which is slow
	// for a list of a million items.
	n := &Node{Type: NodeArray, Line: line, Items: make([]*Node, 0, len(tuple.Exprs))}
	for _, item := range tuple.Exprs {
		if _, nested := item.(*hclsyntax.TupleConsExpr); nested {
			return nil, "nested lists are not ModelSpec v0"
		}
		el, msg := p.scalarNode(item, line)
		if msg != "" {
			return nil, msg
		}
		n.Items = append(n.Items, el)
	}
	return n, ""
}

// isLiteralExpr reports whether the expression is written as a literal: a
// constant, a string, a negative number, or a list of those. Syntax that cannot
// be a literal was refused before parsing (hclLiterals), so the only expressions
// that can reach this are constants, plain strings (a template with no
// interpolation or directive), a sign before a number, lists, objects and bare
// words (references); the last two are not literals.
func isLiteralExpr(expr hclsyntax.Expression) bool {
	switch x := expr.(type) {
	case *hclsyntax.LiteralValueExpr, *hclsyntax.TemplateExpr, *hclsyntax.UnaryOpExpr:
		return true
	case *hclsyntax.TupleConsExpr:
		for _, item := range x.Exprs {
			if !isLiteralExpr(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// scalarNode converts a literal that is not a list. A sign before a number is
// the only operator that reaches it.
func (p *hclReader) scalarNode(expr hclsyntax.Expression, line int) (*Node, string) {
	if neg, ok := expr.(*hclsyntax.UnaryOpExpr); ok {
		// hclLiterals lets `-` through only before a number.
		num := neg.Val.(*hclsyntax.LiteralValueExpr).Val
		return &Node{Type: NodeNumber, Str: numberText(new(big.Float).Neg(num.AsBigFloat())), Line: line}, ""
	}
	val, _ := expr.Value(nil) // a literal evaluates without a context
	switch t := val.Type(); {
	case val.IsNull():
		return nil, "null is not a ModelSpec value"
	case t == cty.String:
		text := val.AsString()
		if p.heredocs[expr.Range().Start.Byte] {
			text = unescapeHeredoc(text)
		}
		return &Node{Type: NodeString, Str: text, Line: line}, ""
	case t == cty.Bool:
		return &Node{Type: NodeBool, Bool: val.True(), Line: line}, ""
	default:
		return &Node{Type: NodeNumber, Str: numberText(val.AsBigFloat()), Line: line}, ""
	}
}

// numberText writes a number as its shortest decimal. Integers that fit 64 bits,
// nearly all numbers in a model, take a fast path: the general conversion of a
// 512-bit value takes microseconds, which adds up in a list of a million items.
func numberText(f *big.Float) string {
	if i, accuracy := f.Int64(); accuracy == big.Exact {
		return strconv.FormatInt(i, 10)
	}
	return f.Text('f', -1)
}
