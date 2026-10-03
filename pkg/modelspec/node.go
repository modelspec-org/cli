package modelspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// NodeType is the JSON type of a Node.
type NodeType int

const (
	NodeNull NodeType = iota
	NodeBool
	NodeNumber
	NodeString
	NodeArray
	NodeObject
)

// Node is a JSON value that keeps object key order and source lines.
// encoding/json's maps cannot do that, and ModelSpec's own tooling compares a
// model with its registered copy with the order of keys and arrays significant
// (the Directory's check says "the order of entities, properties and other keys
// counts"), so export --check does too.
type Node struct {
	Type   NodeType
	Str    string  // NodeString: the value; NodeNumber: the number's text
	Bool   bool    // NodeBool
	Fields []Field // NodeObject, in order
	Items  []*Node // NodeArray
	Line   int     // 1-based source line; 0 when built in memory
	// Dups, set on the root returned by ParseNode only, lists every object key
	// that appears twice in the same object, anywhere in the document, at the
	// line of the repeat. Most JSON readers keep the last of the two, a few the
	// first, so a repeat is never harmless.
	Dups []Field
}

// Field is one member of an object Node.
type Field struct {
	Key   string
	Value *Node
	Line  int
}

// Get returns the first field with the given key.
func (n *Node) Get(key string) (*Node, bool) {
	for _, f := range n.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func str(s string) *Node            { return &Node{Type: NodeString, Str: s} }
func obj(fs ...Field) *Node         { return &Node{Type: NodeObject, Fields: fs} }
func field(k string, v *Node) Field { return Field{Key: k, Value: v} }

// typeName describes a node's type for messages.
func (n *Node) typeName() string {
	switch n.Type {
	case NodeNull:
		return "null"
	case NodeBool:
		return "a boolean"
	case NodeNumber:
		return "a number"
	case NodeString:
		return "a string"
	case NodeArray:
		return "an array"
	default:
		return "an object"
	}
}

// strings returns the node's items when it is an array of strings.
func (n *Node) stringList() ([]string, bool) {
	if n.Type != NodeArray {
		return nil, false
	}
	out := make([]string, 0, len(n.Items))
	for _, it := range n.Items {
		if it.Type != NodeString {
			return nil, false
		}
		out = append(out, it.Str)
	}
	return out, true
}

// lineIndex maps byte offsets to 1-based line numbers.
type lineIndex []int

func newLineIndex(src []byte) lineIndex {
	idx := lineIndex{0}
	for i, b := range src {
		if b == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// at returns the line containing the byte at offset off-1 (the last byte read).
func (l lineIndex) at(off int64) int {
	if off > 0 {
		off--
	}
	return sort.Search(len(l), func(i int) bool { return l[i] > int(off) })
}

// ParseNode reads one JSON document into a Node, keeping key order and lines.
// Anything after the document is an error.
func ParseNode(src []byte) (*Node, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	lines := newLineIndex(src)
	var dups []Field
	n, err := readNode(dec, lines, &dups, 0)
	if err != nil {
		if le, ok := err.(*depthError); ok {
			return nil, &syntaxError{line: le.line, limit: true, msg: fmt.Sprintf("nesting is deeper than %d levels", MaxDepth)}
		}
		return nil, wrapJSONError(err, lines)
	}
	n.Dups = dups
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &syntaxError{line: lines.at(dec.InputOffset()), msg: "unexpected data after the top-level value"}
	}
	return n, nil
}

type syntaxError struct {
	line  int
	msg   string
	limit bool // the document exceeds MaxDepth; it is not a syntax error
}

func (e *syntaxError) Error() string { return e.msg }

func wrapJSONError(err error, lines lineIndex) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return &syntaxError{line: lines.at(se.Offset), msg: se.Error()}
	}
	// Reading tokens from memory fails only on a syntax error or because the
	// input ended.
	return &syntaxError{line: len(lines), msg: "unexpected end of input"}
}

// depthError reports a document nested deeper than MaxDepth; reading stops there,
// so the recursion of readNode is bounded.
type depthError struct{ line int }

func (e *depthError) Error() string { return "nesting is too deep" }

func readNode(dec *json.Decoder, lines lineIndex, dups *[]Field, depth int) (*Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	line := lines.at(dec.InputOffset())
	switch t := tok.(type) {
	case json.Delim:
		if depth++; depth > MaxDepth {
			return nil, &depthError{line}
		}
		if t == '{' {
			n := &Node{Type: NodeObject, Line: line}
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				kline := lines.at(dec.InputOffset())
				v, err := readNode(dec, lines, dups, depth)
				if err != nil {
					return nil, err
				}
				key := kt.(string)
				if seen[key] {
					*dups = append(*dups, Field{Key: key, Value: v, Line: kline})
				}
				seen[key] = true
				n.Fields = append(n.Fields, Field{Key: key, Value: v, Line: kline})
			}
			_, err := dec.Token() // the closing }
			return n, err
		}
		n := &Node{Type: NodeArray, Line: line}
		for dec.More() {
			v, err := readNode(dec, lines, dups, depth)
			if err != nil {
				return nil, err
			}
			n.Items = append(n.Items, v)
		}
		_, err := dec.Token() // the closing ]
		return n, err
	case string:
		return &Node{Type: NodeString, Str: t, Line: line}, nil
	case bool:
		return &Node{Type: NodeBool, Bool: t, Line: line}, nil
	case json.Number:
		return &Node{Type: NodeNumber, Str: t.String(), Line: line}, nil
	default: // nil
		return &Node{Type: NodeNull, Line: line}, nil
	}
}

// Encode writes the node as indented JSON (two spaces) ending in a newline,
// the form JSON.stringify(x, null, 2) produces.
func (n *Node) Encode() []byte {
	var b bytes.Buffer
	n.encode(&b, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func encodeString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// Encoding a string cannot fail.
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func (n *Node) encode(b *bytes.Buffer, depth int) {
	indent := strings.Repeat("  ", depth+1)
	closing := strings.Repeat("  ", depth)
	switch n.Type {
	case NodeNull:
		b.WriteString("null")
	case NodeBool:
		fmt.Fprintf(b, "%t", n.Bool)
	case NodeNumber:
		b.WriteString(n.Str)
	case NodeString:
		b.WriteString(encodeString(n.Str))
	case NodeArray:
		if len(n.Items) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, it := range n.Items {
			b.WriteString(indent)
			it.encode(b, depth+1)
			if i < len(n.Items)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(closing + "]")
	default:
		if len(n.Fields) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, f := range n.Fields {
			b.WriteString(indent + encodeString(f.Key) + ": ")
			f.Value.encode(b, depth+1)
			if i < len(n.Fields)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(closing + "}")
	}
}

// Diff returns "" when a and b are the same JSON value with the same key and
// array order, and otherwise a sentence naming the first difference and its
// path. Whitespace and source lines are not compared.
func Diff(a, b *Node) string {
	return diffAt(a, b, "")
}

func at(path string) string {
	if path == "" {
		return "the document"
	}
	return path
}

func diffAt(a, b *Node, path string) string {
	if a.Type != b.Type {
		return fmt.Sprintf("%s is %s in the first and %s in the second", at(path), a.typeName(), b.typeName())
	}
	switch a.Type {
	case NodeBool:
		if a.Bool != b.Bool {
			return fmt.Sprintf("%s is %t in the first and %t in the second", at(path), a.Bool, b.Bool)
		}
	case NodeNumber, NodeString:
		if a.Str != b.Str {
			return fmt.Sprintf("%s is %s in the first and %s in the second", at(path), quoteNode(a), quoteNode(b))
		}
	case NodeArray:
		for i := 0; i < len(a.Items) && i < len(b.Items); i++ {
			if d := diffAt(a.Items[i], b.Items[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		if len(a.Items) != len(b.Items) {
			return fmt.Sprintf("%s has %d items in the first and %d in the second", at(path), len(a.Items), len(b.Items))
		}
	case NodeObject:
		return diffObject(a, b, path)
	}
	return ""
}

func quoteNode(n *Node) string {
	if n.Type == NodeString {
		return encodeString(n.Str)
	}
	return n.Str
}

func diffObject(a, b *Node, path string) string {
	join := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	for i := 0; i < len(a.Fields) && i < len(b.Fields); i++ {
		fa, fb := a.Fields[i], b.Fields[i]
		if fa.Key != fb.Key {
			if _, ok := b.Get(fa.Key); !ok {
				return fmt.Sprintf("%s has key %q in the first but not in the second", at(path), fa.Key)
			}
			if _, ok := a.Get(fb.Key); !ok {
				return fmt.Sprintf("%s has key %q in the second but not in the first", at(path), fb.Key)
			}
			return fmt.Sprintf("%s has its keys in a different order: %q comes before %q in the first, %q before %q in the second", at(path), fa.Key, fb.Key, fb.Key, fa.Key)
		}
		if d := diffAt(fa.Value, fb.Value, join(fa.Key)); d != "" {
			return d
		}
	}
	if len(a.Fields) > len(b.Fields) {
		return fmt.Sprintf("%s has key %q in the first but not in the second", at(path), a.Fields[len(b.Fields)].Key)
	}
	if len(b.Fields) > len(a.Fields) {
		return fmt.Sprintf("%s has key %q in the second but not in the first", at(path), b.Fields[len(a.Fields)].Key)
	}
	return ""
}
