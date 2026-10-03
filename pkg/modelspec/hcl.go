package modelspec

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ParseHCL reads a ModelSpec HCL source. file is used in findings and, without
// its .modelspec.hcl suffix, as the module short name. The source is parsed with
// the real HCL parser; a syntax error yields a Broken model.
func ParseHCL(file string, src []byte) (*Model, []Finding) {
	m := &Model{File: file, Form: FormHCL, Name: moduleNameFromFile(file), Group: file}
	if f, bad := precheck(file, FormHCL, src); bad {
		m.Broken = true
		return m, []Finding{f}
	}
	p := &hclReader{m: m}
	parsed, diags := hclsyntax.ParseConfig(src, file, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		m.Broken = true
		for _, d := range diags {
			if d.Severity == hcl.DiagError {
				p.add(diagLine(d), RuleSyntax, d.Summary)
			}
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
		n, msg := literalNode(a.Expr, line)
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
// functions). The expression is checked structurally first: HCL would happily
// fold `1 > 0` to true, but that is an expression, not a literal.
func literalNode(expr hclsyntax.Expression, line int) (*Node, string) {
	if _, isObject := expr.(*hclsyntax.ObjectConsExpr); isObject {
		return nil, "map-style values are not ModelSpec v0 syntax; declare members with named blocks (decision 0007)"
	}
	if !isLiteralExpr(expr) {
		return nil, "must be a literal string, number, boolean or list; expressions, references and functions are not ModelSpec v0 (decision 0009)"
	}
	// A literal expression always evaluates without a context.
	val, _ := expr.Value(nil)
	return ctyNode(val, line)
}

// isLiteralExpr reports whether the expression is written as a literal: a
// constant, a string without interpolation, a negative number, or a list of
// those.
func isLiteralExpr(expr hclsyntax.Expression) bool {
	switch x := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.TemplateExpr:
		for _, part := range x.Parts {
			if _, ok := part.(*hclsyntax.LiteralValueExpr); !ok {
				return false
			}
		}
		return true
	case *hclsyntax.UnaryOpExpr:
		_, ok := x.Val.(*hclsyntax.LiteralValueExpr)
		return ok && x.Op == hclsyntax.OpNegate
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

// ctyNode converts the value of a literal expression. Only literal expressions
// reach it, so a value that is not null, a string, a boolean or a number is a
// list.
func ctyNode(val cty.Value, line int) (*Node, string) {
	t := val.Type()
	switch {
	case val.IsNull():
		return nil, "null is not a ModelSpec value"
	case t == cty.String:
		return &Node{Type: NodeString, Str: val.AsString(), Line: line}, ""
	case t == cty.Bool:
		return &Node{Type: NodeBool, Bool: val.True(), Line: line}, ""
	case t == cty.Number:
		return &Node{Type: NodeNumber, Str: val.AsBigFloat().Text('f', -1), Line: line}, ""
	default:
		n := &Node{Type: NodeArray, Line: line}
		for it := val.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			item, msg := ctyNode(ev, line)
			if msg != "" {
				return nil, msg
			}
			if item.Type == NodeArray {
				return nil, "nested lists are not ModelSpec v0"
			}
			n.Items = append(n.Items, item)
		}
		return n, ""
	}
}
