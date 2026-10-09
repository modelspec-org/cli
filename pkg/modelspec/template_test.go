package modelspec

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// patternModel is a model whose one field has the given text as its pattern.
func patternModel(value string) string {
	return "record \"E\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"string\"\n    pattern = " + value + "\n  }\n}\n"
}

// referencePattern reads the pattern the way the HCL library does, from the
// original source, with none of the rewriting parserInput does.
func referencePattern(t *testing.T, src string) (string, bool) {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(src), "f", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return "", false
	}
	prop := f.Body.(*hclsyntax.Body).Blocks[0].Body.Blocks[0]
	val, diags := prop.Body.Attributes["pattern"].Expr.Value(nil)
	if diags.HasErrors() {
		return "", false
	}
	return val.AsString(), true
}

func patternOf(t *testing.T, src string) string {
	t.Helper()
	m, fs := ParseHCL("a"+hclExt, []byte(src))
	if len(fs) != 0 || len(m.Concepts) != 1 {
		t.Fatalf("findings %v", fs)
	}
	a, ok := m.Concepts[0].Members[0].Attr("pattern")
	if !ok {
		t.Fatal("no pattern")
	}
	return a.Value.Str
}

// cliPattern reads the pattern the way the CLI does.
func cliPattern(src string) (string, bool) {
	m, fs := ParseHCL("a"+hclExt, []byte(src))
	if len(fs) != 0 || len(m.Concepts) != 1 {
		return "", false
	}
	a, ok := m.Concepts[0].Members[0].Attr("pattern")
	if !ok {
		return "", false
	}
	return a.Value.Str, true
}

// Rewriting `$` and `%` out of the pieces changes nothing a reader can see, in
// either direction. Over random strings and heredocs full of `$`, `%`, their
// escapes, braces, private-use characters (the ones the rewrite itself uses) and,
// in quoted strings, a backslash before every kind of character, the CLI refuses
// whatever the unmodified HCL library refuses, and where the library accepts, the
// CLI reads the same value .
func TestRewritingLiteralsKeepsTheirValues(t *testing.T) {
	t.Parallel()
	rnd := rand.New(rand.NewSource(1))
	quoted := []string{"a", "b c", "$", "%", "$${", "%%{", "{", "}", "\\n", "\\\"", "\\\\", "\u00e9", "\ue000", "\ue001", "\ue002", "\\u0024", "\\uE000", "\\u00e9", " ", "$$", "%%", "$1", "%s",
		"\\", "\\$", "\\%", "\\{", "\\}", "\\a", "\\q", "\\ ", "\\\u00e9", "\\\ue000", "\\\U0001F600", "\\$${", "\\%%{", "\\u", "\\U0001F600", "\\u00", "\\x41", "\\0"}
	raw := []string{"a", "b c", "$", "%", "$${", "%%{", "{", "}", "\"", "\\", "\u00e9", "\ue000", "\ue001", "\ue002", " ", "\t", "$$", "%%", "$1", "%s", "\n", "\n", "  ", "\U0001F600", "\r", "\\$", "\\n"}
	build := func(alphabet []string, n int) string {
		var b strings.Builder
		prev := ""
		for i := 0; i < n; i++ {
			piece := alphabet[rnd.Intn(len(alphabet))]
			if (prev == "$" || prev == "%") && strings.HasPrefix(piece, "{") {
				piece = "a" // that would write ${ or %{, an interpolation or directive
			}
			b.WriteString(piece)
			prev = piece
		}
		return b.String()
	}
	var both, libRefused int
	for i := 0; i < 500; i++ {
		var src string
		switch i % 3 {
		case 0:
			src = patternModel("\"" + build(quoted, 1+rnd.Intn(12)) + "\"")
		case 1:
			src = patternModel("<<EOT\n" + build(raw, 1+rnd.Intn(40)) + "\nEOT")
		default:
			body := ""
			for l := 0; l < 1+rnd.Intn(5); l++ {
				body += strings.Repeat(" ", rnd.Intn(5)) + build(raw, rnd.Intn(8)) + "\n"
			}
			src = patternModel("<<-EOT\n" + body + "  EOT")
		}
		want, libOK := referencePattern(t, src)
		got, cliOK := cliPattern(src)
		switch {
		case !libOK && cliOK:
			t.Fatalf("source %q: the HCL library refuses it and the CLI reads %q", src, got)
		case !libOK:
			libRefused++
		case !cliOK:
			t.Fatalf("source %q: the HCL library reads %q and the CLI refuses it", src, want)
		case got != want:
			t.Fatalf("source %q: value %q, the HCL library reads %q", src, got, want)
		default:
			both++
		}
	}
	t.Logf("%d read the same, %d refused by both", both, libRefused)
	if both < 150 || libRefused < 90 {
		t.Fatalf("the generated inputs did not cover both directions: %d, %d", both, libRefused)
	}
}

// A carriage return directly after `$` or `%` at the end of a heredoc line is
// part of the `$` piece for the HCL lexer, and stays valid.
func TestCarriageReturnAfterDollarInHeredoc(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"a$\r\r", "a%\r\r", "a$\r\r\nb%\r\r", "$\r", "a$\rb", "$\r$\r\r"} {
		src := patternModel("<<EOT\n" + body + "\nEOT")
		want, libOK := referencePattern(t, src)
		got, cliOK := cliPattern(src)
		if libOK != cliOK || got != want {
			t.Errorf("%q: library %q %v, CLI %q %v", body, want, libOK, got, cliOK)
		}
	}
}

// The inputs of a review that the HCL library refuses and the rewrite used to
// make valid by replacing a `$` or `%` behind a backslash.
func TestBackslashBeforeDollarOrPercentIsRefused(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		patternModel(`"^\$[0-9]+\%$"`),
		"record \"A\\$B\" {\n}\n",
		"record \"A\\%B\" {\n}\n",
	} {
		if _, fs := ParseHCL("a"+hclExt, []byte(src)); len(fs) == 0 {
			t.Errorf("accepted %q", src)
		}
	}
	// An escaped backslash and then a `$` is a backslash and a `$`.
	if got := patternOf(t, patternModel(`"a\\$b\\%"`)); got != `a\$b\%` {
		t.Errorf("value %q", got)
	}
}

// A private-use character in a heredoc is not mistaken for what stands in for `$`
// or `%`, and a heredoc beside another keeps each one's own text.
func TestHeredocEscapesAreKeptApart(t *testing.T) {
	t.Parallel()
	src := "record \"E\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"string\"\n    pattern = <<A\n\ue000$\ue001%\ue002\nA\n    format = <<B\n$$ %% \ue000\nB\n    enum = [<<C\nx$y\nC\n    , \"$\"]\n  }\n}\n"
	m, fs := ParseHCL("a"+hclExt, []byte(src))
	if len(fs) != 0 {
		t.Fatalf("findings %v", fs)
	}
	mem := m.Concepts[0].Members[0]
	want := map[string]string{"pattern": "\ue000$\ue001%\ue002\n", "format": "$$ %% \ue000\n"}
	for name, text := range want {
		if a, _ := mem.Attr(name); a.Value.Str != text {
			t.Errorf("%s = %q, want %q", name, a.Value.Str, text)
		}
	}
	enum, _ := mem.Attr("enum")
	if len(enum.Value.Items) != 2 || enum.Value.Items[0].Str != "x$y\n" || enum.Value.Items[1].Str != "$" {
		t.Errorf("enum = %+v", enum.Value)
	}
	// Block labels are quoted strings: `$` there goes through the escapes.
	m, fs = ParseHCL("a"+hclExt, []byte("record \"A$%B${\" {\n}\n"))
	if len(fs) == 0 || m.Broken == false {
		t.Errorf("an interpolation in a label was accepted: %v", fs)
	}
	m, fs = ParseHCL("a"+hclExt, []byte("record \"A$%B$${\" {\n}\n"))
	if len(m.Concepts) != 1 || m.Concepts[0].Name != "A$%B${" {
		t.Errorf("label = %v, findings %v", m.Concepts, fs)
	}
}

// An offset in the parser's input maps back to the source, whatever was rewritten
// before it.
func TestOffsetsMapBackToTheSource(t *testing.T) {
	t.Parallel()
	src := []byte("before \"$\" \"%$$\" <<EOT\n$ %\nEOT\nafter \"$${\" entity\n")
	input, _, _, offsets := parserInput(src, lexHCL("a.hcl", src))
	if string(input) == string(src) || len(offsets) == 0 {
		t.Fatalf("nothing was rewritten: %q", input)
	}
	for _, word := range []string{"before", "after", "entity"} {
		at := strings.Index(string(input), word)
		if got := offsets.source(at); got != strings.Index(string(src), word) {
			t.Errorf("%s: input offset %d maps to %d, want %d", word, at, got, strings.Index(string(src), word))
		}
	}
	if (offsetMap(nil)).source(7) != 7 {
		t.Error("an input that was not rewritten maps to itself")
	}
}
