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
		{"duplicate top-level", doc(jEntities, jEntities), []string{`duplicate key "entities"`}},
		{"unknown top-level is a warning", doc(jEntities, `"extra": 1`), []string{`warning: unknown top-level field "extra"`}},
		{"$schema is accepted", doc(jEntities, `"$schema": "https://modelspec.org/schema/modelspec-ast-1.0-draft.schema.json"`), nil},
		{"projections and migrations", doc(jEntities, `"projections": {"p": {}}`, `"migrations": {"m": {}}`), nil},
		{"projections not an object", doc(jEntities, `"projections": []`), []string{`"projections" must be an object, not an array`}},
		{"migrations not an object", doc(jEntities, `"migrations": 1`), []string{`"migrations" must be an object, not a number`}},
		{"no modelspec", `{"module": {"id": "a", "name": "y", "version": "1"}, "entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`, []string{`has no "modelspec" version`}},
		{"modelspec not a string", `{"modelspec": 1, "module": {"id": "a", "name": "y", "version": "1"}, ` + jEntities + `}`, []string{`has no "modelspec" version`}},
		{"modelspec wrong value", `{"modelspec": "2", "module": {"id": "a", "name": "y", "version": "1"}, ` + jEntities + `}`, []string{`"modelspec" is "2"; the only defined value is "1.0-draft"`}},
		{"no module", `{"modelspec": "1.0-draft", ` + jEntities + `}`, []string{`has no module object`}},
		{"module not an object", `{"modelspec": "1.0-draft", "module": "y", ` + jEntities + `}`, []string{`:1: error: has no module object`}},
		{"module id and version missing", `{"modelspec": "1.0-draft", "module": {"id": 1, "name": "", "version": null}, ` + jEntities + `}`, []string{"has no module.id", "has no module.name", "has no module.version"}},
		{"module.name is optional", `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}, ` + jEntities + `}`, nil},
		{"module.name need not be an identifier", `{"modelspec": "1.0-draft", "module": {"id": "a", "name": "todo-app", "version": "1"}, ` + jEntities + `}`, nil},
		{"entities are optional", doc(`"enums": {"E": {"values": ["a"]}}`), nil},
		{"a document with no groups at all", doc(), nil},
		{"empty entities", doc(`"entities": {}`), nil},
		{"entities not an object", doc(`"entities": []`), []string{`"entities" must be an object keyed by name, not an array`}},
		{"duplicate concept", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}}}, "A": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`), []string{`duplicate key "A"`}},
		{"concept not an object", doc(`"entities": {"A": 1, "B": {"key": ["id"], "properties": {"id": {"type": "int"}}}}`), []string{`entity "A" must be an object, not a number`}},
		{"entity without properties is allowed", doc(`"entities": {"A": {"key": []}}`), []string{"has an empty key"}},
		{"entity with empty properties is allowed", doc(`"entities": {"A": {"key": [], "properties": {}}}`), []string{"has an empty key"}},
		{"properties not an object", doc(`"entities": {"A": {"key": ["id"], "properties": []}}`), []string{"properties must be an object keyed by name, not an array", `key "id" is not a property`}},
		{"component with empty fields", doc(jEntities, `"components": {"C": {"fields": {}}}`), nil},
		{"component without fields", doc(jEntities, `"components": {"C": {}}`), nil},
		{"member not an object", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": 1}}}`), []string{`property "id" must be an object, not a number`, `key "id" is not a property`}},
		{"duplicate key inside a property", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int", "type": "bogus"}}}}`), []string{`duplicate key "type"`}},
		{"duplicate key inside module", `{"modelspec": "1.0-draft", "module": {"id": "a", "id": "b", "version": "1"}, ` + jEntities + `}`, []string{`duplicate key "id"`}},
		{"duplicate property names", doc(`"entities": {"A": {"key": ["id"], "properties": {"id": {"type": "int"}, "id": {"type": "int"}}}}`), []string{`duplicate key "id"`}},
		{"duplicate keys in an array element", doc(jEntities, `"recordsets": {"r": {"columns": [{"name": "a", "name": "b", "type": "int"}]}}`), []string{`duplicate key "name"`}},
		{"enums", doc(jEntities, `"enums": {"E": {"values": ["a", "b"]}}`), nil},
		{"integer enum values", doc(jEntities, `"enums": {"E": {"values": [1, 2]}}`), nil},
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
	if m.Form != FormJSON || m.Name != "y" || m.Module == nil || m.Module.ID != "x/y" || m.Module.Version != "1" || m.ModuleLine != 3 {
		t.Fatalf("model = %+v module %+v", m, m.Module)
	}
	if m.Projections == nil || m.Migrations == nil {
		t.Fatal("projections or migrations lost")
	}
	r := m.Concepts[1]
	if r.Kind != KindRecordset || r.Members[0].Name != "a" || len(r.Members[0].Attrs) != 1 || r.Members[0].Attrs[0].Name != "type" {
		t.Fatalf("recordset = %+v", r)
	}
	// Without module.name the file name stands in.
	unnamed, _ := ParseJSON("dir/shared"+jsonExt, []byte(`{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}}`))
	if unnamed.Name != "shared" || unnamed.Module.Name != "" {
		t.Fatalf("unnamed = %+v", unnamed)
	}
	if !IsModelFile("x"+hclExt) || !IsModelFile("x"+jsonExt) || IsModelFile("x.json") {
		t.Fatal("IsModelFile wrong")
	}
	if memberWord(KindEntity) != "property" || memberWord(KindRecordset) != "column" || memberWord(KindComponent) != "field" || memberWord(KindCollection) != "field" {
		t.Fatal("memberWord wrong")
	}
}

// A JSON model and an HCL model resolve each other's qualified references by
// module name.
func TestCheckAcrossForms(t *testing.T) {
	t.Parallel()
	core := `{"modelspec": "1.0-draft", "module": {"id": "x/core", "name": "core", "version": "1"}, "entities": {"Space": {"key": ["id"], "properties": {"id": {"type": "int"}}}}}`
	booking := "entity \"B\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"s\" {\n    entity = \"core.Space\"\n  }\n}\n"
	expect(t, run(map[string]string{"any" + jsonExt: core, "b" + hclExt: booking}))
}

// What the readers refuse before parsing: input that is too large, not UTF-8, or
// nested too deeply for the parsers to survive.
func TestPrecheck(t *testing.T) {
	t.Parallel()
	deepHCL := strings.Repeat("[", MaxDepth+1)
	deepJSON := strings.Repeat("[", MaxDepth+1)
	tests := []struct {
		name string
		form Form
		src  string
		want string // "" means no finding
		line int
	}{
		{"fine", FormHCL, "entity \"A\" {\n  key = [\"id\"]\n}\n", "", 0},
		{"depth at the limit", FormHCL, "x = " + strings.Repeat("[", MaxDepth), "", 0},
		{"depth over the limit", FormHCL, "x = " + deepHCL, "nesting is deeper than 64 levels", 1},
		{"80000 nested brackets", FormHCL, strings.Repeat("[", 80000), "nesting is deeper", 1},
		{"parentheses and braces count in HCL", FormHCL, strings.Repeat("({", MaxDepth), "nesting is deeper", 1},
		{"line number of the excess", FormHCL, "\n\n" + deepHCL, "nesting is deeper", 3},
		{"closing brackets lower the depth", FormHCL, strings.Repeat("[]", 1000), "", 0},
		{"stray closers do not go negative", FormHCL, strings.Repeat("]", 100) + deepHCL[:MaxDepth], "", 0},
		{"brackets in strings are skipped", FormHCL, "x = \"" + deepHCL + "\"", "", 0},
		{"escapes in strings", FormHCL, "x = \"\\\"" + deepHCL + "\\\"\"\ny = \"a\nb\"\n", "", 0},
		{"brackets in # comments are skipped", FormHCL, "# " + deepHCL + "\nx = 1\n", "", 0},
		{"brackets in // comments are skipped", FormHCL, "// " + deepHCL + "\nx = 1\n", "", 0},
		{"brackets in block comments are skipped", FormHCL, "/* " + deepHCL + "\n" + deepHCL + " */\nx = 1\n", "", 0},
		{"depth after a block comment is counted", FormHCL, "/* a\nb */\n" + deepHCL, "nesting is deeper", 3},
		{"an unterminated block comment", FormHCL, "/* " + deepHCL, "", 0},
		{"a division is not a comment", FormHCL, "x = 1 / 2" + deepHCL, "nesting is deeper", 1},
		{"a slash at the end", FormHCL, "x = 1 /", "", 0},
		{"JSON depth over the limit", FormJSON, deepJSON, "nesting is deeper", 1},
		{"JSON parentheses are not brackets", FormJSON, strings.Repeat("(", 1000), "", 0},
		{"JSON # is not a comment", FormJSON, "#" + deepJSON, "nesting is deeper", 1},
		{"JSON strings with brackets", FormJSON, `["` + deepJSON + `"]`, "", 0},
		{"invalid UTF-8 names the line", FormJSON, "{\n\"a\": \"\xff\"}", "not valid UTF-8", 2},
		{"invalid UTF-8 after multibyte text", FormHCL, "# é\n\xc3(\n", "not valid UTF-8", 2},
		{"a truncated rune at the end", FormHCL, "x = 1\xe2\x82", "not valid UTF-8", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, bad := precheck("f", tc.form, []byte(tc.src))
			if tc.want == "" {
				if bad {
					t.Fatalf("unexpected finding %v", f)
				}
				return
			}
			if !bad || !strings.Contains(f.Message, tc.want) || f.Line != tc.line || f.Severity != SeverityError {
				t.Fatalf("finding = %+v (bad %v), want %q at line %d", f, bad, tc.want, tc.line)
			}
		})
	}
}

func TestPrecheckSizeLimit(t *testing.T) {
	t.Parallel()
	f, bad := precheck("f", FormHCL, make([]byte, MaxInputBytes+1))
	if !bad || f.Rule != RuleLimit || !strings.Contains(f.Message, "the limit is 16777216 bytes") || f.Line != 0 {
		t.Fatalf("finding = %+v", f)
	}
	if _, bad := precheck("f", FormHCL, make([]byte, MaxInputBytes)); bad {
		t.Fatal("a file of exactly the limit is refused")
	}
}

// The readers never parse a source the precheck refuses, so a hostile file is a
// finding and not a crash.
func TestReadersRefuseWhatPrecheckRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		parse func() (*Model, []Finding)
		rule  string
	}{
		{"HCL depth", func() (*Model, []Finding) {
			return ParseHCL("a"+hclExt, []byte(strings.Repeat("[", 80000)))
		}, RuleLimit},
		{"JSON depth", func() (*Model, []Finding) {
			return ParseJSON("a"+jsonExt, []byte(strings.Repeat("[", 80000)))
		}, RuleLimit},
		{"HCL encoding", func() (*Model, []Finding) { return ParseHCL("a"+hclExt, []byte("# \xff\n")) }, RuleEncoding},
		{"JSON encoding", func() (*Model, []Finding) { return ParseJSON("a"+jsonExt, []byte("{\"a\": \"\xff\"}")) }, RuleEncoding},
	} {
		m, fs := tc.parse()
		if !m.Broken || len(fs) != 1 || fs[0].Rule != tc.rule {
			t.Errorf("%s: broken %v, findings %v", tc.name, m.Broken, fs)
		}
	}
}
