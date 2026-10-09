package modelspec

import (
	"bytes"
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
// forms, multi-file modules in the SpecScore layout, and standalone module sets,
// in the old spelling (entity, property) and, under new/, the same items in the
// new one (record, field). testdata/golden holds what `specscore graph lint` and
// the Directory's JSON reader said about the old ones (scripts/regen-golden.mjs
// wrote those files; this test only reads them). Nothing here starts a process.

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
// under modules/ and standalone/, and the same under new/. Items that are parts
// of others (they have a path in the manifest) are not in the directories.
func corpusItems(t *testing.T) []string {
	t.Helper()
	var items []string
	for _, kind := range []string{"hcl", "json", "modules", "standalone", "new/hcl", "new/json", "new/modules", "new/standalone"} {
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
			if !strings.HasPrefix(item, "parts/") && !strings.HasPrefix(item, "new/parts/") {
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

// JSON that the library writes from HCL that lints clean must itself lint clean, and is
// in the vocabulary of the HCL: format 1.0-draft-2 for a file with no old spelling,
// 1.0-draft for one with any. The command export refuses a file with the old spelling
// (an error), so the old items are the library's: an old item whose only error is the
// old spelling must export to a document whose only error is the old spelling.
func TestExportOfCleanHCLLintsClean(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	var exported []string
	for item, entry := range m.Items {
		dir := "hcl/"
		if strings.HasPrefix(item, "new/") {
			dir = "new/hcl/"
		}
		// Clean, or clean but for the old spelling, which is then its only error.
		wantErrs := entry.Default.Rules
		if !strings.HasPrefix(item, dir) || (len(wantErrs) > 0 && !same(wantErrs, []string{RuleDeprecated})) {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(item, dir), HCLSuffix)
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
		if err != nil {
			t.Errorf("%s: %v", item, err)
			continue
		}
		exported = append(exported, item)
		wantVersion := SpecVersion
		if model.OldVocabulary() {
			wantVersion = OldSpecVersion
		}
		if v, _ := node.Get("modelspec"); v.Str != wantVersion {
			t.Errorf("%s: exported as %q, want %q", item, v.Str, wantVersion)
		}
		back, parse := ParseJSON(stem+JSONSuffix, node.Encode())
		if len(parse) != 0 {
			t.Errorf("%s: the export does not parse clean:\n%s", item, joinFindings(parse))
			continue
		}
		_, wantWarnings := ruleSets(Check([]*Model{model}, Options{}))
		if gotErrs, gotWarnings := ruleSets(Check([]*Model{back}, Options{})); !same(gotErrs, wantErrs) || !same(gotWarnings, wantWarnings) {
			t.Errorf("%s: the export has errors %v and warnings %v; the HCL has errors %v and warnings %v", item, gotErrs, gotWarnings, wantErrs, wantWarnings)
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
			if len(parse) != 0 || !same(gotErrs, wantErrs) || !same(gotWarnings, wantWarnings) {
				t.Errorf("%s under the name %q: the export has errors %v and warnings %v, parse findings %s; the HCL has errors %v and warnings %v", item, other, gotErrs, gotWarnings, joinFindings(parse), wantErrs, wantWarnings)
			}
		}
	}
	// Nothing that lints clean lacks a JSON form: every HCL item that is clean but for
	// the old spelling is exported, in the old vocabulary or the new.
	if len(exported) < 25 {
		t.Errorf("only %d HCL items were exported: %v", len(exported), exported)
	}
}

// pairs are the HCL files and their committed JSON copies that must lint together
// with nothing but the old spelling to say (an error in each of the old pair, nothing
// in the pairs in the new spelling) and export to each other, byte for byte where the
// JSON was written by a JavaScript converter. The library still writes the old
// vocabulary for a model in the old spelling (stale-twin compares in it), which is
// why the old pair is compared with what it exports to.
func TestChinookAndTodoLintCleanAndExportCheck(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		dir, hcl, json string
		findings       []string // the findings of the pair, which are all deprecated-spelling
		severity       Severity // of each
	}{
		{"", "chinook", "chinook", []string{RuleDeprecated, RuleDeprecated}, SeverityError},
		{"new/", "chinook", "chinook", nil, ""},
		{"new/", "todo-aligned", "todo", nil, ""},
	} {
		hclPath := filepath.Join(corpusDir, filepath.FromSlash(c.dir), "hcl", c.hcl+HCLSuffix)
		jsonPath := filepath.Join(corpusDir, filepath.FromSlash(c.dir), "json", c.json+JSONSuffix)
		for _, profile := range Profiles {
			res, err := Lint(OSFS{}, []string{hclPath, jsonPath}, LintOptions{Profile: profile})
			var rules []string
			for _, f := range res.Findings {
				rules = append(rules, f.Rule)
				if f.Severity != c.severity {
					t.Errorf("%s%s (%s profile): %s", c.dir, c.hcl, profile, f)
				}
			}
			if err != nil || res.Files != 2 || !same(rules, c.findings) {
				t.Errorf("%s%s (%s profile): lint = %v, %d files, %v", c.dir, c.hcl, profile, res.Findings, res.Files, err)
			}
		}
		src, _ := os.ReadFile(hclPath)
		committed, _ := os.ReadFile(jsonPath)
		m, _ := ParseHCL(hclPath, src)
		if d := m.ExportDrift(committed, ModuleIdentity{}); d != "" {
			t.Errorf("%s%s: export --check: %s", c.dir, c.hcl, d)
		}
		if c.hcl == "chinook" {
			// Chinook's committed JSON was written by a JavaScript converter with
			// JSON.stringify(x, null, 2); the export is byte-identical to it.
			node, err := m.JSON(ModuleIdentity{ID: "github.com/datatug/chinookdb/model/chinook", Name: "chinook", Version: "0.1.0"})
			if err != nil || string(node.Encode()) != string(committed) {
				t.Errorf("%schinook export is not byte-identical to the committed JSON (%v)", c.dir, err)
			}
		}
	}
}

// oldSpellingVerdict is what the old spelling does to the verdict of an item: the
// one place a test knows how severe it is (decision 0022, step 4,
// OldSpellingSeverity). The old item's expected verdict is this applied to the
// verdict of its copy in the new spelling. In a file that a run names, which is every
// file the search of the item finds, the old spelling is an error: checked says the
// item has one, and the item is refused with deprecated-spelling among its errors. In
// a file that only the item's modules supply (--module) it is a warning: referred says
// the item has one, and deprecated-spelling is among its warnings.
func oldSpellingVerdict(newSpelling verdict, checked, referred bool) verdict {
	v := verdict{Verdict: newSpelling.Verdict, Rules: newSpelling.Rules, Warnings: newSpelling.Warnings}
	if checked {
		v.Verdict = "refuse"
		v.Rules = append(append([]string(nil), v.Rules...), RuleDeprecated)
		sort.Strings(v.Rules)
	}
	if referred {
		v.Warnings = append(append([]string(nil), v.Warnings...), RuleDeprecated)
		sort.Strings(v.Warnings)
	}
	return v
}

// The items whose copy in new/ was not made by `modelspec rewrite`, because rewrite
// refuses a file of the item: it does not parse, repeats a key in a JSON object (there
// is no one model to rewrite), or holds a construct that no rewriting fixes (decision
// 0019). Their copies were written by hand: the same text
// with the old words replaced, and where the removed or reserved construct was only
// a part of the item's purpose (a collection beside records), without it.
var handWrittenCopies = []string{
	"hcl/collection-bind-unresolved.modelspec.hcl", "hcl/expression-value.modelspec.hcl", "hcl/names-not-identifiers.modelspec.hcl",
	"hcl/reserved-recordset-name.modelspec.hcl", "hcl/shop.modelspec.hcl", "hcl/syntax-error.modelspec.hcl", "hcl/todo-aligned.modelspec.hcl",
	"hcl/todo.modelspec.hcl", "hcl/unmapped-index.modelspec.hcl", "hcl/unmapped-migration.modelspec.hcl", "hcl/unmapped-projection.modelspec.hcl",
	"json/full-shape.modelspec.json", "json/invalid-utf8.modelspec.json", "json/modelspec-not-string.modelspec.json", "json/no-modelspec.modelspec.json",
	"json/not-an-object.modelspec.json", "json/not-json.modelspec.json", "json/shape-wrong-types.modelspec.json", "json/todo.modelspec.json",
	"json/trailing-garbage.modelspec.json", "json/wrong-version.modelspec.json", "json/duplicate-entity-key.modelspec.json",
	"json/duplicate-key-in-property.modelspec.json", "modules/layout-key-through-component",
	"standalone/backslash-before-dollar-in-label", "standalone/backslash-before-dollar-in-pattern", "standalone/name-over-255-bytes",
	"standalone/number-exponent-over-100", "standalone/number-over-40-characters", "standalone/query-201-wildcards",
	"standalone/query-334-placeholders", "standalone/query-one-line-500-placeholders",
}

// The items under new/ that have no copy in the old spelling: they test the readers
// with both spellings in one place.
var newOnly = []string{
	"new/hcl/mixed-spellings.modelspec.hcl", "new/hcl/record-and-entity-both.modelspec.hcl", "new/hcl/property-in-component.modelspec.hcl",
	"new/hcl/reserved-kind-names.modelspec.hcl", "new/json/entities-in-new-format.modelspec.json", "new/json/records-in-old-format.modelspec.json",
	"new/json/reserved-kind-names.modelspec.json", "new/standalone/twin-new-hcl-old-json", "new/standalone/twin-old-hcl-new-json",
}

// itemFiles lists the files of an item as paths relative to the item (a file item
// is itself, ".").
func itemFiles(t *testing.T, root string) []string {
	t.Helper()
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		return []string{"."}
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			out = append(out, rel)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every item in the old spelling has a copy under new/, and where `modelspec
// rewrite` can rewrite every model file of the item, the copy is what it makes of
// the item, byte for byte, and the item's verdict is the copy's with the old
// spelling's effect. The model files are found the way rewrite finds them; the
// other files (a README, specscore.yaml) are copied as they are.
func TestEveryItemHasACopyInTheNewSpelling(t *testing.T) {
	t.Parallel()
	var m manifest
	readJSON(t, filepath.Join(corpusDir, "manifest.json"), &m)
	byHand := map[string]bool{}
	for _, item := range handWrittenCopies {
		byHand[item] = true
	}
	seenByHand, rewritten, old := 0, 0, 0
	twins := map[string]bool{}
	for _, extra := range newOnly {
		twins[extra] = true
	}
	for item, entry := range m.Items {
		if strings.HasPrefix(item, "new/") {
			continue
		}
		old++
		twin := "new/" + item
		twins[twin] = true
		twinEntry, ok := m.Items[twin]
		if !ok {
			t.Errorf("%s has no copy %s in the manifest", item, twin)
			continue
		}
		if entry.Path != "" {
			if twinEntry.Path != "new/"+entry.Path {
				t.Errorf("%s: path %q, want new/%s", twin, twinEntry.Path, entry.Path)
			}
			continue // a part has the verdict of the whole, checked with the whole
		}
		src := filepath.Join(corpusDir, filepath.FromSlash(item))
		dst := filepath.Join(corpusDir, filepath.FromSlash(twin))
		var modules []Assignment
		for _, a := range entry.Modules {
			name, rel, _ := strings.Cut(a, "=")
			modules = append(modules, Assignment{Module: name, Path: filepath.Join(src, rel)})
		}
		found, _, err := DiscoverModules(OSFS{}, []string{src}, modules)
		if err != nil {
			t.Fatalf("%s: %v", item, err)
		}
		// The files the item's path finds are named to lint; the others, which only its
		// modules supply, are only referred to.
		searched, _, err := Discover(OSFS{}, []string{src})
		if err != nil {
			t.Fatalf("%s: %v", item, err)
		}
		named := map[string]bool{}
		for _, f := range searched {
			named[f.Abs] = true
		}
		files := map[string][]byte{} // the rewritten model files, by path relative to the item
		checked, referred, refused := 0, 0, ""
		for _, f := range found {
			data, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(src, f.Path)
			out, n, err := Rewrite(f.Path, data)
			if err != nil {
				refused = err.Error()
				break
			}
			files[rel] = out
			if named[f.Abs] {
				checked += n
			} else {
				referred += n
			}
		}
		if byHand[item] != (refused != "") {
			t.Errorf("%s: rewrite refused = %q, but the list of hand-written copies says %v", item, refused, byHand[item])
		}
		if refused != "" {
			seenByHand++
			continue
		}
		rewritten++
		for _, rel := range itemFiles(t, src) {
			want, err := os.ReadFile(filepath.Join(src, rel))
			if err != nil {
				t.Fatal(err)
			}
			if out, ok := files[rel]; ok {
				want = out
			}
			got, err := os.ReadFile(filepath.Join(dst, rel))
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s: %s is not what rewrite makes of the old item (%v)", twin, rel, err)
			}
		}
		if len(itemFiles(t, src)) != len(itemFiles(t, dst)) {
			t.Errorf("%s has %d files, %s has %d", item, len(itemFiles(t, src)), twin, len(itemFiles(t, dst)))
		}
		// The verdict: the copy's, and the old spelling's effect where there was any.
		for _, c := range []struct {
			name      string
			old, copy verdict
		}{{"default", entry.Default, twinEntry.Default}, {"publish", entry.Publish, twinEntry.Publish}} {
			want := oldSpellingVerdict(c.copy, checked > 0, referred > 0)
			if c.old.Verdict != want.Verdict || !same(c.old.Rules, want.Rules) || !same(c.old.Warnings, want.Warnings) {
				t.Errorf("%s, %s profile: manifest says %+v, the copy %+v with %d old spellings in named files and %d in files only referred to gives %+v", item, c.name, c.old, c.copy, checked, referred, want)
			}
		}
	}
	if seenByHand != len(handWrittenCopies) {
		t.Errorf("%d items were refused by rewrite, %d are listed", seenByHand, len(handWrittenCopies))
	}
	if rewritten < 80 {
		t.Errorf("only %d items were rewritten", rewritten)
	}
	// Nothing under new/ is left without an old item or a place in newOnly.
	for item := range m.Items {
		if strings.HasPrefix(item, "new/") && !twins[item] {
			t.Errorf("%s is a copy of no item, and is not in newOnly", item)
		}
	}
	if got := len(m.Items); got != 2*old+len(newOnly) {
		t.Errorf("%d items, want twice the %d old ones and the %d only in the new spelling", got, old, len(newOnly))
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
