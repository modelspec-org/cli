package modelspec

import (
	"runtime/debug"
	"strings"
	"testing"
)

func hclFindings(src string) []Finding { return hclLiterals("f", []byte(src)) }

// rep repeats s n times.
func rep(s string, n int) string { return strings.Repeat(s, n) }

// The constructs a literal value cannot contain are refused before the parser
// runs, each as a literal finding that names the construct and the line.
func TestNonLiteralSyntaxIsRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want string // a substring of the construct named
		line int
	}{
		{"a parenthesis", "x = (1)", "a parenthesis", 1},
		{"a function call", "x = f(1)", "a parenthesis", 1},
		{"a namespaced call", "x = a::b()", "`::`", 1},
		{"a traversal", "x = a.b", "`.` (a traversal)", 1},
		{"an attribute splat", "x = a.*", "`.` (a traversal)", 1},
		{"an index", "x = a[0]", "an index or a splat", 1},
		{"an index on a string", "x = \"a\"[0]", "an index or a splat", 1},
		{"an index after a list", "x = [1][0]", "an index or a splat", 1},
		{"an index on another line", "x = [a\n[0]]", "an index or a splat", 2},
		{"an index after a comment", "x = [a /* c */ [0]]", "an index or a splat", 1},
		{"a full splat", "x = a[*]", "an index or a splat", 1},
		{"a splat then a traversal", "key = a[*].b", "an index or a splat", 1},
		{"a sum", "x = 1 + 1", "an arithmetic operator", 1},
		{"a division", "x = 1 / 1", "an arithmetic operator", 1},
		{"a remainder", "x = 1 % 1", "an arithmetic operator", 1},
		{"a multiplication", "x = 1 * 1", "`*`", 1},
		{"a subtraction", "x = 1 - 1", "`-` is allowed only as the sign of a number", 1},
		{"a subtraction after a string", "x = \"a\" -1", "`-` is allowed only as the sign of a number", 1},
		{"a minus before a word", "x = -a", "`-` is allowed only as the sign of a number", 1},
		{"a minus before a minus", "x = --1", "`-` is allowed only as the sign of a number", 1},
		{"a minus at the end", "x = -", "`-` is allowed only as the sign of a number", 1},
		{"a negation", "x = !true", "a logical operator", 1},
		{"a comparison", "x = 1 < 2", "a comparison operator", 1},
		{"an equality", "x = 1 == 1", "a comparison operator", 1},
		{"an inequality", "x = 1 != 1", "a comparison operator", 1},
		{"a logical and", "x = true && false", "a logical operator", 1},
		{"a logical or", "x = true || false", "a logical operator", 1},
		{"a conditional", "x = a ? b : c", "a conditional", 1},
		{"a for expression over a list", "x = [for a in b : a]", "a `for` expression", 1},
		{"a for expression over an object", "x = {for k, v in b : k => v}", "a `for` expression", 1},
		{"an expanded argument", "x = [a...]", "`...`", 1},
		{"a fat arrow", "x = {a => 1}", "`=>`", 1},
		{"an interpolation", "x = \"${a}\"", "a template interpolation", 1},
		{"an interpolation in a heredoc", "x = <<EOT\n${a}\nEOT\n", "a template interpolation", 2},
		{"a directive", "x = \"%{if a}b%{endif}\"", "a template directive", 1},
		{"a directive in a heredoc", "x = <<EOT\n%{ for a in b }c%{ endfor }\nEOT\n", "a template directive", 2},
		{"a directive with a strip marker", "x = <<EOT\n%{~ if a ~}\nEOT\n", "a template directive", 2},
		// The two inputs under the size limit that overflowed the parser's stack in
		// the third review, small: the first token of the construct is the finding.
		{"a full splat repeated", "entity \"A\" {\n  key = a" + rep("[*]", 1000) + "\n}\n", "an index or a splat", 2},
		{"directives split by newlines in a heredoc", "x = <<EOT\n" + rep("%{\nif x}", 1000) + "EOT\n", "a template directive", 2},
		{"directives hidden by comments", "x = <<EOT\n" + rep("%{/**/if true}", 1000) + "EOT\n", "a template directive", 2},
		{"a chain of namespaces", "x = " + rep("a::", 1000) + "b()", "`::`", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hclFindings(tc.src)
			if len(got) == 0 {
				t.Fatal("no finding")
			}
			f := got[0]
			if f.Rule != RuleLiteral || f.Severity != SeverityError || f.Line != tc.line || !strings.Contains(f.Message, tc.want) || !strings.Contains(f.Message, "decision 0009") {
				t.Fatalf("finding = %+v, want %q on line %d", f, tc.want, tc.line)
			}
		})
	}
}

// What a literal can contain is not refused, however it is spelled out.
func TestLiteralSyntaxIsAccepted(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, src string }{
		{"a model", "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n    min_len = -1\n  }\n}\n"},
		{"negative numbers", "x = -1\ny = [1, -2.5, - 3, -1e-3]\n"},
		{"booleans, null and words", "x = true\ny = false\nz = null\nw = abc\n"},
		{"a list of lists", "x = [[1], [2, 3]]\n"},
		{"an object", "x = {a = 1, b : 2}\n"},
		{"a list on the next line", "x = [\n  \"a\",\n  [\"b\"],\n]\n"},
		{"escaped interpolation and directive", "x = \"$${a} %%{if}\"\ny = <<EOT\n$${a}\n%%{if a}\nEOT\n"},
		{"operators in strings, heredocs and comments", "# a + b ? c : (d)\n// a.b[*]\n/* f(x) ${y} */\nx = \"a + b ? (c) . [*] $\"\ny = <<EOT\nf(a)[*] a.b - c ? d : e\nEOT\n"},
		{"unbalanced brackets in a string", "x = \"" + rep("[", 1000) + "\""},
		{"unbalanced brackets in a heredoc", "x = <<EOT\n" + rep("[(", 33) + "\nEOT\n"},
		{"unbalanced brackets in comments", "# " + rep("[", 500) + "\n// " + rep("{", 500) + "\n/* " + rep("(", 500) + " */\nx = 1\n"},
		{"nesting at the limit", "x = " + rep("[", MaxDepth) + rep("]", MaxDepth)},
		{"closers alone", rep("]", 1000)},
		{"empty lists in a row", rep("x = []\n", 1000)},
		{"many labels", "entity " + rep("\"a\" ", 1000) + "{}"},
		{"many attributes", rep("a = 1\n", 1000)},
		{"many blocks side by side", rep("a {}\n", 1000)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hclFindings(tc.src); len(got) != 0 {
				t.Fatalf("unexpected findings %v", got)
			}
		})
	}
}

// Nesting is bounded: the one thing left that the parser recurses on.
func TestNestingLimit(t *testing.T) {
	t.Parallel()
	ten := 10 * MaxDepth
	for _, tc := range []struct {
		name string
		src  string
		line int
	}{
		{"blocks", rep("a {\n", ten) + rep("}\n", ten), MaxDepth + 1},
		{"lists", "x = " + rep("[", ten) + rep("]", ten), 1},
		{"objects", "x = " + rep("{a=", ten) + "1" + rep("}", ten), 1},
		{"one open bracket too many", "x = " + rep("[", MaxDepth+1), 1},
		{"unclosed brackets", rep("[", 1000), 1},
	} {
		got := hclFindings(tc.src)
		if len(got) != 1 || got[len(got)-1].Rule != RuleLimit || got[0].Line != tc.line || !strings.Contains(got[0].Message, "deeper than 64 levels") {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
	// Both kinds of finding in one file: the non-literal ones found before the limit stay.
	got := hclFindings("x = a.b\ny = " + rep("[", MaxDepth+1))
	if len(got) != 2 || got[0].Rule != RuleLiteral || got[1].Rule != RuleLimit {
		t.Errorf("both: %v", got)
	}
}

// A heredoc of many lines is refused: the HCL parser joins its pieces in
// quadratic time.
func TestHeredocLinesLimit(t *testing.T) {
	t.Parallel()
	heredoc := func(lines int) string { return "x = <<EOT\n" + rep("a\n", lines) + "EOT\n" }
	quoted := func(escapes int) string { return "x = \"" + rep("\\n", escapes) + "\"\n" }
	for _, tc := range []struct {
		name string
		src  string
		line int // of the finding; 0 for none
	}{
		{"a heredoc at the limit", heredoc(MaxHeredocLines), 0},
		{"a heredoc over the limit", heredoc(MaxHeredocLines + 1), MaxHeredocLines + 2},
		{"a string with many escapes is one piece", quoted(100000), 0},
		{"many heredocs, each at the limit", rep(heredoc(MaxHeredocLines), 5), 0},
		{"a long string with no escapes", "x = \"" + rep("a", 100000) + "\"\n", 0},
		{"a long heredoc line", "x = <<EOT\n" + rep("a", 100000) + "\nEOT\n", 0},
	} {
		got := hclFindings(tc.src)
		if tc.line == 0 {
			if len(got) != 0 {
				t.Errorf("%s: %v", tc.name, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Rule != RuleLimit || got[0].Line != tc.line || !strings.Contains(got[0].Message, "a heredoc has more than 1000 lines") {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

// A file with many non-literal tokens reports the first few distinct ones, and says so.
func TestNonLiteralFindingsAreCappedAndDeduplicated(t *testing.T) {
	t.Parallel()
	got := hclFindings(rep("x = a.b.c.d\n", 5000))
	if len(got) != MaxSyntaxFindings+1 || !strings.Contains(got[len(got)-1].Message, "more non-literal syntax follows; only the first 50 are shown") {
		t.Fatalf("%d findings, last %+v", len(got), got[len(got)-1])
	}
	// The same construct twice on a line is one finding; another construct on it is another.
	got = hclFindings("x = a.b.c + d.e\n")
	if len(got) != 2 || !strings.Contains(got[0].Message, "`.`") || !strings.Contains(got[1].Message, "arithmetic") {
		t.Fatalf("findings = %v", got)
	}
}

// Through the reader a hostile file is findings and a Broken model, and the
// parser never sees it.
func TestReadersRefuseHostileInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, src, rule string }{
		{"a full splat", "entity \"A\" {\n  key = a" + rep("[*]", 1000) + "\n}\n", RuleLiteral},
		{"directives", "x = <<EOT\n" + rep("%{\nif x}", 1000) + "EOT\n", RuleLiteral},
		{"unary operators", "entity \"A\" {\n  key = " + rep("-", 1000) + "1\n}\n", RuleLiteral},
		{"conditionals", "x = " + rep("a ? b : ", 1000) + "c", RuleLiteral},
		{"nested templates", "x = " + rep("\"${", 1000) + "1" + rep("}\"", 1000), RuleLiteral},
		{"a heredoc with one quote, then nested brackets", "x = <<EOT\n\"\nEOT\nkey = " + rep("[", 1000) + "\n", RuleLimit},
		{"a heredoc with /*, then nested brackets", "x = <<EOT\n/*\nEOT\nkey = " + rep("[", 1000) + "\n", RuleLimit},
	} {
		m, fs := ParseHCL("a"+hclExt, []byte(tc.src))
		found := false
		for _, f := range fs {
			found = found || f.Rule == tc.rule
		}
		if !m.Broken || !found {
			t.Errorf("%s: broken %v, findings %v", tc.name, m.Broken, fs)
		}
	}
}

// The parser itself must never be reached by input that would overflow its stack.
// This test lowers the stack limit of the process to 8 MiB (the parser needs
// several KB of stack for each item of these chains, so one that recursed would
// die within a few thousand items; a Go stack overflow is fatal, and the run
// would fail) and feeds ParseHCL 10,000 repeats of each construct that makes the
// parser recurse. It is not parallel, because the limit is process-wide.
func TestHostileInputDoesNotReachTheParserStack(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(8 << 20))
	const n = 10000
	for name, src := range map[string]string{
		"a full splat":          "entity \"A\" {\n  key = a" + rep("[*]", n) + "\n}\n",
		"an attribute splat":    "x = a" + rep(".*", n),
		"heredoc lines":         "x = <<EOT\n" + rep("a\n", n) + "EOT\n",
		"directives":            "x = <<EOT\n" + rep("%{\nif x}", n) + "EOT\n",
		"hidden directives":     "x = <<EOT\n" + rep("%{/**/if true}", n) + "EOT\n",
		"unary minus":           "x = " + rep("-", n) + "1",
		"unary bangs":           "x = " + rep("!", n) + "true",
		"conditionals":          "x = " + rep("a ? b : ", n) + "c",
		"nested conditionals":   "x = " + rep("a ? ", n) + "b" + rep(" : c", n),
		"a sum":                 "x = 1" + rep(" + 1", n),
		"a traversal":           "x = a" + rep(".b", n),
		"indexes":               "x = a" + rep("[0]", n),
		"namespaces":            "x = " + rep("a::", n) + "b()",
		"nested calls":          "x = " + rep("f(", n) + "1" + rep(")", n),
		"nested for expression": "x = " + rep("[for a in b : ", n) + "1" + rep("]", n),
		"nested templates":      "x = " + rep("\"${", n) + "1" + rep("}\"", n),
		"nested lists":          "x = " + rep("[", n) + rep("]", n),
		"nested objects":        "x = " + rep("{a=", n) + "1" + rep("}", n),
		"nested blocks":         rep("a {\n", n) + rep("}\n", n),
	} {
		if m, fs := ParseHCL("a"+hclExt, []byte(src)); !m.Broken || len(fs) == 0 {
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
	if got := hclFindings(many); len(got) != 0 {
		t.Fatalf("unexpected findings %v", got)
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
		{"1000 open brackets", rep("[", 1000), "nesting is deeper than 64 levels"},
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
