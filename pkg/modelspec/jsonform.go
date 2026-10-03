package modelspec

import (
	"fmt"
	"regexp"
)

// identifier is the form of names the publish profile requires: letters, digits
// and underscore, not starting with a digit.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jsonGroups maps the JSON top-level group of named concepts to its kind and
// the key that holds its members.
var jsonGroups = []struct {
	key     string
	kind    Kind
	members string
}{
	{"components", KindComponent, "fields"},
	{"enums", KindEnum, ""},
	{"entities", KindEntity, "properties"},
	{"collections", KindCollection, "fields"},
	{"recordsets", KindRecordset, "columns"},
}

type jsonReader struct {
	m        *Model
	findings []Finding
}

func (p *jsonReader) add(line int, rule, msg string) {
	p.findings = append(p.findings, Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityError, Message: msg})
}

func (p *jsonReader) warn(line int, rule, msg string) {
	p.findings = append(p.findings, Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityWarning, Message: msg})
}

// ParseJSON reads the JSON interchange form of a model (spec/json-format.md):
// the structure the specification defines and the validation it lists (a
// supported "modelspec" version, module.id and module.version, and well-formed
// groups). Constraints a single consumer adds are the publish profile's, not
// this reader's: entities are optional, module.name is optional, and an entity
// may have no properties.
func ParseJSON(file string, src []byte) (*Model, []Finding) {
	m := &Model{File: file, Form: FormJSON, Name: moduleNameFromFile(file), Group: file}
	if f, bad := precheck(file, src); bad {
		m.Broken = true
		return m, []Finding{f}
	}
	p := &jsonReader{m: m}
	root, err := ParseNode(src)
	if err != nil {
		m.Broken = true
		// ParseNode only returns *syntaxError.
		se := err.(*syntaxError)
		if se.limit {
			p.add(se.line, RuleLimit, se.msg)
		} else {
			p.add(se.line, RuleSyntax, "not valid JSON: "+se.msg)
		}
		return m, p.findings
	}
	if root.Type != NodeObject {
		m.Broken = true
		p.add(root.Line, RuleShape, fmt.Sprintf("a ModelSpec JSON document must be an object, not %s", root.typeName()))
		return m, p.findings
	}
	m.Root = root
	for _, d := range root.Dups {
		p.add(d.Line, RuleDuplicate, fmt.Sprintf("duplicate key %q in one object; names are unique, and JSON readers disagree on which of two equal keys wins", d.Key))
	}
	p.top(root)
	SortFindings(p.findings)
	return m, p.findings
}

func (p *jsonReader) top(root *Node) {
	seen := map[string]bool{}
	for _, f := range root.Fields {
		if seen[f.Key] {
			continue // reported as a duplicate key
		}
		seen[f.Key] = true
		switch f.Key {
		case "modelspec", "module", "$schema":
			// "$schema" may point at a published JSON Schema (decision 0010).
		case "projections", "migrations":
			if f.Value.Type != NodeObject {
				p.add(f.Line, RuleShape, fmt.Sprintf("%q must be an object, not %s", f.Key, f.Value.typeName()))
				continue
			}
			if f.Key == "projections" {
				p.m.Projections = f.Value
			} else {
				p.m.Migrations = f.Value
			}
		default:
			if !isGroup(f.Key) {
				p.warn(f.Line, RuleUnknown, fmt.Sprintf("unknown top-level field %q; ModelSpec defines modelspec, module, components, enums, entities, collections, recordsets, projections and migrations (the format does not say whether other fields are allowed, so this is accepted)", f.Key))
			}
		}
	}
	p.version(root)
	p.module(root)
	for _, g := range jsonGroups {
		p.group(root, g.key, g.kind, g.members)
	}
}

func isGroup(key string) bool {
	for _, g := range jsonGroups {
		if g.key == key {
			return true
		}
	}
	return false
}

func (p *jsonReader) version(root *Node) {
	v, ok := root.Get("modelspec")
	switch {
	case !ok || v.Type != NodeString:
		p.add(root.Line, RuleVersion, `has no "modelspec" version (a string; the only defined value is "`+SpecVersion+`")`)
	case v.Str != SpecVersion:
		p.add(v.Line, RuleVersion, fmt.Sprintf(`"modelspec" is %q; the only defined value is %q`, v.Str, SpecVersion))
	}
}

func (p *jsonReader) module(root *Node) {
	mod, ok := root.Get("module")
	if !ok || mod.Type != NodeObject {
		line := root.Line
		if ok {
			line = mod.Line
		}
		p.add(line, RuleModule, "has no module object (module.id and module.version are required)")
		return
	}
	p.m.ModuleLine = mod.Line
	id := &ModuleIdentity{}
	p.m.Module = id
	for _, f := range []struct {
		key      string
		dst      *string
		required bool
	}{{"id", &id.ID, true}, {"name", &id.Name, false}, {"version", &id.Version, true}} {
		v, ok := mod.Get(f.key)
		switch {
		case ok && v.Type == NodeString && v.Str != "":
			*f.dst = v.Str
		case ok || f.required:
			p.add(mod.Line, RuleModule, fmt.Sprintf("has no module.%s (a non-empty string)", f.key))
		}
	}
	if id.Name != "" {
		p.m.Name = id.Name
	}
}

// group reads one top-level object of named concepts.
func (p *jsonReader) group(root *Node, key string, kind Kind, membersKey string) {
	g, ok := root.Get(key)
	if !ok {
		return
	}
	if g.Type != NodeObject {
		p.add(g.Line, RuleShape, fmt.Sprintf("%q must be an object keyed by name, not %s", key, g.typeName()))
		return
	}
	seen := map[string]bool{}
	for _, f := range g.Fields {
		if seen[f.Key] {
			continue // reported as a duplicate key
		}
		seen[f.Key] = true
		if f.Value.Type != NodeObject {
			p.add(f.Line, RuleShape, fmt.Sprintf("%s %q must be an object, not %s", kind, f.Key, f.Value.typeName()))
			continue
		}
		p.concept(kind, f, membersKey)
	}
}

func (p *jsonReader) concept(kind Kind, f Field, membersKey string) {
	c := &Concept{Kind: kind, Name: f.Key, Line: f.Line}
	var members *Node
	for _, af := range f.Value.Fields {
		if membersKey != "" && af.Key == membersKey {
			members = af.Value
			continue
		}
		c.Attrs = append(c.Attrs, Attr{Name: af.Key, Value: af.Value, Line: af.Line})
	}
	switch {
	case membersKey == "":
		// enums have no members
	case members == nil:
		// An entity without properties is allowed by the standard.
	case kind == KindRecordset:
		p.columns(c, members)
	default:
		p.named(c, members, membersKey)
	}
	p.m.Concepts = append(p.m.Concepts, c)
}

// named reads an object of uniquely named members (properties or fields).
func (p *jsonReader) named(c *Concept, members *Node, key string) {
	if members.Type != NodeObject {
		p.add(members.Line, RuleShape, fmt.Sprintf("%s %q: %s must be an object keyed by name, not %s", c.Kind, c.Name, key, members.typeName()))
		return
	}
	seen := map[string]bool{}
	for _, mf := range members.Fields {
		if seen[mf.Key] {
			continue // reported as a duplicate key
		}
		seen[mf.Key] = true
		if mf.Value.Type != NodeObject {
			p.add(mf.Line, RuleShape, fmt.Sprintf("%s %q %s %q must be an object, not %s", c.Kind, c.Name, memberWord(c.Kind), mf.Key, mf.Value.typeName()))
			continue
		}
		c.Members = append(c.Members, Member{Name: mf.Key, Line: mf.Line, Attrs: attrsOf(mf.Value, "")})
	}
}

// columns reads a recordset's ordered column array.
func (p *jsonReader) columns(c *Concept, members *Node) {
	if members.Type != NodeArray {
		p.add(members.Line, RuleShape, fmt.Sprintf("recordset %q: columns must be an array, not %s", c.Name, members.typeName()))
		return
	}
	for _, it := range members.Items {
		if it.Type != NodeObject {
			p.add(it.Line, RuleShape, fmt.Sprintf("recordset %q: each column must be an object, not %s", c.Name, it.typeName()))
			continue
		}
		name, ok := it.Get("name")
		if !ok || name.Type != NodeString {
			p.add(it.Line, RuleShape, fmt.Sprintf("recordset %q: each column needs a string name", c.Name))
			continue
		}
		c.Members = append(c.Members, Member{Name: name.Str, Line: it.Line, Attrs: attrsOf(it, "name")})
	}
}

// attrsOf lists an object's fields as attributes, leaving out skip.
func attrsOf(n *Node, skip string) []Attr {
	var out []Attr
	for _, f := range n.Fields {
		if f.Key != skip {
			out = append(out, Attr{Name: f.Key, Value: f.Value, Line: f.Line})
		}
	}
	return out
}
