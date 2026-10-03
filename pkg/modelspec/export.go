package modelspec

import (
	"errors"
	"fmt"
	"strings"
)

// JSON builds the JSON interchange form of the model: the "modelspec" field,
// "module", then the groups components, enums, entities, collections,
// recordsets, projections and migrations, in that order. Concepts keep source
// order. An entity's or collection's attributes come first, then its members;
// recordset columns are an ordered array with "name" first (decision 0007).
//
// The mapping is exactly the one spec/json-format.md defines. Two things it
// leaves open are refused rather than invented: HCL `index`, `projection` and
// `migration` blocks (no document says how they map to JSON), and module
// identity for HCL, which the grammar has no place for, so the caller must
// supply id and version (the format requires those two; name is written only
// when given). A model read from JSON already has its identity, which id
// overrides when non-zero.
func (m *Model) JSON(id ModuleIdentity) (*Node, error) {
	if len(m.Unmapped) > 0 {
		parts := make([]string, len(m.Unmapped))
		for i, u := range m.Unmapped {
			parts[i] = fmt.Sprintf("line %d: %s", u.Line, u.What)
		}
		return nil, fmt.Errorf("cannot export: no JSON form is defined for %s (%s)", plural(len(m.Unmapped), "construct", "constructs"), strings.Join(parts, "; "))
	}
	if id == (ModuleIdentity{}) && m.Module != nil {
		id = *m.Module
	}
	if id.ID == "" || id.Version == "" {
		return nil, errors.New("cannot export: the JSON form needs module.id and module.version, and HCL has no place to carry them; supply both (and module.name if you want one written)")
	}
	module := obj(field("id", str(id.ID)))
	if id.Name != "" {
		module.Fields = append(module.Fields, field("name", str(id.Name)))
	}
	module.Fields = append(module.Fields, field("version", str(id.Version)))
	root := obj(field("modelspec", str(SpecVersion)), field("module", module))
	for _, g := range jsonGroups {
		var fields []Field
		for _, k := range m.Concepts {
			if k.Kind == g.kind {
				fields = append(fields, field(k.Name, conceptNode(k, g.members)))
			}
		}
		if len(fields) > 0 {
			root.Fields = append(root.Fields, field(g.key, &Node{Type: NodeObject, Fields: fields}))
		}
	}
	if m.Projections != nil {
		root.Fields = append(root.Fields, field("projections", m.Projections))
	}
	if m.Migrations != nil {
		root.Fields = append(root.Fields, field("migrations", m.Migrations))
	}
	return root, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func conceptNode(k *Concept, membersKey string) *Node {
	n := obj()
	for _, a := range k.Attrs {
		n.Fields = append(n.Fields, field(a.Name, a.Value))
	}
	switch {
	case membersKey == "":
	case k.Kind == KindRecordset:
		cols := &Node{Type: NodeArray}
		for _, col := range k.Members {
			cn := obj(field("name", str(col.Name)))
			for _, a := range col.Attrs {
				cn.Fields = append(cn.Fields, field(a.Name, a.Value))
			}
			cols.Items = append(cols.Items, cn)
		}
		n.Fields = append(n.Fields, field(membersKey, cols))
	default:
		members := obj()
		for _, mem := range k.Members {
			mn := obj()
			for _, a := range mem.Attrs {
				mn.Fields = append(mn.Fields, field(a.Name, a.Value))
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
	want, err := ParseNode(committed)
	if err != nil {
		return fmt.Sprintf("the committed JSON is not valid JSON: %v", err)
	}
	if id == (ModuleIdentity{}) {
		id = identityOf(want)
	}
	got, err := m.JSON(id)
	if err != nil {
		return err.Error()
	}
	if d := Diff(got, want); d != "" {
		return fmt.Sprintf("the committed JSON is not what %s exports to: %s", m.File, d)
	}
	return ""
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
