package modelspec

import (
	"strings"
	"testing"
)

func TestParseNodeKeepsOrderAndLines(t *testing.T) {
	t.Parallel()
	src := "{\n  \"b\": 1,\n  \"a\": [true, null, \"x\"],\n  \"c\": {\"z\": 1.5e3}\n}\n"
	n, err := ParseNode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if n.Type != NodeObject || n.Line != 1 {
		t.Fatalf("root = %+v", n)
	}
	keys := []string{n.Fields[0].Key, n.Fields[1].Key, n.Fields[2].Key}
	if strings.Join(keys, ",") != "b,a,c" {
		t.Fatalf("keys = %v", keys)
	}
	if n.Fields[0].Line != 2 || n.Fields[1].Line != 3 || n.Fields[2].Line != 4 {
		t.Fatalf("lines = %d %d %d", n.Fields[0].Line, n.Fields[1].Line, n.Fields[2].Line)
	}
	if v, _ := n.Get("b"); v.Type != NodeNumber || v.Str != "1" {
		t.Fatalf("b = %+v", v)
	}
	if _, ok := n.Get("missing"); ok {
		t.Fatal("Get found a missing key")
	}
	a, _ := n.Get("a")
	if len(a.Items) != 3 || a.Items[0].Type != NodeBool || !a.Items[0].Bool || a.Items[1].Type != NodeNull || a.Items[2].Str != "x" {
		t.Fatalf("a = %+v", a)
	}
	c, _ := n.Get("c")
	if z, _ := c.Get("z"); z.Str != "1.5e3" {
		t.Fatalf("number text was not kept: %q", z.Str)
	}
}

func TestParseNodeErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src, msg string
		line           int
	}{
		{"bad character", "{\n  \"a\": x\n}", "invalid character", 2},
		{"bad key", "{1: 2}", "member name must be a string", 1},
		{"missing value", "{\"a\":}", "missing value", 1},
		{"bad array item", "[1, ]", "invalid character", 1},
		{"truncated object", "{\"a\": 1,\n", "unexpected end of JSON input", 1},
		{"truncated array", "[1,\n2", "unexpected end of JSON input", 2},
		{"empty", "", "unexpected end of input", 1},
		{"trailing data", "{}\n\n{}", "unexpected data", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseNode([]byte(tc.src))
			se, ok := err.(*syntaxError)
			if !ok || !strings.Contains(se.Error(), tc.msg) || se.line != tc.line {
				t.Fatalf("err = %#v, want %q at line %d", err, tc.msg, tc.line)
			}
		})
	}
}

func TestEncode(t *testing.T) {
	t.Parallel()
	n := obj(
		field("s", str("a<b>&\"q\"\n")),
		field("n", &Node{Type: NodeNumber, Str: "12"}),
		field("t", &Node{Type: NodeBool, Bool: true}),
		field("f", &Node{Type: NodeBool}),
		field("z", &Node{Type: NodeNull}),
		field("ea", &Node{Type: NodeArray}),
		field("eo", obj()),
		field("a", &Node{Type: NodeArray, Items: []*Node{str("x"), obj(field("k", str("v")))}}),
	)
	want := `{
  "s": "a<b>&\"q\"\n",
  "n": 12,
  "t": true,
  "f": false,
  "z": null,
  "ea": [],
  "eo": {},
  "a": [
    "x",
    {
      "k": "v"
    }
  ]
}
`
	if got := string(n.Encode()); got != want {
		t.Fatalf("Encode =\n%s\nwant\n%s", got, want)
	}
	// What Encode writes parses back to an equal document.
	back, err := ParseNode(n.Encode())
	if err != nil || Diff(n, back) != "" {
		t.Fatalf("round trip: %v %s", err, Diff(n, back))
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	parse := func(s string) *Node {
		n, err := ParseNode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	tests := []struct {
		name, a, b, want string
	}{
		{"same", `{"a":[1,"x",true,null,{"b":2}]}`, `{ "a" : [1, "x", true, null, {"b": 2}] }`, ""},
		{"type", `{"a":1}`, `{"a":"1"}`, `a is a number in the first and a string in the second`},
		{"root type", `[]`, `{}`, `the document is an array in the first and an object in the second`},
		{"bool", `[true]`, `[false]`, `[0] is true in the first and false in the second`},
		{"string", `{"a":{"b":"x"}}`, `{"a":{"b":"y"}}`, `a.b is "x" in the first and "y" in the second`},
		{"number", `{"a":1}`, `{"a":2}`, `a is 1 in the first and 2 in the second`},
		{"array item", `[[1],[2]]`, `[[1],[3]]`, `[1][0] is 2 in the first and 3 in the second`},
		{"array length", `{"a":[1]}`, `{"a":[1,2]}`, `a has 1 items in the first and 2 in the second`},
		{"key only in first", `{"a":1,"b":2}`, `{"a":1,"c":2}`, `the document has key "b" in the first but not in the second`},
		{"key only in second", `{"a":1,"c":2}`, `{"a":1,"b":2}`, `the document has key "c" in the first but not in the second`},
		{"key order", `{"a":1,"b":2}`, `{"b":2,"a":1}`, `has its keys in a different order`},
		{"extra key in first", `{"a":1,"b":2}`, `{"a":1}`, `the document has key "b" in the first but not in the second`},
		{"extra key in second", `{"a":1}`, `{"a":1,"b":2}`, `the document has key "b" in the second but not in the first`},
		{"nested path", `{"x":{"y":{"z":1}}}`, `{"x":{"y":{"w":1}}}`, `x.y has key "z" in the first`},
		{"second only after reorder", `{"a":1,"b":2}`, `{"c":0,"a":1}`, `the document has key "c" in the second`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Diff(parse(tc.a), parse(tc.b))
			if tc.want == "" {
				if got != "" {
					t.Fatalf("Diff = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("Diff = %q, want containing %q", got, tc.want)
			}
		})
	}
}

func TestDiffKeyMissingFromFirst(t *testing.T) {
	t.Parallel()
	a, _ := ParseNode([]byte(`{"x":1,"y":2}`))
	b, _ := ParseNode([]byte(`{"w":0,"y":2}`))
	if got := Diff(a, b); !strings.Contains(got, `key "x" in the first but not in the second`) {
		t.Fatalf("Diff = %q", got)
	}
	// "x" exists in b only after the first position: the first mismatch names
	// the key the second lacks.
	c, _ := ParseNode([]byte(`{"y":2,"w":0}`))
	d, _ := ParseNode([]byte(`{"w":0,"y":2}`))
	if got := Diff(c, d); !strings.Contains(got, "different order") {
		t.Fatalf("Diff = %q", got)
	}
}

func TestTypeNamesAndStringList(t *testing.T) {
	t.Parallel()
	want := map[NodeType]string{NodeNull: "null", NodeBool: "a boolean", NodeNumber: "a number", NodeString: "a string", NodeArray: "an array", NodeObject: "an object"}
	for typ, name := range want {
		if got := (&Node{Type: typ}).typeName(); got != name {
			t.Errorf("typeName(%d) = %q, want %q", typ, got, name)
		}
	}
	if l, ok := (&Node{Type: NodeArray, Items: []*Node{str("a"), str("b")}}).stringList(); !ok || len(l) != 2 {
		t.Errorf("stringList of strings = %v, %v", l, ok)
	}
	if _, ok := (&Node{Type: NodeArray, Items: []*Node{{Type: NodeNumber, Str: "1"}}}).stringList(); ok {
		t.Error("stringList accepted a number")
	}
	if _, ok := str("x").stringList(); ok {
		t.Error("stringList accepted a string")
	}
}

func TestParseNodeReportsRepeatedKeys(t *testing.T) {
	t.Parallel()
	n, err := ParseNode([]byte("{\n \"a\": 1,\n \"b\": {\"x\": 1,\n \"x\": 2},\n \"a\": 3,\n \"c\": [{\"k\": 1, \"k\": 1}]\n}"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range n.Dups {
		got = append(got, d.Key+":"+string(rune('0'+d.Line)))
	}
	if strings.Join(got, " ") != "x:4 a:5 k:6" {
		t.Fatalf("Dups = %v", got)
	}
	// The same key in different objects is not a repeat.
	n, _ = ParseNode([]byte(`{"a": {"a": 1}, "b": {"a": 2}}`))
	if len(n.Dups) != 0 {
		t.Fatalf("Dups = %v", n.Dups)
	}
}
