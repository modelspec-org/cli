package modelspec

import (
	"strings"
	"testing"
)

const jsonExt = ".modelspec.json"

// doc builds a JSON document from the module, then the given members.
func doc(members ...string) string {
	all := append([]string{`"modelspec": "1.0-draft"`, `"module": {"id": "x/y", "name": "y", "version": "1"}`}, members...)
	return "{\n" + strings.Join(all, ",\n") + "\n}\n"
}

const (
	jEntities = `"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`
)

func TestParseJSONFindings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", doc(jEntities), nil},
		{"syntax", "{", []string{"1: error: not valid JSON: unexpected end of JSON input [syntax]"}},
		{"not an object", "1", []string{"must be an object, not a number"}},
		{"duplicate top-level", doc(jEntities, jEntities), []string{`duplicate top-level field "entities"`}},
		{"unknown top-level", doc(jEntities, `"extra": 1`), []string{`unknown top-level field "extra"`}},
		{"projections and migrations", doc(jEntities, `"projections": {"p": {}}`, `"migrations": {"m": {}}`), nil},
		{"projections not an object", doc(jEntities, `"projections": []`), []string{`"projections" must be an object, not an array`}},
		{"migrations not an object", doc(jEntities, `"migrations": 1`), []string{`"migrations" must be an object, not a number`}},
		{"no modelspec", `{"module": {"id": "a", "name": "y", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`, []string{`has no "modelspec" version`}},
		{"modelspec not a string", `{"modelspec": 1, "module": {"id": "a", "name": "y", "version": "1"}, ` + jEntities + `}`, []string{`has no "modelspec" version`}},
		{"modelspec wrong value", `{"modelspec": "2", "module": {"id": "a", "name": "y", "version": "1"}, ` + jEntities + `}`, []string{`"modelspec" is "2"; the only defined value is "1.0-draft"`}},
		{"no module", `{"modelspec": "1.0-draft", ` + jEntities + `}`, []string{`has no module object`}},
		{"module not an object", `{"modelspec": "1.0-draft", "module": "y", ` + jEntities + `}`, []string{`:1: error: has no module object`}},
		{"module parts missing", `{"modelspec": "1.0-draft", "module": {"id": 1, "name": "", "version": null}, ` + jEntities + `}`, []string{"has no module.id", "has no module.name", "has no module.version"}},
		{"module name not an identifier", `{"modelspec": "1.0-draft", "module": {"id": "a", "name": "my-mod", "version": "1"}, ` + jEntities + `}`, []string{`module.name that is an identifier: "my-mod"`}},
		{"no entities", doc(), []string{"has no entities"}},
		{"empty entities", doc(`"entities": {}`), []string{"has no entities"}},
		{"entities not an object", doc(`"entities": []`), []string{`"entities" must be an object keyed by name, not an array`}},
		{"duplicate concept", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}, "A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`), []string{`duplicate entity "A" in "entities"`}},
		{"concept not an object", doc(`"entities": {"A": 1, "B": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`), []string{`entity "A" must be an object, not a number`}},
		{"entity without properties", doc(`"entities": {"A": {"key": ["id"]}}`), []string{"entity A has no properties", `key "id" is not a property`}},
		{"entity with empty properties", doc(`"entities": {"A": {"key": ["id"], "properties": {}}}`), []string{"entity A has no properties", `key "id" is not a property`}},
		{"properties not an object", doc(`"entities": {"A": {"key": ["id"], "properties": []}}`), []string{"properties must be an object keyed by name, not an array", `key "id" is not a property`}},
		{"component with empty fields is fine", doc(jEntities, `"components": {"C": {"fields": {}}}`), nil},
		{"component without fields is fine", doc(jEntities, `"components": {"C": {}}`), nil},
		{"member not an object", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": 1}}}`), []string{`property "id" must be an object, not a number`, `key "id" is not a property`}},
		{"enums", doc(jEntities, `"enums": {"E": {"values": ["a", "b"]}}`), nil},
		{"recordset columns", doc(jEntities, `"recordsets": {"r": {"columns": [{"name": "a", "type": "int"}, {"name": "a", "type": "int"}]}}`), nil},
		{"recordset columns not an array", doc(jEntities, `"recordsets": {"r": {"columns": {}}}`), []string{"columns must be an array, not an object"}},
		{"recordset column not an object", doc(jEntities, `"recordsets": {"r": {"columns": [1]}}`), []string{"each column must be an object, not a number"}},
		{"recordset column without name", doc(jEntities, `"recordsets": {"r": {"columns": [{"type": "int"}, {"name": 1}]}}`), []string{"each column needs a string name", "each column needs a string name"}},
		{"recordset without columns", doc(jEntities, `"recordsets": {"r": {}}`), nil},
		{"collection", doc(jEntities, `"collections": {"c": {"kind": "editable", "source": "A", "fields": {"id": {"type": "int", "bind": "A.id"}}}}`), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(map[string]string{"a" + jsonExt: tc.src}), tc.want...)
		})
	}
}

func TestParseJSONModel(t *testing.T) {
	t.Parallel()
	src := doc(jEntities, `"projections": {"p": {}}`, `"migrations": {"m": {}}`, `"recordsets": {"r": {"key": ["a"], "columns": [{"name": "a", "type": "int"}]}}`)
	m, fs := ParseJSON("dir/a"+jsonExt, []byte(src))
	if len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	if m.Form != FormJSON || m.Name != "y" || m.Module == nil || m.Module.ID != "x/y" || m.Module.Version != "1" {
		t.Fatalf("model = %+v module %+v", m, m.Module)
	}
	if m.Projections == nil || m.Migrations == nil {
		t.Fatal("projections or migrations lost")
	}
	r := m.Concepts[1]
	if r.Kind != KindRecordset || r.Members[0].Name != "a" || len(r.Members[0].Attrs) != 1 || r.Members[0].Attrs[0].Name != "type" {
		t.Fatalf("recordset = %+v", r)
	}
	if !IsModelFile("x"+hclExt) || !IsModelFile("x"+jsonExt) || IsModelFile("x.json") {
		t.Fatal("IsModelFile wrong")
	}
	if memberWord(KindEntity) != "property" || memberWord(KindRecordset) != "column" || memberWord(KindComponent) != "field" || memberWord(KindCollection) != "field" {
		t.Fatal("memberWord wrong")
	}
}

// A JSON model and an HCL model of the same module resolve each other's
// qualified references by module name.
func TestCheckAcrossForms(t *testing.T) {
	t.Parallel()
	core := `{"modelspec": "1.0-draft", "module": {"id": "x/core", "name": "core", "version": "1"}, "entities": {"Space": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`
	booking := "entity \"B\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"s\" {\n    entity = \"core.Space\"\n  }\n}\n"
	expect(t, run(map[string]string{"any" + jsonExt: core, "b" + hclExt: booking}))
}
