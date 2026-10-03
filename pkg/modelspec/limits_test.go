package modelspec

import (
	"strings"
	"testing"
)

// limitCase is one hostile or borderline input and what the limits say about it.
type limitCase struct {
	name string
	src  string
	want string // "" means no limit finding; otherwise a substring of the message
	line int    // the line of the finding, when wanted
}

func hclLimitFinding(src string) (Finding, bool) { return hclLimits("f", []byte(src)) }

// rep repeats s n times.
func rep(s string, n int) string { return strings.Repeat(s, n) }

func TestHCLLimits(t *testing.T) {
	t.Parallel()
	ten := 10 * MaxDepth
	tests := []limitCase{
		// Constructs that are fine.
		{"a model", "entity \"A\" {\n  key = [\"id\"]\n}\n", "", 0},
		{"depth at the limit", "x = " + rep("[", MaxDepth) + rep("]", MaxDepth), "", 0},
		{"unbalanced brackets in a string", "x = \"" + rep("[", 1000) + "\"", "", 0},
		{"unbalanced brackets in a heredoc", "x = <<EOT\n" + rep("[(", 33) + "\nEOT\n", "", 0},
		{"unbalanced brackets in # and // and /* comments", "# " + rep("[", 500) + "\n// " + rep("{", 500) + "\n/* " + rep("(", 500) + " */\nx = 1\n", "", 0},
		{"closers alone", rep("]", 1000), "", 0},
		{"balanced pairs in a row", rep("[]", 100000), "", 0},
		{"unary run at the limit", "x = " + rep("-", MaxOperatorRun) + "1", "", 0},
		{"conditionals at the limit", "x = " + rep("a ? b : ", MaxConditionals) + "c", "", 0},
		{"directives at the limit", "x = <<EOT\n" + rep("%{ if a }", MaxDepth-2) + "\nEOT\n", "", 0},
		{"a long sum is iterative", "x = 1" + rep(" + 1", 100000), "", 0},
		{"a long traversal is iterative", "x = a" + rep(".b", 100000), "", 0},
		{"many indexes in a row are iterative", "x = a" + rep("[0]", 100000), "", 0},
		{"many splats in a row are iterative", "x = a" + rep("[*]", 100000), "", 0},
		{"many labels are iterative", "entity " + rep("\"a\" ", 100000) + "{}", "", 0},
		{"many attributes", rep("a = 1\n", 100000), "", 0},
		{"many blocks side by side", rep("a {}\n", 100000), "", 0},
		{"a unary operator between operands", "x = 1 - -1 - !a", "", 0},

		// The reviewer's crashers: the byte-level pre-check was blind to them.
		{"300000 unary minus signs", "entity \"A\" {\n  key = " + rep("-", 300000) + "1\n}\n", "run of unary operators", 2},
		{"400000 unary bangs", "x = " + rep("!", 400000) + "true", "run of unary operators", 1},
		{"a heredoc with one quote, then nested brackets", "x = <<EOT\n\"\nEOT\nkey = " + rep("[", 80000) + "\n", "nesting of brackets", 4},
		{"a heredoc with /*, then nested brackets", "x = <<EOT\n/*\nEOT\nkey = " + rep("[", 80000) + "\n", "nesting of brackets", 4},
		{"80000 nested brackets", rep("[", 80000), "nesting of brackets", 1},

		// One input per recursive construct, at ten times its limit.
		{"nested blocks", rep("a {\n", ten) + rep("}\n", ten), "nesting of brackets", 65},
		{"nested tuples", "x = " + rep("[", ten) + rep("]", ten), "nesting of brackets", 1},
		{"nested objects", "x = " + rep("{a=", ten) + "1" + rep("}", ten), "nesting of brackets", 1},
		{"nested parentheses", "x = " + rep("(", ten) + "1" + rep(")", ten), "nesting of brackets", 1},
		{"nested function calls", "x = " + rep("f(", ten) + "1" + rep(")", ten), "nesting of brackets", 1},
		{"nested indexes", "x = " + rep("a[", ten) + "1" + rep("]", ten), "nesting of brackets", 1},
		{"nested for expressions", "x = " + rep("[for a in b : ", ten) + "1" + rep("]", ten), "nesting of brackets", 1},
		{"nested quoted templates", "x = " + rep("\"${", ten) + "1" + rep("}\"", ten), "nesting of brackets", 1},
		{"nested template interpolations in one string", "x = \"" + rep("${a ? \"", ten) + "\"", "nesting of brackets", 1},
		{"unary minus", "x = " + rep("-", 10*MaxOperatorRun) + "1", "run of unary operators", 1},
		{"unary bang", "x = " + rep("!", 10*MaxOperatorRun) + "1", "run of unary operators", 1},
		{"unary mix", "x = " + rep("-!", 5*MaxOperatorRun) + "1", "run of unary operators", 1},
		{"unary operators separated by newlines inside parentheses", "x = (" + rep("-\n", 10*MaxOperatorRun) + "1)", "run of unary operators", 0},
		{"unary operators separated by comments", "x = " + rep("-/**/", 10*MaxOperatorRun) + "1", "run of unary operators", 1},
		{"chained conditionals", "x = " + rep("a ? b : ", 10*MaxConditionals) + "c", "conditional operators", 1},
		{"conditionals nested in the true branch", "x = " + rep("a ? ", 10*MaxConditionals) + "b" + rep(" : c", 10*MaxConditionals), "conditional operators", 1},
		{"if directives", "x = <<EOT\n" + rep("%{ if a }", ten) + "\nEOT\n", "nesting of template directives", 2},
		{"for directives", "x = <<EOT\n" + rep("%{ for a in b }", ten) + "\nEOT\n", "nesting of template directives", 2},
		{"strip markers on directives", "x = <<EOT\n" + rep("%{~ if a ~}", ten) + "\nEOT\n", "nesting of template directives", 2},
		{"directives in a quoted string", "x = \"" + rep("%{if a}", ten) + "\"", "nesting of template directives", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, bad := hclLimitFinding(tc.src)
			if tc.want == "" {
				if bad {
					t.Fatalf("unexpected finding %+v", f)
				}
				return
			}
			if !bad || !strings.Contains(f.Message, tc.want) || f.Rule != RuleLimit || f.Severity != SeverityError || (tc.line > 0 && f.Line != tc.line && tc.name != "nested blocks") {
				t.Fatalf("finding = %+v (bad %v), want %q at line %d", f, bad, tc.want, tc.line)
			}
		})
	}
}

// Directives that are closed lower the nesting again, so sequences of them are fine.
func TestDirectivesThatClose(t *testing.T) {
	t.Parallel()
	src := "x = <<EOT\n" + rep("%{ if a }b%{ endif }", 1000) + rep("%{ for a in b }c%{ endfor }", 1000) + "%{ endif }%{ endfor }\nEOT\n"
	if f, bad := hclLimitFinding(src); bad {
		t.Fatalf("unexpected finding %+v", f)
	}
}

// The parser must never see a source the limits refuse: through the reader, a
// hostile file is one finding, and the process lives.
func TestReadersRefuseHostileInput(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"300000 unary minus signs": "entity \"A\" {\n  key = " + rep("-", 300000) + "1\n}\n",
		"heredoc quote":            "x = <<EOT\n\"\nEOT\nkey = " + rep("[", 80000) + "\n",
		"heredoc comment":          "x = <<EOT\n/*\nEOT\nkey = " + rep("[", 80000) + "\n",
		"conditionals":             "x = " + rep("a ? b : ", 100000) + "c",
		"directives":               "x = <<EOT\n" + rep("%{if a}", 100000) + "\nEOT\n",
		"nested templates":         "x = " + rep("\"${", 100000) + "1" + rep("}\"", 100000),
	} {
		m, fs := ParseHCL("a"+hclExt, []byte(src))
		if !m.Broken || len(fs) != 1 || fs[0].Rule != RuleLimit {
			t.Errorf("%s: broken %v, findings %v", name, m.Broken, fs)
		}
	}
}

// A valid file whose heredoc holds unbalanced brackets is valid: what is inside
// a heredoc or a string is text.
func TestValidFileWithBracketsInHeredocs(t *testing.T) {
	t.Parallel()
	src := "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type    = \"string\"\n    pattern = <<EOT\n" + rep("[(", 33) + "\nEOT\n  }\n}\n"
	if len(src) > 400 {
		t.Fatalf("the file has %d bytes", len(src))
	}
	expect(t, run(map[string]string{"a" + hclExt: src}))
	// And a heredoc `query` holding a quote, followed by many entities with a pattern of unbalanced brackets.
	many := "collection \"c\" {\n  kind  = \"computed\"\n  query = <<EOT\n\"\nEOT\n}\n" + rep("entity \"E\" {\n  property \"p\" {\n    pattern = \"^[a-z\"\n  }\n}\n", 70)
	if f, bad := hclLimitFinding(many); bad {
		t.Fatalf("unexpected finding %+v", f)
	}
}

// A file with many syntax errors reports the first few and says so.
func TestSyntaxFindingsAreCapped(t *testing.T) {
	t.Parallel()
	_, fs := ParseHCL("a"+hclExt, []byte(rep("a b\n", 5000)))
	if len(fs) != MaxSyntaxFindings+1 || !strings.Contains(fs[len(fs)-1].Message, "more syntax errors follow; only the first 50 are shown") {
		t.Fatalf("%d findings, last %+v", len(fs), fs[len(fs)-1])
	}
}

// Long chains that the parser reads iteratively do not crash it, at the size limit.
func TestLongChainsAreSurvived(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"sum":       "x = 1" + rep(" + 1", 400000),
		"traversal": "x = a" + rep(".b", 600000),
		"indexes":   "x = a" + rep("[0]", 400000),
	} {
		m, fs := ParseHCL("a"+hclExt, []byte(src))
		if m.Broken || len(fs) == 0 {
			t.Errorf("%s: broken %v, %d findings", name, m.Broken, len(fs))
		}
	}
}

func TestJSONDepth(t *testing.T) {
	t.Parallel()
	nested := func(n int) string { return rep("[", n) + rep("]", n) }
	objects := func(n int) string { return rep(`{"a":`, n) + "1" + rep("}", n) }
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"arrays at the limit", nested(MaxDepth), ""},
		{"objects at the limit", objects(MaxDepth), ""},
		{"arrays over the limit", nested(MaxDepth + 1), "nesting is deeper than 64 levels"},
		{"objects over the limit", objects(MaxDepth + 1), "nesting is deeper than 64 levels"},
		{"ten times the limit", nested(10 * MaxDepth), "nesting is deeper than 64 levels"},
		{"80000 open brackets", rep("[", 80000), "nesting is deeper than 64 levels"},
		{"brackets in strings", `["` + rep("[", 1000) + `"]`, ""},
	} {
		_, err := ParseNode([]byte(tc.src))
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		se, ok := err.(*syntaxError)
		if !ok || !se.limit || se.msg != tc.want || se.line != 1 {
			t.Errorf("%s: err = %#v", tc.name, err)
		}
	}
	m, fs := ParseJSON("a"+jsonExt, []byte(nested(10*MaxDepth)))
	if !m.Broken || len(fs) != 1 || fs[0].Rule != RuleLimit || strings.Contains(fs[0].Message, "not valid JSON") {
		t.Errorf("through the reader: broken %v, %v", m.Broken, fs)
	}
	if (&depthError{}).Error() == "" {
		t.Error("depthError has no message")
	}
}

func TestPrecheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  string
		line int
	}{
		{"invalid UTF-8 names the line", "{\n\"a\": \"\xff\"}", 2},
		{"invalid UTF-8 after multibyte text", "# é\n\xc3(\n", 2},
		{"a truncated rune at the end", "x = 1\xe2\x82", 1},
	} {
		f, bad := precheck("f", []byte(tc.src))
		if !bad || f.Rule != RuleEncoding || f.Line != tc.line || !strings.Contains(f.Message, "not valid UTF-8") {
			t.Errorf("%s: %+v (bad %v)", tc.name, f, bad)
		}
	}
	if _, bad := precheck("f", []byte("entity \"é\" {}\n")); bad {
		t.Error("valid UTF-8 refused")
	}
	f, bad := precheck("f", make([]byte, MaxInputBytes+1))
	if !bad || f.Rule != RuleLimit || !strings.Contains(f.Message, "the limit is 4194304 bytes") || f.Line != 0 {
		t.Fatalf("finding = %+v", f)
	}
	if _, bad := precheck("f", make([]byte, MaxInputBytes)); bad {
		t.Fatal("a file of exactly the limit is refused")
	}
	for name, parse := range map[string]func() (*Model, []Finding){
		"HCL encoding":  func() (*Model, []Finding) { return ParseHCL("a"+hclExt, []byte("# \xff\n")) },
		"JSON encoding": func() (*Model, []Finding) { return ParseJSON("a"+jsonExt, []byte("{\"a\": \"\xff\"}")) },
		"HCL size":      func() (*Model, []Finding) { return ParseHCL("a"+hclExt, make([]byte, MaxInputBytes+1)) },
		"JSON size":     func() (*Model, []Finding) { return ParseJSON("a"+jsonExt, make([]byte, MaxInputBytes+1)) },
	} {
		if m, fs := parse(); !m.Broken || len(fs) != 1 {
			t.Errorf("%s: broken %v, findings %v", name, m.Broken, fs)
		}
	}
}
