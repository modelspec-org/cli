package modelspec

import (
	"strings"
	"testing"
)

var testID = ModuleIdentity{ID: "x/y", Name: "y", Version: "1"}

func mustHCL(t *testing.T, src string) *Model {
	t.Helper()
	m, fs := ParseHCL("m.modelspec.hcl", []byte(src))
	if len(fs) != 0 {
		t.Fatalf("findings: %v", fs)
	}
	return m
}

const exportSrc = `component "C" {
  field "f" {
    type = "int"
  }
}
enum "E" {
  values = ["a", "b"]
}
entity "A" {
  key = ["id"]
  use = ["C"]
  property "id" {
    type = "int"
    required = true
  }
  property "e" {
    type = "string"
    enum = "E"
  }
}
collection "c" {
  kind   = "editable"
  source = "A"
  field "id" {
    type = "int"
    bind = "A.id"
  }
}
recordset "r" {
  key = ["id"]
  column "id" {
    type = "int"
  }
  column "id" {
    type = "int"
  }
}
`

func TestExportJSON(t *testing.T) {
	t.Parallel()
	m := mustHCL(t, exportSrc)
	n, err := m.JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, f := range n.Fields {
		order = append(order, f.Key)
	}
	if strings.Join(order, ",") != "modelspec,module,components,enums,entities,collections,recordsets" {
		t.Fatalf("top-level order = %v", order)
	}
	got := string(n.Encode())
	for _, want := range []string{
		`"modelspec": "1.0-draft"`,
		`"id": "x/y"`,
		"      \"key\": [\n        \"id\"\n      ],\n      \"use\": [\n        \"C\"\n      ],\n      \"properties\": {",
		"\"columns\": [\n        {\n          \"name\": \"id\"",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("export lacks %q:\n%s", want, got)
		}
	}
	// The export of a model parses back as the same model and exports the same.
	back, fs := ParseJSON("m.modelspec.json", n.Encode())
	if len(fs) != 0 {
		t.Fatalf("exported JSON has findings: %v", fs)
	}
	again, err := back.JSON(ModuleIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(n, again); d != "" {
		t.Fatalf("JSON -> model -> JSON changed the document: %s", d)
	}
}

func TestExportEntityWithoutProperties(t *testing.T) {
	t.Parallel()
	n, err := mustHCL(t, "entity \"Empty\" {\n  key = []\n}\n").JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(n.Encode()), "\"properties\": {}") {
		t.Fatalf("export:\n%s", n.Encode())
	}
}

func TestExportCarriesProjectionsAndMigrations(t *testing.T) {
	t.Parallel()
	src := doc(jEntities, `"projections": {"p": {"a": 1}}`, `"migrations": {"m": {"b": 2}}`)
	m, fs := ParseJSON("m.modelspec.json", []byte(src))
	if len(fs) != 0 {
		t.Fatal(fs)
	}
	n, err := m.JSON(ModuleIdentity{ID: "other", Name: "o", Version: "2"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(n.Encode())
	if !strings.Contains(got, `"projections"`) || !strings.Contains(got, `"migrations"`) || !strings.Contains(got, `"id": "other"`) {
		t.Fatalf("export:\n%s", got)
	}
}

func TestExportRefusals(t *testing.T) {
	t.Parallel()
	if _, err := mustHCL(t, exportSrc).JSON(ModuleIdentity{ID: "x/y", Name: "y"}); err == nil || !strings.Contains(err.Error(), "module.id and module.version") {
		t.Errorf("incomplete identity: %v", err)
	}
	if _, err := mustHCL(t, exportSrc).JSON(ModuleIdentity{}); err == nil {
		t.Error("no identity at all was accepted for HCL")
	}
	one := mustHCL(t, "projection \"p\" {\n}\n")
	if _, err := one.JSON(testID); err == nil || !strings.Contains(err.Error(), "no JSON form is defined for 1 construct (line 1: projection \"p\")") {
		t.Errorf("one unmapped construct: %v", err)
	}
	two := mustHCL(t, "projection \"p\" {\n}\nentity \"A\" {\n  index \"i\" {\n  }\n}\n")
	if _, err := two.JSON(testID); err == nil || !strings.Contains(err.Error(), "no JSON form is defined for 2 constructs") {
		t.Errorf("two unmapped constructs: %v", err)
	}
}

func TestExportDrift(t *testing.T) {
	t.Parallel()
	m := mustHCL(t, okEntity)
	good, err := m.JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	if d := m.ExportDrift(good.Encode(), ModuleIdentity{}); d != "" {
		t.Errorf("identical: %q", d)
	}
	if d := m.ExportDrift(good.Encode(), testID); d != "" {
		t.Errorf("identical with identity: %q", d)
	}
	if d := m.ExportDrift(good.Encode(), ModuleIdentity{ID: "x/z", Name: "y", Version: "1"}); !strings.Contains(d, "module.id is") {
		t.Errorf("identity override ignored: %q", d)
	}
	compact := strings.NewReplacer("\n", "", "  ", "").Replace(string(good.Encode()))
	if d := m.ExportDrift([]byte(compact), ModuleIdentity{}); d != "" {
		t.Errorf("whitespace must not matter: %q", d)
	}
	stale := strings.Replace(string(good.Encode()), `"int"`, `"string"`, 1)
	if d := m.ExportDrift([]byte(stale), ModuleIdentity{}); !strings.Contains(d, `not what m.modelspec.hcl exports to: entities.A.properties.id.type is "int" in the first and "string" in the second`) {
		t.Errorf("stale: %q", d)
	}
	if d := m.ExportDrift([]byte("{"), ModuleIdentity{}); !strings.Contains(d, "not valid JSON") {
		t.Errorf("invalid JSON: %q", d)
	}
	for _, committed := range []string{`{}`, `{"module": 1}`, `{"module": {"id": 1}}`} {
		if d := m.ExportDrift([]byte(committed), ModuleIdentity{}); !strings.Contains(d, "module.id and module.version") {
			t.Errorf("committed %s without identity: %q", committed, d)
		}
	}
}

func TestExportModuleName(t *testing.T) {
	t.Parallel()
	m := mustHCL(t, okEntity)
	without, err := m.JSON(ModuleIdentity{ID: "x/y", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(without.Encode()); !strings.Contains(got, "\"module\": {\n    \"id\": \"x/y\",\n    \"version\": \"1\"\n  }") {
		t.Errorf("module without a name:\n%s", got)
	}
	with, err := m.JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(with.Encode()); !strings.Contains(got, "\"module\": {\n    \"id\": \"x/y\",\n    \"name\": \"y\",\n    \"version\": \"1\"\n  }") {
		t.Errorf("module with a name:\n%s", got)
	}
	// A committed file without a name is compared without one.
	if d := m.ExportDrift(without.Encode(), ModuleIdentity{}); d != "" {
		t.Errorf("drift: %s", d)
	}
}

// A model that refers to its own module by name can only be exported under that
// name: with another, the JSON would not lint clean on its own.
func TestExportRefusesANameThatBreaksOwnReferences(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"an entity reference":   "entity \"Node\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"p\" {\n    entity = \"m.Node\"\n  }\n}\n",
		"a component reference": "component \"C\" {\n}\nentity \"N\" {\n  key = []\n  property \"c\" {\n    component = \"m.C\"\n  }\n}\n",
		"an enum reference":     "enum \"E\" {\n  values = [\"a\"]\n}\nentity \"N\" {\n  key = []\n  property \"c\" {\n    type = \"string\"\n    enum = \"m.E\"\n  }\n}\n",
		"a use":                 "component \"C\" {\n}\nentity \"N\" {\n  key = []\n  use = [\"m.C\"]\n}\n",
		"a collection source":   "entity \"N\" {\n  key = []\n}\ncollection \"c\" {\n  kind = \"editable\"\n  source = \"m.N\"\n}\n",
		"a bind":                "entity \"N\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n}\nrecordset \"r\" {\n  column \"c\" {\n    type = \"int\"\n    bind = \"m.N.id\"\n  }\n}\n",
	} {
		m, fs := ParseHCL("m.modelspec.hcl", []byte(src))
		if HasErrors(fs) {
			t.Fatalf("%s: %v", name, fs)
		}
		if _, err := m.JSON(ModuleIdentity{ID: "x/m", Name: "Tree", Version: "1"}); err == nil || !strings.Contains(err.Error(), `refers to its own module as "m", so module.name must be "m"`) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := m.JSON(ModuleIdentity{ID: "x/m", Name: "m", Version: "1"}); err != nil {
			t.Errorf("%s with the module's own name: %v", name, err)
		}
		// Without a name, module.name is written, so the JSON lints clean on its own
		// whatever file it is saved as.
		node, err := m.JSON(ModuleIdentity{ID: "x/m", Version: "1"})
		if err != nil {
			t.Errorf("%s without a name: %v", name, err)
			continue
		}
		if n, ok := node.Get("module"); !ok {
			t.Errorf("%s: no module object", name)
		} else if got, ok := n.Get("name"); !ok || got.Str != "m" {
			t.Errorf("%s without a name: module.name = %v, want \"m\"", name, got)
		}
		back, parse := ParseJSON("saved-as-something-else"+jsonExt, node.Encode())
		// (The fixtures have empty keys, which is no reference problem.)
		for _, f := range append(parse, Check([]*Model{back}, Options{})...) {
			if f.Rule == RuleReference || f.Rule == RuleSyntax || f.Rule == RuleShape {
				t.Errorf("%s without a name: the export does not lint clean as another file name: %v", name, f)
			}
		}
	}
	// No such reference: any name will do. Others' names, bare names and a bind of two parts are not self-references.
	m := mustHCL(t, "entity \"N\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"o\" {\n    entity = \"other.N\"\n  }\n  property \"b\" {\n    entity = \"N\"\n  }\n}\nrecordset \"r\" {\n  column \"c\" {\n    type = \"int\"\n    bind = \"N.id\"\n  }\n}\n")
	if _, err := m.JSON(ModuleIdentity{ID: "x/m", Name: "Tree", Version: "1"}); err != nil {
		t.Errorf("no self-reference: %v", err)
	}
	// The drift check says the same through the committed file's name.
	named := mustHCL(t, "entity \"N\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"p\" {\n    entity = \"m.N\"\n  }\n}\n")
	if d := named.ExportDrift([]byte(`{"modelspec": "1.0-draft", "module": {"id": "x", "name": "Tree", "version": "1"}}`), ModuleIdentity{}); !strings.Contains(d, "refers to its own module") {
		t.Errorf("drift = %q", d)
	}
}
