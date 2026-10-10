package modelspec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// Whether the old spelling is an error or a warning in a file is decided for the
// module, once, after every file is loaded (Lint; OldSpellingSeverity). The tests here
// state the rule on their own, not by running the search Lint runs:
//
//   - a module is being checked when one of its files is a file named on the command
//     line or lies under a path named on the command line (whether or not --module also
//     supplies it), or when no path brings in any file: then every module supplied is
//     being checked;
//   - a module is only referred to when a path brings in files and none of the module's
//     files is one of them or lies under it.
//
// The result must not depend on the order of the file names or of the arguments, so
// every case is run in every order of its paths and of its --module assignments.

// spellingOf lints and returns the severity of the deprecated-spelling finding of
// each file that has one.
func spellingOf(t *testing.T, files map[string]string, paths []string, assign []Assignment) map[string]Severity {
	t.Helper()
	res, err := Lint(newMemFS(files), paths, LintOptions{Modules: assign})
	if err != nil {
		t.Fatalf("%v %v: %v", paths, assign, err)
	}
	got := map[string]Severity{}
	for _, f := range res.Findings {
		if f.Rule == RuleDeprecated {
			got[f.File] = f.Severity
		}
	}
	return got
}

// orders returns every order of a list.
func orders[T any](list []T) [][]T {
	if len(list) <= 1 {
		return [][]T{append([]T(nil), list...)}
	}
	var out [][]T
	for i := range list {
		rest := append(append([]T(nil), list[:i]...), list[i+1:]...)
		for _, tail := range orders(rest) {
			out = append(out, append([]T{list[i]}, tail...))
		}
	}
	return out
}

// spellingInEveryOrder is spellingOf, and fails when the order of the paths or of the
// assignments changes the answer.
func spellingInEveryOrder(t *testing.T, name string, files map[string]string, paths []string, assign []Assignment) map[string]Severity {
	t.Helper()
	var first map[string]Severity
	for _, p := range orders(paths) {
		for _, a := range orders(assign) {
			got := spellingOf(t, files, p, a)
			if first == nil {
				first = got
			} else if fmt.Sprint(got) != fmt.Sprint(first) {
				t.Errorf("%s: paths %v, modules %v give %v, but another order gave %v", name, p, a, got, first)
			}
		}
	}
	return first
}

func sameSeverities(t *testing.T, name string, got, want map[string]Severity) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: %v, want %v", name, got, want)
	}
}

var oldWords = strings.NewReplacer("record", "entity", "field", "property")

func oldRecord(name string) string { return oldWords.Replace(recordWith(name)) }

func TestCheckedModuleIsDecidedPerModule(t *testing.T) {
	t.Parallel()
	const layout = "spec/graph/modules/app/models/"
	dir := strings.TrimSuffix(layout, "/")
	e, w := SeverityError, SeverityWarning

	// A layout module: whichever file holds the old spelling, whichever file is named, and
	// whether or not --module also supplies the directory, the whole module is checked.
	for _, oldFile := range []string{"a.hcl", "b.hcl", "c.hcl"} {
		files := map[string]string{}
		for _, f := range []string{"a.hcl", "b.hcl", "c.hcl"} {
			files[layout+f] = recordWith("N" + strings.TrimSuffix(f, ".hcl"))
		}
		files[layout+oldFile] = oldRecord("Old")
		for _, named := range []string{"a.hcl", "b.hcl", "c.hcl"} {
			for _, assigned := range []bool{false, true} {
				var assign []Assignment
				if assigned {
					assign = []Assignment{{"app", dir}}
				}
				name := fmt.Sprintf("a layout module, %s old, %s named, assigned %v", oldFile, named, assigned)
				sameSeverities(t, name, spellingInEveryOrder(t, name, files, []string{layout + named}, assign), map[string]Severity{layout + oldFile: e})
			}
		}
		// Nothing is named: what is assigned is what is checked.
		sameSeverities(t, "a layout module, assigned only", spellingOf(t, files, nil, []Assignment{{"app", dir}}), map[string]Severity{layout + oldFile: e})
		// Another model is named and the layout module is only supplied: it is referred to whole.
		files["main"+hclExt] = okRecord
		sameSeverities(t, "a layout module that is only referred to", spellingInEveryOrder(t, "only referred to", files, []string{"main" + hclExt}, []Assignment{{"app", dir}}), map[string]Severity{layout + oldFile: w})
	}

	// A file that only --module can make a model (any .hcl name) under a path that is named
	// is a file of the path: the directory is named, so what is in it is checked.
	model := map[string]string{
		"model/part1.hcl": recordWith("Order", member("customer", "    record = \"Customer\"\n")),
		"model/part2.hcl": oldRecord("Customer"),
	}
	sameSeverities(t, "a plain .hcl file under the named directory", spellingInEveryOrder(t, "plain", model, []string{"model"}, []Assignment{{"shop", "model"}}), map[string]Severity{"model/part2.hcl": e})
	sameSeverities(t, "no path", spellingOf(t, model, nil, []Assignment{{"shop", "model"}}), map[string]Severity{"model/part2.hcl": e})

	// The same bytes under two names are the same verdict.
	for _, name := range []string{"vendor/customers.hcl", "vendor/customers.modelspec.hcl"} {
		files := map[string]string{"orders" + hclExt: recordWith("Order", member("customer", "    record = \"customers.Customer\"\n")), name: oldRecord("Customer")}
		sameSeverities(t, "the same bytes as "+name, spellingInEveryOrder(t, name, files, []string{"."}, []Assignment{{"customers", name}}), map[string]Severity{name: e})
	}

	// A module put together with --module, one of whose files is named: the module is
	// checked, the file that is not named included.
	shop := map[string]string{
		"shop" + hclExt: recordWith("B", member("a", "    record = \"A\"\n")),
		"parts/a.hcl":   oldRecord("A"),
	}
	sameSeverities(t, "a module assembled with --module, one file named", spellingInEveryOrder(t, "assembled", shop, []string{"shop" + hclExt}, []Assignment{{"shop", "shop" + hclExt}, {"shop", "parts"}}), map[string]Severity{"parts/a.hcl": e})
	sameSeverities(t, "a module assembled with --module, the plain file named by its directory", spellingInEveryOrder(t, "assembled", shop, []string{"shop" + hclExt, "parts"}, []Assignment{{"shop", "parts"}}), map[string]Severity{"parts/a.hcl": e})

	// A path that brings in no file is no path: nothing is then referred to.
	empty := map[string]string{"empty/notes.txt": "nothing here", "old" + hclExt: oldRecord("Old")}
	sameSeverities(t, "a path that holds no model", spellingInEveryOrder(t, "empty", empty, []string{"empty"}, []Assignment{{"m", "old" + hclExt}}), map[string]Severity{"old" + hclExt: e})

	// Two assignments and no path: every module supplied is being checked.
	two := map[string]string{"x.hcl": oldRecord("X"), "y.hcl": oldRecord("Y")}
	sameSeverities(t, "two assignments, no path", spellingInEveryOrder(t, "two", two, nil, []Assignment{{"x", "x.hcl"}, {"y", "y.hcl"}}), map[string]Severity{"x.hcl": e, "y.hcl": e})

	// The exception: a model is named, and another module that it refers to is only supplied.
	refer := map[string]string{
		"main" + hclExt:              recordWith("Booking", member("space", "    record = \"core.Space\"\n")),
		"shared/core.modelspec.hcl":  oldRecord("Space"),
		"shared/core.modelspec.json": oldDoc(jEntities),
	}
	core := map[string]Severity{"shared/core.modelspec.hcl": w, "shared/core.modelspec.json": w}
	sameSeverities(t, "a pair that is only referred to", spellingInEveryOrder(t, "pair", refer, []string{"main" + hclExt}, []Assignment{{"core", "shared/core.modelspec.hcl"}}), core)
	sameSeverities(t, "a directory that is only referred to", spellingInEveryOrder(t, "dir", refer, []string{"main" + hclExt}, []Assignment{{"core", "shared"}}), core)
	sameSeverities(t, "the JSON copy supplied", spellingInEveryOrder(t, "json", refer, []string{"main" + hclExt}, []Assignment{{"core", "shared/core.modelspec.json"}}), core)
	// Named, it is checked, wherever else it is also supplied; and the JSON copy follows the HCL.
	checked := map[string]Severity{"shared/core.modelspec.hcl": e, "shared/core.modelspec.json": e}
	sameSeverities(t, "named too", spellingInEveryOrder(t, "named", refer, []string{"main" + hclExt, "shared/core.modelspec.hcl"}, []Assignment{{"core", "shared/core.modelspec.hcl"}}), checked)
	sameSeverities(t, "named by its directory", spellingInEveryOrder(t, "dir named", refer, []string{"main" + hclExt, "shared"}, []Assignment{{"core", "shared/core.modelspec.hcl"}}), checked)
	sameSeverities(t, "named by its HCL, assigned by its JSON", spellingInEveryOrder(t, "mixed", refer, []string{"shared/core.modelspec.hcl"}, []Assignment{{"core", "shared/core.modelspec.json"}}), checked)
	sameSeverities(t, "named by its JSON", spellingInEveryOrder(t, "json named", refer, []string{"main" + hclExt, "shared/core.modelspec.json"}, []Assignment{{"core", "shared/core.modelspec.hcl"}}), checked)
	sameSeverities(t, "no path", spellingOf(t, refer, nil, []Assignment{{"core", "shared/core.modelspec.hcl"}}), checked)
	// The model that refers is itself in the old spelling: it is named, so an error, and the
	// module it refers to keeps the warning.
	referOld := map[string]string{"main" + hclExt: oldWords.Replace(refer["main"+hclExt]), "shared/core.modelspec.hcl": refer["shared/core.modelspec.hcl"]}
	sameSeverities(t, "the referring model is old", spellingInEveryOrder(t, "referring", referOld, []string{"main" + hclExt}, []Assignment{{"core", "shared/core.modelspec.hcl"}}), map[string]Severity{"main" + hclExt: e, "shared/core.modelspec.hcl": w})

	// A directory beside the named one whose name begins with it is not under it.
	beside := map[string]string{
		"model/sales" + hclExt:       recordWith("Sales", member("c", "    record = \"core.Customer\"\n")),
		"model-pinned/core" + hclExt: oldRecord("Customer"),
	}
	sameSeverities(t, "a directory whose name begins with the named one", spellingInEveryOrder(t, "beside", beside, []string{"model"}, []Assignment{{"core", "model-pinned/core" + hclExt}}), map[string]Severity{"model-pinned/core" + hclExt: w})

	// Two sources that claim one module name are one module for this decision: the module is
	// checked because one of its sources is named, and the other is checked with it (decision D2).
	twoSources := map[string]string{"a/core" + hclExt: recordWith("Space"), "b/core" + hclExt: oldRecord("Other")}
	sameSeverities(t, "two sources of one name, one named", spellingInEveryOrder(t, "two sources", twoSources, []string{"a/core" + hclExt}, []Assignment{{"core", "b/core" + hclExt}}), map[string]Severity{"b/core" + hclExt: e})

	// An HCL file and the JSON copy beside it are checked together, whatever module --module
	// assigns either of them to (decision D1, the implementing session's: the rule for a
	// module that is being checked wins over the exception): when the module of one is being
	// checked, the module of the other is as well, repeated until nothing changes.
	copyPair := map[string]string{"x" + hclExt: okRecord, "x.modelspec.json": oldDoc(jEntities)}
	sameSeverities(t, "the copy of a named HCL file, assigned to another module", spellingInEveryOrder(t, "copy", copyPair, []string{"x" + hclExt}, []Assignment{{"other", "x.modelspec.json"}}), map[string]Severity{"x.modelspec.json": e})
	hclOld := map[string]string{"x" + hclExt: oldRecord("A"), "x.modelspec.json": doc(jRecords)}
	sameSeverities(t, "the HCL file beside a named JSON copy, assigned to another module", spellingInEveryOrder(t, "hcl", hclOld, []string{"x.modelspec.json"}, []Assignment{{"other", "x" + hclExt}}), map[string]Severity{"x" + hclExt: e})
	// The copy is named and assigned to a module of its own, and the HCL file beside it is in the
	// old spelling: the HCL file was read because its copy was named, so it is checked, and so it
	// is when it is also assigned to a third module (the two-assignment form).
	namedCopy := map[string]string{"x" + hclExt: oldRecord("A"), "x.modelspec.json": doc(jRecords)}
	sameSeverities(t, "the named copy is assigned", spellingInEveryOrder(t, "named copy", namedCopy, []string{"x.modelspec.json"}, []Assignment{{"a", "x.modelspec.json"}}), map[string]Severity{"x" + hclExt: e})
	sameSeverities(t, "the named copy and its HCL file are assigned to two modules", spellingInEveryOrder(t, "named copy, two modules", namedCopy, []string{"x.modelspec.json"}, []Assignment{{"a", "x.modelspec.json"}, {"b", "x" + hclExt}}), map[string]Severity{"x" + hclExt: e})
	// A chain: the module of the copy holds another HCL file, whose own copy is given a third
	// module. Every file of the chain is checked.
	chain := map[string]string{
		"x" + hclExt:              okRecord,
		"x.modelspec.json":        oldDoc(jEntities),
		"pinned/y" + hclExt:       oldRecord("Y"),
		"pinned/y.modelspec.json": oldDoc(jEntities),
	}
	sameSeverities(t, "a chain of copies", spellingInEveryOrder(t, "chain", chain, []string{"x" + hclExt}, []Assignment{{"other", "x.modelspec.json"}, {"other", "pinned/y" + hclExt}, {"third", "pinned/y.modelspec.json"}}),
		map[string]Severity{"x.modelspec.json": e, "pinned/y" + hclExt: e, "pinned/y.modelspec.json": e})
	// A copy beside a file that is itself only referred to stays referred to.
	sameSeverities(t, "the copy of a file that is only referred to", spellingInEveryOrder(t, "copy referred", map[string]string{"main" + hclExt: okRecord, "p/x" + hclExt: oldRecord("P"), "p/x.modelspec.json": oldDoc(jEntities)}, []string{"main" + hclExt}, []Assignment{{"other", "p/x.modelspec.json"}}), map[string]Severity{"p/x" + hclExt: w, "p/x.modelspec.json": w})

	// Several modules are supplied, and only one of them is named: the others are referred to.
	three := map[string]string{
		"main" + hclExt: okRecord,
		"p/one.hcl":     oldRecord("One"),
		"q/two.hcl":     oldRecord("Two"),
	}
	sameSeverities(t, "two modules, one named", spellingInEveryOrder(t, "three", three, []string{"main" + hclExt, "q"}, []Assignment{{"one", "p/one.hcl"}, {"two", "q/two.hcl"}}), map[string]Severity{"p/one.hcl": w, "q/two.hcl": e})
}

// The R4 form: a model kept in plain .hcl files is checked against a pinned module in
// the old spelling by naming the model's directory as a path as well; with no path the
// module is checked too (nothing says it is only a reference).
func TestCheckedModuleAgainstAPinnedModule(t *testing.T) {
	t.Parallel()
	pinned := oldRecord("Customer")
	for name, parts := range map[string]string{"current": recordWith("Order", member("customer", "    record = \"core.Customer\"\n")), "old": oldWords.Replace(recordWith("Order", member("customer", "    record = \"core.Customer\"\n")))} {
		files := map[string]string{"parts/a.hcl": parts, "pinned/core" + hclExt: pinned}
		assign := []Assignment{{"shop", "parts"}, {"core", "pinned/core" + hclExt}}
		want := map[string]Severity{"pinned/core" + hclExt: SeverityWarning}
		if name == "old" {
			want["parts/a.hcl"] = SeverityError
		}
		sameSeverities(t, name+", the directory named as a path too", spellingInEveryOrder(t, name, files, []string{"parts"}, assign), want)
		// No path named: both modules are being checked.
		want["pinned/core"+hclExt] = SeverityError
		sameSeverities(t, name+", no path", spellingInEveryOrder(t, name, files, nil, assign), want)
	}
	// The result is the same in the exit status: an error fails the run, a warning does not.
	files := map[string]string{"parts/a.hcl": recordWith("Order", member("customer", "    record = \"core.Customer\"\n")), "pinned/core" + hclExt: pinned}
	res, err := Lint(newMemFS(files), []string{"parts"}, LintOptions{Modules: []Assignment{{"shop", "parts"}, {"core", "pinned/core" + hclExt}}})
	if err != nil || HasErrors(res.Findings) || res.Files != 2 {
		t.Errorf("findings %v, %d files, %v", res.Findings, res.Files, err)
	}
	// The flag is on the models Lint returns, and is off for a model that is read on its own.
	var flags []string
	for _, m := range res.Models {
		if m.ReferenceOnly {
			flags = append(flags, m.File)
		}
	}
	if strings.Join(flags, ",") != "pinned/core.modelspec.hcl" {
		t.Errorf("models only referred to: %v", flags)
	}
	if m, _ := ParseHCL("x"+hclExt, []byte(pinned)); m.ReferenceOnly {
		t.Error("a model read on its own is being checked")
	}
}

// A second name for the same place does not defeat "lies under a named path": a symbolic
// link to the named directory, and (on a file system that ignores letter case) another case
// of its name, are the named directory. Decided on the files themselves (Stat and SameFile).
func TestCheckedModuleLiesUnderANamedPathByAnyName(t *testing.T) {
	t.Parallel()
	files := map[string]string{"model/x" + hclExt: okRecord, "model/part2.hcl": oldRecord("Customer")}
	// An in-memory link.
	for name, tc := range map[string]struct {
		paths  []string
		assign []Assignment
		want   map[string]Severity
	}{
		"a link to the named directory is supplied":    {[]string{"model"}, []Assignment{{"shop", "alias"}}, map[string]Severity{"alias/part2.hcl": SeverityError}},
		"the link is named and the directory supplied": {[]string{"alias"}, []Assignment{{"shop", "model"}}, map[string]Severity{"model/part2.hcl": SeverityError}},
		"another directory is supplied":                {[]string{"model"}, []Assignment{{"shop", "elsewhere"}}, map[string]Severity{"elsewhere/part2.hcl": SeverityWarning}},
	} {
		fsys := newMemFS(files)
		fsys.MapFS["elsewhere/part2.hcl"] = fsys.MapFS["model/part2.hcl"]
		fsys.links["alias"] = "model"
		for _, p := range orders(tc.paths) {
			res, err := Lint(fsys, p, LintOptions{Modules: tc.assign})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]Severity{}
			for _, f := range res.Findings {
				if f.Rule == RuleDeprecated {
					got[f.File] = f.Severity
				}
			}
			sameSeverities(t, name, got, tc.want)
		}
	}
}

// The same on the real file system: a symbolic link (skipped where the platform cannot make
// one) and another letter case (only where the volume ignores case: detected here).
func TestCheckedModuleLiesUnderANamedPathOnTheRealFileSystem(t *testing.T) {
	t.Parallel()
	setup := func() string {
		dir := t.TempDir()
		for name, src := range map[string]string{"model/x" + hclExt: okRecord, "model/part2.hcl": oldRecord("Customer")} {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	severities := func(dir string, paths []string, assign []Assignment) map[string]Severity {
		res, err := Lint(OSFS{}, paths, LintOptions{Modules: assign})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Severity{}
		for _, f := range res.Findings {
			if f.Rule == RuleDeprecated {
				rel, _ := filepath.Rel(dir, f.File)
				got[filepath.ToSlash(rel)] = f.Severity
			}
		}
		return got
	}
	t.Run("symbolic link", func(t *testing.T) {
		dir := setup()
		if err := os.Symlink(filepath.Join(dir, "model"), filepath.Join(dir, "alias")); err != nil {
			t.Skip("cannot make a symbolic link here:", err)
		}
		sameSeverities(t, "link supplied, directory named", severities(dir, []string{filepath.Join(dir, "model")}, []Assignment{{"shop", filepath.Join(dir, "alias")}}), map[string]Severity{"alias/part2.hcl": SeverityError})
		sameSeverities(t, "directory supplied, link named", severities(dir, []string{filepath.Join(dir, "alias")}, []Assignment{{"shop", filepath.Join(dir, "model")}}), map[string]Severity{"model/part2.hcl": SeverityError})
	})
	t.Run("letter case", func(t *testing.T) {
		dir := setup()
		if _, err := os.Stat(filepath.Join(dir, "MODEL")); err != nil {
			t.Skip("this volume tells letter cases apart")
		}
		sameSeverities(t, "another case supplied", severities(dir, []string{filepath.Join(dir, "model")}, []Assignment{{"shop", filepath.Join(dir, "MODEL")}}), map[string]Severity{"MODEL/part2.hcl": SeverityError})
		sameSeverities(t, "another case named", severities(dir, []string{filepath.Join(dir, "MODEL")}, []Assignment{{"shop", filepath.Join(dir, "model")}}), map[string]Severity{"model/part2.hcl": SeverityError})
	})
}

// One file supplied under two names, one of them under a named path, is under it: the verdict
// is the same in either order of the two --module arguments (a file under a named path by any of
// its names). The file is printed under the first name met, so the test counts the findings.
func TestCheckedModuleOfAFileSuppliedUnderTwoNames(t *testing.T) {
	t.Parallel()
	lint := func(fsys FS, paths []string, assign []Assignment) []Finding {
		res, err := Lint(fsys, paths, LintOptions{Modules: assign})
		if err != nil {
			t.Fatal(err)
		}
		var got []Finding
		for _, f := range res.Findings {
			if f.Rule == RuleDeprecated {
				got = append(got, f)
			}
		}
		return got
	}
	one := func(name string, got []Finding, sev Severity) {
		t.Helper()
		if len(got) != 1 || got[0].Severity != sev {
			t.Errorf("%s: %v, want one finding, a %s", name, got, sev)
		}
	}
	// In memory: model/out is a link to pinned.
	files := map[string]string{"model/x" + hclExt: okRecord, "pinned/core" + hclExt: oldRecord("Customer")}
	for _, assign := range orders([]Assignment{{"core", "pinned/core" + hclExt}, {"core", "model/out/core" + hclExt}}) {
		fsys := newMemFS(files)
		fsys.links["model/out"] = "pinned"
		one(fmt.Sprint("a link, ", assign), lint(fsys, []string{"model"}, assign), SeverityError)
	}
	// Supplied through the link only, the file is not under the named path by its other name:
	// there is none. The link points outside the named directory, and a search follows no link.
	fsys := newMemFS(files)
	fsys.links["model/out"] = "pinned"
	one("only its own path", lint(fsys, []string{"model"}, []Assignment{{"core", "pinned/core" + hclExt}}), SeverityWarning)

	// On the real file system: a symbolic link and a hard link (skipped where one cannot be made).
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("model/x"+hclExt, okRecord)
	write("pinned/core"+hclExt, oldRecord("Customer"))
	write("model/part2.hcl", oldRecord("Part"))
	abs := func(n string) string { return filepath.Join(dir, n) }
	if err := os.Symlink(abs("pinned"), abs("model/out")); err != nil {
		t.Log("no symbolic link here:", err)
	} else {
		for _, assign := range orders([]Assignment{{"core", abs("pinned/core" + hclExt)}, {"core", abs("model/out/core" + hclExt)}}) {
			one(fmt.Sprint("a real symbolic link, ", assign), lint(OSFS{}, []string{abs("model")}, assign), SeverityError)
		}
	}
	if err := os.MkdirAll(abs("other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(abs("model/part2.hcl"), abs("other/hard.hcl")); err != nil {
		t.Log("no hard link here:", err)
	} else {
		for _, assign := range orders([]Assignment{{"shop", abs("other/hard.hcl")}, {"shop", abs("model/part2.hcl")}}) {
			one(fmt.Sprint("a hard link, ", assign), lint(OSFS{}, []string{abs("model")}, assign), SeverityError)
		}
		one("a hard link supplied alone", lint(OSFS{}, []string{abs("model")}, []Assignment{{"shop", abs("other/hard.hcl")}}), SeverityWarning)
	}
}

// Same-file comparison reaches a file any depth below a second name of the named directory,
// and compares every named path, whichever comes first.
func TestCheckedModuleLiesUnderANamedPathBelowAndAmongMany(t *testing.T) {
	t.Parallel()
	files := map[string]string{"model/x" + hclExt: okRecord, "model/sub/deep.hcl": oldRecord("Deep"), "other/z" + hclExt: okRecord}
	for name, tc := range map[string]struct {
		paths  []string
		assign []Assignment
		want   map[string]Severity
	}{
		"two levels below the second name":    {[]string{"model"}, []Assignment{{"shop", "alias/sub"}}, map[string]Severity{"alias/sub/deep.hcl": SeverityError}},
		"two named paths, the second matches": {[]string{"other", "model"}, []Assignment{{"shop", "alias"}}, map[string]Severity{"alias/sub/deep.hcl": SeverityError}},
		"two named paths, neither matches":    {[]string{"other", "elsewhere"}, []Assignment{{"shop", "alias"}}, map[string]Severity{"alias/sub/deep.hcl": SeverityWarning}},
	} {
		fsys := newMemFS(files)
		fsys.MapFS["elsewhere/e"+hclExt] = fsys.MapFS["other/z"+hclExt]
		fsys.links["alias"] = "model"
		for _, p := range orders(tc.paths) {
			res, err := Lint(fsys, p, LintOptions{Modules: tc.assign})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]Severity{}
			for _, f := range res.Findings {
				if f.Rule == RuleDeprecated {
					got[f.File] = f.Severity
				}
			}
			sameSeverities(t, fmt.Sprint(name, p), got, tc.want)
		}
	}
}

// When the files cannot be compared the module is treated as being checked, never as only
// referred to (the error): a Stat that fails above the file, or on a named path.
func TestCheckedModuleFailsClosedWhenTheFilesCannotBeCompared(t *testing.T) {
	t.Parallel()
	files := map[string]string{"model/x" + hclExt: okRecord, "model/sub/deep.hcl": oldRecord("Deep")}
	// A directory above the file cannot be read.
	fsys := newMemFS(files)
	fsys.MapFS["main"+hclExt] = &fstest.MapFile{Data: []byte(okRecord)}
	fsys.links["alias"] = "model"
	fsys.statErr["alias"] = errors.New("permission denied")
	assign := []Assignment{{"shop", "alias/sub"}}
	res, err := Lint(fsys, []string{"main" + hclExt}, LintOptions{Modules: assign})
	if err != nil {
		t.Fatal(err)
	}
	var sev Severity
	for _, f := range res.Findings {
		if f.Rule == RuleDeprecated {
			sev = f.Severity
		}
	}
	if sev != SeverityError {
		t.Errorf("a directory above the file cannot be read: %q, want the error", sev)
	}
	// The same, with the directory readable, is the warning: nothing is named above the file.
	delete(fsys.statErr, "alias")
	if res, err = Lint(fsys, []string{"main" + hclExt}, LintOptions{Modules: assign}); err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Findings {
		if f.Rule == RuleDeprecated && f.Severity != SeverityWarning {
			t.Errorf("the directory is readable and not named: %v", f)
		}
	}
	// markReferenceOnly itself: a named path that cannot be read holds every file. "pinned" is
	// under no readable named path, and would be only referred to if the comparison were skipped.
	checkedModel, _ := ParseHCL("main"+hclExt, []byte(okRecord))
	checkedModel.Name = "main"
	pinned, _ := ParseHCL("pinned/a"+hclExt, []byte(oldRecord("A")))
	pinned.Name = "pinned"
	srcs := []Source{{Path: "main" + hclExt, Abs: "main" + hclExt}, {Path: "pinned/a" + hclExt, Abs: "pinned/a" + hclExt}}
	markReferenceOnly(newMemFS(map[string]string{"main" + hclExt: okRecord, "pinned/a" + hclExt: oldRecord("A")}), []*Model{checkedModel, pinned}, srcs, []string{"main" + hclExt}, nil)
	if checkedModel.ReferenceOnly || !pinned.ReferenceOnly {
		t.Fatalf("with every path readable: named %v, pinned %v; want false and true", checkedModel.ReferenceOnly, pinned.ReferenceOnly)
	}
	pinned.ReferenceOnly = false
	markReferenceOnly(newMemFS(map[string]string{"main" + hclExt: okRecord, "pinned/a" + hclExt: oldRecord("A")}), []*Model{checkedModel, pinned}, srcs, []string{"main" + hclExt, "gone"}, nil)
	if checkedModel.ReferenceOnly || pinned.ReferenceOnly {
		t.Error("a named path whose Stat fails: a module is only referred to")
	}
}

// statCounter counts the Stat calls it is asked.
type statCounter struct {
	*memFS
	calls int
}

func (c *statCounter) Stat(name string) (fs.FileInfo, error) {
	c.calls++
	return c.memFS.Stat(name)
}

// With no path named nothing is compared: the same-file comparison does not run.
func TestCheckedModuleDoesNotCompareFilesWhenNoPathIsNamed(t *testing.T) {
	t.Parallel()
	m1, _ := ParseHCL("a"+hclExt, []byte(oldRecord("A")))
	c := &statCounter{memFS: newMemFS(map[string]string{"d/a.hcl": oldRecord("A")})}
	markReferenceOnly(c, []*Model{m1}, []Source{{Path: "d/a.hcl", Abs: "d/a.hcl"}}, nil, nil)
	if c.calls != 0 || m1.ReferenceOnly {
		t.Errorf("%d Stat calls with no path named, ReferenceOnly %v", c.calls, m1.ReferenceOnly)
	}
}
