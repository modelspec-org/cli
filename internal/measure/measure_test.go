// Package measure holds the tests that measure the whole process: the bytes the
// allocator hands out stand for steps, and the stack limit is the process's. They
// cannot run in parallel with other tests of their package, so they run here, in a
// test binary of their own, beside the tests of every other package. They use only
// the public readers and checker.
package measure

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

func rep(s string, n int) string { return strings.Repeat(s, n) }

func parseHCL(src string) (*modelspec.Model, []modelspec.Finding) {
	return modelspec.ParseHCL("a.modelspec.hcl", []byte(src))
}

func parseJSON(src string) (*modelspec.Model, []modelspec.Finding) {
	return modelspec.ParseJSON("a.modelspec.json", []byte(src))
}

// patternModel is a model whose one field has the given text as its pattern.
func patternModel(value string) string {
	return "record \"E\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"string\"\n    pattern = " + value + "\n  }\n}\n"
}

// linearShapes are models of n units of each part that the checker's work must
// grow linearly with: the function builds a model's source and says how to read it.
func linearShapes() map[string]func(n int) (string, func(src string) (*modelspec.Model, []modelspec.Finding)) {
	return map[string]func(n int) (string, func(src string) (*modelspec.Model, []modelspec.Finding)){
		"one use list of many components": func(n int) (string, func(string) (*modelspec.Model, []modelspec.Finding)) {
			var b, use strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "component \"C%d\" {\n}\n", i)
				fmt.Fprintf(&use, "\"C%d\", ", i)
			}
			fmt.Fprintf(&b, "record \"E\" {\n  key = []\n  use = [%s\"C0\"]\n}\n", use.String())
			return b.String(), parseHCL
		},
		"many records that use one big component": func(n int) (string, func(string) (*modelspec.Model, []modelspec.Finding)) {
			var b strings.Builder
			b.WriteString("component \"Big\" {\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"f%d\" {\n    type = \"int\"\n  }\n", i)
			}
			b.WriteString("}\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "record \"E%d\" {\n  use = [\"Big\"]\n  key = [\"f1\"]\n}\n", i)
			}
			return b.String(), parseHCL
		},
		"a long key and many references between records": func(n int) (string, func(string) (*modelspec.Model, []modelspec.Finding)) {
			var b strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "record \"E%d\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"r\" {\n    record = \"E%d\"\n  }\n}\n", i, (i+1)%n)
			}
			return b.String(), parseHCL
		},
		"a record key of many fields": func(n int) (string, func(string) (*modelspec.Model, []modelspec.Finding)) {
			var b, key strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"c%d\" {\n    type = \"int\"\n  }\n", i)
				fmt.Fprintf(&key, "\"c%d\", ", i)
			}
			return fmt.Sprintf("record \"r\" {\n  key = [%s\"c0\"]\n%s}\n", key.String(), b.String()), parseHCL
		},
	}
}

// The checker's work is linear in the size of the model: eight times the model
// allocates about eight times the memory, where a scan for each reference, or a
// map of an record's fields rebuilt for each key lookup, took sixty-four times. The
// allocator's own count stands for steps (see allocated): it does not depend on
// time or on the load of the machine. Not parallel, because the count is of the
// whole process.
func TestCheckIsLinear(t *testing.T) {
	for name, build := range linearShapes() {
		smallSrc, parse := build(80)
		largeSrc, _ := build(640)
		check := func(src string) func() {
			return func() {
				m, _ := parse(src)
				modelspec.Check([]*modelspec.Model{m}, modelspec.Options{})
			}
		}
		a, b := allocated(check(smallSrc)), allocated(check(largeSrc))
		if ratio := float64(b) / float64(a); ratio > 14 {
			t.Errorf("%s: 8 times the model allocated %.1f times the memory (%d and %d bytes), want about 8", name, ratio, a, b)
		}
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
		small, large := build(700), build(5600)
		a := allocated(func() { modelspec.ParseHCL("a"+".modelspec.hcl", []byte(small)) })
		b := allocated(func() { modelspec.ParseHCL("a"+".modelspec.hcl", []byte(large)) })
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

// The parser itself must never be reached by input that would overflow its stack.
// This test lowers the stack limit of the process to 4 MiB (the parser needs
// several KB of stack for each item of these chains, so one that recursed would
// die within a few thousand items; a Go stack overflow is fatal, and the run
// would fail) and feeds ParseHCL 3,000 repeats of each construct that makes the
// parser recurse. It is not parallel, because the limit is process-wide.
func TestHostileInputDoesNotReachTheParserStack(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(4 << 20))
	const n = 3000
	for name, src := range map[string]string{
		"a full splat":                "record \"A\" {\n  key = a" + rep("[*]", n) + "\n}\n",
		"an attribute splat":          "x = a" + rep(".*", n),
		"heredoc lines":               "x = <<EOT\n" + rep("a\n", n) + "EOT\n",
		"directives":                  "x = <<EOT\n" + rep("%{\nif x}", n) + "EOT\n",
		"hidden directives":           "x = <<EOT\n" + rep("%{/**/if true}", n) + "EOT\n",
		"unary minus":                 "x = " + rep("-", n) + "1",
		"unary bangs":                 "x = " + rep("!", n) + "true",
		"conditionals":                "x = " + rep("a ? b : ", n) + "c",
		"nested conditionals":         "x = " + rep("a ? ", n) + "b" + rep(" : c", n),
		"a sum":                       "x = 1" + rep(" + 1", n),
		"a traversal":                 "x = a" + rep(".b", n),
		"indexes":                     "x = a" + rep("[0]", n),
		"namespaces":                  "x = " + rep("a::", n) + "b()",
		"nested calls":                "x = " + rep("f(", n) + "1" + rep(")", n),
		"nested for expression":       "x = " + rep("[for a in b : ", n) + "1" + rep("]", n),
		"nested templates":            "x = " + rep("\"${", n) + "1" + rep("}\"", n),
		"nested lists":                "x = " + rep("[", n) + rep("]", n),
		"closers, then nested lists":  rep("]", n) + "\nx = " + rep("[", n),
		"closing braces, then blocks": rep("}\n", n) + rep("a {\n", n),
		"nested objects":              "x = " + rep("{a=", n) + "1" + rep("}", n),
		"nested blocks":               rep("a {\n", n) + rep("}\n", n),
	} {
		if m, fs := modelspec.ParseHCL("a"+".modelspec.hcl", []byte(src)); !m.Broken || len(fs) == 0 {
			t.Errorf("%s: broken %v, findings %v", name, m.Broken, fs)
		}
	}
}

// Writing a number is a constant amount of work, whatever its spelling: a list of
// fractions and of numbers with a negative exponent allocates in proportion to its
// length, and the bytes for each number (the parser's own number among them, about
// 3 to 4.5 KB) stay within a quarter of what they are. The HCL library's way of
// printing a 512-bit value as the shortest decimal, which took 20 to 35
// microseconds for each such number and made a megabyte of them take five seconds,
// adds its own allocation to that. Bytes stand for steps, see allocated.
func TestNumbersAreWrittenInConstantWork(t *testing.T) {
	for item, perNumber := range map[string]float64{"0.1": 3700, "1e-99": 5600, "123.456e-7": 3800, "9e99": 5200} {
		list := func(n int) string { return "enum \"E\" {\n  values = [" + rep(item+", ", n) + "0]\n}\n" }
		small, large := list(800), list(6400)
		a := allocated(func() { parseHCL(small) })
		b := allocated(func() { parseHCL(large) })
		if ratio := float64(b) / float64(a); ratio > 14 {
			t.Errorf("%s: 8 times the numbers allocated %.1f times the memory (%d and %d bytes)", item, ratio, a, b)
		}
		if each := float64(b) / 6400; each > perNumber {
			t.Errorf("%s: %.0f bytes allocated for each number, want at most %.0f", item, each, perNumber)
		}
	}
}
