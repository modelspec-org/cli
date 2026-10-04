package modelspec

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
)

func TestParseHCLShapeAndLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"clean", okEntity, nil},
		{"syntax error", "entity \"A\" {\n", []string{"a.modelspec.hcl:1: error: Unclosed configuration block [syntax]"}},
		{"top-level attribute", "x = 1\ny = 2\n" + okEntity, []string{`:1: error: top-level attribute "x" is not allowed`, `:2: error: top-level attribute "y" is not allowed`}},
		{"block without label", "entity {\n}\n", []string{":1: error: entity block needs exactly one name label"}},
		{"block with two labels", "enum \"a\" \"b\" {\n}\n", []string{"found 2 labels"}},
		{"projection without label", "projection {\n}\n", []string{"projection block needs exactly one"}},
		{"migration without label", "migration {\n}\n", []string{"migration block needs exactly one"}},
		{"unknown block", "table \"t\" {\n}\n", []string{`unknown block type "table"`}},
		{"member with two labels", "entity \"A\" {\n  property \"a\" \"b\" {\n  }\n}\n", []string{"property block needs exactly one name label"}},
		{"member containing a block", "entity \"A\" {\n  property \"a\" {\n    type = \"int\"\n    x \"y\" {\n    }\n  }\n}\n", []string{`property "a" cannot contain a "x" block`}},
		{"concept with a foreign block", "enum \"E\" {\n  values = [\"a\"]\n  property \"p\" {\n  }\n}\n", []string{`enum "E" cannot contain a "property" block`}},
		{"index without label", "entity \"A\" {\n  index {\n  }\n}\n", []string{"index block needs exactly one name label"}},
		{"non-literal function", "entity \"A\" {\n  key = upper(\"a\")\n}\n", []string{":2: error: a parenthesis (grouping or a function call) is not literal syntax"}},
		{"non-literal reference", "entity \"A\" {\n  key = foo\n}\n", []string{"must be a literal"}},
		{"template with interpolation", "entity \"A\" {\n  key = \"a${x}\"\n}\n", []string{":2: error: a template interpolation (`${`) is not literal syntax"}},
		{"interpolation", "entity \"A\" {\n  key = [\"${x}\"]\n}\n", []string{":2: error: a template interpolation (`${`) is not literal syntax"}},
		{"arithmetic", "entity \"A\" {\n  key = 1 + 1\n}\n", []string{":2: error: an arithmetic operator is not literal syntax"}},
		{"logical not", "entity \"A\" {\n  key = !true\n}\n", []string{":2: error: a logical operator (`!`) is not literal syntax"}},
		{"negated reference", "entity \"A\" {\n  key = -foo\n}\n", []string{":2: error: an operator (`-` is allowed only as the sign of a number) is not literal syntax"}},
		{"list with expression", "entity \"A\" {\n  key = [1 + 1]\n}\n", []string{":2: error: an arithmetic operator is not literal syntax"}},
		{"list with a reference", "entity \"A\" {\n  key = [foo]\n}\n", []string{`attribute "key": must be a literal`}},
		{"a block with a bare label", "entity A {\n}\n", nil},
		{"object value", "entity \"A\" {\n  key = { a = 1 }\n}\n", []string{"map-style values are not ModelSpec v0"}},
		{"null", "entity \"A\" {\n  key = null\n}\n", []string{"null is not a ModelSpec value"}},
		{"null in list", "entity \"A\" {\n  key = [null]\n}\n", []string{"null is not a ModelSpec value"}},
		{"nested list", "entity \"A\" {\n  key = [[\"a\"]]\n}\n", []string{"nested lists are not ModelSpec v0"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, fs := ParseHCL("a.modelspec.hcl", []byte(tc.src))
			var got []string
			for _, f := range fs {
				got = append(got, f.String())
			}
			expect(t, got, tc.want...)
		})
	}
}

func TestParseHCLValues(t *testing.T) {
	t.Parallel()
	src := `entity "A" {
  key = ["id", "b"]
  property "id" {
    type    = "int"
    max_len = 200
    min_len = -1
    ratio   = 1.5
    big     = 123456789012345678901234567890
    required = true
    heredoc = <<EOT
line
EOT
  }
  property "p" {
    type = "string"
  }
  index "i" {
    properties = ["id"]
  }
}

projection "sqlite" {
}

migration "2026-07-08-user-display-name" {
  from = "1.2.0"
  to   = "1.3.0"

  rename "User.fullName" {
    to = "User.displayName"
  }
}
`
	m, fs := ParseHCL("dir/shop.modelspec.hcl", []byte(src))
	if len(fs) != 0 {
		t.Fatalf("findings: %v", fs)
	}
	if m.Name != "shop" || m.Form != FormHCL || m.Module != nil {
		t.Fatalf("model = %+v", m)
	}
	if len(m.Unmapped) != 3 || m.Unmapped[0].What != `entity "A" index "i"` || m.Unmapped[0].Line != 17 || m.Unmapped[1].What != `projection "sqlite"` || m.Unmapped[2].What != `migration "2026-07-08-user-display-name"` {
		t.Fatalf("unmapped = %+v", m.Unmapped)
	}
	a := m.Concepts[0]
	key, _ := a.Attr("key")
	if got, _ := key.Value.stringList(); strings.Join(got, ",") != "id,b" || key.Line != 2 {
		t.Fatalf("key = %+v", key)
	}
	id, ok := a.Members[0].Attr("max_len")
	if !ok || id.Value.Type != NodeNumber || id.Value.Str != "200" {
		t.Fatalf("max_len = %+v", id)
	}
	for _, c := range []struct{ name, text string }{{"min_len", "-1"}, {"ratio", "1.5"}, {"big", "123456789012345678901234567890"}} {
		a, _ := a.Members[0].Attr(c.name)
		if a.Value.Str != c.text {
			t.Errorf("%s = %q, want %q", c.name, a.Value.Str, c.text)
		}
	}
	if h, _ := a.Members[0].Attr("heredoc"); h.Value.Str != "line\n" {
		t.Errorf("heredoc = %q", h.Value.Str)
	}
	if a.Members[0].Attrs[0].Name != "type" || a.Members[0].Attrs[1].Name != "max_len" {
		t.Errorf("attribute order = %+v", a.Members[0].Attrs)
	}
	if _, ok := a.Members[0].Attr("nope"); ok {
		t.Error("Attr found a missing attribute")
	}
	if _, ok := a.Attr("nope"); ok {
		t.Error("Attr found a missing attribute")
	}
	if !m.HasConcept(KindEntity, "A") || m.HasConcept(KindEnum, "A") || m.HasConcept(KindEntity, "B") {
		t.Error("HasConcept wrong")
	}
}

func TestDiagLineWithoutSubject(t *testing.T) {
	t.Parallel()
	if got := diagLine(&hcl.Diagnostic{}); got != 0 {
		t.Fatalf("diagLine = %d, want 0", got)
	}
}

func TestModuleNameFromFile(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"chinook.modelspec.hcl":          "chinook",
		"a/b/chinook.modelspec.json":     "chinook",
		`a\b\chinook.modelspec.hcl`:      "chinook",
		"plain":                          "plain",
		"x.modelspec.hcl.modelspec.json": "x.modelspec.hcl",
	}
	for in, want := range tests {
		if got := moduleNameFromFile(in); got != want {
			t.Errorf("moduleNameFromFile(%q) = %q, want %q", in, got, want)
		}
	}
}

// Numbers have one canonical form (number.go), whichever way they are spelled and
// in whichever reader: a sign only on a number that is not zero, no leading zero
// but the one before a point, no trailing zero after it, a plain decimal when that
// has at most 40 characters, and otherwise digits and an exponent.
var canonicalTable = []struct{ in, want string }{
	{"0", "0"}, {"-0", "0"}, {"-00", "0"}, {"-0.0", "0"}, {"-0e0", "0"}, {"0.0", "0"}, {"00", "0"}, {"0e100", "0"}, {"0.000e-5", "0"},
	{"1", "1"}, {"1.0", "1"}, {"1e0", "1"}, {"10e-1", "1"}, {"01", "1"}, {"1.e0", "1"}, {"0.1e1", "1"}, {"100e-2", "1"},
	{"-1", "-1"}, {"-1.0", "-1"}, {"-10e-1", "-1"}, {"007", "7"}, {"1.5", "1.5"}, {"-1.5", "-1.5"}, {"1.50", "1.5"}, {"15e-1", "1.5"},
	{"1e3", "1000"}, {"-1e3", "-1000"}, {"1e+3", "1000"}, {"1E3", "1000"}, {"1e-3", "0.001"}, {"2.5e2", "250"}, {"0.5", "0.5"}, {"00.50", "0.5"},
	{"0.1", "0.1"}, {"0.01", "0.01"}, {"100", "100"}, {"100.00", "100"}, {"1e2", "100"}, {"1e+100", "1e100"}, {"1e100", "1e100"}, {"10e99", "1e100"},
	{"0.1e101", "1e100"}, {"1e-100", "1e-100"}, {"0.1e-99", "1e-100"}, {"1e-99", "1e-99"}, {"15e-50", "15e-50"}, {"-123e60", "-123e60"},
	{"9223372036854775807", "9223372036854775807"}, {"-9223372036854775808", "-9223372036854775808"},
	{"9223372036854775808", "9223372036854775808"}, {"-9223372036854775809", "-9223372036854775809"},
	{"123456789012345678901234567890", "123456789012345678901234567890"},
	// The plain decimal is used up to 40 characters, and the exponent form beyond.
	{"1e39", "1" + strings.Repeat("0", 39)}, {"1e40", "1e40"}, {"1e41", "1e41"}, {"12e39", "12e39"},
	{"1" + strings.Repeat("0", 39), "1" + strings.Repeat("0", 39)}, {"12e38", "12" + strings.Repeat("0", 38)},
	{"1e-39", "1e-39"}, {"12e-39", "12e-39"}, {"0." + strings.Repeat("0", 36) + "12", "0." + strings.Repeat("0", 36) + "12"},
	{"1e-37", "0." + strings.Repeat("0", 36) + "1"}, {"1e-38", "0." + strings.Repeat("0", 37) + "1"},
	{"1234567890123456789012345678901234567e1", "1234567890123456789012345678901234567" + "0"},
	{"1234567890123456789012345678901234567.5", "1234567890123456789012345678901234567.5"},
	{"1.2345678901234567890123456e-30", "12345678901234567890123456e-55"},
	// 39 digits and a point are 40 characters, and the plain decimal; one digit more is not.
	{"12345678901234567890123456789012345678.9", "12345678901234567890123456789012345678.9"},
}

var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func TestCanonicalNumber(t *testing.T) {
	t.Parallel()
	for _, tc := range canonicalTable {
		if problem := numberProblem(tc.in); problem != "" {
			t.Errorf("%q is refused: %s", tc.in, problem)
			continue
		}
		got := canonicalNumber(tc.in)
		if got != tc.want {
			t.Errorf("canonicalNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
		// Written again, it is read back as the same number, and is within the limits.
		if problem := numberProblem(got); problem != "" || canonicalNumber(got) != got {
			t.Errorf("%q: the canonical form %q is refused (%q) or changes (%q)", tc.in, got, problem, canonicalNumber(got))
		}
	}
}

// Both readers read a number to the same canonical form, and the same value is the
// same text: the HCL reader (a model of the table) and the JSON reader.
func TestReadersAgreeOnNumbers(t *testing.T) {
	t.Parallel()
	for _, tc := range canonicalTable {
		src := "entity \"A\" {\n  key = []\n  x = " + tc.in + "\n}\n"
		if !strings.HasPrefix(tc.in, "-") { // a sign before a sign is not a number
			src = "entity \"A\" {\n  key = []\n  x = " + tc.in + "\n  y = -" + tc.in + "\n}\n"
		}
		m, fs := ParseHCL("a"+hclExt, []byte(src))
		if len(fs) != 0 || len(m.Concepts) != 1 {
			t.Errorf("%s: findings %v", tc.in, fs)
			continue
		}
		x, _ := m.Concepts[0].Attr("x")
		if x.Value.Type != NodeNumber || x.Value.Str != tc.want {
			t.Errorf("HCL %s: read as %q, want %q", tc.in, x.Value.Str, tc.want)
		}
		if y, ok := m.Concepts[0].Attr("y"); ok {
			wantNeg := "-" + tc.want
			if tc.want == "0" {
				wantNeg = "0"
			}
			if y.Value.Str != wantNeg {
				t.Errorf("HCL -%s: read as %q, want %q", tc.in, y.Value.Str, wantNeg)
			}
		}
		if !jsonNumber.MatchString(tc.in) {
			continue // not a number in JSON: leading zeros, a point with no digit after it
		}
		n, err := ParseNode([]byte(`{"x": ` + tc.in + `}`))
		if err != nil {
			t.Errorf("JSON %s: %v", tc.in, err)
			continue
		}
		if v, _ := n.Get("x"); v.Str != tc.want {
			t.Errorf("JSON %s: read as %q, want %q", tc.in, v.Str, tc.want)
		}
	}
	// Equal values are duplicates in the checker whatever they are spelled, in both readers.
	for _, group := range [][]string{{"1", "1.0", "1e0", "10e-1"}, {"0", "-0", "0.0"}, {"100", "1e2", "1E+2", "0.1e3"}} {
		hcl := "enum \"E\" {\n  values = [" + strings.Join(group, ", ") + "]\n}\n"
		json := `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "x", "version": "1"}, "enums": {"E": {"values": [` + strings.Join(group, ", ") + `]}}}`
		for name, files := range map[string]map[string]string{"HCL": {"a" + hclExt: hcl}, "JSON": {"a" + jsonExt: json}} {
			got := run(files)
			if len(got) != len(group)-1 {
				t.Errorf("%s %v: %d findings, want %d duplicates: %v", name, group, len(got), len(group)-1, got)
			}
		}
	}
	// A negative zero is zero in a count too, and a fraction is not a count.
	expect(t, run(map[string]string{"a" + jsonExt: `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "x", "version": "1"}, "entities": {"E": {"key": ["id"], "properties": {"id": {"type": "string", "max_len": -0, "min_len": 1e1}}}}}`}))
	expect(t, run(map[string]string{"a" + jsonExt: `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "x", "version": "1"}, "entities": {"E": {"key": ["id"], "properties": {"id": {"type": "string", "max_len": 1.5}}}}}`}), `"max_len"`)
}

// Writing a number takes a few small allocations, whatever the spelling: no
// floating point value is built to print it.
func TestCanonicalNumberAllocatesLittle(t *testing.T) { // not parallel: AllocsPerRun
	for _, in := range []string{"0.1", "1e-99", "123.456e-7", "9e99", "-1234567890123456789012345678901234567e1"} {
		if allocs := testing.AllocsPerRun(100, func() { canonicalNumber(in) }); allocs > 6 {
			t.Errorf("canonicalNumber(%q) makes %.0f allocations", in, allocs)
		}
	}
}

// A number is read from its own token wherever it stands, after text whose `$` and
// `%` were rewritten (which moves everything after them): on a later line, on the same
// line after a string, after a heredoc. The offsets the reader looks the numbers up by
// are those of the rewritten source.
func TestNumbersAfterRewrittenText(t *testing.T) {
	t.Parallel()
	texts := []string{`"^[a-z]+$"`, `"50%"`, `"$$"`, `"%%"`, `"$${"`, `"a$b%c"`, `"$A"`, "<<EOT\n^[a-z]+$\nEOT"}
	for _, text := range texts {
		earlier := "entity \"E\" {\n  key = [\"id\"]\n  property \"id\" {\n    type    = \"string\"\n    pattern = " + text + "\n    max_len = 17\n    min_len = 3\n  }\n}\n"
		m, fs := ParseHCL("a"+hclExt, []byte(earlier))
		if len(fs) != 0 {
			t.Errorf("%s: %v", text, fs)
			continue
		}
		mem := m.Concepts[0].Members[0]
		for name, want := range map[string]string{"max_len": "17", "min_len": "3"} {
			if a, _ := mem.Attr(name); a.Value.Str != want {
				t.Errorf("%s: %s is %q after the text, want %s", text, name, a.Value.Str, want)
			}
		}
		node, err := m.JSON(ModuleIdentity{ID: "x", Version: "1"})
		if err != nil || !strings.Contains(string(node.Encode()), `"max_len": 17`) {
			t.Errorf("%s: export %v: %s", text, err, node.Encode())
		}
	}
	// On the same line, between strings and after them.
	for _, strs := range [][]string{{`"a$"`, "5", `"b%"`, "7"}, {`"$$"`, "-5", `"%%"`, "1e2"}, {"5", `"$"`, "-0.5", `"%"`, "9"}} {
		src := "enum \"E\" {\n  values = [" + strings.Join(strs, ", ") + "]\n}\n"
		m, fs := ParseHCL("a"+hclExt, []byte(src))
		if len(fs) != 0 {
			t.Errorf("%v: %v", strs, fs)
			continue
		}
		items := m.Concepts[0].Attrs[0].Value.Items
		if len(items) != len(strs) {
			t.Fatalf("%v: %d items", strs, len(items))
		}
		for i, s := range strs {
			if s[0] != '"' && items[i].Type == NodeNumber && items[i].Str != canonicalNumber(s) {
				t.Errorf("%v: item %d is %q, want %q", strs, i, items[i].Str, canonicalNumber(s))
			}
			if s[0] != '"' && items[i].Type != NodeNumber {
				t.Errorf("%v: item %d is not a number", strs, i)
			}
		}
	}
}

// A whole number is one with no point and no negative exponent: a number with a negative
// exponent is not an enum value or a count, however small it makes the digits.
func TestNegativeExponentIsNotAWholeNumber(t *testing.T) {
	t.Parallel()
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [1e-50, 7]\n}\n"}), `"values" must be a list of strings or integers`)
	expect(t, run(map[string]string{"a" + hclExt: "entity \"E\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"string\"\n    max_len = 1e-50\n  }\n}\n"}), `"max_len"`)
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [10e-1, 7]\n}\n"})) // 10e-1 is 1
	for in, want := range map[string]bool{"1": true, "-7": true, "1e41": true, "15e-50": false, "0.5": false, "1e-50": false} {
		if got := isIntegerNumber(canonicalNumber(in)); got != want {
			t.Errorf("isIntegerNumber(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestNumberProblemShowsTheNormalForm(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"100e99":  "the number 100e99 is 1e101, whose exponent 101 is past the limit of 100 either way (the digits without trailing zeros, times a power of ten): a larger or smaller number cannot be read exactly",
		"-100e99": "the number -100e99 is -1e101, whose exponent 101 is past the limit of 100 either way (the digits without trailing zeros, times a power of ten): a larger or smaller number cannot be read exactly",
		"0.1e101": "",
	} {
		if got := numberProblem(in); got != want {
			t.Errorf("numberProblem(%s) = %q, want %q", in, got, want)
		}
	}
}
