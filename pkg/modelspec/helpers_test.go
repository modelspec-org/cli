package modelspec

import (
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// memFS is an in-memory FS. links maps a path to the path it is a symbolic link
// to; "" is a dangling link. cwd, when set, is the directory relative names are
// relative to. caseFold makes it a case-insensitive filesystem as far as SameFile
// goes. sizes overrides the size Stat reports, to stand for a huge file.
type memFS struct {
	fstest.MapFS
	written  map[string][]byte
	perms    map[string]fs.FileMode
	writeErr error
	links    map[string]string
	cwd      string
	caseFold bool
	sizes    map[string]int64
	absErr   map[string]error
	statErr  map[string]error
	// modes gives a path's target a type other than a regular file (a named pipe, a
	// device), and streams what Open of it reads: a fake of /dev/zero is a reader
	// that never ends.
	modes   map[string]fs.FileMode
	streams map[string]func() io.Reader
	// openModes is the mode the opened file reports when it is not the one Stat
	// reported: the file was swapped between the check and the open.
	openModes map[string]fs.FileMode
}

func newMemFS(files map[string]string) *memFS {
	m := &memFS{MapFS: fstest.MapFS{}, written: map[string][]byte{}, perms: map[string]fs.FileMode{}, links: map[string]string{}, sizes: map[string]int64{}, absErr: map[string]error{}, statErr: map[string]error{}, modes: map[string]fs.FileMode{}, streams: map[string]func() io.Reader{}, openModes: map[string]fs.FileMode{}}
	for n, s := range files {
		m.MapFS[n] = &fstest.MapFile{Data: []byte(s)}
	}
	return m
}

func (m *memFS) path(name string) string {
	if m.cwd != "" && !filepath.IsAbs(name) {
		name = filepath.Join(m.cwd, name)
	}
	return strings.TrimPrefix(filepath.Clean(name), "/")
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
	name = m.path(name)
	if t, ok := m.links[name]; ok {
		return t
	}
	return name
}

func (m *memFS) Abs(name string) (string, error) {
	if err, ok := m.absErr[m.path(name)]; ok {
		return "", err
	}
	return m.path(name), nil
}

// pathInfo is a FileInfo that remembers which path it describes.
type pathInfo struct {
	fs.FileInfo
	path string
	size int64
	mode fs.FileMode // when not zero, the mode of the file
}

func (p pathInfo) Mode() fs.FileMode {
	if p.mode != 0 {
		return p.mode
	}
	return p.FileInfo.Mode()
}

func (p pathInfo) Size() int64 {
	if p.size >= 0 {
		return p.size
	}
	return p.FileInfo.Size()
}

func (m *memFS) SameFile(a, b fs.FileInfo) bool {
	pa, aok := a.(pathInfo)
	pb, bok := b.(pathInfo)
	if !aok || !bok {
		return false
	}
	if m.caseFold {
		return strings.EqualFold(pa.path, pb.path)
	}
	return pa.path == pb.path
}

// specialFile is an opened file that is not a regular file, or that never ends.
type specialFile struct {
	io.Reader
	info fs.FileInfo
}

func (f specialFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (specialFile) Close() error                 { return nil }

func (m *memFS) Open(name string) (fs.File, error) {
	t := m.resolve(name)
	if stream, ok := m.streams[t]; ok {
		info, err := m.Stat(name)
		if err != nil {
			return nil, err
		}
		if mode, ok := m.openModes[t]; ok {
			info = pathInfo{FileInfo: info, mode: mode, size: -1}
		}
		return specialFile{Reader: stream(), info: info}, nil
	}
	return m.MapFS.Open(t)
}

// Lstat describes a symbolic link as a link, and anything else as Stat does.
func (m *memFS) Lstat(name string) (fs.FileInfo, error) {
	if _, ok := m.links[m.path(name)]; ok {
		return pathInfo{FileInfo: fakeInfo{name: filepath.Base(name)}, path: m.path(name), size: -1}, nil
	}
	return m.Stat(name)
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	if err, ok := m.statErr[m.path(name)]; ok {
		return nil, err
	}
	t := m.resolve(name)
	if t == "" {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	info, err := m.MapFS.Stat(t)
	if err != nil {
		return nil, err
	}
	size := int64(-1)
	if s, ok := m.sizes[m.path(name)]; ok {
		size = s
	}
	return pathInfo{FileInfo: info, path: t, size: size, mode: m.modes[t]}, nil
}

type linkEntry struct{ fs.DirEntry }

func (linkEntry) Type() fs.FileMode { return fs.ModeSymlink }
func (linkEntry) IsDir() bool       { return false }

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = m.path(name)
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
		if target == "" && filepath.Dir(link) == name {
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
