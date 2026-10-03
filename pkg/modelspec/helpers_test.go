package modelspec

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// run parses every file by its extension and checks them together, returning
// all findings as strings.
func run(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var models []*Model
	var all []Finding
	for _, n := range names {
		m, fs := Parse(n, []byte(files[n]))
		models = append(models, m)
		all = append(all, fs...)
	}
	all = append(all, Check(models)...)
	SortFindings(all)
	out := make([]string, len(all))
	for i, f := range all {
		out[i] = f.String()
	}
	return out
}

// expect fails unless each want substring matches a distinct finding and there
// are no other findings.
func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	remaining := append([]string(nil), got...)
	for _, w := range want {
		found := -1
		for i, g := range remaining {
			if strings.Contains(g, w) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Errorf("no finding contains %q\n got: %s", w, strings.Join(got, "\n      "))
			continue
		}
		remaining = append(remaining[:found], remaining[found+1:]...)
	}
	for _, g := range remaining {
		t.Errorf("unexpected finding: %s", g)
	}
}

// memFS is an in-memory FS.
type memFS struct {
	fstest.MapFS
	written  map[string][]byte
	writeErr error
}

func newMemFS(files map[string]string) *memFS {
	m := &memFS{MapFS: fstest.MapFS{}, written: map[string][]byte{}}
	for n, s := range files {
		m.MapFS[n] = &fstest.MapFile{Data: []byte(s)}
	}
	return m
}

func (m *memFS) WriteFile(name string, data []byte, _ fs.FileMode) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.written[name] = data
	return nil
}

const okEntity = `entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`
