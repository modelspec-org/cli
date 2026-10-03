package modelspec

import "strings"

// Kind is a concept kind.
type Kind string

const (
	KindEntity     Kind = "entity"
	KindComponent  Kind = "component"
	KindEnum       Kind = "enum"
	KindCollection Kind = "collection"
	KindRecordset  Kind = "recordset"
)

// Form is the serialisation a model was read from.
type Form string

const (
	FormHCL  Form = "hcl"
	FormJSON Form = "json"
)

// Extensions of the two model file forms.
const (
	HCLSuffix  = ".modelspec.hcl"
	JSONSuffix = ".modelspec.json"
)

// SpecVersion is the only value the JSON form's "modelspec" field may carry
// (spec/json-format.md, "Format Identity").
const SpecVersion = "1.0-draft"

// Attr is one attribute of a concept or member: `required = true`.
type Attr struct {
	Name  string
	Value *Node
	Line  int
}

// Member is a property (entity), field (component or collection) or column
// (recordset). Column names may repeat; the others are unique.
type Member struct {
	Name  string
	Line  int
	Attrs []Attr
}

// Attr returns the member's attribute with the given name.
func (m *Member) Attr(name string) (*Attr, bool) { return findAttr(m.Attrs, name) }

// Concept is an entity, component, enum, collection or recordset.
type Concept struct {
	Kind    Kind
	Name    string
	Line    int
	Attrs   []Attr
	Members []Member
}

// Attr returns the concept's attribute with the given name.
func (c *Concept) Attr(name string) (*Attr, bool) { return findAttr(c.Attrs, name) }

func findAttr(attrs []Attr, name string) (*Attr, bool) {
	for i := range attrs {
		if attrs[i].Name == name {
			return &attrs[i], true
		}
	}
	return nil, false
}

// memberWord is what a concept kind calls its members.
func memberWord(k Kind) string {
	switch k {
	case KindEntity:
		return "property"
	case KindRecordset:
		return "column"
	default:
		return "field"
	}
}

// Unmapped is an HCL construct that the reader parses for syntax but that has
// no defined place in the JSON form, so it cannot be exported.
type Unmapped struct {
	What string // for example `entity "User" index "by_email"`
	Line int
}

// ModuleIdentity is the identity the JSON form carries and HCL does not.
type ModuleIdentity struct {
	ID      string
	Name    string
	Version string
}

// Model is one parsed ModelSpec module: the contents of one file.
type Model struct {
	File string
	Form Form
	// Name is the module short name used to resolve module-qualified
	// references: the file name without .modelspec.hcl for HCL, module.name
	// for JSON ("" when the JSON has none).
	Name string
	// Module is the identity from a JSON file; nil for HCL.
	Module *ModuleIdentity
	// Concepts are in source order.
	Concepts []*Concept
	// Unmapped lists HCL constructs the JSON form does not define.
	Unmapped []Unmapped
	// Projections and Migrations are carried through unchanged from a JSON
	// file; nil otherwise.
	Projections *Node
	Migrations  *Node
	// Broken is set when the source could not be read into a model; Check skips
	// it and does not report references into its module.
	Broken bool
}

// HasConcept reports whether the model declares a concept of the kind with the
// name.
func (m *Model) HasConcept(kind Kind, name string) bool {
	for _, c := range m.Concepts {
		if c.Kind == kind && c.Name == name {
			return true
		}
	}
	return false
}

// moduleNameFromFile derives the short module name from a model file name.
func moduleNameFromFile(file string) string {
	base := file
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(base, HCLSuffix), JSONSuffix)
}
