package modelspec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscover(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"a.modelspec.hcl":              okEntity,
		"sub/b.modelspec.json":         "{}",
		"sub/deeper/c.modelspec.hcl":   okEntity,
		"sub/readme.md":                "",
		"sub/model.hcl":                "",
		".hidden/d.modelspec.hcl":      okEntity,
		"node_modules/e.modelspec.hcl": okEntity,
		"other/f.modelspec.hcl":        okEntity,
	})
	got, err := Discover(fsys, []string{".", "other/f.modelspec.hcl", "a.modelspec.hcl"})
	if err != nil {
		t.Fatal(err)
	}
	want := "a.modelspec.hcl other/f.modelspec.hcl sub/b.modelspec.json sub/deeper/c.modelspec.hcl"
	if strings.Join(got, " ") != want {
		t.Fatalf("Discover = %v, want %s", got, want)
	}
	// An explicitly named file is taken even in a hidden directory.
	got, err = Discover(fsys, []string{".hidden/d.modelspec.hcl"})
	if err != nil || len(got) != 1 {
		t.Fatalf("explicit hidden file = %v, %v", got, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"x.txt": "", "d/y.modelspec.hcl": okEntity})
	if _, err := Discover(fsys, []string{"missing"}); err == nil {
		t.Error("a missing path was accepted")
	}
	if _, err := Discover(fsys, []string{"x.txt"}); err == nil || !strings.Contains(err.Error(), "not a ModelSpec file") {
		t.Errorf("a non-model file: %v", err)
	}
	boom := errors.New("boom")
	if _, err := Discover(&failingFS{memFS: fsys, readDirErr: boom}, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("ReadDir error: %v", err)
	}
	if _, err := Discover(&failingFS{memFS: fsys, readDirErr: boom, failAt: "d"}, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("nested ReadDir error: %v", err)
	}
}

type failingFS struct {
	*memFS
	readDirErr error
	failAt     string // when set, only ReadDir of this directory fails
}

func (f *failingFS) ReadDir(name string) ([]os.DirEntry, error) {
	if f.readDirErr != nil && (f.failAt == "" || f.failAt == name) {
		return nil, f.readDirErr
	}
	return f.memFS.ReadDir(name)
}

func TestLoadAndLint(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"ok.modelspec.hcl":    okEntity,
		"bad.modelspec.hcl":   "entity \"A\" {\n  key = [\"id\"]\n}\n",
		"data.modelspec.json": doc(jEntities),
	})
	findings, n, err := Lint(fsys, []string{"."})
	if err != nil || n != 3 {
		t.Fatalf("Lint = %v files, %v", n, err)
	}
	if len(findings) != 1 || findings[0].File != "bad.modelspec.hcl" || findings[0].Rule != RuleKey {
		t.Fatalf("findings = %v", findings)
	}
	if _, _, err := Lint(fsys, []string{"missing"}); err == nil {
		t.Error("Lint of a missing path succeeded")
	}
	if _, _, err := Lint(newMemFS(map[string]string{"x.txt": ""}), []string{"."}); err == nil || !strings.Contains(err.Error(), "no ModelSpec files") {
		t.Errorf("empty directory: %v", err)
	}
	if _, _, err := Lint(&unreadableFS{fsys}, []string{"."}); err == nil {
		t.Error("an unreadable file was not an error")
	}
	if _, _, err := Load(&unreadableFS{fsys}, []string{"ok.modelspec.hcl"}); err == nil {
		t.Error("Load of an unreadable file succeeded")
	}
}

type unreadableFS struct{ *memFS }

func (unreadableFS) ReadFile(string) ([]byte, error) { return nil, errors.New("unreadable") }

func TestOSFS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var fsys FS = OSFS{}
	file := filepath.Join(dir, "m.modelspec.hcl")
	if err := fsys.WriteFile(file, []byte(okEntity), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := fsys.ReadFile(file); err != nil || string(b) != okEntity {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	if info, err := fsys.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("Stat = %v, %v", info, err)
	}
	entries, err := fsys.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir = %v, %v", entries, err)
	}
	findings, n, err := Lint(fsys, []string{dir})
	if err != nil || n != 1 || len(findings) != 0 {
		t.Fatalf("Lint = %v, %d, %v", findings, n, err)
	}
}
