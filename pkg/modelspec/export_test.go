package modelspec

import (
	"fmt"
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
record "A" {
  key = ["id"]
  use = ["C"]
  field "id" {
    type = "int"
    required = true
  }
  field "e" {
    type = "string"
    enum = "E"
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
	if strings.Join(order, ",") != "modelspec,module,components,enums,records" {
		t.Fatalf("top-level order = %v", order)
	}
	got := string(n.Encode())
	for _, want := range []string{
		`"modelspec": "1.0-draft-2"`,
		`"id": "x/y"`,
		"      \"key\": [\n        \"id\"\n      ],\n      \"use\": [\n        \"C\"\n      ],\n      \"fields\": {",
		"\"records\": {\n    \"A\"",
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

func TestExportRecordWithoutFields(t *testing.T) {
	t.Parallel()
	n, err := mustHCL(t, "record \"Empty\" {\n  key = []\n}\n").JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(n.Encode()), "\"fields\": {}") {
		t.Fatalf("export:\n%s", n.Encode())
	}
}

// The export is written in the vocabulary of its source (decision 0022, step 1).
func TestExportWritesTheVocabularyOfItsSource(t *testing.T) {
	t.Parallel()
	const withRef = "record \"B\" {\n  key = []\n  %s \"p\" {\n    %s = \"B\"\n  }\n}\n"
	for name, tc := range map[string]struct {
		src                          string
		version, group, members, ref string
	}{
		"new spelling":                   {fmt.Sprintf(withRef, "field", "record"), "1.0-draft-2", `"records"`, `"fields"`, `"record"`},
		"old spelling of the block":      {strings.Replace(fmt.Sprintf(withRef, "field", "record"), "record \"B\"", "entity \"B\"", 1), "1.0-draft", `"entities"`, `"properties"`, `"entity"`},
		"old spelling of the member":     {fmt.Sprintf(withRef, "property", "record"), "1.0-draft", `"entities"`, `"properties"`, `"entity"`},
		"old spelling of the setting":    {fmt.Sprintf(withRef, "field", "entity"), "1.0-draft", `"entities"`, `"properties"`, `"entity"`},
		"nothing that has two spellings": {"enum \"E\" {\n  values = [\"a\"]\n}\n", "1.0-draft-2", `"enums"`, ``, ``},
	} {
		m := mustHCL(t, tc.src)
		n, err := m.JSON(testID)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := string(n.Encode())
		for _, want := range []string{`"modelspec": "` + tc.version + `"`, tc.group, tc.members, tc.ref} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: export lacks %s:\n%s", name, want, got)
			}
		}
		// The other vocabulary's keys are not there.
		other := []string{`"entities"`, `"properties"`, `"entity"`}
		if tc.version == "1.0-draft" {
			other = []string{`"records"`, `"fields"`, `"record"`}
		}
		for _, key := range other {
			if strings.Contains(got, key) {
				t.Errorf("%s: unexpected %s:\n%s", name, key, got)
			}
		}
		// It reads back as the same model and exports the same.
		back, parse := ParseJSON("m.modelspec.json", n.Encode())
		if len(parse) != 0 {
			t.Fatalf("%s: exported JSON has findings: %v", name, parse)
		}
		if again, err := back.JSON(ModuleIdentity{}); err != nil || Diff(n, again) != "" {
			t.Errorf("%s: JSON -> model -> JSON changed the document: %v %v", name, err, Diff(n, again))
		}
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
}

func TestExportDrift(t *testing.T) {
	t.Parallel()
	m := mustHCL(t, okRecord)
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
	if d := m.ExportDrift([]byte(stale), ModuleIdentity{}); !strings.Contains(d, `not what m.modelspec.hcl exports to: records.A.fields.id.type is "int" in the first and "string" in the second`) {
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
	m := mustHCL(t, okRecord)
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
		"a record reference":    "record \"Node\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"p\" {\n    record = \"m.Node\"\n  }\n}\n",
		"a component reference": "component \"C\" {\n}\nrecord \"N\" {\n  key = []\n  field \"c\" {\n    component = \"m.C\"\n  }\n}\n",
		"an enum reference":     "enum \"E\" {\n  values = [\"a\"]\n}\nrecord \"N\" {\n  key = []\n  field \"c\" {\n    type = \"string\"\n    enum = \"m.E\"\n  }\n}\n",
		"a use":                 "component \"C\" {\n}\nrecord \"N\" {\n  key = []\n  use = [\"m.C\"]\n}\n",
		"an old reference":      "entity \"Node\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"p\" {\n    entity = \"m.Node\"\n  }\n}\n",
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
	// No such reference: any name will do. Others' names and bare names are not self-references.
	m := mustHCL(t, "record \"N\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"o\" {\n    record = \"other.N\"\n  }\n  field \"b\" {\n    record = \"N\"\n  }\n}\n")
	if _, err := m.JSON(ModuleIdentity{ID: "x/m", Name: "Tree", Version: "1"}); err != nil {
		t.Errorf("no self-reference: %v", err)
	}
	// The drift check says the same through the committed file's name.
	named := mustHCL(t, "record \"N\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"p\" {\n    record = \"m.N\"\n  }\n}\n")
	if d := named.ExportDrift([]byte(`{"modelspec": "1.0-draft-2", "module": {"id": "x", "name": "Tree", "version": "1"}}`), ModuleIdentity{}); !strings.Contains(d, "refers to its own module") {
		t.Errorf("drift = %q", d)
	}
}

// A difference between a model's export and a JSON document in the other
// vocabulary says that modelspec rewrite brings them in line; between documents in
// the same vocabulary, or when the identifier is not one this tool knows, it says
// nothing of the sort.
func TestExportDriftNamesTheVocabulary(t *testing.T) {
	t.Parallel()
	m := mustHCL(t, okRecord)
	n, err := m.JSON(testID)
	if err != nil {
		t.Fatal(err)
	}
	good := string(n.Encode())
	old := strings.Replace(strings.Replace(strings.Replace(strings.Replace(good, "1.0-draft-2", "1.0-draft", 1), `"records"`, `"entities"`, 1), `"fields"`, `"properties"`, 1), `"int"`, `"string"`, 1)
	const note = "modelspec rewrite --write on both files brings the pair in line"
	if d := m.ExportDrift([]byte(old), ModuleIdentity{}); !strings.Contains(d, note) {
		t.Errorf("another vocabulary: %q", d)
	}
	for name, committed := range map[string]string{
		"the same vocabulary":       strings.Replace(good, `"int"`, `"string"`, 1),
		"another identifier":        strings.Replace(old, "1.0-draft", "3", 1),
		"an identifier that is not": strings.Replace(old, `"1.0-draft"`, "3", 1),
		"no identifier":             strings.Replace(old, `"modelspec": "1.0-draft",`, "", 1),
	} {
		if d := m.ExportDrift([]byte(committed), ModuleIdentity{}); d == "" || strings.Contains(d, note) {
			t.Errorf("%s: %q", name, d)
		}
	}
}

// A model that is the same text in both vocabularies (components and enums only, or
// nothing) exports as 1.0-draft-2, and is compared with a copy written as 1.0-draft
// in that vocabulary: the copy was right before the rename, and is not drift.
func TestExportOfAModelWithNoVocabularyMarker(t *testing.T) {
	t.Parallel()
	const src = "component \"Audit\" {\n  field \"at\" {\n    type = \"datetime\"\n  }\n}\nenum \"S\" {\n  values = [\"a\", \"b\"]\n}\n"
	for name, tc := range map[string]struct {
		model *Model
		free  bool
	}{
		"components and enums":                   {mustHCL(t, src), true},
		"nothing":                                {mustHCL(t, ""), true},
		"a record":                               {mustHCL(t, okRecord), false},
		"an old spelling":                        {mustHCL(t, "entity \"E\" {\n}\n"), false},
		"a reference to a record in a component": {mustHCL(t, "component \"C\" {\n  field \"r\" {\n    record = \"R\"\n  }\n}\n"), false},
	} {
		if got := tc.model.vocabularyFree(); got != tc.free {
			t.Errorf("%s: vocabularyFree = %v", name, got)
		}
	}
	m := mustHCL(t, src)
	plain, err := m.JSON(testID)
	if err != nil || !strings.Contains(string(plain.Encode()), `"modelspec": "1.0-draft-2"`) {
		t.Fatalf("a plain export is not 1.0-draft-2: %v\n%s", err, plain.Encode())
	}
	oldCopy := strings.Replace(string(plain.Encode()), "1.0-draft-2", "1.0-draft", 1)
	if d := m.ExportDrift([]byte(oldCopy), ModuleIdentity{}); d != "" {
		t.Errorf("a copy written as 1.0-draft is drift: %q", d)
	}
	if d := m.ExportDrift(plain.Encode(), ModuleIdentity{}); d != "" {
		t.Errorf("a copy written as 1.0-draft-2 is drift: %q", d)
	}
	// A real difference in the old copy is still reported, with no word about vocabularies.
	stale := strings.Replace(oldCopy, `"a"`, `"z"`, 1)
	if d := m.ExportDrift([]byte(stale), ModuleIdentity{}); !strings.Contains(d, "values[0]") || strings.Contains(d, "vocabular") {
		t.Errorf("drift = %q", d)
	}
	// The same through lint: the copy is a stale twin of nothing, and is an error only for being old.
	got := run(map[string]string{"core" + hclExt: src, "core.modelspec.json": strings.Replace(oldCopy, `"id": "x/y"`, `"id": "x/core"`, 1)})
	for _, g := range got {
		if strings.Contains(g, "stale") {
			t.Errorf("a stale twin: %s", g)
		}
	}
	expect(t, got, "core.modelspec.json:2: error: is in format 1.0-draft and holds 1 old spelling")
}
