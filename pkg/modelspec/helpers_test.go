package modelspec

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// memFS is an in-memory FS. links maps a path to the path it is a symbolic link
// to; "" is a dangling link. alias maps a path to a different canonical path, to
// stand for the absolute name of a relative path.
type memFS struct {
	fstest.MapFS
	written  map[string][]byte
	perms    map[string]fs.FileMode
	writeErr error
	links    map[string]string
	alias    map[string]string
	canonErr map[string]error
}

func newMemFS(files map[string]string) *memFS {
	m := &memFS{MapFS: fstest.MapFS{}, written: map[string][]byte{}, perms: map[string]fs.FileMode{}, links: map[string]string{}, alias: map[string]string{}, canonErr: map[string]error{}}
	for n, s := range files {
		m.MapFS[n] = &fstest.MapFile{Data: []byte(s)}
	}
	return m
}

func (m *memFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.written[name] = data
	m.perms[name] = perm
	return nil
}

func (m *memFS) resolve(name string) string {
	name = filepath.Clean(name)
	if t, ok := m.links[name]; ok {
		return t
	}
	return name
}

func (m *memFS) ReadFile(name string) ([]byte, error) { return m.MapFS.ReadFile(m.resolve(name)) }

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	t := m.resolve(name)
	if t == "" {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return m.MapFS.Stat(t)
}

type linkEntry struct{ fs.DirEntry }

func (linkEntry) Type() fs.FileMode { return fs.ModeSymlink }
func (linkEntry) IsDir() bool       { return false }

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := m.MapFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if _, ok := m.links[filepath.Join(name, e.Name())]; ok {
			entries[i] = linkEntry{e}
		}
	}
	for link, target := range m.links {
		if target == "" && filepath.Dir(link) == filepath.Clean(name) {
			entries = append(entries, linkEntry{fs.FileInfoToDirEntry(fakeInfo{name: filepath.Base(link)})})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

type fakeInfo struct{ name string }

func (f fakeInfo) Name() string     { return f.name }
func (fakeInfo) Size() int64        { return 0 }
func (fakeInfo) Mode() fs.FileMode  { return 0o777 | fs.ModeSymlink }
func (fakeInfo) ModTime() time.Time { return time.Time{} }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

func (m *memFS) Canonical(name string) (string, error) {
	if err, ok := m.canonErr[filepath.Clean(name)]; ok {
		return "", err
	}
	t := m.resolve(name)
	if t == "" {
		return "", &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
	}
	if _, err := m.MapFS.Stat(t); err != nil {
		return "", err
	}
	if a, ok := m.alias[t]; ok {
		return a, nil
	}
	return t, nil
}

var errNoSuch = errors.New("no such file")

// lintFiles lints a whole in-memory tree and returns every finding as a string.
func lintFiles(files map[string]string, opts LintOptions) []string {
	res, err := Lint(newMemFS(files), []string{"."}, opts)
	if err != nil {
		return []string{"error: " + err.Error()}
	}
	out := make([]string, len(res.Findings))
	for i, f := range res.Findings {
		out[i] = f.String()
	}
	return out
}

// run lints a tree under the default profile.
func run(files map[string]string) []string { return lintFiles(files, LintOptions{}) }

// runPublish lints a tree under the publish profile.
func runPublish(files map[string]string) []string {
	return lintFiles(files, LintOptions{Profile: ProfilePublish})
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

const okEntity = `entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
}
`

func sortStrings(s []string) { sort.Strings(s) }
