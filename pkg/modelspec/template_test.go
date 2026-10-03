package modelspec

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// patternModel is a model whose one property has the given text as its pattern.
func patternModel(value string) string {
	return "entity \"E\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"string\"\n    pattern = " + value + "\n  }\n}\n"
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
	for i := 0; i < 3000; i++ {
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
	if both < 800 || libRefused < 400 {
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
		"entity \"A\\$B\" {\n}\n",
		"entity \"A\\%B\" {\n}\n",
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
	src := "entity \"E\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"string\"\n    pattern = <<A\n\ue000$\ue001%\ue002\nA\n    format = <<B\n$$ %% \ue000\nB\n    enum = [<<C\nx$y\nC\n    , \"$\"]\n  }\n}\n"
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
	m, fs = ParseHCL("a"+hclExt, []byte("entity \"A$%B${\" {\n}\n"))
	if len(fs) == 0 || m.Broken == false {
		t.Errorf("an interpolation in a label was accepted: %v", fs)
	}
	m, fs = ParseHCL("a"+hclExt, []byte("entity \"A$%B$${\" {\n}\n"))
	if len(m.Concepts) != 1 || m.Concepts[0].Name != "A$%B${" {
		t.Errorf("label = %v, findings %v", m.Concepts, fs)
	}
}

// allocated returns the bytes the function allocates, measured from the
// allocator's own count, which does not depend on time or the machine's load. The
// calling test is not parallel, so nothing else of the package allocates at the
// same time.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// The work for a string or heredoc is linear in its size: eight times the text
// takes about eight times the memory, where the HCL parser's joining of pieces
// took sixty-four times. (Allocated bytes stand for steps: the joining copies the
// text once for each piece.) Not parallel, see allocated.
func TestManyPieceStringsAreLinear(t *testing.T) {
	for name, build := range map[string]func(n int) string{
		"dollars in a string":  func(n int) string { return patternModel("\"" + rep("$a", n) + "\"") },
		"escapes in a string":  func(n int) string { return patternModel("\"" + rep("$${", n) + "\"") },
		"dollars in a heredoc": func(n int) string { return patternModel("<<EOT\n" + rep("$1, ", n) + "\nEOT") },
	} {
		small, large := build(1200), build(9600)
		a := allocated(func() { ParseHCL("a"+hclExt, []byte(small)) })
		b := allocated(func() { ParseHCL("a"+hclExt, []byte(large)) })
		if ratio := float64(b) / float64(a); ratio > 14 {
			t.Errorf("%s: 8 times the text allocated %.1f times the memory (%d and %d bytes), want about 8 (up to 14: a little more at small sizes)", name, ratio, a, b)
		}
	}
	// And the control: the library's own parser on the same text, which is why the
	// rewrite exists, grows with the square.
	small, large := patternModel("\""+rep("$a", 1000)+"\""), patternModel("\""+rep("$a", 8000)+"\"")
	parse := func(src string) func() {
		return func() { hclsyntax.ParseConfig([]byte(src), "f", hcl.Pos{Line: 1, Column: 1}) }
	}
	if ratio := float64(allocated(parse(large))) / float64(allocated(parse(small))); ratio < 20 {
		t.Errorf("the control no longer grows with the square (ratio %.1f): the rewrite may not be needed", ratio)
	}
}
