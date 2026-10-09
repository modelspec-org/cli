package modelspec

import (
	"errors"
	"fmt"
	"strings"
)

// JSON builds the JSON interchange form of the model: the "modelspec" field,
// "module", then the groups components, enums and records, in that order.
// Concepts keep source order. A record's attributes come first, then its members.
//
// The form is written in the vocabulary of the model's source (decision 0022,
// step 1): a model with no old spelling is "1.0-draft-2", with records, fields
// and record, and a model with any old spelling is "1.0-draft", with entities,
// properties and entity, so that a committed copy stays what its source exports
// to until modelspec rewrite brings both up to date.
//
// The mapping is exactly the one spec/json-format.md defines. What it leaves
// open is refused rather than invented: module identity for HCL, which the
// grammar has no place for, so the caller must supply id and version (the
// format requires those two; name is written only when given, or when the model
// refers to its own module by name). A model read from JSON already has its
// identity, which id overrides when non-zero.
func (m *Model) JSON(id ModuleIdentity) (*Node, error) {
	return m.json(id, m.OldVocabulary())
}

// vocabularyFree reports whether the model is written the same in both
// vocabularies: no old spelling, no record, and no reference to one. An HCL file of
// components and enums only is that, and so is an empty one.
func (m *Model) vocabularyFree() bool {
	if m.OldVocabulary() {
		return false
	}
	for _, k := range m.Concepts {
		if k.Kind == KindRecord {
			return false
		}
		for _, mem := range k.Members {
			if _, ok := mem.Attr("record"); ok {
				return false
			}
		}
	}
	return true
}

// json is JSON with the vocabulary given: old is format 1.0-draft.
func (m *Model) json(id ModuleIdentity, old bool) (*Node, error) {
	if id == (ModuleIdentity{}) && m.Module != nil {
		id = *m.Module
	}
	if id.ID == "" || id.Version == "" {
		return nil, errors.New("cannot export: the JSON form needs module.id and module.version, and HCL has no place to carry them; supply both (and module.name if you want one written)")
	}
	if id.Name != "" && id.Name != m.Name && m.refersTo(m.Name) {
		return nil, fmt.Errorf("cannot export: the model refers to its own module as %q, so module.name must be %q; with %q the JSON would not lint clean on its own", m.Name, m.Name, id.Name)
	}
	if id.Name == "" && m.refersTo(m.Name) {
		// A JSON file takes its module's name from module.name or, without it, from
		// its file name; a model that refers to itself by name must not depend on
		// what the file is called.
		id.Name = m.Name
	}
	module := obj(field("id", str(id.ID)))
	if id.Name != "" {
		module.Fields = append(module.Fields, field("name", str(id.Name)))
	}
	module.Fields = append(module.Fields, field("version", str(id.Version)))
	version, recordsKey, membersKey, refKey := SpecVersion, "records", "fields", "record"
	if old {
		version, recordsKey, membersKey, refKey = OldSpecVersion, "entities", "properties", "entity"
	}
	root := obj(field("modelspec", str(version)), field("module", module))
	for _, g := range []struct {
		key     string
		kind    Kind
		members string
	}{{"components", KindComponent, "fields"}, {"enums", KindEnum, ""}, {recordsKey, KindRecord, membersKey}} {
		var fields []Field
		for _, k := range m.Concepts {
			if k.Kind == g.kind {
				fields = append(fields, field(k.Name, conceptNode(k, g.members, refKey)))
			}
		}
		if len(fields) > 0 {
			root.Fields = append(root.Fields, field(g.key, &Node{Type: NodeObject, Fields: fields}))
		}
	}
	return root, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// conceptNode builds a concept's object: its attributes, then its members under
// membersKey, in which a reference to a record is written under refKey.
func conceptNode(k *Concept, membersKey, refKey string) *Node {
	n := obj()
	for _, a := range k.Attrs {
		n.Fields = append(n.Fields, field(a.Name, a.Value))
	}
	if membersKey != "" {
		members := obj()
		for _, mem := range k.Members {
			mn := obj()
			for _, a := range mem.Attrs {
				name := a.Name
				if name == "record" {
					name = refKey
				}
				mn.Fields = append(mn.Fields, field(name, a.Value))
			}
			members.Fields = append(members.Fields, field(mem.Name, mn))
		}
		n.Fields = append(n.Fields, field(membersKey, members))
	}
	return n
}

// ExportDrift compares what the model exports to with committed JSON. It
// returns "" when they are the same document with the same key and array order
// (whitespace does not count), and otherwise one sentence saying what is wrong.
//
// Module identity is not in the HCL. When id is the zero value the committed
// JSON's own module object supplies it, so the comparison covers everything
// except the identity; pass id to compare that too.
func (m *Model) ExportDrift(committed []byte, id ModuleIdentity) string {
	return clipText(m.exportDrift(committed, id), MaxMessageBytes)
}

// exportDrift is ExportDrift without the cut of the message.
func (m *Model) exportDrift(committed []byte, id ModuleIdentity) string {
	want, err := ParseNode(committed)
	if err != nil {
		return fmt.Sprintf("the committed JSON is not valid JSON: %v", err)
	}
	if id == (ModuleIdentity{}) {
		id = identityOf(want)
	}
	diff, err := m.exportDiff(want, id)
	if err != nil {
		return err.Error()
	}
	if diff != "" {
		return fmt.Sprintf("the committed JSON is not what %s exports to: %s%s", m.File, diff, vocabularyNote(m, want))
	}
	return ""
}

// vocabularyNote is what to say about a difference between a model's export and a
// JSON document when they are in different vocabularies: the one instruction that
// fixes it, which replaces any other.
func vocabularyNote(m *Model, doc *Node) string {
	v, ok := doc.Get("modelspec")
	if !ok || v.Type != NodeString || (v.Str != SpecVersion && v.Str != OldSpecVersion) || (v.Str == OldSpecVersion) == m.OldVocabulary() || m.vocabularyFree() {
		return ""
	}
	return "; the two are in different vocabularies (the old entities, properties and entity, and the new records, fields and record); modelspec rewrite --write on both files brings the pair in line"
}

// exportDiff compares the export of the model, with the identity id, with a
// parsed JSON document: "" when they are the same document with the same key and
// array order. The error is the export's own refusal.
func (m *Model) exportDiff(want *Node, id ModuleIdentity) (string, error) {
	// A model that is the same text in both vocabularies is compared in the one the
	// document is in, so a copy written as 1.0-draft before the rename is not drift.
	v, _ := want.Get("modelspec")
	old := m.OldVocabulary() || (m.vocabularyFree() && v != nil && v.Type == NodeString && v.Str == OldSpecVersion)
	got, err := m.json(id, old)
	if err != nil {
		return "", err
	}
	return Diff(got, want), nil
}

// identityOf reads the module object of a JSON document; missing parts stay
// empty.
func identityOf(root *Node) ModuleIdentity {
	var id ModuleIdentity
	mod, ok := root.Get("module")
	if !ok || mod.Type != NodeObject {
		return id
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"id", &id.ID}, {"name", &id.Name}, {"version", &id.Version}} {
		if v, ok := mod.Get(f.key); ok && v.Type == NodeString {
			*f.dst = v.Str
		}
	}
	return id
}

// refersTo reports whether the model has a module-qualified reference into the
// module of that name: <name>.<Concept> in a record, component, enum or use
// reference.
func (m *Model) refersTo(name string) bool {
	qualified := func(s string) bool { return strings.HasPrefix(s, name+".") }
	check := func(a Attr) bool {
		switch a.Name {
		case "use":
			list, _ := a.Value.stringList()
			for _, s := range list {
				if qualified(s) {
					return true
				}
			}
		case "record", "component", "enum":
			return a.Value.Type == NodeString && qualified(a.Value.Str)
		}
		return false
	}
	for _, k := range m.Concepts {
		for _, a := range k.Attrs {
			if check(a) {
				return true
			}
		}
		for _, mem := range k.Members {
			for _, a := range mem.Attrs {
				if check(a) {
					return true
				}
			}
		}
	}
	return false
}
