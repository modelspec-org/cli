package modelspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The corpus under testdata/corpus holds accepting and refusing models in both
// forms, and testdata/golden holds what `specscore graph lint` and the
// Directory's JSON reader said about them (scripts/regen-golden.mjs wrote those
// files; this test only reads them). Nothing here starts a process.

type manifestItem struct {
	Lint     string            `json:"lint"`
	Rules    []string          `json:"rules"`
	Warnings []string          `json:"warnings"`
	Differs  map[string]string `json:"differs"`
}

type manifest struct {
	Items map[string]manifestItem `json:"items"`
}

type golden struct {
	SpecscoreVersion string `json:"specscore_version"`
	DirectoryCommit  string `json:"directory_commit"`
	Verdicts         map[string]struct {
		Verdict string `json:"verdict"`
	} `json:"verdicts"`
}

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

func corpusFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, form := range []string{"hcl", "json"} {
		entries, err := os.ReadDir(filepath.Join("..", "..", "testdata", "corpus", form))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			files = append(files, form+"/"+e.Name())
		}
	}
	sort.Strings(files)
	return files
}

func TestCorpusMatchesManifest(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join("..", "..", "testdata", "corpus", "manifest.json"), &m)
	files := corpusFiles(t)
	if len(files) != len(m.Items) {
		t.Errorf("corpus has %d files, manifest %d items", len(files), len(m.Items))
	}
	for _, rel := range files {
		item, ok := m.Items[rel]
		if !ok {
			t.Errorf("%s is not in the manifest", rel)
			continue
		}
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("..", "..", "testdata", "corpus", filepath.FromSlash(rel))
			findings, n, err := Lint(OSFS{}, []string{path})
			if err != nil || n != 1 {
				t.Fatalf("Lint: %d files, %v", n, err)
			}
			errRules, warnRules := ruleSets(findings)
			verdict := "accept"
			if HasErrors(findings) {
				verdict = "refuse"
			}
			if verdict != item.Lint {
				t.Errorf("verdict %s, manifest says %s:\n%s", verdict, item.Lint, joinFindings(findings))
			}
			if strings.Join(errRules, ",") != strings.Join(item.Rules, ",") {
				t.Errorf("error rules %v, manifest says %v:\n%s", errRules, item.Rules, joinFindings(findings))
			}
			if strings.Join(warnRules, ",") != strings.Join(item.Warnings, ",") {
				t.Errorf("warning rules %v, manifest says %v", warnRules, item.Warnings)
			}
		})
	}
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

// TestParityWithTheOtherReaders asserts that `modelspec lint` refuses everything
// the recorded readers refused, except where the manifest names the difference
// and gives its reason, and that every recorded difference is real.
func TestParityWithTheOtherReaders(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join("..", "..", "testdata", "corpus", "manifest.json"), &m)
	for _, tool := range []struct {
		golden, form, key string
	}{{"specscore.json", "hcl/", "specscore"}, {"directory.json", "json/", "directory"}} {
		var g golden
		readJSON(t, filepath.Join("..", "..", "testdata", "golden", tool.golden), &g)
		if g.SpecscoreVersion == "" && g.DirectoryCommit == "" {
			t.Errorf("%s records neither a tool version nor a commit", tool.golden)
		}
		compared := 0
		for rel, item := range m.Items {
			if !strings.HasPrefix(rel, tool.form) {
				continue
			}
			compared++
			v, ok := g.Verdicts[rel]
			if !ok {
				t.Errorf("%s has no recorded verdict for %s", tool.golden, rel)
				continue
			}
			reason, differs := item.Differs[tool.key]
			if (v.Verdict != item.Lint) != differs {
				t.Errorf("%s: %s recorded %q, lint says %q, differs entry present: %v", rel, tool.key, v.Verdict, item.Lint, differs)
			}
			if differs && reason == "" {
				t.Errorf("%s: a difference needs a reason", rel)
			}
			if v.Verdict == "refuse" && item.Lint == "accept" && !strings.Contains(strings.ToLower(reason), "lint") {
				t.Errorf("%s: %s refuses and lint accepts; the reason must say why lint accepts", rel, tool.key)
			}
		}
		if compared != len(g.Verdicts) {
			t.Errorf("%s records %d verdicts, the manifest has %d items of that form", tool.golden, len(g.Verdicts), compared)
		}
	}
}

func TestChinookAndTodoLintCleanAndExportCheck(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "corpus")
	for _, c := range []struct{ hcl, json string }{{"chinook", "chinook"}, {"todo-aligned", "todo"}} {
		hclPath := filepath.Join(dir, "hcl", c.hcl+HCLSuffix)
		jsonPath := filepath.Join(dir, "json", c.json+JSONSuffix)
		findings, n, err := Lint(OSFS{}, []string{hclPath, jsonPath})
		if err != nil || n != 2 || len(findings) != 0 {
			t.Errorf("%s: lint = %v, %d files, %v", c.hcl, findings, n, err)
		}
		src, _ := os.ReadFile(hclPath)
		committed, _ := os.ReadFile(jsonPath)
		m, _ := ParseHCL(hclPath, src)
		if d := m.ExportDrift(committed, ModuleIdentity{}); d != "" {
			t.Errorf("%s: export --check: %s", c.hcl, d)
		}
	}
}

// The todo example in the ModelSpec repository declares its recordset
// task_summary in HCL and taskSummary in JSON, and no document defines a
// renaming. The export does not invent one, so the check reports exactly that.
func TestTodoExampleDriftIsTheRecordsetName(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "corpus")
	src, _ := os.ReadFile(filepath.Join(dir, "hcl", "todo.modelspec.hcl"))
	committed, _ := os.ReadFile(filepath.Join(dir, "json", "todo.modelspec.json"))
	m, _ := ParseHCL("todo.modelspec.hcl", src)
	d := m.ExportDrift(committed, ModuleIdentity{})
	if !strings.Contains(d, `recordsets has key "task_summary" in the first but not in the second`) {
		t.Fatalf("drift = %q", d)
	}
}
