package modelspec

import "strings"

// Kind is a concept kind.
type Kind string

const (
	KindRecord    Kind = "record"
	KindComponent Kind = "component"
	KindEnum      Kind = "enum"
)

// KindEntity is the old name of KindRecord, with the same value.
//
// Deprecated: use KindRecord (decision 0018).
const KindEntity = KindRecord

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

// The two values the JSON form's "modelspec" field may carry
// (spec/json-format.md, "Format Identity"). The identifier decides the
// vocabulary of the whole document (decision 0018): SpecVersion is the new one,
// with the keys records, fields and record; OldSpecVersion is the old one, with
// entities, properties and entity, which a reader still accepts (decision 0022).
const (
	SpecVersion    = "1.0-draft-2"
	OldSpecVersion = "1.0-draft"
)

// Attr is one attribute of a concept or member: `required = true`.
type Attr struct {
	Name  string
	Value *Node
	Line  int
}

// Member is a field of a record or of a component (decision 0020). Names are
// unique in a concept.
type Member struct {
	Name  string
	Line  int
	Attrs []Attr
}

// Attr returns the member's attribute with the given name.
func (m *Member) Attr(name string) (*Attr, bool) { return findAttr(m.Attrs, name) }

// Concept is a record, a component or an enum.
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

// memberWord is what a record and a component call their members (decision
// 0020), in messages.
const memberWord = "field"

// OldSpelling is one place where a source uses the old spelling of the
// vocabulary (decisions 0018 and 0020): the bytes [Start, End) of the source hold
// Old, and modelspec rewrite puts New there. The reader records every one, so the
// deprecation finding, the export's choice of vocabulary and the rewrite all come
// from the same list.
type OldSpelling struct {
	Line       int
	Start, End int
	Old, New   string
}

// ModuleIdentity is the identity the JSON form carries and HCL does not.
type ModuleIdentity struct {
	ID      string
	Name    string
	Version string
}

// Model is one parsed ModelSpec file. A module is one or more Models: the HCL
// files of a module share a Group (see Load); a JSON file is a whole module.
type Model struct {
	File string // as given or found, used in findings
	Form Form
	// Name is the module short name used to resolve module-qualified
	// references. ParseHCL sets the file name without .modelspec.hcl; ParseJSON
	// sets module.name, or that same file-name stem when the JSON has none; Load
	// then applies the module rules (SpecScore layout, --module).
	Name string
	// Group identifies the module the file belongs to: files with the same Group
	// are one module. ParseHCL and ParseJSON set it to File (one file, one
	// module); Load groups the files of a module.
	Group string
	// Twin is set by Load on a JSON file that sits beside the HCL file of the same
	// name: it is the interchange copy of that module, not a second module, and
	// references into the module resolve to the HCL.
	Twin bool
	// TwinOf is the HCL model whose export a Twin is the copy of, when that is one
	// HCL file; Check compares the two.
	TwinOf *Model
	// Root is the parsed document of a JSON file (nil for HCL).
	Root *Node
	// Module is the identity from a JSON file; nil for HCL.
	Module *ModuleIdentity
	// ModuleLine is the line of the JSON module object (0 for HCL).
	ModuleLine int
	// Concepts are in source order.
	Concepts []*Concept
	// Old lists the old spellings the source uses, in source order; empty when it
	// uses none, and for a JSON file whose format identifier is not the old one.
	// An HCL file has no version marker, so for it this list is the whole of what
	// tells the old vocabulary from the new.
	Old []OldSpelling
	// cannotRewrite is why modelspec rewrite must not touch the file: it holds a
	// construct that no rewriting fixes (removed or reserved), or mixes the two
	// vocabularies. Empty when the file can be rewritten.
	cannotRewrite string
	// Broken is set when the source could not be read into a model; Check skips
	// it and does not report references into its module.
	Broken bool
	// Incomplete is set when a file of the model's module was found by a search
	// and not read (a link, a pipe, a device: rule skipped-file). Check does not
	// check an incomplete module: a partial load would report what is missing as
	// unresolved references.
	Incomplete bool
}

// OldVocabulary reports whether the model is in the old vocabulary: export
// writes the vocabulary of its source (decision 0022, step 1).
func (m *Model) OldVocabulary() bool { return len(m.Old) > 0 }

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
