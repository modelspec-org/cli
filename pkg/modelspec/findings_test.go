package modelspec

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// finalFinding is the finding that says how many were not listed: the last of a
// reader's list, and, sorted into a run's findings, the first of its file (it has
// no line).
func finalFinding(t *testing.T, fs []Finding) Finding {
	t.Helper()
	var found []Finding
	for _, f := range fs {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "are not listed") {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d findings say how many were not listed, want one", len(found))
	}
	return found[0]
}

// summaryOf is finalFinding for findings as strings.
func summaryOf(t *testing.T, fs []string) string {
	t.Helper()
	var found []string
	for _, f := range fs {
		if strings.Contains(f, "are not listed") {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d findings say how many were not listed, want one", len(found))
	}
	return found[0]
}

// A file can hold millions of mistakes; the findings kept are bounded in the
// library, so memory is, and one finding counts the rest. The exit code does not
// depend on the limit.
func TestFindingsAreCapped(t *testing.T) {
	t.Parallel()
	// Checker: 5,000 duplicate values of one enum.
	vals := make([]string, 5001)
	for i := range vals {
		vals[i] = `"v"`
	}
	src := "enum \"E\" {\n  values = [" + strings.Join(vals, ", ") + "]\n}\n"
	got := lintFiles(map[string]string{"e" + hclExt: src}, LintOptions{})
	if len(got) != MaxFindings+1 || !strings.Contains(summaryOf(t, got), "4000 more findings (4000 errors, 0 warnings) are not listed: at most 1000 are kept") {
		t.Fatalf("%d findings, summary %q", len(got), summaryOf(t, got))
	}
	// Reader (HCL): 3,000 top-level attributes, each a shape error.
	var attrs strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&attrs, "x%d = 1\n", i)
	}
	m, fs := ParseHCL("a"+hclExt, []byte(attrs.String()))
	if len(fs) != MaxFindings+1 || finalFinding(t, fs).Severity != SeverityError || !strings.Contains(finalFinding(t, fs).Message, "2000 more findings (2000 errors, 0 warnings)") || m.Broken {
		t.Fatalf("HCL reader: %d findings, last %v", len(fs), fs[len(fs)-1])
	}
	// Reader (JSON): 3,000 repeated keys.
	var keys strings.Builder
	keys.WriteString(`{"modelspec": "1.0-draft", "module": {"id": "x", "version": "1"}`)
	for i := 0; i < 3000; i++ {
		keys.WriteString(`, "components": {}`)
	}
	keys.WriteString("}")
	_, fs = ParseJSON("a"+jsonExt, []byte(keys.String()))
	if len(fs) != MaxFindings+1 || !strings.Contains(finalFinding(t, fs).Message, "more findings") {
		t.Fatalf("JSON reader: %d findings", len(fs))
	}
	// A whole run: three files with 900 findings each. Each file is under the limit, the run is not.
	files := map[string]string{}
	for _, name := range []string{"a", "b", "c"} {
		var b strings.Builder
		for i := 0; i < 900; i++ {
			fmt.Fprintf(&b, "%s%d = 1\n", name, i)
		}
		files[name+hclExt] = b.String()
	}
	res, err := Lint(newMemFS(files), []string{"."}, LintOptions{})
	if err != nil || len(res.Findings) != MaxFindings+1 {
		t.Fatalf("run: %d findings, %v", len(res.Findings), err)
	}
	if f := finalFinding(t, res.Findings); !strings.Contains(f.Message, "1700 more findings (1700 errors, 0 warnings)") {
		t.Fatalf("run: %v", f)
	}
}

// What is dropped decides the severity of the finding that counts it: warnings
// listed and an error dropped is an error, so the exit code is the one the
// unlimited run has; warnings dropped are a warning.
func TestFindingsLimitKeepsTheVerdict(t *testing.T) {
	t.Parallel()
	member := func(name string) string { return "  property \"" + name + "\" {\n    type = \"int\"\n  }\n" }
	var warnings strings.Builder
	for i := 0; i < 1100; i++ { // pairs of names that differ only by case: 1,100 warnings
		fmt.Fprintf(&warnings, "%s%s", member(fmt.Sprintf("n%d", i)), member(fmt.Sprintf("N%d", i)))
	}
	head := "entity \"E\" {\n  key = [\"n0\"]\n" + warnings.String()
	onlyWarnings := head + "}\n"
	res, _ := Lint(newMemFS(map[string]string{"e" + hclExt: onlyWarnings}), []string{"."}, LintOptions{})
	if got := summaryOf(t, findingStrings(res.Findings)); len(res.Findings) != MaxFindings+1 || !strings.Contains(got, "warning: 100 more findings (0 errors, 100 warnings)") {
		t.Fatalf("warnings only: %d findings, summary %q", len(res.Findings), got)
	}
	if HasErrors(res.Findings) {
		t.Error("only warnings were dropped, and the run has an error")
	}
	// The same, with an error after the warnings: the error is listed, and a warning is dropped in its place.
	withError := head + member("dup") + member("dup") + "}\n"
	res, _ = Lint(newMemFS(map[string]string{"e" + hclExt: withError}), []string{"."}, LintOptions{})
	last := finalFinding(t, res.Findings)
	if last.Severity != SeverityWarning || !HasErrors(res.Findings) || !strings.Contains(last.Message, "(0 errors, 101 warnings)") {
		t.Fatalf("the error was not kept in place of a warning: %v", last)
	}
	listed := 0
	for _, f := range res.Findings {
		if f.Severity == SeverityError && f.Rule != RuleLimit {
			listed++
		}
	}
	if listed != 1 || !sort.SliceIsSorted(res.Findings[:MaxFindings], func(i, j int) bool { return res.Findings[i].Line < res.Findings[j].Line }) {
		t.Fatalf("%d errors listed, or the output is not in order", listed)
	}
}

// Errors are kept in preference to warnings, in whichever order they come, and an
// error is dropped only when more than MaxFindings errors came.
func TestErrorsAreKeptInPreferenceToWarnings(t *testing.T) {
	t.Parallel()
	mk := func(sev Severity, n int) Finding {
		return Finding{File: "f", Line: n, Rule: "r", Severity: sev, Message: "m"}
	}
	for name, order := range map[string]func(l *findingList){
		"warnings then the error": func(l *findingList) {
			for i := 1; i <= 1500; i++ {
				l.put(mk(SeverityWarning, i))
			}
			l.put(mk(SeverityError, 2000))
		},
		"the error then warnings": func(l *findingList) {
			l.put(mk(SeverityError, 2000))
			for i := 1; i <= 1500; i++ {
				l.put(mk(SeverityWarning, i))
			}
		},
		"errors between warnings": func(l *findingList) {
			for i := 1; i <= 1500; i++ {
				l.put(mk(SeverityWarning, i))
				if i%500 == 0 {
					l.put(mk(SeverityError, 3000+i))
				}
			}
		},
	} {
		var l findingList
		order(&l)
		got := l.result()
		errs := 0
		for _, f := range got[:MaxFindings] {
			if f.Severity == SeverityError {
				errs++
			}
		}
		wantErrs := 1
		if name == "errors between warnings" {
			wantErrs = 3
		}
		if len(got) != MaxFindings+1 || errs != wantErrs || got[MaxFindings].Severity != SeverityWarning || !strings.Contains(got[MaxFindings].Message, fmt.Sprintf("(0 errors, %d warnings)", 1500+wantErrs-MaxFindings)) {
			t.Errorf("%s: %d findings, %d errors listed, last %v", name, len(got), errs, got[len(got)-1])
		}
		if !sort.SliceIsSorted(got[:MaxFindings], func(i, j int) bool { return got[i].Line < got[j].Line }) {
			t.Errorf("%s: not sorted", name)
		}
	}
	// More errors than the limit: the extra errors are dropped, as errors.
	var l findingList
	for i := 1; i <= 300; i++ {
		l.put(mk(SeverityWarning, i))
	}
	for i := 1; i <= MaxFindings+5; i++ {
		l.put(mk(SeverityError, 5000+i))
	}
	got := l.result()
	if last := got[len(got)-1]; last.Severity != SeverityError || !strings.Contains(last.Message, fmt.Sprintf("%d more findings (5 errors, 300 warnings)", 305)) {
		t.Errorf("last = %v", last)
	}
	for _, f := range got[:MaxFindings] {
		if f.Severity != SeverityError {
			t.Fatalf("a warning is listed beside %d errors: %v", MaxFindings, f)
		}
	}
}

func TestCapFindingsBoundary(t *testing.T) {
	t.Parallel()
	list := func(n int) []Finding {
		out := make([]Finding, n)
		for i := range out {
			out[i] = Finding{File: "f", Line: i + 1, Rule: "r", Severity: SeverityWarning, Message: "m"}
		}
		return out
	}
	// One cut reader or check gives the limit and the finding that counts the rest: left alone.
	for _, n := range []int{0, 1, MaxFindings, MaxFindings + 1} {
		if got := capFindings(list(n)); len(got) != n {
			t.Errorf("%d findings became %d", n, len(got))
		}
	}
	// More is cut to the limit, with one that counts the others.
	got := capFindings(list(MaxFindings + 2))
	if len(got) != MaxFindings+1 || got[MaxFindings].Rule != RuleLimit || !strings.Contains(got[MaxFindings].Message, "2 more findings (0 errors, 2 warnings) are not listed") || got[MaxFindings].Severity != SeverityWarning {
		t.Fatalf("%d findings, last %v", len(got), got[len(got)-1])
	}
	if got[0].Line != 1 || got[MaxFindings-1].Line != MaxFindings {
		t.Errorf("the findings kept are not the first: %v ... %v", got[0], got[MaxFindings-1])
	}
}

// The finding that counts the rest is in the file of the first finding that was
// not kept.
func TestTheCountingFindingNamesTheFirstFileDropped(t *testing.T) {
	t.Parallel()
	dup := func(n int) string { // an enum with n repeats of a value
		return "enum \"E\" {\n  values = [" + strings.Repeat(`"v", `, n) + `"v"]` + "\n}\n"
	}
	// One module of three files: the first fills the limit, the second and the third each add one.
	files := map[string]string{
		layout("m", "a.hcl"): dup(MaxFindings),
		layout("m", "b.hcl"): strings.Replace(dup(1), `"E"`, `"F"`, 1),
		layout("m", "c.hcl"): strings.Replace(dup(1), `"E"`, `"G"`, 1),
	}
	res, err := Lint(newMemFS(files), []string{"."}, LintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := finalFinding(t, res.Findings)
	if f.File != layout("m", "b.hcl") || !strings.Contains(f.Message, "2 more findings") {
		t.Fatalf("summary %v, want it in %s", f, layout("m", "b.hcl"))
	}
}

// What a reader or a check returns is sorted by file and line, whatever order it
// found them in.
func TestFindingsComeSorted(t *testing.T) {
	t.Parallel()
	sorted := func(fs []Finding) bool {
		copied := append([]Finding(nil), fs...)
		SortFindings(copied)
		for i := range fs {
			if fs[i] != copied[i] {
				return false
			}
		}
		return true
	}
	// The checker reports repeated names first, then the rest, which is earlier in the file.
	m, _ := ParseHCL("a"+hclExt, []byte("entity \"A\" {\n}\nentity \"A\" {\n  key = []\n}\n"))
	if got := Check([]*Model{m}, Options{}); len(got) < 2 || !sorted(got) {
		t.Errorf("Check: %v", got)
	}
	// The HCL reader finds a model's blocks before its top-level attributes.
	_, fs := ParseHCL("a"+hclExt, []byte("table \"t\" {\n}\nx = 1\n"))
	if len(fs) != 2 || !sorted(fs) {
		t.Errorf("HCL reader: %v", fs)
	}
	// The JSON reader reports the repeated keys first.
	_, fs = ParseJSON("a"+jsonExt, []byte("{\n\"modelspec\": \"1.0-draft\",\n\"module\": {\"id\": \"x\", \"version\": \"1\"},\n\"zzz\": 1,\n\"enums\": {}, \"enums\": {}}"))
	if len(fs) < 2 || !sorted(fs) {
		t.Errorf("JSON reader: %v", fs)
	}
	var l findingList
	l.put(Finding{File: "b", Line: 2})
	l.put(Finding{File: "a", Line: 9})
	l.put(Finding{File: "a", Line: 1})
	if got := l.result(); !sorted(got) || got[0].File != "a" || got[0].Line != 1 {
		t.Errorf("findingList.result: %v", got)
	}
}

func findingStrings(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.String()
	}
	return out
}

// A skipped-file finding says that a module was not checked, so the cap drops every
// other finding before it: it takes the place of a warning, then of any other error, in
// whichever order the findings come, and is dropped only when more than MaxFindings of them
// came, and then the summary says so.
func TestSkippedFileFindingsAreKeptAheadOfOthers(t *testing.T) {
	t.Parallel()
	mk := func(rule string, sev Severity, n int) Finding {
		return Finding{File: "f", Line: n, Rule: rule, Severity: sev, Message: "m"}
	}
	skipped := func(n int) Finding { return mk(RuleSkipped, SeverityError, n) }
	listed := func(got []Finding) (skips, errs int) {
		for _, f := range got[:MaxFindings] {
			switch {
			case f.Rule == RuleSkipped:
				skips++
			case f.Severity == SeverityError:
				errs++
			}
		}
		return skips, errs
	}
	for name, order := range map[string]func(l *findingList){
		"errors then the skipped file": func(l *findingList) {
			for i := 1; i <= 1201; i++ {
				l.put(mk("r", SeverityError, i))
			}
			l.put(skipped(5000))
		},
		"the skipped file then errors": func(l *findingList) {
			l.put(skipped(5000))
			for i := 1; i <= 1201; i++ {
				l.put(mk("r", SeverityError, i))
			}
		},
		"warnings, errors and the skipped file": func(l *findingList) {
			for i := 1; i <= 600; i++ {
				l.put(mk("r", SeverityWarning, i))
				l.put(mk("r", SeverityError, 2000+i))
			}
			l.put(skipped(5000))
		},
	} {
		var l findingList
		order(&l)
		got := l.result()
		if skips, _ := listed(got); skips != 1 || len(got) != MaxFindings+1 || strings.Contains(got[MaxFindings].Message, "skipped-file") {
			t.Errorf("%s: %d skipped files listed, %d findings, summary %q", name, skips, len(got), got[len(got)-1].Message)
		}
	}
	// More skipped files than the limit: the extra are dropped, and the summary says so.
	var l findingList
	for i := 1; i <= 300; i++ {
		l.put(mk("r", SeverityWarning, i))
	}
	for i := 1; i <= 400; i++ {
		l.put(mk("r", SeverityError, 400+i))
	}
	for i := 1; i <= MaxFindings+7; i++ {
		l.put(skipped(5000 + i))
	}
	got := l.result()
	skips, errs := listed(got)
	last := got[len(got)-1]
	if skips != MaxFindings || errs != 0 || last.Severity != SeverityError || !strings.Contains(last.Message, "707 more findings (407 errors, 300 warnings)") || !strings.Contains(last.Message, "; 7 of them are skipped-file errors") {
		t.Errorf("%d skipped files and %d other errors listed; summary %v", skips, errs, last)
	}
}
