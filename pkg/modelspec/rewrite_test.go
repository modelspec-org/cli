package modelspec

import (
	"strings"
	"testing"
)

// rewriteOK rewrites a source that must be rewritable and returns the result and
// the count.
func rewriteOK(t *testing.T, file, src string) (string, int) {
	t.Helper()
	out, n, err := Rewrite(file, []byte(src))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return string(out), n
}

func TestRewriteHCLReplacesOnlyTheOldSpellings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src, want string
		count           int
	}{
		{"everything, with its comments, spacing and alignment", `# entity "X" and property "y" and entity = "z" are only a comment here.
entity   "Order" {   # a trailing comment about the entity
  key = ["id"]

  property "id" {
    type = "uuid"
  }
  property  "customer" {
    entity   = "Customer"
    required = true
  }
}
`, `# entity "X" and property "y" and entity = "z" are only a comment here.
record   "Order" {   # a trailing comment about the entity
  key = ["id"]

  field "id" {
    type = "uuid"
  }
  field  "customer" {
    record   = "Customer"
    required = true
  }
}
`, 4},
		{"CRLF line endings, tabs and no final newline", "entity \"A\" {\r\n\tproperty \"p\" {\r\n\t\tentity\t= \"A\"\r\n\t}\r\n}", "record \"A\" {\r\n\tfield \"p\" {\r\n\t\trecord\t= \"A\"\r\n\t}\r\n}", 3},
		{"offsets after text that the parser's input changes", `record "A" {
  field "p" {
    type    = "string"
    pattern = "a$b%c$${"
    format  = <<EOT
$1 and $$ 100%
EOT
  }
}
entity "B" {
  property "q" {
    type    = "string"
    pattern = "$$ $ %"
    entity  = "A"
  }
}
`, `record "A" {
  field "p" {
    type    = "string"
    pattern = "a$b%c$${"
    format  = <<EOT
$1 and $$ 100%
EOT
  }
}
record "B" {
  field "q" {
    type    = "string"
    pattern = "$$ $ %"
    record  = "A"
  }
}
`, 3},
		{"names and strings that look like the old words", "record \"entity\" {\n  property \"entity\" {\n    type = \"entity\"\n    pattern = \"property\"\n  }\n  field \"property\" {\n    entity = \"entity\"\n  }\n}\n", "record \"entity\" {\n  field \"entity\" {\n    type = \"entity\"\n    pattern = \"property\"\n  }\n  field \"property\" {\n    record = \"entity\"\n  }\n}\n", 2},
		{"an attribute of the block itself is not a member's", "entity \"A\" {\n  entity = \"B\"\n}\n", "record \"A\" {\n  entity = \"B\"\n}\n", 1},
		{"a component's members are not renamed, their references are", "component \"C\" {\n  field \"f\" {\n    entity = \"A\"\n  }\n}\n", "component \"C\" {\n  field \"f\" {\n    record = \"A\"\n  }\n}\n", 1},
		{"a label that is wrong still has its block type renamed", "entity {\n}\nentity \"A\" \"B\" {\n  property {\n  }\n}\nentity \"C\" {\n  property \"a\" \"b\" {\n  }\n}\n", "record {\n}\nrecord \"A\" \"B\" {\n  property {\n  }\n}\nrecord \"C\" {\n  field \"a\" \"b\" {\n  }\n}\n", 4},
		{"already in the new spelling", "record \"A\" {\n  field \"f\" {\n    record = \"A\"\n  }\n}\n", "record \"A\" {\n  field \"f\" {\n    record = \"A\"\n  }\n}\n", 0},
		{"nothing at all", "", "", 0},
		{"only a comment, no final newline", "# entity", "# entity", 0},
		{"a file with other mistakes is rewritten", "entity \"A\" {\n  key = [\"nope\"]\n  property \"id\" {\n    type = \"nonsense\"\n  }\n}\n", "record \"A\" {\n  key = [\"nope\"]\n  field \"id\" {\n    type = \"nonsense\"\n  }\n}\n", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, n := rewriteOK(t, "a"+hclExt, tc.src)
			if got != tc.want || n != tc.count {
				t.Errorf("got %d replacements:\n%s\nwant %d:\n%s", n, got, tc.count, tc.want)
			}
			// Rewriting what it wrote changes nothing.
			if again, n := rewriteOK(t, "a"+hclExt, got); again != got || n != 0 {
				t.Errorf("rewriting the result again: %d replacements\n%s", n, again)
			}
		})
	}
}

func TestRewriteJSONReplacesOnlyTheOldSpellings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src, want string
		count           int
	}{
		{"compact, with every spelling", `{"modelspec":"1.0-draft","module":{"id":"a","version":"1"},"entities":{"A":{"key":["id"],"properties":{"id":{"type":"int"},"r":{"entity":"A"}}}},"components":{"C":{"fields":{"x":{"entity":"A"}}}}}`,
			`{"modelspec":"1.0-draft-2","module":{"id":"a","version":"1"},"records":{"A":{"key":["id"],"fields":{"id":{"type":"int"},"r":{"record":"A"}}}},"components":{"C":{"fields":{"x":{"record":"A"}}}}}`, 5},
		{"spacing, tabs, CRLF and no final newline", "{\r\n\t\"modelspec\" :\t\"1.0-draft\" ,\r\n\t\"module\": {\"id\": \"a\", \"version\": \"1\"},\r\n\t\"entities\" : {\r\n\t\t\"A\": { \"properties\": { \"p\": { \"entity\": \"A\" } } }\r\n\t}\r\n}",
			"{\r\n\t\"modelspec\" :\t\"1.0-draft-2\" ,\r\n\t\"module\": {\"id\": \"a\", \"version\": \"1\"},\r\n\t\"records\" : {\r\n\t\t\"A\": { \"fields\": { \"p\": { \"record\": \"A\" } } }\r\n\t}\r\n}", 4},
		{"key order, other keys and values that look like keys are kept", `{"notes": "entities properties entity", "entities": {"properties": {"properties": {"entity": {"type": "string"}}}}, "modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}}`,
			`{"notes": "entities properties entity", "records": {"properties": {"fields": {"entity": {"type": "string"}}}}, "modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`, 3},
		{"keys and the identifier written with escapes", `{"modelspec": "1.0-\u0064raft", "module": {"id": "a", "version": "1"}, "ent\u0069ties": {"A": {"\u0070roperties": {"p": {"\u0065ntity": "A"}}}}}`,
			`{"modelspec": "2.0", "module": {"id": "a", "version": "1"}, "records": {"A": {"fields": {"p": {"record": "A"}}}}}`, 4},
		{"a repeated key is renamed wherever it is", `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}, "entities": {"A": {"properties": {"p": {"entity": "A", "entity": "A"}}}}, "entities": {"B": {"properties": {"q": {"entity": "A"}}}}}`,
			`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, "records": {"A": {"fields": {"p": {"record": "A", "record": "A"}}}}, "records": {"B": {"fields": {"q": {"record": "A"}}}}}`, 8},
		{"values that are not objects are left as they are", `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}, "components": [], "entities": {"A": 1, "B": {"key": []}, "C": {"properties": []}, "D": {"properties": {"p": 1}}}}`,
			`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, "components": [], "records": {"A": 1, "B": {"key": []}, "C": {"fields": []}, "D": {"fields": {"p": 1}}}}`, 4},
		{"entities that is not an object", `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}, "entities": []}`, `{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, "records": []}`, 2},
		{"the identifier alone", `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}}`, `{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`, 1},
		{"already in the new format", `{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, "records": {"A": {"fields": {"p": {"record": "A"}}}}}`, `{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}, "records": {"A": {"fields": {"p": {"record": "A"}}}}}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, n := rewriteOK(t, "a"+jsonExt, tc.src)
			want := tc.want
			if tc.name == "keys and the identifier written with escapes" {
				// The identifier is "1.0-draft" spelled with an escape: a plain "1.0-draft-2" replaces it.
				want = strings.Replace(want, `"2.0"`, `"1.0-draft-2"`, 1)
			}
			if got != want || n != tc.count {
				t.Errorf("got %d replacements:\n%s\nwant %d:\n%s", n, got, tc.count, want)
			}
			if again, n := rewriteOK(t, "a"+jsonExt, got); again != got || n != 0 {
				t.Errorf("rewriting the result again: %d replacements\n%s", n, again)
			}
		})
	}
}

func TestRewriteRefusals(t *testing.T) {
	t.Parallel()
	const m = `"module": {"id": "a", "version": "1"}`
	tests := []struct {
		name, file, src, want string
	}{
		{"HCL that does not parse", "a" + hclExt, "entity \"A\" {\n", "does not parse (Unclosed configuration block)"},
		{"HCL with an expression", "a" + hclExt, "entity \"A\" {\n  key = upper(\"a\")\n}\n", "does not parse"},
		{"HCL that is not UTF-8", "a" + hclExt, "entity \"A\" {\n}\n\xff", "does not parse (not valid UTF-8"},
		{"a collection", "a" + hclExt, "entity \"A\" {\n}\ncollection \"c\" {\n}\n", "it holds the collection block at line 3, a construct decision 0019 removed"},
		{"a recordset", "a" + hclExt, "recordset \"c\" {\n}\n", "it holds the recordset block at line 1, a construct decision 0019 removed"},
		{"a projection", "a" + hclExt, "projection \"c\" {\n}\n", "it holds the projection block at line 1, a word decision 0019 reserved"},
		{"a migration", "a" + hclExt, "entity \"A\" {\n}\nmigration \"m\" {\n}\n", "migration block at line 3"},
		{"an index", "a" + hclExt, "entity \"A\" {\n  index \"i\" {\n  }\n}\n", "it holds the index block at line 2, a word decision 0019 reserved"},
		{"a member with both record and entity", "a" + hclExt, "record \"A\" {\n  field \"f\" {\n    record = \"A\"\n    entity = \"A\"\n  }\n}\n", "a member has both record and entity (line 4)"},
		{"JSON that does not parse", "a" + jsonExt, "{", "does not parse (not valid JSON"},
		{"JSON that is not an object", "a" + jsonExt, "[]", "does not parse (a ModelSpec JSON document must be an object"},
		{"JSON that is not UTF-8", "a" + jsonExt, "{\"modelspec\": \"1.0-draft\"}\xff", "does not parse (not valid UTF-8"},
		{"JSON without an identifier", "a" + jsonExt, `{` + m + `, "entities": {}}`, `it has no "modelspec" format identifier`},
		{"JSON with an identifier that is not a string", "a" + jsonExt, `{"modelspec": 1, ` + m + `}`, `it has no "modelspec" format identifier`},
		{"JSON with another identifier", "a" + jsonExt, `{"modelspec": "2", ` + m + `, "entities": {}}`, `its "modelspec" is "2", which is neither "1.0-draft" nor "1.0-draft-2"`},
		{"entities in a 1.0-draft-2 document", "a" + jsonExt, `{"modelspec": "1.0-draft-2", ` + m + `, "entities": {}}`, `it uses the key "entities" of format 1.0-draft in a 1.0-draft-2 document`},
		{"records in a 1.0-draft document", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "records": {}}`, `it uses the key "records" of format 1.0-draft-2 in a 1.0-draft document`},
		{"two lists of members in a record", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "entities": {"A": {"properties": {}, "properties": {}}}}`, `record "A" has two lists of members, "properties" among them`},
		{"two lists of members in a component", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "components": {"C": {"fields": {}, "fields": {}}}}`, `component "C" has two lists of members`},
		{"collections", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "collections": {}}`, `it holds "collections", a construct decision 0019 removed`},
		{"recordsets", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "recordsets": {}}`, `it holds "recordsets"`},
		{"projections", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "projections": {}}`, `it holds "projections", a word decision 0019 reserved`},
		{"migrations", "a" + jsonExt, `{"modelspec": "1.0-draft", ` + m + `, "migrations": {}}`, `it holds "migrations"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, n, err := Rewrite(tc.file, []byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) || out != nil || n != 0 {
				t.Errorf("got %q, %d, %v; want an error containing %q", out, n, err, tc.want)
			}
		})
	}
}

// A file larger than the limit is not read, so it is not rewritten.
func TestRewriteRefusesWhatIsTooLarge(t *testing.T) {
	t.Parallel()
	src := "# " + strings.Repeat("a", MaxInputBytes) + "\n"
	if _, _, err := Rewrite("a"+hclExt, []byte(src)); err == nil || !strings.Contains(err.Error(), "does not parse (file is") {
		t.Errorf("err = %v", err)
	}
}

// The rewritten text is read again and must be the same model with no old spelling
// left; a rewrite that is not is refused, and nothing is returned.
func TestRewriteChecksItsOwnResult(t *testing.T) {
	t.Parallel()
	src := []byte("entity \"A\" {\n  property \"p\" {\n    entity = \"A\"\n  }\n}\n")
	for name, apply := range map[string]func([]byte, []OldSpelling) []byte{
		"edits that change nothing": func(src []byte, _ []OldSpelling) []byte { return src },
		"edits that make a mistake": func(src []byte, e []OldSpelling) []byte { return applyEdits(src, e[:1]) },
		"edits that change the model": func(src []byte, e []OldSpelling) []byte {
			return []byte(strings.Replace(string(applyEdits(src, e)), `"A"`, `"B"`, 1))
		},
		"edits that break the syntax": func(src []byte, e []OldSpelling) []byte {
			return []byte(strings.Replace(string(applyEdits(src, e)), "{", "", 1))
		},
		"edits that move a finding": func(src []byte, e []OldSpelling) []byte { return []byte("\n" + string(applyEdits(src, e))) },
		"edits that add a finding":  func(src []byte, e []OldSpelling) []byte { return []byte(string(applyEdits(src, e)) + "x = 1\n") },
		"edits that change a finding": func(src []byte, e []OldSpelling) []byte {
			return []byte(strings.Replace(string(applyEdits(src, e)), "record = ", "record = 1 + ", 1))
		},
	} {
		out, n, err := rewriteWith("a"+hclExt, src, apply)
		if err == nil || !strings.Contains(err.Error(), "does not read as the same model") || out != nil || n != 0 {
			t.Errorf("%s: got %q, %d, %v", name, out, n, err)
		}
	}
	out, n, err := rewriteWith("a"+hclExt, src, applyEdits)
	if err != nil || n != 3 || string(out) != "record \"A\" {\n  field \"p\" {\n    record = \"A\"\n  }\n}\n" {
		t.Errorf("the real edits: %q, %d, %v", out, n, err)
	}
}

func TestSameModelAndSameFindings(t *testing.T) {
	t.Parallel()
	hcl := func(src string) *Model {
		m, _ := ParseHCL("a"+hclExt, []byte(src))
		return m
	}
	json := func(src string) *Model {
		m, _ := ParseJSON("a"+jsonExt, []byte(src))
		return m
	}
	base := "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	for name, tc := range map[string]struct {
		a, b *Model
		want string
	}{
		"the same":                       {hcl(base), hcl(base), ""},
		"another form":                   {hcl(base), json(`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`), "the module"},
		"another module name":            {hcl(base), func() *Model { m := hcl(base); m.Name = "b"; return m }(), "the module"},
		"a module on one side":           {json(`{"modelspec": "1.0-draft-2"}`), json(`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`), "the module"},
		"another module":                 {json(`{"modelspec": "1.0-draft-2", "module": {"id": "a", "version": "1"}}`), json(`{"modelspec": "1.0-draft-2", "module": {"id": "b", "version": "1"}}`), "the module"},
		"another number of concepts":     {hcl(base), hcl(base + base), "the number of concepts"},
		"another kind":                   {hcl(base), hcl(strings.Replace(base, "record", "component", 1)), `concept "A"`},
		"another name":                   {hcl(base), hcl(strings.Replace(base, `"A"`, `"B"`, 1)), `concept "A"`},
		"another line":                   {hcl(base), hcl("\n" + base), `concept "A"`},
		"another number of members":      {hcl(base), hcl(strings.Replace(base, "  }\n}", "  }\n  field \"x\" {\n    type = \"int\"\n  }\n}", 1)), `concept "A"`},
		"another attribute of a concept": {hcl(base), hcl(strings.Replace(base, `["id"]`, `["x"]`, 1)), `the attributes of "A"`},
		"another attribute name":         {hcl(base), hcl(strings.Replace(base, "key", "use", 1)), `the attributes of "A"`},
		"another attribute line":         {hcl(base), hcl(strings.Replace(base, "key = [\"id\"]\n", "\nkey = [\"id\"]\n", 1)), `the attributes of "A"`},
		"another number of attributes":   {hcl(base), hcl(strings.Replace(base, "  key = [\"id\"]\n", "", 1)), `the attributes of "A"`},
		"another member name":            {hcl(base), hcl(strings.Replace(base, `"id" {`, `"ids" {`, 1)), `member "id" of "A"`},
		"another member attribute":       {hcl(base), hcl(strings.Replace(base, `"int"`, `"uuid"`, 1)), `member "id" of "A"`},
	} {
		if got := sameModel(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameModel = %q, want %q", name, got, tc.want)
		}
	}
	f := func(line int, rule string, sev Severity) Finding {
		return Finding{Line: line, Rule: rule, Severity: sev}
	}
	a := []Finding{f(1, "x", SeverityError), f(2, "y", SeverityWarning)}
	if !sameFindings(a, []Finding{f(2, "y", SeverityWarning), f(1, "x", SeverityError)}) || !sameFindings(nil, nil) {
		t.Error("the same findings in another order are not the same")
	}
	for name, b := range map[string][]Finding{
		"fewer":            a[:1],
		"another line":     {f(1, "x", SeverityError), f(3, "y", SeverityWarning)},
		"another rule":     {f(1, "x", SeverityError), f(2, "z", SeverityWarning)},
		"another severity": {f(1, "x", SeverityError), f(2, "y", SeverityError)},
	} {
		if sameFindings(a, b) {
			t.Errorf("%s: the same", name)
		}
	}
}

// The old spellings are recorded where the source has them: the finding and the
// rewrite are made from the same list.
func TestOldSpellingsAreRecordedWhereTheSourceHasThem(t *testing.T) {
	t.Parallel()
	src := "# entity\nentity \"A\" {\n  property \"p\" {\n    entity = \"A\"\n  }\n}\n"
	m, _ := ParseHCL("a"+hclExt, []byte(src))
	want := []OldSpelling{{2, 9, 15, "entity", "record"}, {3, 28, 36, "property", "field"}, {4, 49, 55, "entity", "record"}}
	// (Offsets: "# entity\n" is 9 bytes, so the block type starts at 9.)
	want[1].Start, want[1].End = strings.Index(src, "property"), strings.Index(src, "property")+len("property")
	want[2].Start, want[2].End = strings.LastIndex(src, "entity"), strings.LastIndex(src, "entity")+len("entity")
	if len(m.Old) != 3 {
		t.Fatalf("Old = %+v", m.Old)
	}
	for i, w := range want {
		if m.Old[i] != w {
			t.Errorf("Old[%d] = %+v, want %+v", i, m.Old[i], w)
		}
	}
	j := `{"modelspec": "1.0-draft", "module": {"id": "a", "version": "1"}, "entities": {"A": {"properties": {"p": {"entity": "A"}}}}}`
	mj, _ := ParseJSON("a"+jsonExt, []byte(j))
	var got []string
	for _, o := range mj.Old {
		if j[o.Start:o.End] != o.Old {
			t.Errorf("%+v is not the text at its place: %q", o, j[o.Start:o.End])
		}
		got = append(got, o.Old+">"+o.New)
	}
	if strings.Join(got, " ") != `"1.0-draft">"1.0-draft-2" "entities">"records" "properties">"fields" "entity">"record"` {
		t.Errorf("Old = %v", got)
	}
}
