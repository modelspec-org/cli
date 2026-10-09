package modelspec

import (
	"fmt"
	"regexp"
	"sort"
)

// identifier is the form of names the publish profile requires: letters, digits
// and underscore, not starting with a digit.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jsonGroups are the JSON top-level groups of named concepts and their kinds,
// the keys of both vocabularies: records (format 1.0-draft-2) and entities
// (1.0-draft). A key of the other vocabulary is read too, so that one wrong key
// is one finding and not a cascade of unresolved references; it is an error when
// the identifier says which vocabulary the document is in (misplaced).
// Components keep fields in both.
var jsonGroups = []struct {
	key  string
	kind Kind
}{
	{"components", KindComponent},
	{"enums", KindEnum},
	{"records", KindRecord},
	{"entities", KindRecord},
}

// removedKeys and reservedKeys are the top-level keys of constructs that decision
// 0019 removed and reserved.
var (
	removedKeys  = map[string]bool{"collections": true, "recordsets": true}
	reservedKeys = map[string]bool{"projections": true, "migrations": true}
)

type jsonReader struct {
	m   *Model
	src []byte
	// version is the format identifier when it is one of the two this reader
	// knows, and empty otherwise (then the document is read in either vocabulary
	// and no key is out of place).
	version string
	findingList
}

func (p *jsonReader) add(line int, rule, msg string) {
	p.put(Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityError, Message: msg})
}

func (p *jsonReader) warn(line int, rule, msg string) {
	p.put(Finding{File: p.m.File, Line: line, Rule: rule, Severity: SeverityWarning, Message: msg})
}

// refuse records why modelspec rewrite must not touch the file; the first reason
// stays.
func (p *jsonReader) refuse(reason string) {
	if p.m.cannotRewrite == "" {
		p.m.cannotRewrite = reason
	}
}

// old records an old spelling at the bytes [start, end) of the source, to be
// replaced by repl.
func (p *jsonReader) old(line, start, end int, repl string) {
	p.m.Old = append(p.m.Old, OldSpelling{Line: line, Start: start, End: end, Old: string(p.src[start:end]), New: repl})
}

// oldKey records the old spelling of a key, which is replaced by newKey.
func (p *jsonReader) oldKey(f Field, newKey string) {
	p.old(f.Line, f.Start, f.End, encodeString(newKey))
}

// ParseJSON reads the JSON interchange form of a model (spec/json-format.md):
// the structure the specification defines and the validation it lists (a
// supported "modelspec" version, module.id and module.version, and well-formed
// groups). Constraints a single consumer adds are the publish profile's, not
// this reader's: records are optional, module.name is optional, and a record
// may have no fields.
//
// The format identifier decides the vocabulary: "1.0-draft-2" has records,
// fields and record, "1.0-draft" has entities, properties and entity (decisions
// 0018 and 0020). Both are read, into one model in the new words; a key of the
// wrong vocabulary is an error, and the old identifier is the deprecated
// spelling that modelspec rewrite brings up to date (decision 0022).
func ParseJSON(file string, src []byte) (*Model, []Finding) {
	m := &Model{File: file, Form: FormJSON, Name: moduleNameFromFile(file), Group: file}
	if f, bad := precheck(file, src); bad {
		m.Broken = true
		return m, []Finding{f}
	}
	p := &jsonReader{m: m, src: src}
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
		return m, p.result()
	}
	if root.Type != NodeObject {
		m.Broken = true
		p.add(root.Line, RuleShape, fmt.Sprintf("a ModelSpec JSON document must be an object, not %s", root.typeName()))
		return m, p.result()
	}
	m.Root = root
	for _, d := range root.Dups {
		p.add(d.Line, RuleDuplicate, fmt.Sprintf("duplicate key %q in one object; names are unique, and JSON readers disagree on which of two equal keys wins", d.Key))
	}
	p.top(root)
	sort.Slice(m.Old, func(i, j int) bool { return m.Old[i].Start < m.Old[j].Start })
	return m, p.result()
}

func (p *jsonReader) top(root *Node) {
	p.versionOf(root)
	seen := map[string]bool{}
	for _, f := range root.Fields {
		if seen[f.Key] {
			continue // reported as a duplicate key
		}
		seen[f.Key] = true
		switch {
		case f.Key == "modelspec" || f.Key == "module" || f.Key == "$schema":
			// "$schema" may point at a published JSON Schema (decision 0010).
		case removedKeys[f.Key]:
			p.add(f.Line, RuleRemoved, fmt.Sprintf("%q was removed (decision 0019): a stored set of rows is described by the database's own description, and the shape of a result is a record with no key", f.Key))
			p.refuse(fmt.Sprintf("it holds %q, a construct decision 0019 removed", f.Key))
		case reservedKeys[f.Key]:
			p.add(f.Line, RuleReservedWord, fmt.Sprintf("%q is a reserved word with no content yet (decision 0019); a model cannot use it", f.Key))
			p.refuse(fmt.Sprintf("it holds %q, a word decision 0019 reserved", f.Key))
		case !isGroup(f.Key):
			p.warn(f.Line, RuleUnknown, fmt.Sprintf("unknown top-level field %q; ModelSpec defines modelspec, module, components, enums and records (entities in format %s) (the format does not say whether other fields are allowed, so this is accepted)", f.Key, OldSpecVersion))
		}
	}
	p.module(root)
	for _, g := range jsonGroups {
		p.group(root, g.key, g.kind)
	}
	if p.version == OldSpecVersion {
		p.oldEdits(root)
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

// versionOf reads the format identifier, which decides the vocabulary.
func (p *jsonReader) versionOf(root *Node) {
	v, ok := root.Get("modelspec")
	switch {
	case !ok || v.Type != NodeString:
		p.add(root.Line, RuleVersion, `has no "modelspec" version (a string; the defined values are "`+SpecVersion+`", and "`+OldSpecVersion+`" the old one)`)
		p.refuse(`it has no "modelspec" format identifier`)
	case v.Str != SpecVersion && v.Str != OldSpecVersion:
		p.add(v.Line, RuleVersion, fmt.Sprintf(`"modelspec" is %s; the defined values are %q, and %q the old one`, quote1(v.Str), SpecVersion, OldSpecVersion))
		p.refuse(fmt.Sprintf(`its "modelspec" is %s, which is neither %q nor %q`, quote1(v.Str), OldSpecVersion, SpecVersion))
	default:
		p.version = v.Str
		if v.Str == OldSpecVersion {
			p.old(v.Line, v.Start, v.End, encodeString(SpecVersion))
		}
	}
}

// misplaced reports a key (oldKey or newKey, the pair of spellings) that belongs
// to the other vocabulary than the one the document's identifier names.
func (p *jsonReader) misplaced(line int, key, oldKey, newKey string) {
	switch {
	case key == oldKey && p.version == SpecVersion:
		p.add(line, RuleVersion, fmt.Sprintf("%q is the key of format %s; this document says %q, where it is %q", key, OldSpecVersion, SpecVersion, newKey))
		p.refuse(fmt.Sprintf("it uses the key %q of format %s in a %s document", key, OldSpecVersion, SpecVersion))
	case key == newKey && p.version == OldSpecVersion:
		p.add(line, RuleVersion, fmt.Sprintf("%q belongs to format %s; this document says %q, where the key is %q", key, SpecVersion, OldSpecVersion, oldKey))
		p.refuse(fmt.Sprintf("it uses the key %q of format %s in a %s document", key, SpecVersion, OldSpecVersion))
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
func (p *jsonReader) group(root *Node, key string, kind Kind) {
	var gf Field
	found := false
	for _, f := range root.Fields {
		if f.Key == key {
			gf, found = f, true
			break
		}
	}
	if !found {
		return
	}
	g := gf.Value
	if kind == KindRecord {
		p.misplaced(gf.Line, key, "entities", "records")
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
		p.concept(kind, f)
	}
}

func (p *jsonReader) concept(kind Kind, f Field) {
	c := &Concept{Kind: kind, Name: f.Key, Line: f.Line}
	var members *Node
	membersKey := ""
	for _, af := range f.Value.Fields {
		if isMembers := kind != KindEnum && (af.Key == "fields" || (kind == KindRecord && af.Key == "properties")); isMembers && members == nil {
			members, membersKey = af.Value, af.Key
			if kind == KindRecord {
				p.misplaced(af.Line, af.Key, "properties", "fields")
			}
			continue
		} else if isMembers {
			// A second list is an attribute named by its key, which a rewrite would rename.
			p.refuse(fmt.Sprintf("%s %q has two lists of members, %q among them", kind, f.Key, af.Key))
		}
		c.Attrs = append(c.Attrs, Attr{Name: af.Key, Value: af.Value, Line: af.Line})
	}
	if members != nil {
		p.named(c, members, membersKey)
	}
	p.m.Concepts = append(p.m.Concepts, c)
}

// named reads an object of uniquely named members.
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
			p.add(mf.Line, RuleShape, fmt.Sprintf("%s %q %s %q must be an object, not %s", c.Kind, c.Name, memberWord, mf.Key, mf.Value.typeName()))
			continue
		}
		c.Members = append(c.Members, Member{Name: mf.Key, Line: mf.Line, Attrs: p.memberAttrs(mf.Value)})
	}
}

// memberAttrs lists a member's fields as attributes. The reference to a record
// is the attribute record, whichever of record and entity the document used.
func (p *jsonReader) memberAttrs(n *Node) []Attr {
	var out []Attr
	for _, f := range n.Fields {
		name := f.Key
		if name == "entity" || name == "record" {
			p.misplaced(f.Line, f.Key, "entity", "record")
			name = "record"
		}
		out = append(out, Attr{Name: name, Value: f.Value, Line: f.Line})
	}
	return out
}

// oldEdits lists the old spellings of a document in format 1.0-draft that the
// identifier does not cover: the key entities, the key properties of each of its
// objects, and the key entity of each member of those and of the components. It
// walks every key, repeated ones too, so that rewriting one leaves no other.
func (p *jsonReader) oldEdits(root *Node) {
	for _, f := range root.Fields {
		switch f.Key {
		case "entities":
			p.oldKey(f, "records")
			p.oldMembers(f.Value, "properties", "fields")
		case "components":
			p.oldMembers(f.Value, "fields", "")
		}
	}
}

// oldMembers visits the concepts of a group, and in each the key that holds its
// members; rename is the new name of that key, or "" when it is not renamed.
func (p *jsonReader) oldMembers(group *Node, key, rename string) {
	if group.Type != NodeObject {
		return
	}
	for _, cf := range group.Fields {
		if cf.Value.Type != NodeObject {
			continue
		}
		for _, af := range cf.Value.Fields {
			if af.Key != key {
				continue
			}
			if rename != "" {
				p.oldKey(af, rename)
			}
			if af.Value.Type != NodeObject {
				continue
			}
			for _, mf := range af.Value.Fields {
				if mf.Value.Type != NodeObject {
					continue
				}
				for _, rf := range mf.Value.Fields {
					if rf.Key == "entity" {
						p.oldKey(rf, "record")
					}
				}
			}
		}
	}
}
