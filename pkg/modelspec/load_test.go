package modelspec

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
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
		"a.modelspec.hcl":              okRecord,
		"sub/b.modelspec.json":         "{}",
		"sub/deeper/c.modelspec.hcl":   okRecord,
		"sub/readme.md":                "",
		"sub/model.hcl":                "",
		".hidden/d.modelspec.hcl":      okRecord,
		"node_modules/e.modelspec.hcl": okRecord,
		"other/f.modelspec.hcl":        okRecord,
		layout("m", "plain.hcl"):       okRecord,
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
	if err != nil || len(got) != 1 || got[0].Abs != ".hidden/d.modelspec.hcl" {
		t.Fatalf("explicit hidden file = %v, %v", got, err)
	}
	// An .hcl file of any name is a model file in a layout directory, also when named.
	if got, _, err = Discover(fsys, []string{layout("m", "plain.hcl")}); err != nil || len(got) != 1 {
		t.Fatalf("explicit layout file = %v, %v", got, err)
	}
}

func TestDiscoverErrors(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"x.txt": "", "d/y.modelspec.hcl": okRecord, "z.hcl": ""})
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
	fsys.absErr["d/y.modelspec.hcl"] = boom
	if _, _, err := Discover(fsys, []string{"d/y.modelspec.hcl"}); !errors.Is(err, boom) {
		t.Errorf("Abs error for a named file: %v", err)
	}
	delete(fsys.absErr, "d/y.modelspec.hcl")
	// A file a search finds but cannot read is a warning, not the end of the run.
	// Each case has its own file system and its own error, and asserts that its
	// error is the one in the warning: a case whose injected call is not reached
	// fails instead of passing on another path's warning.
	for _, tc := range []struct {
		name   string
		inject func(*memFS, error)
	}{
		{"abs", func(m *memFS, err error) { m.absErr["d/y.modelspec.hcl"] = err }},
		{"stat", func(m *memFS, err error) { m.statErr["d/y.modelspec.hcl"] = err }},
	} {
		cause := errors.New(tc.name + "-failure")
		each := newMemFS(map[string]string{"d/y.modelspec.hcl": okRecord})
		tc.inject(each, cause)
		got, warnings, err := Discover(each, []string{"d"})
		if err != nil || len(got) != 0 || len(warnings) != 1 || warnings[0].Rule != RuleIO || warnings[0].Severity != SeverityWarning || !strings.Contains(warnings[0].Message, cause.Error()) {
			t.Errorf("%s: unreadable found file: %v %v %v", tc.name, got, warnings, err)
		}
	}
	fsys.absErr["."] = boom
	if _, _, err := Discover(fsys, []string{"."}); !errors.Is(err, boom) {
		t.Errorf("Abs error for a named directory: %v", err)
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

// A module directory that cannot be listed is an error, not a quiet partial check.
func TestLintRefusesAModuleItCannotListWhole(t *testing.T) {
	t.Parallel()
	boom := errors.New("cannot list")
	dir := "spec/modules/sales/models"
	fsys := &failingFS{memFS: newMemFS(map[string]string{dir + "/a.hcl": okRecord}), readDirErr: boom, failAt: dir}
	if _, err := Lint(fsys, []string{dir + "/a.hcl"}, LintOptions{}); !errors.Is(err, boom) {
		t.Errorf("Lint = %v, want the listing error", err)
	}
}

// The same file reached by a relative and an absolute name, or by a name that
// differs by case on a case-insensitive filesystem, is read once.
func TestDiscoverDeduplicates(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"work/m/a.modelspec.hcl": okRecord, "work/m/b.modelspec.hcl": okRecord})
	fsys.cwd = "/work"
	got, _, err := Discover(fsys, []string{"m/a.modelspec.hcl", "./m/a.modelspec.hcl", "/work/m/a.modelspec.hcl", "m", "m/b.modelspec.hcl"})
	if err != nil || paths(got) != "m/a.modelspec.hcl m/b.modelspec.hcl" {
		t.Fatalf("Discover = %v, %v", paths(got), err)
	}
	res, err := Lint(fsys, []string{"m/a.modelspec.hcl", "m", "./m/a.modelspec.hcl"}, LintOptions{})
	if err != nil || res.Files != 2 {
		t.Fatalf("Lint read %d files, %v", res.Files, err)
	}

	// Two spellings that differ by case: one file on a case-insensitive filesystem, two on another.
	for _, fold := range []bool{true, false} {
		ci := newMemFS(map[string]string{"a.modelspec.hcl": okRecord, "A.modelspec.hcl": okRecord})
		ci.caseFold = fold
		got, _, err := Discover(ci, []string{"a.modelspec.hcl", "A.modelspec.hcl"})
		want := 2
		if fold {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Errorf("case-insensitive %v: %v, %v", fold, paths(got), err)
		}
	}
	// Different files of one size are two files.
	same := newMemFS(map[string]string{"a.modelspec.hcl": okRecord, "b.modelspec.hcl": okRecord})
	if got, _, err := Discover(same, []string{"a.modelspec.hcl", "b.modelspec.hcl"}); err != nil || len(got) != 2 {
		t.Errorf("two files of equal size: %v, %v", paths(got), err)
	}
}

func TestDiscoverSymlinks(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"real/a.modelspec.hcl":   okRecord,
		"real/other.txt":         "",
		"dir/keep.modelspec.hcl": okRecord,
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
	// A search follows no link: the model-named ones (the link to a file, the dangling one)
	// are warnings that name them, the link to a directory and the ones that are not model-named
	// are not, and the files are the two real ones.
	if paths(got) != "dir/keep.modelspec.hcl real/a.modelspec.hcl" {
		t.Fatalf("files = %v", paths(got))
	}
	if len(warnings) != 2 || warnings[0].File != "dangling.modelspec.hcl" || warnings[1].File != "link.modelspec.hcl" || warnings[0].Severity != SeverityError {
		t.Fatalf("warnings = %v", warnings)
	}
	for _, w := range warnings {
		if !strings.Contains(w.Message, "is a symbolic link, which a search does not read") || w.Rule != RuleSkipped {
			t.Errorf("warning = %v", w)
		}
	}
	// The rest of the run goes on, and the warnings are in the findings.
	res, err := Lint(fsys, []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != 2 {
		t.Fatalf("Lint = %v, %v", res.Findings, err)
	}
	// A link named on the command line is read through to its regular file.
	if got, _, err := Discover(fsys, []string{"link.modelspec.hcl"}); err != nil || paths(got) != "link.modelspec.hcl" {
		t.Errorf("a link named explicitly: %v, %v", paths(got), err)
	}
	// A dangling link named on the command line is an error.
	if _, _, err := Discover(fsys, []string{"dangling.modelspec.hcl"}); err == nil {
		t.Error("a dangling link named explicitly was accepted")
	}
}

// The SpecScore layout is read from the path as given or found, not from the
// path a symbolic link resolves to.
func TestLayoutIsReadFromTheGivenPath(t *testing.T) {
	t.Parallel()
	enums := "enum \"Status\" {\n  values = [\"open\"]\n}\n"
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"s\" {\n    type = \"string\"\n    enum = \"Status\"\n  }\n}\n"
	fsys := newMemFS(map[string]string{
		layout("sales", "records.hcl"): order,
		"shared/enums.hcl":             enums,
	})
	// A part of the module that is a symbolic link to a file elsewhere is not followed: a
	// warning names it, and the module is checked without it.
	fsys.links[layout("sales", "enums.hcl")] = "shared/enums.hcl"
	fsys.MapFS[layout("sales", "enums.hcl")] = fsys.MapFS["shared/enums.hcl"]
	expect(t, lintTree(fsys, "spec"), "enums.hcl: error: is a symbolic link") // and no reference error from a module read in part
	// Named, the link has a model name and the same module as its siblings.
	linked := newMemFS(map[string]string{
		layout("sales", "records.hcl"): order,
		"shared/enums.modelspec.hcl":   enums,
	})
	linked.links[layout("sales", "enums.modelspec.hcl")] = "shared/enums.modelspec.hcl"
	linked.MapFS[layout("sales", "enums.modelspec.hcl")] = linked.MapFS["shared/enums.modelspec.hcl"]
	expect(t, lintTree(linked, "spec"), "enums.modelspec.hcl: error: is a symbolic link")
	// Given by name, a link is read through, and is part of its module.
	res, err := Lint(linked, []string{layout("sales", "records.hcl"), layout("sales", "enums.modelspec.hcl")}, LintOptions{})
	if err != nil || len(res.Findings) != 0 {
		t.Errorf("links named explicitly: %v, %v", res.Findings, err)
	}

	// A tree reached through a symbolic link to its modules directory has the layout of the path used.
	via := newMemFS(map[string]string{
		"elsewhere/sales/models/records.hcl": order,
		"elsewhere/sales/models/enums.hcl":   enums,
	})
	via.links["project/modules"] = "elsewhere"
	via.MapFS["project/modules"] = via.MapFS["elsewhere"]
	// project/modules/sales/models is the lexical path; it is reached through the link by name.
	via.MapFS["project/modules/sales/models/records.hcl"] = via.MapFS["elsewhere/sales/models/records.hcl"]
	via.MapFS["project/modules/sales/models/enums.hcl"] = via.MapFS["elsewhere/sales/models/enums.hcl"]
	expect(t, lintTree(via, "project/modules/sales/models"))
}

func lintTree(fsys FS, path string) []string {
	res, err := Lint(fsys, []string{path}, LintOptions{})
	if err != nil {
		return []string{"error: " + err.Error()}
	}
	out := make([]string, len(res.Findings))
	for i, f := range res.Findings {
		out[i] = f.String()
	}
	return out
}

func TestLayoutDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		path string
		id   string
		ok   bool
	}{
		{"spec/graph/modules/sales/models/a.hcl", "sales", true},
		{"/abs/modules/sales/models/a.modelspec.json", "sales", true},
		{"modules/sales/models/a.hcl", "sales", true},
		{"spec/modules/sales/other/a.hcl", "", false},         // not models
		{"spec/mods/sales/models/a.hcl", "", false},           // not modules
		{"spec/modules/sales/models/deeper/a.hcl", "", false}, // not directly inside
		{"sales/models/a.hcl", "", false},                     // too short
		{"models/a.hcl", "", false},
	} {
		id, dir, ok := layoutDir(tc.path)
		if ok != tc.ok || id != tc.id || (ok && !strings.HasSuffix(dir, "/models")) {
			t.Errorf("layoutDir(%q) = %q %q %v", tc.path, id, dir, ok)
		}
	}
	if _, _, ok := layoutModule("modules/sales/models/a.json"); ok {
		t.Error("layoutModule accepted a non-.hcl file")
	}
	if id, _, ok := layoutModule("modules/sales/models/a.hcl"); !ok || id != "sales" {
		t.Error("layoutModule refused an .hcl file")
	}
	// A path given from inside the models directory is made absolute first.
	fsys := newMemFS(map[string]string{layout("sales", "a.hcl"): okRecord})
	fsys.cwd = "/" + layout("sales", "")
	got, _, err := Discover(fsys, []string{"a.hcl"})
	if err != nil || len(got) != 1 || got[0].Abs != layout("sales", "a.hcl") {
		t.Fatalf("from inside the models directory: %v, %v", got, err)
	}
}

// A module is the unit of checking: given one file of it, the whole module is
// loaded and checked.
func TestLintLoadsTheWholeModule(t *testing.T) {
	t.Parallel()
	records := layout("sales", "records.modelspec.hcl")
	enums := layout("sales", "enums.modelspec.hcl")
	extra := layout("sales", "extra.modelspec.hcl")
	tree := func() *memFS {
		return newMemFS(map[string]string{
			records:                     "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"s\" {\n    type = \"string\"\n    enum = \"OrderStatus\"\n  }\n}\n",
			enums:                       "enum \"OrderStatus\" {\n  values = [\"open\"]\n}\n",
			extra:                       "enum \"OrderStatus\" {\n  values = [\"x\"]\n}\n",
			layout("core", "model.hcl"): okRecord,
		})
	}
	wantNote := `a module is the unit of checking: module "sales" has more files in ` + filepath.ToSlash(filepath.Dir(records)) + ` than were given, so the whole module was checked`

	// The reviewer's first case: an enum in a sibling file resolves.
	res, err := Lint(tree(), []string{records}, LintOptions{})
	if err != nil || res.Files != 3 || len(res.Notes) != 1 || res.Notes[0] != wantNote {
		t.Fatalf("records alone: %d files, notes %q, %v", res.Files, res.Notes, err)
	}
	// ... and the module holds the enum twice, which is reported, with the sibling's path.
	got := []string{}
	for _, f := range res.Findings {
		got = append(got, f.String())
	}
	expect(t, got, extra+":1: error: duplicate concept name \"OrderStatus\" in the record/component/enum scope (also declared at "+enums+":1)")

	// The reviewer's second case: a file that repeats a sibling's concept is not clean.
	res, err = Lint(tree(), []string{extra}, LintOptions{})
	if err != nil || res.Files != 3 || len(res.Findings) != 1 || res.Findings[0].File != extra || res.Findings[0].Rule != RuleDuplicate {
		t.Fatalf("extra alone: %d files, %v, %v", res.Files, res.Findings, err)
	}

	// Without the duplicate, one file is clean and the sibling is found.
	clean := tree()
	delete(clean.MapFS, extra)
	res, err = Lint(clean, []string{records}, LintOptions{})
	if err != nil || res.Files != 2 || len(res.Findings) != 0 || len(res.Notes) != 1 {
		t.Fatalf("clean module, one file: %d files, %v, notes %q, %v", res.Files, res.Findings, res.Notes, err)
	}

	// The same module reached through two files of one invocation is checked once, and said once.
	res, err = Lint(clean, []string{records, enums}, LintOptions{})
	if err != nil || res.Files != 2 || len(res.Notes) != 0 {
		t.Fatalf("both files given: %d files, notes %q, %v", res.Files, res.Notes, err)
	}
	// Three files of a module, two of them given: the module is checked once and said once.
	three := tree()
	res, err = Lint(three, []string{records, extra}, LintOptions{})
	if err != nil || res.Files != 3 || len(res.Notes) != 1 {
		t.Fatalf("two of three given: %d files, notes %q, %v", res.Files, res.Notes, err)
	}
	// Two different modules, one file of each: one note each.
	two := tree()
	two.MapFS[layout("core", "second.hcl")] = &fstest.MapFile{Data: []byte("enum \"E\" {\n  values = [\"x\"]\n}\n")}
	res, err = Lint(two, []string{records, layout("core", "model.hcl")}, LintOptions{})
	if err != nil || len(res.Notes) != 2 {
		t.Fatalf("two modules: notes %q, %v", res.Notes, err)
	}
	// A directory given is complete already: no note.
	res, err = Lint(tree(), []string{layout("sales", "")}, LintOptions{})
	if err != nil || res.Files != 3 || len(res.Notes) != 0 {
		t.Fatalf("a directory: %d files, notes %q, %v", res.Files, res.Notes, err)
	}
	// A file outside the layout is a module of its own: nothing to add.
	alone := newMemFS(map[string]string{"x.modelspec.hcl": okRecord, "y.modelspec.hcl": okRecord})
	res, err = Lint(alone, []string{"x.modelspec.hcl"}, LintOptions{})
	if err != nil || res.Files != 1 || len(res.Notes) != 0 {
		t.Fatalf("a standalone file: %d files, notes %q, %v", res.Files, res.Notes, err)
	}
	// A models directory that cannot be listed is an error.
	broken := tree()
	if _, err = Lint(&failingFS{memFS: broken, readDirErr: errors.New("no listing"), failAt: filepath.Dir(records)}, []string{records}, LintOptions{}); err == nil {
		t.Fatal("an unlistable directory was linted")
	}
}

// X.modelspec.hcl and X.modelspec.json are one module: given one, both are checked.
func TestLintLoadsTheTwin(t *testing.T) {
	t.Parallel()
	hclSrc := recordWith("Space")
	jsonSrc := `{"modelspec": "1.0-draft-2", "module": {"id": "x/core", "name": "core", "version": "1"}, "records": {"Space": {"key": ["id"], "fields": {"id": {"type": "int"}}}}}`
	for _, given := range []string{"d/core.modelspec.hcl", "d/core.modelspec.json"} {
		fsys := newMemFS(map[string]string{"d/core.modelspec.hcl": hclSrc, "d/core.modelspec.json": jsonSrc})
		res, err := Lint(fsys, []string{given}, LintOptions{})
		if err != nil || res.Files != 2 || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "are one module (the JSON is the interchange copy of the HCL), so both were checked") || len(res.Findings) != 0 {
			t.Errorf("%s: %d files, notes %q, findings %v, %v", given, res.Files, res.Notes, res.Findings, err)
		}
	}
	// With the JSON stale, linting only the HCL (the file a hook passes) says so.
	stale := newMemFS(map[string]string{"d/core.modelspec.hcl": hclSrc, "d/core.modelspec.json": strings.Replace(jsonSrc, "Space", "Room", 2)})
	res, err := Lint(stale, []string{"d/core.modelspec.hcl"}, LintOptions{})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].File != "d/core.modelspec.json" || res.Findings[0].Rule != RuleStaleTwin || res.Findings[0].Severity != SeverityWarning {
		t.Fatalf("stale twin: %v, %v", res.Findings, err)
	}
	// Only one of the pair exists: nothing to add, and a lone JSON is a module of its own.
	lone := newMemFS(map[string]string{"d/core.modelspec.json": jsonSrc})
	if res, err := Lint(lone, []string{"d/core.modelspec.json"}, LintOptions{}); err != nil || res.Files != 1 || len(res.Notes) != 0 {
		t.Fatalf("a lone JSON file: %d files, %v, %v", res.Files, res.Notes, err)
	}
	// A directory with the same name as the partner is not a partner.
	dir := newMemFS(map[string]string{"d/core.modelspec.hcl": hclSrc, "d/core.modelspec.json/x": "", "d/other.txt": ""})
	if res, err := Lint(dir, []string{"d/core.modelspec.hcl"}, LintOptions{}); err != nil || res.Files != 1 {
		t.Fatalf("a directory named like the partner: %d files, %v", res.Files, err)
	}
}

func TestLoadModuleRules(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		layout("sales", "a.hcl"):                okRecord,
		layout("sales", "b.hcl"):                "enum \"E\" {\n  values = [\"x\"]\n}\n",
		layout("sales", "sales.modelspec.json"): strings.Replace(doc(jRecords), `"name": "y"`, `"name": "sales"`, 1),
		"std/core" + hclExt:                     okRecord,
		"std/core.modelspec.json":               doc(jRecords),
		"std/other.modelspec.json":              strings.Replace(doc(jRecords), `"name": "y"`, `"name": "named"`, 1),
		"std/odd.hcl":                           okRecord,
		"std/odd2.hcl":                          "enum \"F\" {\n  values = [\"x\"]\n}\n",
		"std/shop.modelspec.json":               strings.Replace(doc(jRecords), `"name": "y"`, `"name": "shop"`, 1),
	})
	sources, _, err := Discover(fsys, []string{layout("sales", ""), "std/core" + hclExt, "std/core.modelspec.json", "std/other.modelspec.json"})
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
	a, b, sj := byFile[layout("sales", "a.hcl")], byFile[layout("sales", "b.hcl")], byFile[layout("sales", "sales.modelspec.json")]
	if a.Name != "sales" || b.Name != "sales" || a.Group != b.Group || a.Group != "spec/graph/modules/sales/models" {
		t.Errorf("layout: %q %q groups %q %q", a.Name, b.Name, a.Group, b.Group)
	}
	if !sj.Twin || sj.Name != "sales" || sj.TwinOf != nil {
		t.Errorf("a JSON file in a layout module's directory is its interchange copy: %+v", sj)
	}
	core, twin, other := byFile["std/core"+hclExt], byFile["std/core.modelspec.json"], byFile["std/other.modelspec.json"]
	if core.Name != "core" || !twin.Twin || twin.Name != "core" || twin.TwinOf != core || core.Twin {
		t.Errorf("twin: core %+v twin %+v", core, twin)
	}
	if other.Twin || other.Name != "named" {
		t.Errorf("a JSON file without an HCL twin: %+v", other)
	}

	// A single-file layout module's JSON also has the HCL of the same stem as its source.
	solo := newMemFS(map[string]string{layout("solo", "solo.modelspec.hcl"): okRecord, layout("solo", "solo.modelspec.json"): doc(jRecords)})
	sources, _, _ = Discover(solo, []string{layout("solo", "")})
	models, _, _ = Load(solo, sources, nil)
	if j := models[1]; !j.Twin || j.TwinOf != models[0] || j.Name != "solo" {
		t.Errorf("solo layout twin: %+v", j)
	}

	// --module assignments win, accept any .hcl file name, and unite files.
	assign := []Assignment{{Module: "shop", Path: "std/odd.hcl"}, {Module: "shop", Path: "std/odd2.hcl"}, {Module: "renamed", Path: layout("sales", "")}, {Module: "forced", Path: "std/other.modelspec.json"}, {Module: "other", Path: "std/core.modelspec.json"}, {Module: "shop", Path: "std/shop.modelspec.json"}}
	d := newDiscovery(fsys)
	for _, p := range []string{layout("sales", ""), "std/other.modelspec.json", "std/core.modelspec.json", "std/core" + hclExt} {
		if err := d.addPath(p, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range assign {
		if err := d.addPath(a.Path, true); err != nil {
			t.Fatal(err)
		}
	}
	models, _, err = Load(fsys, d.sorted(), assign)
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
	if got := byFile["std/other.modelspec.json"]; got.Name != "forced" || got.Twin {
		t.Errorf("explicit name on JSON: %+v", got)
	}
	if got := byFile["std/core.modelspec.json"]; got.Name != "other" || got.Twin || got.TwinOf != nil {
		t.Errorf("an explicit assignment of a JSON file wins over the twin rule: %+v", got)
	}
	if got := byFile["std/shop.modelspec.json"]; got.Name != "shop" || !got.Twin || got.TwinOf != nil {
		t.Errorf("a JSON file assigned to a module that has HCL files is its interchange copy: %+v", got)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"a.modelspec.hcl": okRecord, "d/b.hcl": okRecord, layout("sales", "p.hcl"): okRecord, layout("sales", "q.hcl"): okRecord})
	sources, _, _ := Discover(fsys, []string{"a.modelspec.hcl"})
	if _, _, err := Load(&unreadableFS{fsys}, sources, nil); err == nil {
		t.Error("Load of an unreadable file succeeded")
	}
	if _, _, err := Load(fsys, []Source{{Path: "missing.modelspec.hcl", Abs: "missing.modelspec.hcl"}}, nil); err == nil {
		t.Error("Load of a missing file succeeded")
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
	// Assigning part of a layout module's directory would split the module.
	_, _, err = Load(fsys, sources, []Assignment{{Module: "sales2", Path: layout("sales", "p.hcl")}})
	if err == nil || !strings.Contains(err.Error(), `would split it, so assign the whole directory: --module sales2=spec/graph/modules/sales/models`) {
		t.Errorf("a part of a layout module: %v", err)
	}
	// Assigning the whole directory, under any name, or the files with the module's own name, is fine.
	for _, a := range [][]Assignment{
		{{Module: "renamed", Path: layout("sales", "")}},
		{{Module: "sales", Path: layout("sales", "p.hcl")}, {Module: "sales", Path: layout("sales", "q.hcl")}},
	} {
		if _, _, err := Load(fsys, sources, a); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	boom := errors.New("no listing")
	if _, _, err := Load(&failingFS{memFS: fsys, readDirErr: boom, failAt: filepath.Dir(layout("sales", "p.hcl"))}, sources, []Assignment{{Module: "x", Path: layout("sales", "p.hcl")}}); !errors.Is(err, boom) {
		t.Errorf("a models directory that cannot be listed: %v", err)
	}
}

// A file larger than the limit is refused from its size, before it is read.
func TestOversizeFileIsNotRead(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"big.modelspec.hcl": okRecord, "bigj.modelspec.json": "{}", "ok.modelspec.hcl": okRecord})
	fsys.sizes["big.modelspec.hcl"] = MaxInputBytes + 1
	fsys.sizes["bigj.modelspec.json"] = MaxInputBytes + 1
	read := map[string]bool{}
	spy := &readSpy{memFS: fsys, read: read}
	res, err := Lint(spy, []string{"."}, LintOptions{})
	if err != nil || read["big.modelspec.hcl"] || read["bigj.modelspec.json"] || !read["ok.modelspec.hcl"] {
		t.Fatalf("read %v, %v", read, err)
	}
	if len(res.Findings) != 2 || res.Findings[0].Rule != RuleLimit || !strings.Contains(res.Findings[0].Message, "the limit is 1048576 bytes") || res.Findings[0].File != "big.modelspec.hcl" || res.Findings[1].File != "bigj.modelspec.json" {
		t.Fatalf("findings = %v", res.Findings)
	}
	for _, m := range res.Models {
		if m.File != "ok.modelspec.hcl" && !m.Broken {
			t.Errorf("%s is not broken", m.File)
		}
	}
	if res.Models[1].Form != FormJSON || res.Models[0].Form != FormHCL {
		t.Errorf("forms: %v %v", res.Models[0].Form, res.Models[1].Form)
	}
}

type readSpy struct {
	*memFS
	read map[string]bool
}

func (r *readSpy) Open(name string) (fs.File, error) {
	r.read[name] = true
	return r.memFS.Open(name)
}

func TestLint(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"ok.modelspec.hcl":    okRecord,
		"bad.modelspec.hcl":   "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"nope\"\n  }\n}\n",
		"data.modelspec.json": doc(jRecords),
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
	if _, err := Lint(fsys, nil, LintOptions{Modules: []Assignment{{Module: "x", Path: "nope"}}}); err == nil {
		t.Error("an assignment of a missing path was accepted")
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
	// Warnings from an assigned directory are kept.
	fsys.links["ctx/dead.hcl"] = ""
	res, err = Lint(fsys, nil, LintOptions{Modules: []Assignment{{Module: "core", Path: "ctx"}}})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleSkipped || res.Findings[0].Severity != SeverityError {
		t.Fatalf("assigned directory with a dead link: %v %v", res.Findings, err)
	}
}

type unreadableFS struct{ *memFS }

func (unreadableFS) Open(string) (fs.File, error) { return nil, errors.New("unreadable") }

func TestOSFS(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var fsys FS = OSFS{}
	file := filepath.Join(dir, "m.modelspec.hcl")
	if err := fsys.WriteFile(file, []byte(okRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadSource(fsys, file); err != nil || string(b) != okRecord {
		t.Fatalf("ReadSource = %q, %v", b, err)
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
	// Abs is the lexical absolute path, symbolic links left as they are.
	link := filepath.Join(dir, "alias.modelspec.hcl")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if abs, err := fsys.Abs(filepath.Join(dir, ".", "alias.modelspec.hcl")); err != nil || abs != link {
		t.Fatalf("Abs = %q, %v; want %q", abs, err, link)
	}
	// Two names for one file (a symbolic link, and a path with a detour) are one file.
	detour := filepath.Join(dir, ".", "m.modelspec.hcl")
	res, err = Lint(fsys, []string{dir, link, detour}, LintOptions{})
	if err != nil || res.Files != 1 {
		t.Fatalf("Lint of three names read %d files, %v", res.Files, err)
	}
	// A symbolic link is followed to the file, every link on the way resolved.
	wantTarget, _ := filepath.EvalSymlinks(file)
	if got, err := fsys.EvalSymlinks(link); err != nil || got != wantTarget {
		t.Fatalf("EvalSymlinks = %q, %v; want %q", got, err, wantTarget)
	}
	if _, err := fsys.EvalSymlinks(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("EvalSymlinks of a missing file succeeded")
	}
	a, _ := fsys.Stat(link)
	b, _ := fsys.Stat(file)
	other := filepath.Join(dir, "other.modelspec.hcl")
	if err := os.WriteFile(other, []byte(okRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := fsys.Stat(other)
	if !fsys.SameFile(a, b) || fsys.SameFile(a, c) {
		t.Fatal("SameFile wrong")
	}
	// A relative name is made absolute from the working directory.
	boom := errors.New("no working directory")
	if _, err := (OSFS{getwd: func() (string, error) { return "", boom }}).Abs("rel"); !errors.Is(err, boom) {
		t.Fatalf("Abs without a working directory: %v", err)
	}
	if got, err := (OSFS{getwd: func() (string, error) { return dir, nil }}).Abs("m.modelspec.hcl"); err != nil || got != file {
		t.Fatalf("Abs of a relative name = %q, %v; want %q", got, err, file)
	}
	if got, err := (OSFS{}).Abs("."); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("Abs(.) = %q, %v", got, err)
	}
	// A dangling link found in a search is a warning; named, an error. A link to a directory is not followed.
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(dir, "dangling.modelspec.hcl")); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "x.modelspec.hcl"), []byte(okRecord), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	res, err = Lint(fsys, []string{dir}, LintOptions{})
	if err != nil || res.Files != 2 || len(res.Findings) != 2 || res.Findings[0].Rule != RuleSkipped || res.Findings[1].Rule != RuleSkipped {
		t.Fatalf("Lint with a dangling link, the alias and a directory link = %d files, %v, %v", res.Files, res.Findings, err)
	}
	var pathErr *fs.PathError
	if _, err := Lint(fsys, []string{filepath.Join(dir, "dangling.modelspec.hcl")}, LintOptions{}); !errors.As(err, &pathErr) {
		t.Fatalf("a dangling link named explicitly: %v", err)
	}
}

// ReadSource is the one way a file is read: refused by its size before the read,
// and by its length after it when the file grew in between; errors pass through.
func TestReadSource(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"edge": strings.Repeat("x", MaxInputBytes), "grew": strings.Repeat("x", MaxInputBytes+1), "small": "ok"})
	fsys.sizes["grew"] = 10 // Stat said 10 bytes; the file is larger when read
	if src, err := ReadSource(fsys, "edge"); err != nil || len(src) != MaxInputBytes {
		t.Errorf("a file of the limit: %d bytes, %v", len(src), err)
	}
	var tooLarge *TooLargeError
	if _, err := ReadSource(fsys, "grew"); !errors.As(err, &tooLarge) || tooLarge.Size != MaxInputBytes+1 || !tooLarge.Partial || tooLarge.File != "grew" || !strings.Contains(err.Error(), "grew: file is more than 1048576 bytes; the limit is 1048576 bytes") {
		t.Errorf("a file that grew: %v", err)
	}
	if _, err := ReadSource(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	spy := &failRead{memFS: fsys}
	if _, err := ReadSource(spy, "small"); err == nil || err.Error() != "read failed" {
		t.Errorf("a read error: %v", err)
	}
}

// failRead is an FS whose reads fail.
type failRead struct{ *memFS }

func (f failRead) Open(name string) (fs.File, error) {
	info, err := f.memFS.Stat(name)
	return specialFile{Reader: errReader{}, info: info}, err
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// countingZeros is a reader that never ends and counts what it gave.
type countingZeros struct{ n *int64 }

func (c countingZeros) Read(p []byte) (int, error) {
	*c.n += int64(len(p))
	return len(p), nil
}

// What is not a regular file is refused before it is opened and without being read,
// whatever it is and however it is reached: a device that reports a size of zero and
// never ends, a named pipe, a directory, a symbolic link to each; a regular file that
// reports a small size and grows is read only up to one byte over the limit.
func TestReadSourceRefusesWhatIsNotRegular(t *testing.T) {
	t.Parallel()
	var given int64
	zeros := func() io.Reader { return countingZeros{&given} }
	fsys := newMemFS(map[string]string{"dev": "", "pipe": "", "dir/x": "", "plain": "ok", "grows": "", "swapped": "", "loop": ""})
	fsys.modes["dev"] = fs.ModeDevice | fs.ModeCharDevice
	fsys.modes["pipe"] = fs.ModeNamedPipe
	fsys.streams["dev"], fsys.streams["pipe"] = zeros, zeros
	fsys.links["link-dev"], fsys.links["link-pipe"], fsys.links["link-dir"] = "dev", "pipe", "dir"
	fsys.MapFS["link-dev"], fsys.MapFS["link-pipe"], fsys.MapFS["link-dir"] = fsys.MapFS["dev"], fsys.MapFS["pipe"], fsys.MapFS["dir"]
	spy := &readSpy{memFS: fsys, read: map[string]bool{}}
	for name, want := range map[string]string{
		"dev":       "dev is a character device, not a regular file",
		"pipe":      "pipe is a named pipe, not a regular file",
		"dir":       "dir is a directory, not a regular file",
		"link-dev":  "link-dev is a symbolic link to a character device, not a regular file",
		"link-pipe": "link-pipe is a symbolic link to a named pipe, not a regular file",
		"link-dir":  "link-dir is a symbolic link to a directory, not a regular file",
	} {
		_, err := ReadSource(spy, name)
		var notRegular *NotRegularError
		if !errors.As(err, &notRegular) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	if len(spy.read) != 0 || given != 0 {
		t.Errorf("opened %v and read %d bytes of what is not regular", spy.read, given)
	}
	// A device is a device whatever mode bit it has.
	for mode, want := range map[fs.FileMode]string{fs.ModeDevice: "a device", fs.ModeSocket: "a socket", fs.ModeIrregular: "not an ordinary file"} {
		if got := fileKind(mode); got != want {
			t.Errorf("fileKind(%v) = %q, want %q", mode, got, want)
		}
	}
	// A loop of links is an error from Stat, and is not read.
	fsys.statErr["loop"] = errors.New("too many levels of symbolic links")
	if _, err := ReadSource(spy, "loop"); err == nil || !strings.Contains(err.Error(), "too many levels") || len(spy.read) != 0 {
		t.Errorf("a loop of links: %v", err)
	}
	// A regular file that reports no size and never ends is read up to one byte over the limit.
	given = 0
	fsys.streams["grows"] = zeros
	var tooLarge *TooLargeError
	if _, err := ReadSource(fsys, "grows"); !errors.As(err, &tooLarge) || !tooLarge.Partial || given != MaxInputBytes+1 {
		t.Errorf("an endless regular file: %v after %d bytes, want %d", err, given, MaxInputBytes+1)
	}
	// A file that was regular when checked and is a pipe when opened is refused on the opened file.
	given = 0
	fsys.streams["swapped"] = zeros
	fsys.openModes["swapped"] = fs.ModeNamedPipe
	var notRegular *NotRegularError
	if _, err := ReadSource(fsys, "swapped"); !errors.As(err, &notRegular) || given != 0 {
		t.Errorf("a file swapped for a pipe: %v after %d bytes", err, given)
	}
	// Exactly the limit is read, and one byte over is refused with the size.
	for size, wantErr := range map[int]bool{MaxInputBytes: false, MaxInputBytes + 1: true} {
		edge := newMemFS(map[string]string{"f": strings.Repeat("x", size)})
		src, err := ReadSource(edge, "f")
		if wantErr != errors.As(err, &tooLarge) || (!wantErr && len(src) != size) || (wantErr && (tooLarge.Partial || tooLarge.Size != int64(size))) {
			t.Errorf("%d bytes: read %d, %v", size, len(src), err)
		}
	}
	// A read error of an open file is the error.
	if _, err := ReadSource(&failRead{memFS: fsys}, "plain"); err == nil || err.Error() != "read failed" {
		t.Errorf("read error: %v", err)
	}
	// A file that cannot be asked about once open.
	if _, err := ReadSource(statFails{fsys}, "plain"); err == nil || err.Error() != "stat failed" {
		t.Errorf("stat of an open file: %v", err)
	}
}

// statFails is an FS whose opened files cannot be asked about.
type statFails struct{ *memFS }

func (s statFails) Open(name string) (fs.File, error) {
	f, err := s.memFS.Open(name)
	return noStat{f}, err
}

type noStat struct{ fs.File }

func (noStat) Stat() (fs.FileInfo, error) { return nil, errors.New("stat failed") }

// A search names what it will not read and does not read it: a link (to a device, a
// pipe or a file), a pipe and a device found in a directory are warnings that name
// them; the other files are read.
func TestSearchDoesNotReadLinksPipesOrDevices(t *testing.T) {
	t.Parallel()
	var given int64
	zeros := func() io.Reader { return countingZeros{&given} }
	fsys := newMemFS(map[string]string{"repo/ok.modelspec.hcl": okRecord, "repo/pipe.modelspec.hcl": "", "repo/dev.modelspec.json": "", "repo/real.modelspec.hcl": okRecord, "dir/models.txt": ""})
	fsys.modes["repo/pipe.modelspec.hcl"] = fs.ModeNamedPipe
	fsys.modes["repo/dev.modelspec.json"] = fs.ModeDevice | fs.ModeCharDevice
	fsys.streams["repo/pipe.modelspec.hcl"], fsys.streams["repo/dev.modelspec.json"] = zeros, zeros
	fsys.links["repo/evil.modelspec.hcl"] = "repo/dev.modelspec.json" // what a repository can hold: a link to /dev/zero
	fsys.links["repo/alias.modelspec.hcl"] = "repo/real.modelspec.hcl"
	fsys.MapFS["repo/evil.modelspec.hcl"] = fsys.MapFS["repo/dev.modelspec.json"]
	fsys.MapFS["repo/alias.modelspec.hcl"] = fsys.MapFS["repo/real.modelspec.hcl"]
	spy := &readSpy{memFS: fsys, read: map[string]bool{}}
	res, err := Lint(spy, []string{"repo"}, LintOptions{})
	if err != nil || given != 0 {
		t.Fatalf("Lint: %v, %d bytes read from what is not a file", err, given)
	}
	var warned []string
	for _, f := range res.Findings {
		if f.Severity != SeverityError || f.Rule != RuleSkipped {
			t.Errorf("finding %v", f)
		}
		warned = append(warned, f.File+" "+f.Message[:strings.Index(f.Message, ",")])
	}
	want := "repo/alias.modelspec.hcl is a symbolic link; repo/dev.modelspec.json is a character device; repo/evil.modelspec.hcl is a symbolic link; repo/pipe.modelspec.hcl is a named pipe"
	if got := strings.Join(warned, "; "); got != want {
		t.Errorf("warnings:\n got %s\nwant %s", got, want)
	}
	for name := range spy.read {
		if name != "repo/ok.modelspec.hcl" && name != "repo/real.modelspec.hcl" {
			t.Errorf("read %s", name)
		}
	}
	// Named on the command line, a pipe, a device and a link to one are errors, and a link to a file is read.
	for _, name := range []string{"repo/pipe.modelspec.hcl", "repo/dev.modelspec.json", "repo/evil.modelspec.hcl"} {
		var notRegular *NotRegularError
		if _, err := Lint(spy, []string{name}, LintOptions{}); !errors.As(err, &notRegular) {
			t.Errorf("%s named: %v", name, err)
		}
	}
	if res, err := Lint(spy, []string{"repo/alias.modelspec.hcl"}, LintOptions{}); err != nil || res.Files != 1 {
		t.Errorf("a link to a file named: %d files, %v", res.Files, err)
	}
	if given != 0 {
		t.Errorf("%d bytes were read from what is not a regular file", given)
	}
}

// The operating system's own files: a device is refused, whatever the fake says.
func TestOSFSRefusesADeviceAndOpensWithoutWaiting(t *testing.T) {
	t.Parallel()
	var notRegular *NotRegularError
	if _, err := ReadSource(OSFS{}, os.DevNull); !errors.As(err, &notRegular) {
		t.Errorf("%s: %v", os.DevNull, err)
	}
	dir := t.TempDir()
	if _, err := ReadSource(OSFS{}, dir); !errors.As(err, &notRegular) || !strings.Contains(err.Error(), "a directory") {
		t.Errorf("a directory: %v", err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if src, err := ReadSource(OSFS{}, file); err != nil || string(src) != "data" {
		t.Errorf("a regular file: %q, %v", src, err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(os.DevNull, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := ReadSource(OSFS{}, link); !errors.As(err, &notRegular) || !strings.Contains(err.Error(), "a symbolic link to a character device") {
		t.Errorf("a link to the null device: %v", err)
	}
	if _, err := (OSFS{}).Open(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file opened")
	}
}

// A file that grows after it was checked is a finding as well, saying only that it
// is more than the limit.
func TestFileThatGrewIsAFinding(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"grew.modelspec.hcl": strings.Repeat("#", MaxInputBytes+5)})
	fsys.sizes["grew.modelspec.hcl"] = 10
	res, err := Lint(fsys, []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleLimit || res.Findings[0].Message != "file is more than 1048576 bytes; the limit is 1048576 bytes" {
		t.Fatalf("findings %v, %v", res.Findings, err)
	}
}

// skippedKinds make a model-named file, p, that a search finds and does not read: a link
// to a regular file, a dangling link, a named pipe, a device.
var skippedKinds = map[string]func(fsys *memFS, p, content string){
	"a link to a regular file": func(fsys *memFS, p, content string) {
		fsys.MapFS["elsewhere/"+filepath.Base(p)] = &fstest.MapFile{Data: []byte(content)}
		fsys.links[p] = "elsewhere/" + filepath.Base(p)
		fsys.MapFS[p] = fsys.MapFS["elsewhere/"+filepath.Base(p)]
	},
	"a dangling link": func(fsys *memFS, p, content string) { fsys.links[p] = "" },
	"a named pipe": func(fsys *memFS, p, content string) {
		fsys.MapFS[p] = &fstest.MapFile{Data: []byte(content)}
		fsys.modes[p] = fs.ModeNamedPipe
	},
	"a device": func(fsys *memFS, p, content string) {
		fsys.MapFS[p] = &fstest.MapFile{Data: []byte(content)}
		fsys.modes[p] = fs.ModeDevice | fs.ModeCharDevice
	},
}

// A model file that a search finds and does not read is an error under both
// profiles, with a rule of its own, and its module is not checked in part: no
// finding of what the read files lack, and none that the missing file would have
// answered. Three sequences of a review: a valid module whose sibling is such a file is
// refused for that and not for an unresolved reference; an invalid model reached
// through one is not green; a JSON twin that is one is not passed over.
func TestSkippedModelFileIsAnErrorAndItsModuleIsNotCheckedInPart(t *testing.T) {
	t.Parallel()
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"Customer\"\n  }\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	invalid := "record \"Bad\" {\n  key = [\"nope\"]\n}\n"
	for kind, make := range skippedKinds {
		for _, profile := range []Profile{ProfileDefault, ProfilePublish} {
			lint := func(fsys *memFS, paths ...string) []string {
				res, err := Lint(fsys, paths, LintOptions{Profile: profile})
				if err != nil {
					t.Fatalf("%s: %v", kind, err)
				}
				var out []string
				for _, f := range res.Findings {
					out = append(out, f.String())
				}
				return out
			}
			want := func(path string) string { return path + ": error: is " }
			// 1 and 3: the sibling of a layout module's file.
			shop := func() *memFS {
				fsys := newMemFS(map[string]string{layout("shop", "order.hcl"): order, "other.modelspec.hcl": "record \"Other\" {\n  key = [\"nope\"]\n}\n"})
				make(fsys, layout("shop", "customer.hcl"), customer)
				return fsys
			}
			for _, paths := range [][]string{{"."}, {filepath.Dir(layout("shop", "x"))}, {layout("shop", "order.hcl")}} {
				got := lint(shop(), paths...)
				found := false
				for _, g := range got {
					switch {
					case strings.Contains(g, want(layout("shop", "customer.hcl"))) && strings.Contains(g, "[skipped-file]"):
						found = true
					case strings.Contains(g, "does not resolve"):
						t.Errorf("%s, %v, %s: a reference error from a module read in part: %s", kind, paths, profile, g)
					case strings.Contains(g, "other.modelspec.hcl") && paths[0] == ".":
						// another module of the run is still checked
					default:
						t.Errorf("%s, %v, %s: unexpected finding %s", kind, paths, profile, g)
					}
				}
				if !found {
					t.Errorf("%s, %v, %s: no skipped-file error: %v", kind, paths, profile, got)
				}
				if paths[0] == "." && !strings.Contains(strings.Join(got, "\n"), "other.modelspec.hcl:2: error: record \"Other\" key") {
					t.Errorf("%s, %s: the other module of the run was not checked: %v", kind, profile, got)
				}
			}
			// 2: an invalid model reached through one, alone in its module, is not green; and the
			// module's own real files are not checked in part either.
			bad := newMemFS(map[string]string{"models/note.txt": ""})
			make(bad, "models/bad.modelspec.hcl", invalid)
			if got := lint(bad, "models"); len(got) != 1 || !strings.Contains(got[0], "models/bad.modelspec.hcl: error: is ") || !strings.Contains(got[0], "[skipped-file]") {
				t.Errorf("%s, %s: an invalid model behind it: %v", kind, profile, got)
			}
			// A JSON twin that is one: the HCL beside it is not reported as stale or as lacking its twin.
			twin := newMemFS(map[string]string{"models/a.modelspec.hcl": customer})
			make(twin, "models/a.modelspec.json", "{}")
			if got := lint(twin, "models"); len(got) != 1 || !strings.Contains(got[0], "models/a.modelspec.json: error: is ") {
				t.Errorf("%s, %s: a JSON twin behind it: %v", kind, profile, got)
			}
			// Only the HCL file is named: its twin is met beside it, and is not passed over.
			if got := lint(twin, "models/a.modelspec.hcl"); len(got) != 1 || !strings.Contains(got[0], "models/a.modelspec.json: error: is ") {
				t.Errorf("%s, %s: a JSON twin behind it, the HCL named: %v", kind, profile, got)
			}
		}
	}
}

// A model file that is one is not a reason for the run to stop: given by name it is read when it
// resolves to a regular file, and --module assignments carry the same rule.
func TestSkippedFileOfAnAssignedModule(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{
		"a.modelspec.hcl": "record \"A\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"core.X\"\n  }\n}\n",
		"ctx/x.hcl":       "record \"X\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n",
	})
	skippedKinds["a named pipe"](fsys, "ctx/y.hcl", "record \"Y\" {}\n")
	res, err := Lint(fsys, []string{"a.modelspec.hcl"}, LintOptions{Modules: []Assignment{{Module: "core", Path: "ctx"}}})
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleSkipped || res.Findings[0].File != "ctx/y.hcl" {
		t.Fatalf("findings %v, %v", res.Findings, err)
	}
	for _, m := range res.Models {
		if m.File == "ctx/x.hcl" && !m.Incomplete || m.File == "a.modelspec.hcl" && m.Incomplete {
			t.Errorf("%s: incomplete = %v", m.File, m.Incomplete)
		}
	}
}

// On the operating system's own file system: the sibling of a layout module's file that
// is a symbolic link to a regular file is an error and the module is not checked in part.
func TestSkippedLinkOnTheRealFileSystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	models := filepath.Join(dir, "spec", "modules", "shop", "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"Customer\"\n  }\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	real := filepath.Join(dir, "customer-real.hcl")
	for path, content := range map[string]string{filepath.Join(models, "order.hcl"): order, real: customer} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(models, "customer.hcl")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	for _, profile := range []Profile{ProfileDefault, ProfilePublish} {
		res, err := Lint(OSFS{}, []string{filepath.Join(models, "order.hcl")}, LintOptions{Profile: profile})
		if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleSkipped || res.Findings[0].File != link || !HasErrors(res.Findings) {
			t.Fatalf("%s: findings %v, %v", profile, res.Findings, err)
		}
	}
	// Named, the link is read through, and the module is whole.
	if res, err := Lint(OSFS{}, []string{filepath.Join(models, "order.hcl"), link}, LintOptions{}); err != nil || len(res.Findings) != 0 {
		t.Fatalf("named: findings %v, %v", res.Findings, err)
	}
}

// Only the module that has the skipped file is incomplete, and an incomplete module is
// not checked at all: two layout modules, one with a link, the other with an error of its
// own (reported); the affected one with an error of its own (not reported); a module that
// refers into the affected one (its references are not reported); and the twin of the same
// name, in either direction.
func TestOnlyTheAffectedModuleIsNotChecked(t *testing.T) {
	t.Parallel()
	keyError := "record \"Bad\" {\n  key = [\"nope\"]\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	refersToCore := "record \"App\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"core.Customer\"\n  }\n}\n"
	for kind, make := range skippedKinds {
		lint := func(fsys *memFS, paths ...string) []string {
			res, err := Lint(fsys, paths, LintOptions{})
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			var out []string
			for _, f := range res.Findings {
				out = append(out, f.String())
			}
			return out
		}
		only := func(got []string, wants ...string) {
			t.Helper()
			expect(t, got, wants...)
		}
		// Two layout modules: billing has an error of its own and is reported; core, which has
		// the skipped file and an error of its own, is not checked and so reports only the file.
		fsys := newMemFS(map[string]string{layout("billing", "a.hcl"): keyError, layout("core", "bad.hcl"): keyError})
		make(fsys, layout("core", "customer.hcl"), customer)
		only(lint(fsys, "."), layout("billing", "a.hcl")+":2: error: record \"Bad\" key", layout("core", "customer.hcl")+": error: is ")
		// A module that refers into the incomplete one: its reference is not reported.
		fsys = newMemFS(map[string]string{layout("core", "other.hcl"): "record \"Other\" {\n  key = []\n}\n", "app.modelspec.hcl": refersToCore}) // Customer is in the file not read
		make(fsys, layout("core", "customer2.hcl"), customer)
		only(lint(fsys, "."), layout("core", "customer2.hcl")+": error: is ")
		// The twin of the same name: the HCL a link and the JSON real, and the other way round.
		fsys = newMemFS(map[string]string{"models/a.modelspec.json": doc(jRecords)})
		make(fsys, "models/a.modelspec.hcl", keyError)
		only(lint(fsys, "models"), "models/a.modelspec.hcl: error: is ")
		fsys = newMemFS(map[string]string{"models/a.modelspec.hcl": keyError})
		make(fsys, "models/a.modelspec.json", "{}")
		only(lint(fsys, "models"), "models/a.modelspec.json: error: is ")
		fsys = newMemFS(map[string]string{"models/a.modelspec.json": `{"modelspec": "1.0-draft-2", "module": {"id": "x", "version": "1"}, "records": {"Bad": {"key": ["nope"], "fields": {}}}}`})
		make(fsys, "models/a.modelspec.hcl", customer)
		only(lint(fsys, "models"), "models/a.modelspec.hcl: error: is ")
	}
}

// A file named on the command line is read, whatever else in the run would have skipped it:
// naming a link after its directory behaves as naming it before, for paths and for --module
// assignments, and what cannot be read at all is an error in either order.
func TestANamedLinkIsReadInEitherOrder(t *testing.T) {
	t.Parallel()
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	order := "record \"Order\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"Customer\"\n  }\n}\n"
	link := layout("shop", "customer.hcl")
	tree := func(kind string) *memFS {
		fsys := newMemFS(map[string]string{layout("shop", "order.hcl"): order})
		skippedKinds[kind](fsys, link, customer)
		return fsys
	}
	lint := func(fsys *memFS, paths []string, modules ...Assignment) (Result, error) {
		return Lint(fsys, paths, LintOptions{Modules: modules})
	}
	for _, paths := range [][]string{{link, "."}, {".", link}, {link, "spec"}, {"spec", link}} {
		res, err := lint(tree("a link to a regular file"), paths)
		if err != nil || len(res.Findings) != 0 || res.Files != 2 || len(res.Skipped) != 0 {
			t.Errorf("%v: %d files, findings %v, skipped %d, %v", paths, res.Files, res.Findings, len(res.Skipped), err)
		}
	}
	// The same through --module, in either order of the two assignments.
	fsys := newMemFS(map[string]string{"core/space.hcl": customer})
	skippedKinds["a link to a regular file"](fsys, "core/customer.hcl", customer)
	dir, file := Assignment{Module: "core", Path: "core"}, Assignment{Module: "core", Path: "core/customer.hcl"}
	for _, assign := range [][]Assignment{{file, dir}, {dir, file}} {
		res, err := lint(fsys, nil, assign...)
		if err != nil || res.Files != 2 || len(res.Skipped) != 0 {
			t.Errorf("%v: %d files, skipped %d, %v", assign, res.Files, len(res.Skipped), err)
		}
		// Both files declare Customer: read, so the module says so; nothing was left unread.
		if len(res.Findings) != 1 || res.Findings[0].Rule != RuleDuplicate {
			t.Errorf("%v: findings %v", assign, res.Findings)
		}
	}
	// A path that cannot be read is an error whichever comes first, and a link that is not named is still skipped.
	for _, kind := range []string{"a named pipe", "a device", "a dangling link"} {
		for _, paths := range [][]string{{link, "."}, {".", link}} {
			var notRegular *NotRegularError
			_, err := lint(tree(kind), paths)
			if err == nil || kind != "a dangling link" && !errors.As(err, &notRegular) {
				t.Errorf("%s, %v: %v", kind, paths, err)
			}
		}
		if res, err := lint(tree(kind), []string{"."}); err != nil || len(res.Skipped) != 1 {
			t.Errorf("%s: not named: %d skipped, %v", kind, len(res.Skipped), err)
		}
	}
}

// Only the skipped-file error is reported for a module that was not read whole, and no
// finding from another module into it: a directory given both as a path and to --module
// (the file was first met in the search that has no module name), and a standalone module
// that is itself the file that was not read, whatever its form.
func TestPartlyLoadedModulesReportOnlyTheSkippedFile(t *testing.T) {
	t.Parallel()
	space := "record \"Space\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	customer := "record \"Customer\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n}\n"
	app := "record \"App\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"core.Customer\"\n  }\n}\n"
	for kind, make := range skippedKinds {
		// A directory as a path and with --module: core.hcl is read only as part of the assignment.
		fsys := newMemFS(map[string]string{"core/core.hcl": space, "app/app.modelspec.hcl": app})
		make(fsys, "core/customer.modelspec.hcl", customer)
		res, err := Lint(fsys, []string{"core", "app"}, LintOptions{Modules: []Assignment{{Module: "core", Path: "core"}}})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range res.Findings {
			got = append(got, f.String())
		}
		expect(t, got, "core/customer.modelspec.hcl: error: is ")
		if len(res.Skipped) != 1 || res.Skipped[0].Module != "core" {
			t.Errorf("%s: skipped %+v, want one found under module core", kind, res.Skipped)
		}
		// A standalone module that is the file not read, as HCL or as JSON.
		for suffix, content := range map[string]string{HCLSuffix: customer, JSONSuffix: doc(jRecords)} {
			fsys = newMemFS(map[string]string{"app.modelspec.hcl": app})
			make(fsys, "core"+suffix, content)
			res, err = Lint(fsys, []string{"."}, LintOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got = got[:0]
			for _, f := range res.Findings {
				got = append(got, f.String())
			}
			expect(t, got, "core"+suffix+": error: is ")
		}
	}
}

// A models directory that is itself a link is not searched, and says so: an error that
// names the directory (rule skipped-file, both profiles), with the module of the directory
// not checked and no reference into it reported, and the way out the same as for a file:
// naming it on the command line, before or after the directory above it. A link named models
// elsewhere, one that is not to a directory, and a real directory are not touched.
func TestAModelsDirectoryThatIsALinkIsAnError(t *testing.T) {
	t.Parallel()
	shopModels := layout("shop", "")
	shopModels = shopModels[:len(shopModels)-1] // spec/graph/modules/shop/models
	invalid := "record \"Bad\" {\n  key = [\"nope\"]\n}\n"
	app := "record \"App\" {\n  key = [\"id\"]\n  field \"id\" {\n    type = \"int\"\n  }\n  field \"c\" {\n    record = \"shop.Bad\"\n  }\n}\n"
	tree := func() *memFS {
		fsys := newMemFS(map[string]string{"elsewhere/shop/bad.hcl": invalid, "app.modelspec.hcl": app, "other.modelspec.hcl": invalid})
		fsys.MapFS[shopModels] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
		fsys.links[shopModels] = "elsewhere/shop"
		return fsys
	}
	lint := func(profile Profile, paths ...string) (Result, []string) {
		res, err := Lint(tree(), paths, LintOptions{Profile: profile})
		if err != nil {
			t.Fatalf("%v: %v", paths, err)
		}
		var out []string
		for _, f := range res.Findings {
			out = append(out, f.String())
		}
		return res, out
	}
	// Searched: the directory is named by the error, its model is not read, the module is not
	// reported as unknown to the model that refers into it, and the other model of the run is checked.
	res, got := lint(ProfileDefault, ".")
	expect(t, got, shopModels+": error: is a symbolic link to a directory, which a search does not follow", "other.modelspec.hcl:2: error: record \"Bad\" key")
	if len(res.Skipped) != 1 || res.Skipped[0].Finding.Rule != RuleSkipped || res.Skipped[0].Finding.File != shopModels {
		t.Errorf("skipped %+v", res.Skipped)
	}
	// An error under the publish profile too.
	if res, _ := lint(ProfilePublish, "."); len(res.Skipped) != 1 || !HasErrors(res.Findings) {
		t.Errorf("publish: skipped %d, findings %v", len(res.Skipped), res.Findings)
	}
	// Named, before or after the directory above it, the link is entered and its model is read.
	keyOfBad := layout("shop", "bad.hcl") + ":2: error: record \"Bad\" key"
	_, got = lint(ProfileDefault, shopModels)
	expect(t, got, keyOfBad)
	for _, paths := range [][]string{{shopModels, "."}, {".", shopModels}} {
		res, got := lint(ProfileDefault, paths...)
		expect(t, got, keyOfBad, "other.modelspec.hcl:2: error: record \"Bad\" key")
		if len(res.Skipped) != 0 {
			t.Errorf("%v: %d skipped", paths, len(res.Skipped))
		}
	}
	// Not a models directory, or nothing to search: no finding.
	quiet := newMemFS(map[string]string{"ok.modelspec.hcl": okRecord, "elsewhere/x.hcl": okRecord, "docs/real/models/ok.modelspec.hcl": okRecord})
	quiet.MapFS["docs/models"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}
	quiet.links["docs/models"] = "elsewhere" // named models, but not under modules/<id>
	quiet.MapFS[layout("a", "")[:len(layout("a", ""))-1]] = &fstest.MapFile{Data: []byte("x")}
	quiet.links[layout("a", "")[:len(layout("a", ""))-1]] = "ok.modelspec.hcl" // a link to a file
	quiet.links[layout("b", "")[:len(layout("b", ""))-1]] = ""                 // a dangling one
	if res, err := Lint(quiet, []string{"."}, LintOptions{}); err != nil || len(res.Findings) != 0 || len(res.Skipped) != 0 {
		t.Errorf("findings %v, skipped %d, %v", res.Findings, len(res.Skipped), err)
	}
}

// On the operating system's file system: a models directory that is a link, searched, and
// named.
func TestAModelsDirectoryThatIsALinkOnTheRealFileSystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	modules := filepath.Join(dir, "spec", "modules", "shop")
	for _, d := range []string{real, modules} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(real, "bad.hcl"), []byte("record \"Bad\" {\n  key = [\"nope\"]\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(modules, "models")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	for _, profile := range []Profile{ProfileDefault, ProfilePublish} {
		res, err := Lint(OSFS{}, []string{dir}, LintOptions{Profile: profile})
		if err != nil || len(res.Findings) != 1 || res.Findings[0].Rule != RuleSkipped || res.Findings[0].File != link || !strings.Contains(res.Findings[0].Message, "a symbolic link to a directory") {
			t.Fatalf("%s: findings %v, %v", profile, res.Findings, err)
		}
		res, err = Lint(OSFS{}, []string{link, dir}, LintOptions{Profile: profile})
		if err != nil || len(res.Skipped) != 0 || len(res.Findings) == 0 || res.Findings[0].Rule == RuleSkipped {
			t.Fatalf("%s, named: findings %v, %v", profile, res.Findings, err)
		}
	}
}

// OSFS.WriteFile writes through a temporary file in the same directory, created exclusively,
// and a rename, so that what is at the name is replaced and never followed, and a temporary
// file does not stay.
func TestOSFSWriteFileReplacesThroughATemporaryFile(t *testing.T) {
	t.Parallel()
	listing := func(dir string) string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, " ")
	}
	read := func(name string) string {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// A new file and an existing one: the content is the new data, nothing else is left, and a replaced file keeps its permissions.
	dir := t.TempDir()
	file := filepath.Join(dir, "out.json")
	if err := (OSFS{}).WriteFile(file, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&^0o644 != 0 {
		t.Fatalf("a new file: %v, %v", info, err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (OSFS{}).WriteFile(file, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file); err != nil || read(file) != "second" || info.Mode().Perm() != 0o600 || listing(dir) != "out.json" {
		t.Fatalf("a replaced file: %q, %v, listing %q", read(file), info.Mode(), listing(dir))
	}
	// A link at the name is replaced, and what it pointed to is left as it was: a link swapped in after a check cannot redirect the write.
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(victim, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := (OSFS{}).WriteFile(link, []byte("through"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || !info.Mode().IsRegular() || read(link) != "through" || read(victim) != "mine" {
		t.Fatalf("a link at the name: %v, %q, victim %q", info, read(link), read(victim))
	}
	// The temporary file is created exclusively: one that is already there, or a link planted at its name, is not written through, and is left alone.
	fixed := OSFS{tempName: func() string { return ".planted" }}
	if err := os.WriteFile(filepath.Join(dir, ".planted"), []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fixed.WriteFile(file, []byte("x"), 0o644); !errors.Is(err, fs.ErrExist) || read(filepath.Join(dir, ".planted")) != "planted" || read(file) != "second" {
		t.Fatalf("a temporary file that exists: %v, %q, %q", err, read(filepath.Join(dir, ".planted")), read(file))
	}
	if err := os.Remove(filepath.Join(dir, ".planted")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, ".planted")); err != nil {
		t.Fatal(err)
	}
	if err := fixed.WriteFile(file, []byte("x"), 0o644); !errors.Is(err, fs.ErrExist) || read(victim) != "mine" {
		t.Fatalf("a link at the temporary name: %v, victim %q", err, read(victim))
	}
	// A failure leaves nothing behind: the name is a directory (the rename fails), or its directory is not there.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, "out.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (OSFS{}).WriteFile(filepath.Join(other, "out.json"), []byte("x"), 0o644); err == nil || listing(other) != "out.json" {
		t.Fatalf("a directory at the name: %v, listing %q", err, listing(other))
	}
	if err := (OSFS{}).WriteFile(filepath.Join(dir, "no", "such", "out.json"), []byte("x"), 0o644); err == nil {
		t.Fatal("a directory that is not there: no error")
	}
}

// writeAndClose reports a failed write and a failed close, and closes in every case.
func TestWriteAndClose(t *testing.T) {
	t.Parallel()
	boom := errors.New("write failed")
	closeErr := errors.New("close failed")
	for name, tc := range map[string]struct {
		w    *fakeWriter
		want error
	}{
		"both succeed": {&fakeWriter{}, nil},
		"write fails":  {&fakeWriter{writeErr: boom}, boom},
		"close fails":  {&fakeWriter{closeErr: closeErr}, closeErr},
		"both fail":    {&fakeWriter{writeErr: boom, closeErr: closeErr}, boom},
	} {
		if err := writeAndClose(tc.w, []byte("data")); !errors.Is(err, tc.want) && err != tc.want || !tc.w.closed {
			t.Errorf("%s: %v, closed %v", name, err, tc.w.closed)
		}
	}
}

type fakeWriter struct {
	writeErr, closeErr error
	closed             bool
}

func (f *fakeWriter) Write(p []byte) (int, error) { return len(p), f.writeErr }
func (f *fakeWriter) Close() error                { f.closed = true; return f.closeErr }

// DiscoverModules finds what Lint is given: the paths, and the files assigned to
// modules, which may be called anything.
func TestDiscoverModules(t *testing.T) {
	t.Parallel()
	fsys := newMemFS(map[string]string{"a.modelspec.hcl": okRecord, "parts/b.hcl": okRecord, "parts/readme.txt": ""})
	files, found, err := DiscoverModules(fsys, []string{"a.modelspec.hcl"}, []Assignment{{Module: "shop", Path: "parts"}})
	if err != nil || len(found) != 0 || len(files) != 2 || files[0].Path != "a.modelspec.hcl" || files[1].Path != "parts/b.hcl" {
		t.Fatalf("files = %v, findings = %v, %v", files, found, err)
	}
	if _, _, err := DiscoverModules(fsys, []string{"a.modelspec.hcl"}, []Assignment{{Module: "shop", Path: "missing"}}); err == nil {
		t.Error("an assignment of a missing path was accepted")
	}
	if _, _, err := DiscoverModules(fsys, []string{"missing"}, nil); err == nil {
		t.Error("a missing path was accepted")
	}
}
