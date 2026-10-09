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
	if f, bad := precheck(file, src); bad {
		m.Broken = true
		return m, []Finding{f}
	}
	tokens := lexHCL(file, src)
	if found := hclLiterals(file, tokens); len(found) > 0 {
		m.Broken = true
		return m, found
	}
	input, heredocs, numbers, offsets := parserInput(src, tokens)
	p := &hclReader{m: m, heredocs: heredocs, numbers: numbers, offsets: offsets}
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
		return m, p.result()
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
	sort.Slice(m.Old, func(i, j int) bool { return m.Old[i].Start < m.Old[j].Start })
	return m, p.result()
}

func diagLine(d *hcl.Diagnostic) int {
	if d.Subject == nil {
		return 0
	}
	return d.Subject.Start.Line
}

type hclReader struct {
	m *Model
	findingList
	heredocs map[int]bool   // where, in the parsed source, a heredoc begins: see parserInput
	numbers  map[int]string // the text of the number that begins there
	offsets  offsetMap      // from the parsed source back to the file
}

func (p *hclReader) add(line int, rule, msg string) {
	p.put(Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityError, Message: msg})
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

// old records the old spelling from at r (a range in the parsed source), which
// modelspec rewrite replaces by repl.
func (p *hclReader) old(r hcl.Range, from, repl string) {
	p.m.Old = append(p.m.Old, OldSpelling{Line: r.Start.Line, Start: p.offsets.source(r.Start.Byte), End: p.offsets.source(r.End.Byte), Old: from, New: repl})
}

// refuse records why modelspec rewrite must not touch the file; the first reason
// stays.
func (p *hclReader) refuse(reason string) {
	if p.m.cannotRewrite == "" {
		p.m.cannotRewrite = reason
	}
}

// removed reports a block of a kind that decision 0019 removed, and reserved one
// of a word it reserved. The block is not read.
func (p *hclReader) removed(line int, blockType string) {
	p.add(line, RuleRemoved, fmt.Sprintf("%s blocks were removed (decision 0019): a stored set of rows is described by the database's own description, and the shape of a result is a record with no key", blockType))
	p.refuse(fmt.Sprintf("it holds the %s block at line %d, a construct decision 0019 removed", blockType, line))
}

func (p *hclReader) reserved(line int, blockType string) {
	p.add(line, RuleReservedWord, fmt.Sprintf("%s is a reserved word with no content yet (decision 0019); a model cannot use it", blockType))
	p.refuse(fmt.Sprintf("it holds the %s block at line %d, a word decision 0019 reserved", blockType, line))
}

func (p *hclReader) topBlock(blk *hclsyntax.Block) {
	line := blk.DefRange().Start.Line
	switch blk.Type {
	case "record", "entity", "component", "enum":
		if blk.Type == "entity" {
			p.old(blk.TypeRange, "entity", "record")
		}
		name, ok := p.label(blk, line)
		if !ok {
			// The block is not read, but a rewrite must not leave old spellings in it.
			if blk.Type != "enum" {
				p.oldInBody(blk.Body, blk.Type == "record" || blk.Type == "entity")
			}
			return
		}
		kind := Kind(blk.Type)
		if blk.Type == "entity" {
			kind = KindRecord
		}
		p.concept(kind, name, line, blk.Body)
	case "collection", "recordset":
		p.removed(line, blk.Type)
	case "projection", "migration":
		p.reserved(line, blk.Type)
	default:
		p.add(line, RuleShape, fmt.Sprintf("unknown block type %q; ModelSpec declares record, component and enum blocks (and entity, the old spelling of record)", blk.Type))
	}
}

func (p *hclReader) concept(kind Kind, name string, line int, body *hclsyntax.Body) {
	c := &Concept{Kind: kind, Name: name, Line: line}
	c.Attrs = p.attrs(body, false)
	for _, blk := range body.Blocks {
		bline := blk.DefRange().Start.Line
		switch {
		case (blk.Type == "field" && kind != KindEnum) || (blk.Type == "property" && kind == KindRecord):
			if blk.Type == "property" {
				p.old(blk.TypeRange, "property", "field")
			}
			mname, ok := p.label(blk, bline)
			if !ok {
				p.oldInMember(blk.Body)
				continue
			}
			p.checkNoBlocks(blk)
			c.Members = append(c.Members, Member{Name: mname, Line: bline, Attrs: p.attrs(blk.Body, true)})
		case kind == KindRecord && blk.Type == "index":
			p.reserved(bline, blk.Type)
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

// oldInMember records the old spelling of the reference setting in a member whose
// block is not read (its labels are wrong).
func (p *hclReader) oldInMember(body *hclsyntax.Body) {
	if a, ok := body.Attributes["entity"]; ok {
		p.oldEntity(body, a)
	}
}

// oldEntity records the old spelling of the reference setting in a member, and
// refuses a rewrite of a member that has the new one beside it: the rewrite would
// make two. It is called wherever the old setting is recorded, whether or not the
// member is read or its value is a literal.
func (p *hclReader) oldEntity(body *hclsyntax.Body, a *hclsyntax.Attribute) {
	p.old(a.NameRange, "entity", "record")
	if _, both := body.Attributes["record"]; both {
		p.refuse(fmt.Sprintf("a member has both record and entity (line %d)", a.SrcRange.Start.Line))
	}
}

// oldInBody records the old spellings in the body of a block that is not read: the
// members of a record written property, and the references in its members.
func (p *hclReader) oldInBody(body *hclsyntax.Body, isRecord bool) {
	for _, blk := range body.Blocks {
		switch {
		case isRecord && blk.Type == "property":
			p.old(blk.TypeRange, "property", "field")
			p.oldInMember(blk.Body)
		case blk.Type == "field":
			p.oldInMember(blk.Body)
		}
	}
}

// attrs reads a body's attributes as literals, in source order. In a member the
// reference to a record is written record, or entity in the old spelling; it is
// the attribute record either way, and a member that has both is an error.
func (p *hclReader) attrs(body *hclsyntax.Body, member bool) []Attr {
	var out []Attr
	for _, name := range sortedAttrNames(body) {
		a := body.Attributes[name]
		line := a.SrcRange.Start.Line
		if member && name == "entity" {
			p.oldEntity(body, a) // before the value is judged: a value that is refused is still spelled the old way
		}
		n, msg := p.literalNode(a.Expr, line)
		if msg != "" {
			p.add(line, RuleLiteral, fmt.Sprintf("attribute %q: %s", name, msg))
			continue
		}
		if member && name == "entity" {
			if _, both := body.Attributes["record"]; both {
				p.add(line, RuleAttribute, "has both record and entity; entity is the old spelling of record (decision 0018), and a member refers to one record")
				continue
			}
			name = "record"
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
		return &Node{Type: NodeNumber, Str: canonicalNumber("-" + p.numbers[neg.Val.Range().Start.Byte]), Line: line}, ""
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
		return &Node{Type: NodeNumber, Str: canonicalNumber(p.numbers[expr.Range().Start.Byte]), Line: line}, ""
	}
}
