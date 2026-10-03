package modelspec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The corpus under testdata/corpus holds accepting and refusing models in both
// forms, multi-file modules in the SpecScore layout, and standalone module sets.
// testdata/golden holds what `specscore graph lint` and the Directory's JSON
// reader said about them (scripts/regen-golden.mjs wrote those files; this test
// only reads them). Nothing here starts a process.

type verdict struct {
	Verdict  string   `json:"verdict"`
	Rules    []string `json:"rules"`
	Warnings []string `json:"warnings"`
}

type manifestItem struct {
	// Path, for an item that is a part of another item (one file of a tree, as a
	// hook or an editor would pass it), is its path under the corpus directory.
	Path    string            `json:"path"`
	Default verdict           `json:"default"`
	Publish verdict           `json:"publish"`
	Modules []string          `json:"modules"`
	Differs map[string]string `json:"differs"`
}

type manifest struct {
	Items map[string]manifestItem `json:"items"`
}

type golden struct {
	SpecscoreVersion string `json:"specscore_version"`
	DirectoryBranch  string `json:"directory_branch"`
	DirectoryCommit  string `json:"directory_commit"`
	Verdicts         map[string]struct {
		Verdict string `json:"verdict"`
	} `json:"verdicts"`
}

var corpusDir = filepath.Join("..", "..", "testdata", "corpus")

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// corpusItems lists the corpus: every file under hcl/ and json/, every directory
// under modules/ and standalone/. Items that are parts of others (they have a
// path in the manifest) are not in the directories.
func corpusItems(t *testing.T) []string {
	t.Helper()
	var items []string
	for _, kind := range []string{"hcl", "json", "modules", "standalone"} {
		entries, err := os.ReadDir(filepath.Join(corpusDir, kind))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			items = append(items, kind+"/"+e.Name())
		}
	}
	sort.Strings(items)
	return items
}

func summarise(findings []Finding) verdict {
	errs, warns := ruleSets(findings)
	v := verdict{Verdict: "accept", Rules: errs, Warnings: warns}
	if HasErrors(findings) {
		v.Verdict = "refuse"
	}
	return v
}

func ruleSets(fs []Finding) (errs, warns []string) {
	seenE, seenW := map[string]bool{}, map[string]bool{}
	for _, f := range fs {
		if f.Severity == SeverityError && !seenE[f.Rule] {
			seenE[f.Rule] = true
			errs = append(errs, f.Rule)
		}
		if f.Severity == SeverityWarning && !seenW[f.Rule] {
			seenW[f.Rule] = true
			warns = append(warns, f.Rule)
		}
	}
	sort.Strings(errs)
	sort.Strings(warns)
	return errs, warns
}

func joinFindings(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.String() + "\n")
	}
	return b.String()
}

func lintItem(t *testing.T, item string, entry manifestItem, profile Profile) []Finding {
	t.Helper()
	path := filepath.Join(corpusDir, filepath.FromSlash(item))
	if entry.Path != "" {
		path = filepath.Join(corpusDir, filepath.FromSlash(entry.Path))
	}
	var assign []Assignment
	for _, m := range entry.Modules {
		name, rel, _ := strings.Cut(m, "=")
		assign = append(assign, Assignment{Module: name, Path: filepath.Join(path, rel)})
	}
	res, err := Lint(OSFS{}, []string{path}, LintOptions{Profile: profile, Modules: assign})
	if err != nil {
		t.Fatalf("%s: %v", item, err)
	}
	return res.Findings
}

func same(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestCorpusMatchesManifest(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	items := corpusItems(t)
	parts := 0
	for item, entry := range m.Items {
		if entry.Path != "" {
			parts++
			items = append(items, item)
			if !strings.HasPrefix(item, "parts/") {
				t.Errorf("%s has a path but is not under parts/", item)
			}
			if _, err := os.Stat(filepath.Join(corpusDir, filepath.FromSlash(entry.Path))); err != nil {
				t.Errorf("%s: %v", item, err)
			}
		}
	}
	if parts < 5 {
		t.Errorf("only %d parts in the corpus", parts)
	}
	if len(items) != len(m.Items) {
		t.Errorf("corpus has %d items, manifest %d", len(items), len(m.Items))
	}
	for _, item := range items {
		entry, ok := m.Items[item]
		if !ok {
			t.Errorf("%s is not in the manifest", item)
			continue
		}
		t.Run(item, func(t *testing.T) {
			t.Parallel()
			for _, c := range []struct {
				profile Profile
				want    verdict
			}{{ProfileDefault, entry.Default}, {ProfilePublish, entry.Publish}} {
				findings := lintItem(t, item, entry, c.profile)
				got := summarise(findings)
				if got.Verdict != c.want.Verdict || !same(got.Rules, c.want.Rules) || !same(got.Warnings, c.want.Warnings) {
					t.Errorf("%s profile: got %+v, manifest says %+v:\n%s", c.profile, got, c.want, joinFindings(findings))
				}
			}
		})
	}
}

// TestParityWithTheOtherReaders asserts that `modelspec lint` refuses everything
// the recorded readers refused, except where the manifest names the difference
// and gives its reason, and that every recorded difference is real. The default
// profile is compared with `specscore graph lint`, and the publish profile with
// the Directory's JSON reader, which publish is meant to match.
func TestParityWithTheOtherReaders(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	for _, tool := range []struct {
		file     string
		prefixes []string
		profile  Profile
		key      string
	}{
		{"specscore.json", []string{"hcl/", "modules/"}, ProfileDefault, "specscore"},
		{"directory.json", []string{"json/"}, ProfilePublish, "directory"},
	} {
		var g golden
		readJSON(t, filepath.Join("..", "..", "testdata", "golden", tool.file), &g)
		switch tool.key {
		case "specscore":
			if g.SpecscoreVersion == "" {
				t.Errorf("%s records no specscore version", tool.file)
			}
		default:
			if g.DirectoryCommit == "" || g.DirectoryBranch != "main" {
				t.Errorf("%s must record a commit of the reader's main branch, has branch %q commit %q", tool.file, g.DirectoryBranch, g.DirectoryCommit)
			}
		}
		compared := 0
		for item, entry := range m.Items {
			if !hasAnyPrefix(item, tool.prefixes) {
				continue
			}
			compared++
			v, ok := g.Verdicts[item]
			if !ok {
				t.Errorf("%s has no recorded verdict for %s", tool.file, item)
				continue
			}
			ours := entry.Default.Verdict
			if tool.profile == ProfilePublish {
				ours = entry.Publish.Verdict
			}
			reason, differs := entry.Differs[tool.key]
			if (v.Verdict != ours) != differs {
				t.Errorf("%s: %s recorded %q, lint (%s profile) says %q, differs entry present: %v", item, tool.key, v.Verdict, tool.profile, ours, differs)
			}
			if differs && reason == "" {
				t.Errorf("%s: a difference needs a reason", item)
			}
			if v.Verdict == "refuse" && ours == "accept" && !strings.HasPrefix(reason, "lint accepts") {
				t.Errorf("%s: %s refuses and lint accepts; the reason must start with \"lint accepts\" and say why", item, tool.key)
			}
			if v.Verdict == "accept" && ours == "refuse" && strings.HasPrefix(reason, "lint accepts") {
				t.Errorf("%s: lint refuses and %s accepts; the reason must not say lint accepts", item, tool.key)
			}
		}
		if compared != len(g.Verdicts) {
			t.Errorf("%s records %d verdicts, the manifest has %d items of those kinds", tool.file, len(g.Verdicts), compared)
		}
	}
	for item, entry := range m.Items {
		for key := range entry.Differs {
			if key != "specscore" && key != "directory" {
				t.Errorf("%s: unknown reader %q in differs", item, key)
			}
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// The publish profile only adds to the default profile: whatever the default
// profile refuses, publish refuses.
func TestPublishRefusesWhateverTheDefaultRefuses(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	for item, entry := range m.Items {
		if entry.Default.Verdict == "refuse" && entry.Publish.Verdict != "refuse" {
			t.Errorf("%s: refused by default, accepted by publish", item)
		}
	}
}

// JSON that `export` writes from HCL that lints clean must itself lint clean. The
// exception is HCL with constructs the standard gives no JSON form (index,
// projection and migration blocks), which export refuses.
func TestExportOfCleanHCLLintsClean(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	var refused, exported []string
	for item, entry := range m.Items {
		if !strings.HasPrefix(item, "hcl/") || entry.Default.Verdict != "accept" {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(item, "hcl/"), HCLSuffix)
		path := filepath.Join(corpusDir, filepath.FromSlash(item))
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		model, findings := ParseHCL(path, src)
		if HasErrors(findings) {
			t.Fatalf("%s: %v", item, findings)
		}
		id := ModuleIdentity{ID: "github.com/acme/" + stem, Name: stem, Version: "0.1.0"}
		node, err := model.JSON(id)
		if len(model.Unmapped) > 0 {
			if err == nil {
				t.Errorf("%s: export of a file with unmapped constructs succeeded", item)
			}
			refused = append(refused, stem)
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", item, err)
			continue
		}
		exported = append(exported, stem)
		back, parse := ParseJSON(stem+JSONSuffix, node.Encode())
		if len(parse) != 0 {
			t.Errorf("%s: the export does not parse clean:\n%s", item, joinFindings(parse))
			continue
		}
		_, wantWarnings := ruleSets(Check([]*Model{model}, Options{}))
		if gotErrs, gotWarnings := ruleSets(Check([]*Model{back}, Options{})); len(gotErrs) != 0 || !same(gotWarnings, wantWarnings) {
			t.Errorf("%s: the export has errors %v and warnings %v; the HCL has warnings %v", item, gotErrs, gotWarnings, wantWarnings)
		}
		// Under another name, or none, the export must still lint clean on its own,
		// or be refused because the model refers to itself by its name.
		for _, other := range []string{"Other", ""} {
			node, err := model.JSON(ModuleIdentity{ID: id.ID, Name: other, Version: id.Version})
			if other != "" && model.refersTo(model.Name) {
				if err == nil {
					t.Errorf("%s: exported under the name %q although it refers to itself as %q", item, other, model.Name)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s under the name %q: %v", item, other, err)
				continue
			}
			// Whatever the file is called: a model that refers to itself by name has
			// module.name written even when none was given.
			named, parse := ParseJSON("other"+JSONSuffix, node.Encode())
			gotErrs, gotWarnings := ruleSets(Check([]*Model{named}, Options{}))
			if len(parse) != 0 || len(gotErrs) != 0 || !same(gotWarnings, wantWarnings) {
				t.Errorf("%s under the name %q: the export has errors %v and warnings %v, parse findings %s; the HCL has warnings %v", item, other, gotErrs, gotWarnings, joinFindings(parse), wantWarnings)
			}
		}
	}
	sort.Strings(refused)
	if got := strings.Join(refused, " "); got != "unmapped-index unmapped-migration unmapped-projection" {
		t.Errorf("exports refused for: %s", got)
	}
	if len(exported) < 12 {
		t.Errorf("only %d accepting HCL items were exported: %v", len(exported), exported)
	}
}

func TestChinookAndTodoLintCleanAndExportCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ hcl, json string }{{"chinook", "chinook"}, {"todo-aligned", "todo"}} {
		hclPath := filepath.Join(corpusDir, "hcl", c.hcl+HCLSuffix)
		jsonPath := filepath.Join(corpusDir, "json", c.json+JSONSuffix)
		for _, profile := range Profiles {
			res, err := Lint(OSFS{}, []string{hclPath, jsonPath}, LintOptions{Profile: profile})
			if err != nil || res.Files != 2 || len(res.Findings) != 0 {
				t.Errorf("%s (%s profile): lint = %v, %d files, %v", c.hcl, profile, res.Findings, res.Files, err)
			}
		}
		src, _ := os.ReadFile(hclPath)
		committed, _ := os.ReadFile(jsonPath)
		m, _ := ParseHCL(hclPath, src)
		if d := m.ExportDrift(committed, ModuleIdentity{}); d != "" {
			t.Errorf("%s: export --check: %s", c.hcl, d)
		}
		if c.hcl == "chinook" {
			// Chinook's committed JSON was written by a JavaScript converter with
			// JSON.stringify(x, null, 2); the export is byte-identical to it.
			node, err := m.JSON(ModuleIdentity{ID: "github.com/datatug/chinookdb/model/chinook", Name: "chinook", Version: "0.1.0"})
			if err != nil || string(node.Encode()) != string(committed) {
				t.Errorf("chinook export is not byte-identical to the committed JSON (%v)", err)
			}
		}
	}
}

// The todo example in the ModelSpec repository declares its recordset
// task_summary in HCL and taskSummary in JSON, and no document defines a
// renaming. The export does not invent one, so the check reports exactly that.
func TestTodoExampleDriftIsTheRecordsetName(t *testing.T) {
	t.Parallel()
	src, _ := os.ReadFile(filepath.Join(corpusDir, "hcl", "todo.modelspec.hcl"))
	committed, _ := os.ReadFile(filepath.Join(corpusDir, "json", "todo.modelspec.json"))
	m, _ := ParseHCL("todo.modelspec.hcl", src)
	d := m.ExportDrift(committed, ModuleIdentity{})
	if !strings.Contains(d, `recordsets has key "task_summary" in the first but not in the second`) {
		t.Fatalf("drift = %q", d)
	}
}

// statedCorpusSize returns the number the README states for the corpus (in bold,
// once), or a problem.
func statedCorpusSize(readme string) (string, string) {
	found := regexp.MustCompile(`\*\*(\d+) manifest items\*\*`).FindAllStringSubmatch(readme, -1)
	if len(found) != 1 {
		return "", fmt.Sprintf("the README states the corpus size %d times, want once", len(found))
	}
	return found[0][1], ""
}

// The README states the size of the corpus once, and it must be the manifest's.
func TestREADMEStatesTheCorpusSize(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, problem := statedCorpusSize(string(readme))
	if problem != "" {
		t.Fatal(problem)
	}
	if want := strconv.Itoa(len(m.Items)); got != want {
		t.Errorf("the README says %s manifest items, the manifest has %s", got, want)
	}
	// The check itself: a number that differs, none, or two are found.
	for text, wantNumber := range map[string]string{"(**7 manifest items**)": "7", "(7 manifest items)": "", "**7 manifest items** and **8 manifest items**": ""} {
		if n, p := statedCorpusSize(text); n != wantNumber || (n == "") != (p != "") {
			t.Errorf("statedCorpusSize(%q) = %q, %q", text, n, p)
		}
	}
}
