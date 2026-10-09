package modelspec

import (
	"strings"
	"testing"
)

const jsonExt = ".modelspec.json"

// doc builds a JSON document from the module, then the given members.
func doc(members ...string) string {
	all := append([]string{`"modelspec": "1.0-draft-2"`, `"module": {"id": "x/y", "name": "y", "version": "1"}`}, members...)
	return "{\n" + strings.Join(all, ",\n") + "\n}\n"
}

// oldDoc is doc in the old format, 1.0-draft.
func oldDoc(members ...string) string {
	all := append([]string{`"modelspec": "1.0-draft"`, `"module": {"id": "x/y", "name": "y", "version": "1"}`}, members...)
	return "{\n" + strings.Join(all, ",\n") + "\n}\n"
}

const (
	jRecords  = `"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}`
	jEntities = `"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`
)

func TestParseJSONFindings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", doc(jRecords), nil},
		{"syntax", "{", []string{"1: error: not valid JSON: unexpected end of JSON input [syntax]"}},
		{"not an object", "1", []string{"must be an object, not a number"}},
		{"duplicate top-level", doc(jRecords, jRecords), []string{`duplicate key "records"`}},
		{"unknown top-level is a warning", doc(jRecords, `"extra": 1`), []string{`warning: unknown top-level field "extra"`}},
		{"$schema is accepted", doc(jRecords, `"$schema": "https://modelspec.org/schema/modelspec-ast-1.0-draft.schema.json"`), nil},
		{"projections and migrations are reserved", doc(jRecords, `"projections": {"p": {}}`, `"migrations": {"m": {}}`), []string{`:5: error: "projections" is a reserved word with no content yet (decision 0019)`, `:6: error: "migrations" is a reserved word with no content yet (decision 0019)`}},
		{"collections and recordsets were removed", doc(jRecords, `"collections": {"c": {}}`, `"recordsets": {"r": {"columns": []}}`), []string{`:5: error: "collections" was removed (decision 0019): a stored set of rows is described by the database's own description, and the shape of a result is a record with no key [removed-construct]`, `:6: error: "recordsets" was removed (decision 0019)`}},
		{"no modelspec", `{"module": {"id": "a", "name": "y", "version": "1"}, "records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`, []string{`has no "modelspec" version`}},
		{"modelspec not a string", `{"modelspec": 1, "module": {"id": "a", "name": "y", "version": "1"}, ` + jRecords + `}`, []string{`has no "modelspec" version`}},
		{"modelspec wrong value", `{"modelspec": "2", "module": {"id": "a", "name": "y", "version": "1"}, ` + jRecords + `}`, []string{`"modelspec" is "2"; the defined values are "1.0-draft-2", and "1.0-draft" the old one`}},
		{"no module", `{"modelspec": "1.0-draft-2", ` + jRecords + `}`, []string{`has no module object`}},
		{"module not an object", `{"modelspec": "1.0-draft-2", "module": "y", ` + jRecords + `}`, []string{`:1: error: has no module object`}},
		{"module id and version missing", `{"modelspec": "1.0-draft-2", "module": {"id": 1, "name": "", "version": null}, ` + jRecords + `}`, []string{"has no module.id", "has no module.name", "has no module.version"}},
		{"module.name is optional", `{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, ` + jRecords + `}`, nil},
		{"module.name need not be an identifier", `{"modelspec": "1.0-draft-2", "module": {"id": "a", "name": "todo-app", "version": "1"}, ` + jRecords + `}`, nil},
		{"records are optional", doc(`"enums": {"E": {"values": ["a"]}}`), nil},
		{"a document with no groups at all", doc(), nil},
		{"empty records", doc(`"records": {}`), nil},
		{"records not an object", doc(`"records": []`), []string{`"records" must be an object keyed by name, not an array`}},
		{"duplicate concept", doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}, "A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}`), []string{`duplicate key "A"`}},
		{"concept not an object", doc(`"records": {"A": 1, "B": {"key": ["id"], "fields": {"id": {"type": "int"}}}}`), []string{`record "A" must be an object, not a number`}},
		{"record without fields is allowed", doc(`"records": {"A": {"key": []}}`), []string{"has an empty key"}},
		{"record with empty fields is allowed", doc(`"records": {"A": {"key": [], "fields": {}}}`), []string{"has an empty key"}},
		{"fields not an object", doc(`"records": {"A": {"key": ["id"], "fields": []}}`), []string{"fields must be an object keyed by name, not an array", `key "id" is not a field`}},
		{"component with empty fields", doc(jRecords, `"components": {"C": {"fields": {}}}`), nil},
		{"component without fields", doc(jRecords, `"components": {"C": {}}`), nil},
		{"member not an object", doc(`"records": {"A": {"key": ["id"], "fields": {"id": 1}}}`), []string{`field "id" must be an object, not a number`, `key "id" is not a field`}},
		{"duplicate key inside a field", doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int", "type": "bogus"}}}}`), []string{`duplicate key "type"`}},
		{"duplicate key inside module", `{"modelspec": "1.0-draft-2", "module": {"id": "a", "id": "b", "version": "1"}, ` + jRecords + `}`, []string{`duplicate key "id"`}},
		{"duplicate field names", doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}, "id": {"type": "int"}}}}`), []string{`duplicate key "id"`}},
		{"duplicate keys in an array element", doc(jRecords, `"extra": [{"name": "a", "name": "b"}]`), []string{`duplicate key "name"`, `unknown top-level field "extra"`}},
		{"enums", doc(jRecords, `"enums": {"E": {"values": ["a", "b"]}}`), nil},
		{"integer enum values", doc(jRecords, `"enums": {"E": {"values": [1, 2]}}`), nil},
		{"a component keeps fields", doc(jRecords, `"components": {"C": {"fields": {"x": {"record": "A"}}}}`), nil},
		{"fields of a record is an attribute of an enum", doc(jRecords, `"enums": {"E": {"values": ["a"], "fields": {}}}`), []string{`enum "E" has unsupported attribute "fields"`}},
		{"a second members key is an attribute", doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}, "properties": {}}}`), []string{`record "A" has unsupported attribute "properties"`}},
		// The identifier decides the vocabulary.
		{"the old vocabulary is read", oldDoc(jEntities, `"components": {"C": {"fields": {"x": {"entity": "A"}}}}`), []string{"deprecated-spelling"}},
		{"entities in a 1.0-draft-2 document", doc(jEntities), []string{`:4: error: "entities" is the key of format 1.0-draft; this document says "1.0-draft-2", where it is "records" [modelspec-version]`, `:4: error: "properties" is the key of format 1.0-draft`}},
		{"properties in a 1.0-draft-2 document", doc(`"records": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`), []string{`error: "properties" is the key of format 1.0-draft; this document says "1.0-draft-2", where it is "fields"`}},
		{"entity in a 1.0-draft-2 document", doc(`"records": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}, "p": {"entity": "A"}}}}`), []string{`error: "entity" is the key of format 1.0-draft; this document says "1.0-draft-2", where it is "record"`}},
		{"entity in a component of a 1.0-draft-2 document", doc(jRecords, `"components": {"C": {"fields": {"p": {"entity": "A"}}}}`), []string{`"entity" is the key of format 1.0-draft`}},
		{"records in a 1.0-draft document", oldDoc(jRecords), []string{`:4: error: "records" belongs to format 1.0-draft-2; this document says "1.0-draft", where the key is "entities" [modelspec-version]`, `:4: error: "fields" belongs to format 1.0-draft-2`, "deprecated-spelling"}},
		{"fields of a record in a 1.0-draft document", oldDoc(`"entities": {"A": {"key": ["id"], "fields": {"id": {"type": "int"}}}}`), []string{`error: "fields" belongs to format 1.0-draft-2; this document says "1.0-draft", where the key is "properties"`, "deprecated-spelling"}},
		{"record in a 1.0-draft document", oldDoc(`"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}, "p": {"record": "A"}}}}`), []string{`error: "record" belongs to format 1.0-draft-2; this document says "1.0-draft", where the key is "entity"`, "deprecated-spelling"}},
		{"another identifier reads either vocabulary and says nothing about keys", `{"modelspec": "2", "module": {"id": "a", "name": "y", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}, "records": {"B": {"fields": {}}}}`, []string{`"modelspec" is "2"`}},
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
	src := doc(jRecords, `"components": {"C": {"fields": {"a": {"type": "int"}}}}`)
	m, fs := ParseJSON("dir/a"+jsonExt, []byte(src))
	if len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	if m.Form != FormJSON || m.Name != "y" || m.Module == nil || m.Module.ID != "x/y" || m.Module.Version != "1" || m.ModuleLine != 3 {
		t.Fatalf("model = %+v module %+v", m, m.Module)
	}
	if len(m.Old) != 0 || m.OldVocabulary() {
		t.Fatalf("a 1.0-draft-2 document holds old spellings: %+v", m.Old)
	}
	r := m.Concepts[0]
	if r.Kind != KindComponent || r.Members[0].Name != "a" || len(r.Members[0].Attrs) != 1 || r.Members[0].Attrs[0].Name != "type" {
		t.Fatalf("component = %+v", r)
	}
	// Without module.name the file name stands in.
	unnamed, _ := ParseJSON("dir/shared"+jsonExt, []byte(`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`))
	if unnamed.Name != "shared" || unnamed.Module.Name != "" {
		t.Fatalf("unnamed = %+v", unnamed)
	}
	if !IsModelFile("x"+hclExt) || !IsModelFile("x"+jsonExt) || IsModelFile("x.json") {
		t.Fatal("IsModelFile wrong")
	}
	if KindRecord != "record" {
		t.Fatal("KindRecord is not record")
	}
}

// A JSON model and an HCL model resolve each other's qualified references by
// module name.
func TestCheckAcrossForms(t *testing.T) {
	t.Parallel()
	core := `{"modelspec": "1.0-draft-2", "module": {"id": "x/core", "name": "core", "version": "1"}, "records": {"Space": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`
	booking := "record \"B\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"s\" {\n    record = \"core.Space\"\n  }\n}\n"
	expect(t, run(map[string]string{"any" + jsonExt: core, "b" + hclExt: booking}))
}
