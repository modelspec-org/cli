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
	if _, err := mustHCL(t, exportSrc).JSON(ModuleIdentity{ID: "x/y", Name: "y"}); err == nil || !strings.Contains(err.Error(), "module.id, module.name and module.version") {
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
		if d := m.ExportDrift([]byte(committed), ModuleIdentity{}); !strings.Contains(d, "module.id, module.name and module.version") {
			t.Errorf("committed %s without identity: %q", committed, d)
		}
	}
}
