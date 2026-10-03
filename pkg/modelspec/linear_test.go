package modelspec

import (
	"fmt"
	"strings"
	"testing"
)

// The checker's work is linear in the size of the model: eight times the model
// allocates about eight times the memory, where a scan for each reference, or a
// map of an entity's properties rebuilt for each bind, took sixty-four times. The
// allocator's own count stands for steps (see allocated): it does not depend on
// time or on the load of the machine. Not parallel, because the count is of the
// whole process.
func TestCheckIsLinear(t *testing.T) {
	// hcl builds a model of n units of each part; json builds the same in JSON.
	shapes := map[string]func(n int) (string, func(src string) (*Model, []Finding)){
		"binds to one entity's properties": func(n int) (string, func(string) (*Model, []Finding)) {
			var b strings.Builder
			b.WriteString("entity \"E\" {\n  key = [\"p0\"]\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  property \"p%d\" {\n    type = \"int\"\n  }\n", i)
			}
			b.WriteString("}\ncollection \"c\" {\n  kind = \"editable\"\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"f%d\" {\n    bind = \"E.p%d\"\n  }\n", i, i)
			}
			b.WriteString("}\n")
			return b.String(), parseHCLString
		},
		"binds to one entity's properties, in JSON": func(n int) (string, func(string) (*Model, []Finding)) {
			var props, fields []string
			for i := 0; i < n; i++ {
				props = append(props, fmt.Sprintf(`"p%d": {"type": "int"}`, i))
				fields = append(fields, fmt.Sprintf(`"f%d": {"bind": "E.p%d"}`, i, i))
			}
			return `{"modelspec": "1.0-draft", "module": {"id": "x", "version": "1"}, "entities": {"E": {"key": ["p0"], "properties": {` +
				strings.Join(props, ", ") + `}}}, "collections": {"c": {"kind": "editable", "fields": {` + strings.Join(fields, ", ") + `}}}}`, parseJSONString
		},
		"one use list of many components": func(n int) (string, func(string) (*Model, []Finding)) {
			var b, use strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "component \"C%d\" {\n}\n", i)
				fmt.Fprintf(&use, "\"C%d\", ", i)
			}
			fmt.Fprintf(&b, "entity \"E\" {\n  key = []\n  use = [%s\"C0\"]\n}\n", use.String())
			return b.String(), parseHCLString
		},
		"many entities that use one big component": func(n int) (string, func(string) (*Model, []Finding)) {
			var b strings.Builder
			b.WriteString("component \"Big\" {\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"f%d\" {\n    type = \"int\"\n  }\n", i)
			}
			b.WriteString("}\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "entity \"E%d\" {\n  use = [\"Big\"]\n  key = [\"f1\"]\n}\n", i)
			}
			return b.String(), parseHCLString
		},
		"a long key and many references between entities": func(n int) (string, func(string) (*Model, []Finding)) {
			var b strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "entity \"E%d\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"int\"\n  }\n  property \"r\" {\n    entity = \"E%d\"\n  }\n}\n", i, (i+1)%n)
			}
			return b.String(), parseHCLString
		},
		"a recordset key of many columns": func(n int) (string, func(string) (*Model, []Finding)) {
			var b, key strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  column \"c%d\" {\n    type = \"int\"\n  }\n", i)
				fmt.Fprintf(&key, "\"c%d\", ", i)
			}
			return fmt.Sprintf("recordset \"r\" {\n  key = [%s\"c0\"]\n  query = \"q\"\n%s}\n", key.String(), b.String()), parseHCLString
		},
	}
	for name, build := range shapes {
		smallSrc, parse := build(128)
		largeSrc, _ := build(1024)
		check := func(src string) func() {
			return func() {
				m, _ := parse(src)
				Check([]*Model{m}, Options{})
			}
		}
		a, b := allocated(check(smallSrc)), allocated(check(largeSrc))
		if ratio := float64(b) / float64(a); ratio > 14 {
			t.Errorf("%s: 8 times the model allocated %.1f times the memory (%d and %d bytes), want about 8", name, ratio, a, b)
		}
	}
}

func parseHCLString(src string) (*Model, []Finding)  { return ParseHCL("a"+hclExt, []byte(src)) }
func parseJSONString(src string) (*Model, []Finding) { return ParseJSON("a"+jsonExt, []byte(src)) }

// The index of a unit answers as the scan of every concept it replaced: the first
// declaration of a name wins, for each kind and in the entity/component/enum scope.
func TestUnitIndexAgreesWithAScan(t *testing.T) {
	t.Parallel()
	src := "entity \"A\" {\n  key = []\n}\nenum \"A\" {\n  values = [\"x\"]\n}\ncomponent \"B\" {\n}\ncomponent \"B\" {\n}\nentity \"C\" {\n  key = []\n}\ncollection \"A\" {\n  kind = \"editable\"\n}\nrecordset \"R\" {\n}\n"
	m1, _ := ParseHCL("a"+hclExt, []byte(src))
	m2, _ := ParseHCL("b"+hclExt, []byte("component \"B\" {\n}\nenum \"D\" {\n  values = [\"y\"]\n}\ncomponent \"A\" {\n}\n"))
	u := &unit{models: []*Model{m1, m2}}
	for _, kind := range []Kind{KindEntity, KindComponent, KindEnum, KindCollection, KindRecordset} {
		for _, name := range []string{"A", "B", "C", "D", "R", "none"} {
			var want *Concept
			for _, m := range u.models {
				for _, c := range m.Concepts {
					if want == nil && c.Kind == kind && c.Name == name {
						want = c
					}
				}
			}
			if got := u.find(kind, name); got != want {
				t.Errorf("find(%s, %s) = %v, the scan finds %v", kind, name, got, want)
			}
		}
	}
	for _, name := range []string{"A", "B", "C", "D", "R", "none"} {
		var want Kind
		for _, m := range u.models {
			for _, c := range m.Concepts {
				if want == "" && c.Name == name && (c.Kind == KindEntity || c.Kind == KindComponent || c.Kind == KindEnum) {
					want = c.Kind
				}
			}
		}
		if got, ok := u.trioKind(name); got != want || ok != (want != "") {
			t.Errorf("trioKind(%s) = %s %v, the scan finds %q", name, got, ok, want)
		}
	}
	if u.index() != u.index() {
		t.Error("the index is built more than once")
	}
}
