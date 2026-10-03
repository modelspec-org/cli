package modelspec

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paths(ss []Source) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Path
	}
	return strings.Join(out, " ")
}

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
		layout("m", "plain.hcl"):       okEntity,
		layout("m", "notes.txt"):       "",
	})
	got, warnings, err := Discover(fsys, []string{".", "other/f.modelspec.hcl", "a.modelspec.hcl"})
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	want := "a.modelspec.hcl other/f.modelspec.hcl " + layout("m", "plain.hcl") + " sub/b.modelspec.json sub/deeper/c.modelspec.hcl"
	if paths(got) != want {
		t.Fatalf("Discover = %v, want %s", paths(got), want)
	}
	// An explicitly named file is taken even in a hidden directory.
	got, _, err = Discover(fsys, []string{".hidden/d.modelspec.hcl"})
	if err != nil || len(got) != 1 || got[0].Canon != ".hidden/d.modelspec.hcl" {
		t.Fatalf("explicit hidden file = %v, %v", got, err)
	}
	// An .hcl file of any name is a model file in a layout directory, also when named.
	if got, _, err = Discover(fsys, []string{layout("m", "plain.hcl")}); err != nil || len(got) != 1 {
		t.Fatalf("explicit layout file = %v, %v", got, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"x.txt": "", "d/y.modelspec.hcl": okEntity, "z.hcl": ""})
	if _, _, err := Discover(fsys, []string{"missing"}); err == nil {
		t.Error("a missing path was accepted")
	}
	if _, _, err := Discover(fsys, []string{"x.txt"}); err == nil || !strings.Contains(err.Error(), "not a ModelSpec file") {
		t.Errorf("a non-model file: %v", err)
	}
	if _, _, err := Discover(fsys, []string{"z.hcl"}); err == nil || !strings.Contains(err.Error(), "not a ModelSpec file") {
		t.Errorf("a .hcl file outside a layout is not a model file by itself: %v", err)
	}
	boom := errors.New("boom")
	if _, _, err := Discover(&failingFS{memFS: fsys, readDirErr: boom}, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("ReadDir error: %v", err)
	}
	if _, _, err := Discover(&failingFS{memFS: fsys, readDirErr: boom, failAt: "d"}, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("nested ReadDir error: %v", err)
	}
	fsys.canonErr["d/y.modelspec.hcl"] = boom
	if _, _, err := Discover(fsys, []string{"d/y.modelspec.hcl"}); !errors.Is(err, boom) {
		t.Errorf("Canonical error for a named file: %v", err)
	}
	// A file a search finds but cannot canonicalise is a warning, not the end.
	got, warnings, err := Discover(fsys, []string{"d"})
	if err != nil || len(got) != 0 || len(warnings) != 1 || warnings[0].Rule != RuleIO || warnings[0].Severity != SeverityWarning || !strings.Contains(warnings[0].Message, "boom") {
		t.Errorf("unreadable found file: %v %v %v", got, warnings, err)
	}
	delete(fsys.canonErr, "d/y.modelspec.hcl")
	fsys.canonErr["."] = boom
	if _, _, err := Discover(fsys, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("Canonical error for a named directory: %v", err)
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

// The same file reached by a relative and an absolute name is read once.
func TestDiscoverDeduplicates(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"m/a.modelspec.hcl": okEntity, "m/b.modelspec.hcl": okEntity})
	fsys.alias["m/a.modelspec.hcl"] = "/abs/m/a.modelspec.hcl"
	fsys.alias["m/b.modelspec.hcl"] = "/abs/m/b.modelspec.hcl"
	got, _, err := Discover(fsys, []string{"m/a.modelspec.hcl", "./m/a.modelspec.hcl", "m", "m/b.modelspec.hcl"})
	if err != nil || paths(got) != "m/a.modelspec.hcl m/b.modelspec.hcl" {
		t.Fatalf("Discover = %v, %v", paths(got), err)
	}
	res, err := Lint(fsys, []string{"m/a.modelspec.hcl", "m", "./m/a.modelspec.hcl"}, LintOptions{})
	if err != nil || res.Files != 2 {
		t.Fatalf("Lint read %d files, %v", res.Files, err)
	}
}

func TestDiscoverSymlinks(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"real/a.modelspec.hcl":   okEntity,
		"real/other.txt":         "",
		"dir/keep.modelspec.hcl": okEntity,
	})
	fsys.links["link.modelspec.hcl"] = "real/a.modelspec.hcl" // valid link to a file
	fsys.links["dirlink"] = "real"                            // link to a directory: not followed
	fsys.links["dangling.modelspec.hcl"] = ""                 // dangling
	fsys.links["dangling.hcl"] = ""
	fsys.links["dangling.txt"] = "" // not model-like: ignored without a word
	fsys.MapFS["link.modelspec.hcl"] = fsys.MapFS["real/a.modelspec.hcl"]
	fsys.MapFS["dirlink"] = fsys.MapFS["real"]
	got, warnings, err := Discover(fsys, []string{".", "dir"})
	if err != nil {
		t.Fatal(err)
	}
	// The link's canonical path is its target, so the file is read once, under
	// the name the search met first.
	if paths(got) != "dir/keep.modelspec.hcl link.modelspec.hcl" {
		t.Fatalf("files = %v", paths(got))
	}
	if len(warnings) != 2 || warnings[0].File != "dangling.hcl" || warnings[1].File != "dangling.modelspec.hcl" || !strings.Contains(warnings[0].Message, "symbolic link cannot be followed") || warnings[0].Severity != SeverityWarning {
		t.Fatalf("warnings = %v", warnings)
	}
	// The rest of the run goes on, and the warning is in the findings.
	res, err := Lint(fsys, []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != 2 {
		t.Fatalf("Lint = %v, %v", res.Findings, err)
	}
	// A dangling link named on the command line is an error.
	if _, _, err := Discover(fsys, []string{"dangling.modelspec.hcl"}); err == nil {
		t.Error("a dangling link named explicitly was accepted")
	}
}

func TestLoadModuleRules(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		layout("sales", "a.hcl"):   okEntity,
		layout("sales", "b.hcl"):   "enum \"E\" {\n  values = [\"x\"]\n}\n",
		"std/core" + hclExt:        okEntity,
		"std/core.modelspec.json":  doc(jEntities),
		"std/other.modelspec.json": strings.Replace(doc(jEntities), `"name": "y"`, `"name": "named"`, 1),
		"std/odd.hcl":              okEntity,
		"std/odd2.hcl":             "enum \"F\" {\n  values = [\"x\"]\n}\n",
	})
	sources, _, err := Discover(fsys, []string{layout("sales", "a.hcl"), layout("sales", "b.hcl"), "std/core" + hclExt, "std/core.modelspec.json", "std/other.modelspec.json"})
	if err != nil {
		t.Fatal(err)
	}
	models, _, err := Load(fsys, sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]*Model{}
	for _, m := range models {
		byFile[m.File] = m
	}
	a, b := byFile[layout("sales", "a.hcl")], byFile[layout("sales", "b.hcl")]
	if a.Name != "sales" || b.Name != "sales" || a.Group != b.Group || a.Group != "spec/graph/modules/sales/models" {
		t.Errorf("layout: %q %q groups %q %q", a.Name, b.Name, a.Group, b.Group)
	}
	core, twin, other := byFile["std/core"+hclExt], byFile["std/core.modelspec.json"], byFile["std/other.modelspec.json"]
	if core.Name != "core" || core.Group == twin.Group && !twin.Twin || !twin.Twin || twin.Name != "core" || core.Twin {
		t.Errorf("twin: core %+v twin %+v", core, twin)
	}
	if other.Twin || other.Name != "named" {
		t.Errorf("a JSON file without an HCL twin: %+v", other)
	}

	// --module assignments win, accept any .hcl file name, and unite files.
	assign := []Assignment{{Module: "shop", Path: "std/odd.hcl"}, {Module: "shop", Path: "std/odd2.hcl"}, {Module: "renamed", Path: layout("sales", "a.hcl")}, {Module: "forced", Path: "std/other.modelspec.json"}}
	sources, _, err = Discover(fsys, []string{layout("sales", "a.hcl"), "std/other.modelspec.json"})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assign[:2] {
		more, _, err := discover(fsys, []string{a.Path}, true)
		if err != nil {
			t.Fatal(err)
		}
		sources = mergeSources(sources, more)
	}
	models, _, err = Load(fsys, sources, assign)
	if err != nil {
		t.Fatal(err)
	}
	byFile = map[string]*Model{}
	for _, m := range models {
		byFile[m.File] = m
	}
	odd, odd2 := byFile["std/odd.hcl"], byFile["std/odd2.hcl"]
	if odd.Name != "shop" || odd2.Name != "shop" || odd.Group != odd2.Group {
		t.Errorf("explicit: %q %q %q %q", odd.Name, odd2.Name, odd.Group, odd2.Group)
	}
	if got := byFile[layout("sales", "a.hcl")]; got.Name != "renamed" || got.Group != "--module renamed" {
		t.Errorf("explicit wins over layout: %+v", got)
	}
	if got := byFile["std/other.modelspec.json"]; got.Name != "forced" || got.Group == "--module forced" {
		t.Errorf("explicit name on JSON: %+v", got)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"a.modelspec.hcl": okEntity, "d/b.hcl": okEntity})
	sources, _, _ := Discover(fsys, []string{"a.modelspec.hcl"})
	if _, _, err := Load(&unreadableFS{fsys}, sources, nil); err == nil {
		t.Error("Load of an unreadable file succeeded")
	}
	if _, _, err := Load(fsys, sources, []Assignment{{Module: "x", Path: "missing"}}); err == nil {
		t.Error("an assignment of a missing path succeeded")
	}
	_, _, err := Load(fsys, sources, []Assignment{{Module: "x", Path: "a.modelspec.hcl"}, {Module: "y", Path: "d"}, {Module: "z", Path: "a.modelspec.hcl"}})
	if err == nil || !strings.Contains(err.Error(), `is assigned to module "x" and to module "z"`) {
		t.Errorf("conflicting assignments: %v", err)
	}
	// The same assignment twice is not a conflict.
	if _, _, err := Load(fsys, sources, []Assignment{{Module: "x", Path: "a.modelspec.hcl"}, {Module: "x", Path: "."}}); err != nil {
		t.Errorf("repeated assignment: %v", err)
	}
}

func TestLint(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"ok.modelspec.hcl":    okEntity,
		"bad.modelspec.hcl":   "entity \"A\" {\n  key = [\"id\"]\n  property \"id\" {\n    type = \"nope\"\n  }\n}\n",
		"data.modelspec.json": doc(jEntities),
	})
	res, err := Lint(fsys, []string{"."}, LintOptions{})
	if err != nil || res.Files != 3 || len(res.Models) != 3 {
		t.Fatalf("Lint = %d files, %v", res.Files, err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "bad.modelspec.hcl" || res.Findings[0].Rule != RuleType {
		t.Fatalf("findings = %v", res.Findings)
	}
	if _, err := Lint(fsys, []string{"missing"}, LintOptions{}); err == nil {
		t.Error("Lint of a missing path succeeded")
	}
	if _, err := Lint(newMemFS(map[string]string{"x.txt": ""}), []string{"."}, LintOptions{}); err == nil || !strings.Contains(err.Error(), "no ModelSpec files") {
		t.Errorf("empty directory: %v", err)
	}
	if _, err := Lint(&unreadableFS{fsys}, []string{"."}, LintOptions{}); err == nil {
		t.Error("an unreadable file was not an error")
	}
	if _, err := Lint(fsys, nil, LintOptions{}); err == nil {
		t.Error("no paths and no assignments was accepted")
	}
	// Assignments lint their own files even when no path names them, and add them to the paths'.
	fsys.MapFS["ctx/core.hcl"] = fsys.MapFS["ok.modelspec.hcl"]
	res, err = Lint(fsys, []string{"ok.modelspec.hcl"}, LintOptions{Modules: []Assignment{{Module: "core", Path: "ctx/core.hcl"}}})
	if err != nil || res.Files != 2 {
		t.Fatalf("Lint with an assignment = %d files, %v", res.Files, err)
	}
	res, err = Lint(fsys, nil, LintOptions{Modules: []Assignment{{Module: "core", Path: "ctx"}}})
	if err != nil || res.Files != 1 {
		t.Fatalf("Lint with only an assignment = %d files, %v", res.Files, err)
	}
	if _, err := Lint(fsys, nil, LintOptions{Modules: []Assignment{{Module: "core", Path: "nope"}}}); err == nil {
		t.Error("an assignment of a missing path was accepted")
	}
	// Warnings from an assigned directory are kept.
	fsys.links["ctx/dead.hcl"] = ""
	res, err = Lint(fsys, nil, LintOptions{Modules: []Assignment{{Module: "core", Path: "ctx"}}})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleIO {
		t.Fatalf("assigned directory with a dead link: %v %v", res.Findings, err)
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
	res, err := Lint(fsys, []string{dir}, LintOptions{})
	if err != nil || res.Files != 1 || len(res.Findings) != 0 {
		t.Fatalf("Lint = %v, %d, %v", res.Findings, res.Files, err)
	}
	// Two names for one file (a symlink, and a path with a detour) are one file.
	link := filepath.Join(dir, "alias.modelspec.hcl")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	detour := filepath.Join(dir, ".", "m.modelspec.hcl")
	canonA, errA := fsys.Canonical(link)
	canonB, errB := fsys.Canonical(detour)
	if errA != nil || errB != nil || canonA != canonB || !filepath.IsAbs(canonA) {
		t.Fatalf("Canonical = %q %v, %q %v", canonA, errA, canonB, errB)
	}
	res, err = Lint(fsys, []string{dir, link, detour}, LintOptions{})
	if err != nil || res.Files != 1 {
		t.Fatalf("Lint of three names read %d files, %v", res.Files, err)
	}
	// A relative name is made absolute from the working directory.
	boom := errors.New("no working directory")
	if _, err := (OSFS{getwd: func() (string, error) { return "", boom }}).Canonical("rel"); !errors.Is(err, boom) {
		t.Fatalf("Canonical without a working directory: %v", err)
	}
	if got, err := (OSFS{getwd: func() (string, error) { return dir, nil }}).Canonical("m.modelspec.hcl"); err != nil || got != canonA {
		t.Fatalf("Canonical of a relative name = %q, %v; want %q", got, err, canonA)
	}
	if got, err := (OSFS{}).Canonical("."); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("Canonical(.) = %q, %v", got, err)
	}
	// A dangling link found in a search is a warning; named, an error. A link to a directory is not followed.
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling.modelspec.hcl")); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "x.modelspec.hcl"), []byte(okEntity), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	res, err = Lint(fsys, []string{dir}, LintOptions{})
	if err != nil || res.Files != 1 || len(res.Findings) != 1 || res.Findings[0].Rule != RuleIO {
		t.Fatalf("Lint with a dangling link and a directory link = %d files, %v, %v", res.Files, res.Findings, err)
	}
	if _, err := fsys.Canonical(filepath.Join(dir, "dangling.modelspec.hcl")); err == nil {
		t.Fatal("Canonical of a dangling link succeeded")
	}
	var pathErr *fs.PathError
	if _, err := Lint(fsys, []string{filepath.Join(dir, "dangling.modelspec.hcl")}, LintOptions{}); !errors.As(err, &pathErr) {
		t.Fatalf("a dangling link named explicitly: %v", err)
	}
}

func TestLayoutModule(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		layout("sales", "a.modelspec.hcl"):   okEntity,
		layout("sales", "b.hcl"):             okEntity,
		layout("sales", "notes.md"):          "",
		layout("solo", "only.modelspec.hcl"): okEntity,
		"plain.modelspec.hcl":                okEntity,
	})
	id, files, err := LayoutModule(fsys, layout("sales", "a.modelspec.hcl"))
	if err != nil || id != "sales" || strings.Join(files, " ") != layout("sales", "a.modelspec.hcl")+" "+layout("sales", "b.hcl") {
		t.Fatalf("LayoutModule = %q %v %v", id, files, err)
	}
	if id, files, err = LayoutModule(fsys, layout("solo", "only.modelspec.hcl")); err != nil || id != "solo" || len(files) != 1 {
		t.Fatalf("a module of one file: %q %v %v", id, files, err)
	}
	if id, files, err = LayoutModule(fsys, "plain.modelspec.hcl"); err != nil || id != "" || files != nil {
		t.Fatalf("outside the layout: %q %v %v", id, files, err)
	}
	if _, _, err = LayoutModule(fsys, "missing.modelspec.hcl"); err == nil {
		t.Fatal("a missing file succeeded")
	}
	boom := errors.New("boom")
	if _, _, err = LayoutModule(&failingFS{memFS: fsys, readDirErr: boom}, layout("sales", "a.modelspec.hcl")); !errors.Is(err, boom) {
		t.Fatalf("ReadDir error: %v", err)
	}
}
