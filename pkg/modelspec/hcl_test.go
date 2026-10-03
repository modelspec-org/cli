package modelspec

import (
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
		{"non-literal function", "entity \"A\" {\n  key = upper(\"a\")\n}\n", []string{`attribute "key": must be a literal`}},
		{"non-literal reference", "entity \"A\" {\n  key = foo\n}\n", []string{"must be a literal"}},
		{"template with interpolation", "entity \"A\" {\n  key = \"a${x}\"\n}\n", []string{"must be a literal"}},
		{"interpolation", "entity \"A\" {\n  key = [\"${x}\"]\n}\n", []string{"must be a literal"}},
		{"arithmetic", "entity \"A\" {\n  key = 1 + 1\n}\n", []string{"must be a literal"}},
		{"logical not", "entity \"A\" {\n  key = !true\n}\n", []string{"must be a literal"}},
		{"negated reference", "entity \"A\" {\n  key = -foo\n}\n", []string{"must be a literal"}},
		{"list with expression", "entity \"A\" {\n  key = [1 + 1]\n}\n", []string{"must be a literal"}},
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

// Inputs that make a recursive parser deep or slow are findings, not crashes. A
// stack overflow in Go is fatal, so this is a test that the process survives.
func TestPathologicalHCLIsAFinding(t *testing.T) {
	t.Parallel()
	n := 120000
	cases := map[string]string{
		"unary not":           "x = " + strings.Repeat("!", n),
		"unary minus":         "x = " + strings.Repeat("-", n),
		"conditionals":        "x = " + strings.Repeat("a ? b : ", n/8) + "c",
		"nested templates":    "x = " + strings.Repeat("\"${", n/4) + "1" + strings.Repeat("}\"", n/4),
		"long sum":            "x = 1" + strings.Repeat(" + 1", n/4),
		"long traversal":      "x = a" + strings.Repeat(".b", n/2),
		"many indexes":        "x = a" + strings.Repeat("[0]", n/3),
		"many labels":         "entity " + strings.Repeat("\"a\" ", n/4) + "{}",
		"nested blocks":       strings.Repeat("a {\n", 70) + strings.Repeat("}\n", 70),
		"nested for":          "x = " + strings.Repeat("[for a in b : ", 70) + "1" + strings.Repeat("]", 70),
		"unclosed heredoc":    "x = <<EOT\n" + strings.Repeat("${", n/2) + "\n",
		"80000 open brackets": strings.Repeat("[", 80000),
	}
	for name, src := range cases {
		_, findings := ParseHCL("a"+hclExt, []byte(src))
		if len(findings) == 0 {
			t.Errorf("%s: no finding", name)
		}
	}
}
