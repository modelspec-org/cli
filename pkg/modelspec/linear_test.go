package modelspec

import (
	"fmt"
	"strings"
	"testing"
)

// linearShapes are models of n units of each part that the checker's work must
// grow linearly with: the function builds a model's source and says how to read it.
func linearShapes() map[string]func(n int) (string, func(src string) (*Model, []Finding)) {
	return map[string]func(n int) (string, func(src string) (*Model, []Finding)){
		"one use list of many components": func(n int) (string, func(string) (*Model, []Finding)) {
			var b, use strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "component \"C%d\" {\n}\n", i)
				fmt.Fprintf(&use, "\"C%d\", ", i)
			}
			fmt.Fprintf(&b, "record \"E\" {\n  key = []\n  use = [%s\"C0\"]\n}\n", use.String())
			return b.String(), parseHCLString
		},
		"many records that use one big component": func(n int) (string, func(string) (*Model, []Finding)) {
			var b strings.Builder
			b.WriteString("component \"Big\" {\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"f%d\" {\n    type = \"int\"\n  }\n", i)
			}
			b.WriteString("}\n")
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "record \"E%d\" {\n  use = [\"Big\"]\n  key = [\"f1\"]\n}\n", i)
			}
			return b.String(), parseHCLString
		},
		"a long key and many references between records": func(n int) (string, func(string) (*Model, []Finding)) {
			var b strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "record \"E%d\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"r\" {\n    record = \"E%d\"\n  }\n}\n", i, (i+1)%n)
			}
			return b.String(), parseHCLString
		},
		"a record key of many fields": func(n int) (string, func(string) (*Model, []Finding)) {
			var b, key strings.Builder
			for i := 0; i < n; i++ {
				fmt.Fprintf(&b, "  field \"c%d\" {\n    type = \"int\"\n  }\n", i)
				fmt.Fprintf(&key, "\"c%d\", ", i)
			}
			return fmt.Sprintf("record \"r\" {\n  key = [%s\"c0\"]\n%s}\n", key.String(), b.String()), parseHCLString
		},
	}
}

func parseHCLString(src string) (*Model, []Finding)  { return ParseHCL("a"+hclExt, []byte(src)) }
func parseJSONString(src string) (*Model, []Finding) { return ParseJSON("a"+jsonExt, []byte(src)) }

// The index of a unit answers as the scan of every concept it replaced: the first
// declaration of a name wins, for each kind and in the record/component/enum scope.
func TestUnitIndexAgreesWithAScan(t *testing.T) {
	t.Parallel()
	src := "record \"A\" {\n  key = []\n}\nenum \"A\" {\n  values = [\"x\"]\n}\ncomponent \"B\" {\n}\ncomponent \"B\" {\n}\nrecord \"C\" {\n  key = []\n}\n"
	m1, _ := ParseHCL("a"+hclExt, []byte(src))
	m2, _ := ParseHCL("b"+hclExt, []byte("component \"B\" {\n}\nenum \"D\" {\n  values = [\"y\"]\n}\ncomponent \"A\" {\n}\n"))
	u := &unit{models: []*Model{m1, m2}}
	for _, kind := range []Kind{KindRecord, KindComponent, KindEnum} {
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
				if want == "" && c.Name == name && (c.Kind == KindRecord || c.Kind == KindComponent || c.Kind == KindEnum) {
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

// The allocation test above cannot see a scan, which allocates nothing. These
// count the steps the lookups take (concepts visited, members listed, sets
// probed): a unit's index visits each concept once however many lookups there
// are, a member set is built once, and a record's field lookups cost at most
// twice the fields of the components it uses however many lookups there are.
func TestLookupsTakeStepsInProportionToTheModel(t *testing.T) {
	t.Parallel()
	for _, n := range []int{100, 200} {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "record \"E%d\" {\n  key = []\n}\n", i)
		}
		m, _ := ParseHCL("a"+hclExt, []byte(b.String()))
		u := &unit{models: []*Model{m}}
		for i := 0; i < 5*n; i++ {
			if u.find(KindRecord, fmt.Sprintf("E%d", i%n)) == nil {
				t.Fatal("not found")
			}
			if _, ok := u.trioKind("none"); ok {
				t.Fatal("found")
			}
		}
		if u.visits != n {
			t.Errorf("%d concepts, %d lookups: %d concepts visited, want each once", n, 10*n, u.visits)
		}
	}
	// Member sets: one build for a concept's members, however often it is asked for.
	for _, n := range []int{100, 1000} {
		k := &Concept{}
		for i := 0; i < n; i++ {
			k.Members = append(k.Members, Member{Name: fmt.Sprintf("m%d", i)})
		}
		c := &checker{memberSets: map[*Concept]map[string]bool{}}
		for i := 0; i < 5*n; i++ {
			c.memberSet(k)
		}
		if _, members, _ := c.steps(); members != n {
			t.Errorf("%d members: %d listed, want each once", n, members)
		}
	}
	// Field lookups: the probes stop growing once the merged set is built.
	for _, n := range []int{30, 90} {
		var b strings.Builder
		fields := 0
		for i := 0; i < 5; i++ {
			fmt.Fprintf(&b, "component \"C%d\" {\n", i)
			for j := 0; j < n; j++ {
				fmt.Fprintf(&b, "  field \"f%d_%d\" {\n    type = \"int\"\n  }\n", i, j)
				fields++
			}
			b.WriteString("}\n")
		}
		b.WriteString("record \"E\" {\n  use = [\"C0\", \"C1\", \"C2\", \"C3\", \"C4\"]\n  key = []\n}\n")
		m, _ := ParseHCL("a"+hclExt, []byte(b.String()))
		u := &unit{name: "a", models: []*Model{m}}
		c := &checker{byName: map[string][]*unit{"a": {u}}, props: map[*Concept]*propSet{}, memberSets: map[*Concept]map[string]bool{}, units: []*unit{u}}
		p := c.fieldNames(u, u.find(KindRecord, "E"))
		probes := func(lookups int) int {
			for i := 0; i < lookups; i++ {
				p.has("none")
			}
			_, _, probes := c.steps()
			return probes
		}
		few, many := probes(3), probes(20*fields)
		if bound := 2 * (fields + 5); many > bound || many-few > bound {
			t.Errorf("%d fields: %d probes after %d lookups, bound %d", fields, many, 20*fields, bound)
		}
	}
	// Through a whole check, each concept of a unit is visited once and no member
	// set is built twice, at two sizes of each shape.
	for name, build := range linearShapes() {
		for _, n := range []int{32, 128} {
			src, parse := build(n)
			m, _ := parse(src)
			c := runCheck([]*Model{m}, Options{})
			concepts, members, probes := c.steps()
			total := 0
			for _, k := range m.Concepts {
				total += len(k.Members)
			}
			wantConcepts := len(m.Concepts)
			if name == "a record key of many fields" {
				wantConcepts = 0 // it refers to no concept, so no index is built
			}
			if concepts != wantConcepts || members > total || probes > 2*(total+n) {
				t.Errorf("%s, n=%d: %d of %d concepts visited, %d of %d members listed, %d probes", name, n, concepts, wantConcepts, members, total, probes)
			}
		}
	}
}
