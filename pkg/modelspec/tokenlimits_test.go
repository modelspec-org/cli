package modelspec

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// hclWith is a model whose entity has one property carrying the given attribute
// line.
func hclWith(line string) string {
	return "entity \"E\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"string\"\n    " + line + "\n  }\n}\n"
}

// jsonWith is the JSON form of a model whose entity has one property whose
// attributes are the given JSON object members.
func jsonWith(members string) string {
	return `{"modelspec": "1.0-draft", "module": {"id": "x", "name": "x", "version": "1"}, "entities": {"E": {"key": ["id"], "properties": {"id": {"type": "string"` + members + `}}}}}`
}

func limitFinding(t *testing.T, fs []Finding, want string) {
	t.Helper()
	if len(fs) != 1 || fs[0].Rule != RuleLimit || !strings.Contains(fs[0].Message, want) {
		t.Errorf("want one limit finding with %q, got %v", want, fs)
	}
}

// A number at the limit is read; one character or one power of ten past it is
// refused, with what was counted and the limit, by both readers.
func TestNumberLimitBoundary(t *testing.T) {
	t.Parallel()
	forty := "1" + rep("0", MaxNumberLength-1)
	for _, tc := range []struct {
		name, num, want string // want is "" when accepted
	}{
		{"40 digits", forty, ""},
		{"41 digits", forty + "0", "a number is 41 characters long; the limit is 40"},
		{"a decimal of 40 characters", "0." + rep("1", 38), ""},
		{"a decimal of 41 characters", "0." + rep("1", 39), "41 characters"},
		{"exponent 100", "1e100", ""},
		{"exponent 101", "1e101", "the exponent 101; the limit is 100"},
		{"exponent -100", "1e-100", ""},
		{"exponent -101", "1e-101", "the exponent -101; the limit is 100"},
		{"exponent +100", "1E+100", ""},
		{"exponent +101", "1E+101", "the exponent +101"},
		{"the reviewer's number", "1e10000000", "the exponent 10000000"},
		{"an exponent that overflows an integer", "1e" + rep("9", 30), "the exponent"},
		{"4000 digits", rep("9", 4000), "a number is 4000 characters long"},
	} {
		hcl := hclWith("max_len = " + tc.num)
		_, fs := ParseHCL("a"+hclExt, []byte(hcl))
		neg := hclWith("max_len = -" + tc.num)
		_, nfs := ParseHCL("a"+hclExt, []byte(neg))
		js := jsonWith(`, "max_len": ` + tc.num)
		_, jfs := ParseJSON("a"+jsonExt, []byte(js))
		njs := jsonWith(`, "max_len": -` + tc.num)
		_, njfs := ParseJSON("a"+jsonExt, []byte(njs))
		for form, got := range map[string][]Finding{"hcl": fs, "hcl negative": nfs, "json": jfs, "json negative": njfs} {
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("%s, %s: %v", tc.name, form, got)
				}
				continue
			}
			limitFinding(t, got, tc.want)
		}
	}
}

// Every number that is accepted is read exactly: the value it is written as is
// the value that is kept and compared. Two numbers are equal when, and only when,
// they are the same value, however they are spelled; two that differ in the last
// place of the largest allowed spelling are different.
func TestAcceptedNumbersAreExact(t *testing.T) {
	t.Parallel()
	rnd := rand.New(rand.NewSource(7))
	spell := func() string {
		digits := 1 + rnd.Intn(MaxNumberLength-8)
		point := -1
		if digits > 1 && rnd.Intn(2) == 0 {
			point = 1 + rnd.Intn(digits-1)
		}
		var b strings.Builder
		for i := 0; i < digits; i++ {
			if i == point {
				b.WriteByte('.')
			}
			b.WriteByte(byte('0' + rnd.Intn(10)))
		}
		if rnd.Intn(2) == 0 {
			fmt.Fprintf(&b, "e%+d", rnd.Intn(2*MaxNumberExponent+1)-MaxNumberExponent)
		}
		return b.String()
	}
	exact := func(text string) *big.Rat {
		r, ok := new(big.Rat).SetString(text)
		if !ok {
			t.Fatalf("%q is not a number", text)
		}
		return r
	}
	read := func(nums ...string) ([]string, []Finding) {
		src := "enum \"E\" {\n  values = [" + strings.Join(nums, ", ") + "]\n}\n"
		m, fs := ParseHCL("a"+hclExt, []byte(src))
		var out []string
		if len(m.Concepts) == 1 {
			for _, it := range m.Concepts[0].Attrs[0].Value.Items {
				out = append(out, it.Str)
			}
		}
		return out, fs
	}
	accepted, equal := 0, 0
	for i := 0; i < 1500; i++ {
		a, b := spell(), spell()
		if i%5 == 0 { // the same value, spelled another way
			b = "00" + a
			if !strings.ContainsAny(a, "e.") {
				b = a + "e0"
			}
		}
		if i%7 == 0 { // the same digits with the last one changed
			last := a[:strings.IndexAny(a+"e", "e")]
			if c := last[len(last)-1]; c >= '0' && c <= '8' {
				b = a[:len(last)-1] + string(c+1) + a[len(last):]
			}
		}
		texts, fs := read(a, b)
		if len(texts) != 2 {
			t.Fatalf("%q, %q: %v", a, b, fs)
		}
		for j, in := range []string{a, b} {
			if exact(texts[j]).Cmp(exact(in)) != 0 {
				t.Fatalf("%q was read as %q", in, texts[j])
			}
		}
		accepted += 2
		same := exact(a).Cmp(exact(b)) == 0
		if same != (texts[0] == texts[1]) {
			t.Fatalf("%q and %q: same value %v, read as %q and %q", a, b, same, texts[0], texts[1])
		}
		if same {
			equal++
		}
	}
	// The extremes of the limit.
	nines := rep("9", MaxNumberLength)
	for _, pair := range [][2]string{
		{nines, nines[:MaxNumberLength-1] + "8"},
		{nines[:MaxNumberLength-2] + "e0", nines[:MaxNumberLength-2] + "e1"},
		{"0." + rep("0", MaxNumberLength-3) + "1", "0." + rep("0", MaxNumberLength-3) + "2"},
		{"1e100", "1e-100"},
		{"1" + rep("0", MaxNumberLength-1), "1" + rep("0", MaxNumberLength-2) + "1"},
	} {
		texts, fs := read(pair[0], pair[1])
		if len(fs) != 0 || len(texts) != 2 || texts[0] == texts[1] {
			t.Errorf("%v: read as %v, findings %v", pair, texts, fs)
		}
	}
	// The check compares those values: integers of 40 digits that differ are two
	// values, and two spellings of one integer are a repeat.
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [" + nines + ", " + nines[:MaxNumberLength-1] + "8]\n}\n"}))
	expect(t, run(map[string]string{"a" + hclExt: "enum \"E\" {\n  values = [1e2, 100]\n}\n"}), `duplicate value "100"`)
	if accepted < 2500 || equal < 100 {
		t.Fatalf("the random numbers covered too little: %d accepted, %d equal pairs", accepted, equal)
	}
}

// A name at the limit is accepted and one byte longer is refused, wherever the
// standard puts a name: a block label, an identifier, an attribute that holds a
// name and the items of the ones that hold a list; and the JSON form's keys and
// the same strings. Text that is not a name is not limited by it.
func TestNameLimitBoundary(t *testing.T) {
	t.Parallel()
	ok, long := rep("a", MaxNameLength), rep("a", MaxNameLength+1)
	hclCases := map[string]string{
		"entity label":     "entity \"%s\" {\n}\n",
		"second label":     "entity \"x\" \"%s\" {\n}\n",
		"block type":       "%s \"x\" {\n}\n",
		"attribute name":   "entity \"x\" {\n  %s = 1\n}\n",
		"type":             "entity \"x\" {\n  property \"p\" {\n    type = \"%s\"\n  }\n}\n",
		"entity reference": "entity \"x\" {\n  property \"p\" {\n    entity = \"%s\"\n  }\n}\n",
		"enum reference":   "entity \"x\" {\n  property \"p\" {\n    enum = \"%s\"\n  }\n}\n",
		"key item":         "entity \"x\" {\n  key = [\"a\", \"%s\"]\n}\n",
		"first key item":   "entity \"x\" {\n  key = [\"%s\"]\n}\n",
		"use item":         "entity \"x\" {\n  use = [\n    \"%s\",\n  ]\n}\n",
	}
	for name, tmpl := range hclCases {
		if fs := hclFindings(fmt.Sprintf(tmpl, ok)); len(fs) != 0 {
			t.Errorf("%s at the limit: %v", name, fs)
		}
		limitFinding(t, hclFindings(fmt.Sprintf(tmpl, long)), "is 256 bytes long; the limit is 255")
	}
	// Strings that are values, not names, may be longer (up to the file's limit).
	for _, line := range []string{"pattern = \"" + rep("a", 5000) + "\"", "format = <<EOT\n" + rep("a", 5000) + "\nEOT", "enum = [\"" + rep("a", 5000) + "\"]"} {
		if fs := hclFindings(hclWith(line)); len(fs) != 0 {
			t.Errorf("%.20s...: %v", line, fs)
		}
	}
	jsonCases := map[string]string{
		"key":         `{"%s": 1}`,
		"name":        `{"name": "%s"}`,
		"type":        `{"type": "%s"}`,
		"key item":    `{"key": ["a", "%s"]}`,
		"use item":    `{"use": ["%s"]}`,
		"nested name": `{"a": {"b": [{"type": "%s"}]}}`,
	}
	for name, tmpl := range jsonCases {
		if _, err := ParseNode([]byte(fmt.Sprintf(tmpl, ok))); err != nil {
			t.Errorf("JSON %s at the limit: %v", name, err)
		}
		_, err := ParseNode([]byte(fmt.Sprintf(tmpl, long)))
		if se, isLimit := err.(*syntaxError); !isLimit || !se.limit || !strings.Contains(se.msg, "is 256 bytes long; the limit is 255") {
			t.Errorf("JSON %s over the limit: %v", name, err)
		}
	}
	for _, members := range []string{`"pattern": "` + rep("a", 5000) + `"`, `"enum": ["` + rep("a", 5000) + `"]`, `"query": "` + rep("a", 5000) + `"`} {
		if _, err := ParseNode([]byte("{" + members + "}")); err != nil {
			t.Errorf("JSON %.20s...: %v", members, err)
		}
	}
	_, fs := ParseJSON("a"+jsonExt, []byte(jsonWith(`, "`+long+`": 1`)))
	limitFinding(t, fs, "a key is 256 bytes long")
	// Through the whole pipeline, with the name where the review put it.
	_, fs = ParseHCL("a"+hclExt, []byte("entity \""+rep("A", 2_000_000)+"\" {\n}\n"))
	limitFinding(t, fs, "a name is 2000000 bytes long")
}

// The pieces of a quoted string (the lexer cuts at each `$` and `%`) add up to
// the name's length.
func TestNameLengthCountsEveryPieceOfAString(t *testing.T) {
	t.Parallel()
	piece := "a$$"
	name := rep(piece, MaxNameLength/len(piece)) // 255 bytes
	if fs := hclFindings("entity \"" + name + "\" {\n}\n"); len(fs) != 0 {
		t.Errorf("at the limit: %v", fs)
	}
	limitFinding(t, hclFindings("entity \""+name+"b\" {\n}\n"), "256 bytes")
}

// Findings about limits are capped like the other refusals of the lexer's tokens.
func TestLimitFindingsAreCapped(t *testing.T) {
	t.Parallel()
	got := hclFindings(rep("x = 1e999\n", 5000))
	if len(got) != MaxSyntaxFindings+1 || !strings.Contains(got[len(got)-1].Message, "numbers and names over their limits follows; only the first 50 are shown") || got[len(got)-1].Rule != RuleLimit {
		t.Fatalf("%d findings, last %+v", len(got), got[len(got)-1])
	}
}

func TestClipText(t *testing.T) {
	t.Parallel()
	if got := clipText("short", 10); got != "short" {
		t.Errorf("clipText = %q", got)
	}
	long := rep("é", 5000) // two bytes each, so a cut can fall inside one
	for n := 30; n < 60; n++ {
		got := clipText(long, n)
		if len(got) > n || !strings.Contains(got, "…[10000 bytes in all]") || strings.ContainsRune(got, '�') {
			t.Fatalf("clipText(%d) = %q (%d bytes)", n, got, len(got))
		}
	}
	// A limit smaller than the marker leaves just the marker.
	if got := clipText("abcdefghijklmnopqrstuvwxyz", 5); !strings.HasPrefix(got, "…[26 bytes") {
		t.Errorf("clipText = %q", got)
	}
}

// One run's output is bounded whatever the input: at most MaxFindings+1 findings
// of at most MaxMessageBytes each. The input here makes a finding for every one
// of 1,500 properties, each echoing text of the longest length a name may have,
// and 200 repeats of a 4,000-byte value that is not a name.
func TestOutputIsBounded(t *testing.T) {
	t.Parallel()
	name := rep("n", MaxNameLength)
	var b strings.Builder
	b.WriteString("entity \"" + name + "\" {\n  key = [\"" + name + "\"]\n")
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&b, "  property \"%s%d\" {\n    type = \"%s\"\n  }\n", name[:200], i, name)
	}
	b.WriteString("}\nenum \"E\" {\n  values = [" + rep("\""+rep("v", 4000)+"\", ", 200) + "\"x\"]\n}\n")
	const path = "long.modelspec.hcl"
	res, err := Lint(newMemFS(map[string]string{path: b.String()}), []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != MaxFindings+1 {
		t.Fatalf("%d findings, %v", len(res.Findings), err)
	}
	total := 0
	for _, f := range res.Findings {
		if len(f.Message) > MaxMessageBytes {
			t.Fatalf("a message of %d bytes: %.100s", len(f.Message), f.Message)
		}
		total += len(f.String()) + 1
	}
	// The bound the README states: a line is the path, the line number, the
	// severity, the message and the rule (at most 64 bytes besides the path and the message).
	if limit := (MaxFindings + 1) * (len(path) + 64 + MaxMessageBytes); total > limit {
		t.Fatalf("%d bytes of output, over the bound %d", total, limit)
	}
	t.Logf("%d findings, %d bytes", len(res.Findings), total)
}

// Every finding of every reader and checker goes through findingList.put, which
// cuts the message; and the checker cuts each piece of text it echoes, so the end
// of a message is not lost to a long name.
func TestMessagesAreCutWhereTheyAreMade(t *testing.T) {
	t.Parallel()
	var l findingList
	l.put(Finding{File: "f", Rule: "r", Severity: SeverityError, Message: rep("m", 1<<20)})
	if got := l.list[0].Message; len(got) != MaxMessageBytes || !strings.Contains(got, "…[1048576 bytes in all]") {
		t.Errorf("message of %d bytes: %.60s", len(got), got)
	}
	// A name of the limit's length is shown whole; a value past it is cut, and the
	// words after it stay.
	v := rep("v", 5000)
	got := run(map[string]string{"a" + jsonExt: `{"modelspec": "` + v + `", "module": {"id": "x", "version": "1"}}`})
	if len(got) == 0 || !strings.Contains(got[0], `"modelspec" is "vvvv`) || !strings.Contains(got[0], "bytes in all]\"; the only defined value is") {
		t.Errorf("findings %v", got)
	}
	got = run(map[string]string{"a" + hclExt: "entity \"E\" {\n  key = [\"" + rep("k", MaxNameLength) + "\"]\n}\n"})
	if len(got) != 1 || !strings.Contains(got[0], rep("k", MaxNameLength)+`" is not a property`) {
		t.Errorf("findings %v", got)
	}
}
