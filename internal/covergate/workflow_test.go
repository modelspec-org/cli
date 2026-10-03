package covergate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests parse the repository's workflow files and assert on their
// structure, so that the coverage gate cannot be made optional by an edit that a
// text search would miss: a comment, an `if:`, a step that rewrites the profile,
// a job that no longer waits for the gate. They are test code: the checkers below
// are not part of the binary.
//
// They are a tripwire, not the control. They run in the pull request that changes
// the workflows, so a change that edits them passes its own tests. The control is
// a branch rule on main (see the README, "What stops a release without the gate").

const (
	testCommand = "go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./..."
	gateCommand = "go run ./cmd/covergate cover.out"
	ciName      = "CI"
	testJobName = "Test, vet, race, exact coverage"
	packJobName = "GoReleaser check and snapshot build"
	cancelExpr  = "${{ github.event_name == 'pull_request' }}"
	// A run for main (called by release.yml, so with the caller's push event) gets
	// a concurrency group of its own, so that a newer pending run can never replace
	// it; only pull requests share a group per ref.
	ciGroup      = "ci-${{ github.workflow }}-${{ github.event_name == 'pull_request' && github.ref || github.sha }}"
	releaseGroup = "release-${{ github.ref }}"
	gateUses     = "./.github/workflows/ci.yml"
	releaseIf    = "github.ref == 'refs/heads/main'"
	sharedRef    = "strongo/cicd/.github/workflows/release.yml@5d96b1f3fbb3f12bb1e2762ff5ba54ccb9506504"
	checkoutRef  = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
	setupGoRef   = "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"
	goreleaseRef = "goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94"
)

// pinned is every action a workflow may use: a full commit SHA, with the version
// that commit is, which must stand in a comment after the reference. The shared
// release workflow's v1.21.0 was verified with git ls-remote.
var pinned = map[string]string{
	sharedRef:    "v1.21.0",
	checkoutRef:  "v7.0.1",
	setupGoRef:   "v7.0.0",
	goreleaseRef: "v7.2.3",
}

// fullSHA is what a pinned reference must end in.
var fullSHA = regexp.MustCompile(`@[0-9a-f]{40}$`)

// allowedWorkflows is every file that may exist under .github/workflows, each
// with the reason it may. A new workflow file fails TestOnlyTheKnownWorkflows
// until it is added here with a stated reason: a workflow that calls the shared
// release workflow, or is named CI, could release without the gate.
var allowedWorkflows = map[string]string{
	"ci.yml":      "runs the exact coverage gate and the packaging checks on pull requests, and is called by release.yml; the only workflow named CI",
	"release.yml": "on a push to main calls ci.yml, then, only if it passed, the shared release workflow",
}

// the secrets release.yml forwards to the shared workflow, all optional.
var releaseSecrets = obj{
	"MACOS_SIGN_P12":      "${{ secrets.MACOS_SIGN_P12 }}",
	"MACOS_SIGN_PASSWORD": "${{ secrets.MACOS_SIGN_PASSWORD }}",
	"NOTARIZE_ISSUER_ID":  "${{ secrets.NOTARIZE_ISSUER_ID }}",
	"NOTARIZE_KEY_ID":     "${{ secrets.NOTARIZE_KEY_ID }}",
	"NOTARIZE_KEY":        "${{ secrets.NOTARIZE_KEY }}",
}

// the packaging steps of ci.yml, exactly: they may check and build, never release.
var packagingSteps = []any{
	obj{"uses": checkoutRef, "with": obj{"fetch-depth": 0}},
	obj{"uses": setupGoRef, "with": obj{"go-version-file": "go.mod", "cache": true}},
	obj{"uses": goreleaseRef, "with": obj{"distribution": "goreleaser", "version": "v2.18.2", "args": "check"}},
	obj{"uses": goreleaseRef, "with": obj{"distribution": "goreleaser", "version": "v2.18.2", "args": "build --snapshot --clean"}},
}

type obj = map[string]any

func parse(text string) (obj, error) {
	var doc obj
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("empty document")
	}
	return doc, nil
}

func asObj(v any) obj {
	o, _ := v.(obj)
	return o
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// has reports whether the mapping has the key at all, whatever its value: an
// `if: true` or an `if: ""` is still a condition somebody wrote.
func has(o obj, key string) bool {
	_, ok := o[key]
	return ok
}

// plain reports the problems of a job or step that must run unconditionally,
// must fail the workflow when it fails, and must run in the plain environment.
func plain(what string, o obj, forbidden ...string) []string {
	var problems []string
	for _, key := range append([]string{"if", "continue-on-error", "env", "working-directory", "shell", "defaults", "strategy", "timeout-minutes"}, forbidden...) {
		if has(o, key) {
			problems = append(problems, fmt.Sprintf("%s has %q; it must run unconditionally and in the plain environment", what, key))
		}
	}
	return problems
}

// mentions reports whether any string anywhere in v contains s.
func mentions(v any, s string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, s)
	case obj:
		for k, e := range x {
			if strings.Contains(k, s) || mentions(e, s) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if mentions(e, s) {
				return true
			}
		}
	}
	return false
}

func branches(trigger any) []string {
	var out []string
	for _, b := range asList(asObj(trigger)["branches"]) {
		out = append(out, str(b))
	}
	return out
}

// exactKeys reports a problem unless the mapping has exactly the keys wanted.
func exactKeys(what string, o obj, want ...string) []string {
	sort.Strings(want)
	if got := keys(o); strings.Join(got, ",") != strings.Join(want, ",") {
		return []string{fmt.Sprintf("%s must have exactly the keys %v, it has %v", what, want, got)}
	}
	return nil
}

// same reports a problem unless the value is deeply equal to the one wanted.
func same(what string, got, want any) []string {
	if !reflect.DeepEqual(got, want) {
		return []string{fmt.Sprintf("%s must be %v, it is %v", what, want, got)}
	}
	return nil
}

// keys lists the keys of a mapping, sorted.
func keys(v any) []string {
	var out []string
	for k := range asObj(v) {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// usesOf lists every `uses` value in a workflow, in a job or a step.
func usesOf(v any) []string {
	var out []string
	switch x := v.(type) {
	case obj:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "uses" {
				out = append(out, s)
			} else {
				out = append(out, usesOf(e)...)
			}
		}
	case []any:
		for _, e := range x {
			out = append(out, usesOf(e)...)
		}
	}
	sort.Strings(out)
	return out
}

// pinProblems checks every action a workflow uses: the one local workflow allowed
// (only where allowLocal), or a full commit SHA from the pinned table with its
// version in the comment after the reference.
func pinProblems(what, text string, doc obj, allowLocal bool) []string {
	var problems []string
	seen := map[string]bool{}
	for _, ref := range usesOf(doc["jobs"]) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		version, ok := pinned[ref]
		switch {
		case ref == gateUses && allowLocal:
		case !ok:
			problems = append(problems, fmt.Sprintf("%s uses %q, which is not one of the pinned actions (a full commit SHA from the pinned table)", what, ref))
		case !fullSHA.MatchString(ref):
			problems = append(problems, fmt.Sprintf("%s uses %q, which is not pinned to a full commit SHA", what, ref))
		case strings.Count(text, "uses: "+ref) != strings.Count(text, "uses: "+ref+" # "+version+"\n"):
			problems = append(problems, fmt.Sprintf("%s uses %q without the comment \"# %s\" after it", what, ref, version))
		}
	}
	return problems
}

// forbiddenInCI is what no step of ci.yml may run or mention: it must not be able
// to tag, publish or release, whatever job it is in.
var forbiddenInCI = []string{"git tag", "git push", "gh release", "gh api", "goreleaser release", "goreleaser publish", "secrets.", "GITHUB_TOKEN"}

// ciJobProblems checks every job of ci.yml: no job
// has permissions of its own, calls a workflow, or runs anything that releases.
func ciJobProblems(doc obj) []string {
	var problems []string
	jobs := asObj(doc["jobs"])
	problems = append(problems, exactKeys("ci.yml jobs", jobs, "release-check", "test")...)
	for _, name := range keys(jobs) {
		job := asObj(jobs[name])
		what := fmt.Sprintf("job %q", name)
		for _, key := range []string{"permissions", "uses", "secrets", "needs", "container", "services", "environment"} {
			if has(job, key) {
				problems = append(problems, fmt.Sprintf("%s has %q: no job of ci.yml may have permissions of its own, call a workflow, or wait for another job", what, key))
			}
		}
		if got := str(job["runs-on"]); got != "ubuntu-latest" {
			problems = append(problems, fmt.Sprintf("%s must run on ubuntu-latest, not %q", what, got))
		}
		for _, bad := range forbiddenInCI {
			if mentions(job, bad) {
				problems = append(problems, fmt.Sprintf("%s mentions %q: ci.yml must not be able to tag, publish or release", what, bad))
			}
		}
	}
	return problems
}

// triggerProblems checks the triggers of a workflow, exactly: each has no other.
// A paths filter or a tag list on a push, a dispatch or a schedule would let a
// commit be released, or a tag be made, without the gate.
func triggerProblems(what string, doc obj, want ...string) []string {
	on := asObj(doc["on"])
	problems := exactKeys(what+" triggers", on, want...)
	for _, name := range want {
		if name == "push" {
			if got := branches(on["push"]); len(got) != 1 || got[0] != "main" {
				problems = append(problems, fmt.Sprintf("%s must run on push to main only, has branches %v", what, got))
			}
			problems = append(problems, exactKeys("the push trigger of "+what, asObj(on["push"]), "branches")...)
		} else if on[name] != nil {
			problems = append(problems, fmt.Sprintf("the %s trigger of %s must be bare, with no filter, input or secret: %v", name, what, on[name]))
		}
	}
	return problems
}

// topLevelProblems refuses what changes the environment of every job of a
// workflow: a workflow-level env or defaults (a default shell can make every run
// step execute nothing and succeed), and any permissions but the ones wanted.
func topLevelProblems(what string, doc obj, permissions obj) []string {
	var problems []string
	for _, key := range []string{"env", "defaults"} {
		if has(doc, key) {
			problems = append(problems, fmt.Sprintf("%s has a workflow-level %q, which changes every step", what, key))
		}
	}
	return append(problems, same(what+" permissions", asObj(doc["permissions"]), permissions)...)
}

// checkCI returns the problems in the CI workflow's text: the gate job and its
// steps, in structure, and the other jobs.
func checkCI(text string) []string {
	doc, err := parse(text)
	if err != nil {
		return []string{"ci.yml does not parse: " + err.Error()}
	}
	var problems []string
	if str(doc["name"]) != ciName {
		problems = append(problems, fmt.Sprintf("the workflow must be named %q; it is %q", ciName, str(doc["name"])))
	}
	problems = append(problems, triggerProblems("ci.yml", doc, "pull_request", "workflow_call")...)
	problems = append(problems, topLevelProblems("ci.yml", doc, obj{"contents": "read"})...)
	problems = append(problems, pinProblems("ci.yml", text, doc, false)...)
	if got := str(asObj(doc["concurrency"])["cancel-in-progress"]); got != cancelExpr {
		problems = append(problems, fmt.Sprintf("cancel-in-progress must be %q so that a run for main is never cancelled, it is %q", cancelExpr, got))
	}
	if got := str(asObj(doc["concurrency"])["group"]); got != ciGroup {
		problems = append(problems, fmt.Sprintf("the concurrency group must be %q so that a run for main never shares one (a pending run would be replaced), it is %q", ciGroup, got))
	}
	problems = append(problems, ciJobProblems(doc)...)
	pack := asObj(asObj(doc["jobs"])["release-check"])
	problems = append(problems, same("the name of the packaging job", str(pack["name"]), packJobName)...)
	problems = append(problems, same("the steps of the packaging job", pack["steps"], packagingSteps)...)
	job := asObj(asObj(doc["jobs"])["test"])
	if job == nil {
		return append(problems, `ci.yml has no "test" job`)
	}
	problems = append(problems, same("the name of the test job", str(job["name"]), testJobName)...)
	problems = append(problems, plain(`job "test"`, job, "needs", "uses", "with", "container", "services", "permissions")...)
	steps := asList(job["steps"])
	problems = append(problems, same("the first step of the test job", firstSteps(steps, 2), []any{obj{"uses": checkoutRef}, obj{"uses": setupGoRef, "with": obj{"go-version-file": "go.mod", "cache": true}}})...)
	testAt, gateAt := -1, -1
	for i, raw := range steps {
		step := asObj(raw)
		switch str(step["run"]) {
		case testCommand:
			if testAt >= 0 {
				problems = append(problems, "the test command appears twice")
			}
			testAt = i
		case gateCommand:
			if gateAt >= 0 {
				problems = append(problems, "the gate command appears twice")
			}
			gateAt = i
		default:
			if mentions(step, "cover.out") {
				problems = append(problems, fmt.Sprintf("step %d touches the cover profile, which only the test and gate steps may", i+1))
			}
			if mentions(step, "GITHUB_ENV") || mentions(step, "GITHUB_PATH") || mentions(step, "GOFLAGS") {
				problems = append(problems, fmt.Sprintf("step %d sets the environment or the path of later steps", i+1))
			}
		}
	}
	switch {
	case testAt < 0:
		problems = append(problems, "no step runs exactly: "+testCommand)
	case gateAt < 0:
		problems = append(problems, "no step runs exactly: "+gateCommand)
	case gateAt != testAt+1:
		problems = append(problems, "the gate step must directly follow the test step; nothing may run between them")
	}
	for _, at := range []int{testAt, gateAt} {
		if at >= 0 {
			problems = append(problems, plain(fmt.Sprintf("step %d", at+1), asObj(steps[at]), "uses", "with")...)
		}
	}
	return problems
}

// firstSteps returns the first n steps, or all of them when there are fewer.
func firstSteps(steps []any, n int) []any {
	if len(steps) < n {
		return steps
	}
	return steps[:n]
}

// checkRelease returns the problems in the release workflow's text: a push to
// main, a job that calls the CI workflow, and a release job that needs it and
// calls the shared release workflow, pinned, with nothing else.
func checkRelease(ci, text string) []string {
	doc, err := parse(text)
	if err != nil {
		return []string{"release.yml does not parse: " + err.Error()}
	}
	cidoc, err := parse(ci)
	if err != nil {
		return []string{"ci.yml does not parse: " + err.Error()}
	}
	problems := triggerProblems("release.yml", doc, "push")
	problems = append(problems, topLevelProblems("release.yml", doc, obj{})...)
	problems = append(problems, pinProblems("release.yml", text, doc, true)...)
	if name := str(doc["name"]); name == "" || name == str(cidoc["name"]) {
		problems = append(problems, fmt.Sprintf("release.yml must have a name of its own, not %q: a second workflow named like the CI workflow could stand in for it", name))
	}
	conc := asObj(doc["concurrency"])
	problems = append(problems, same("the concurrency group of release.yml", str(conc["group"]), releaseGroup)...)
	problems = append(problems, same("cancel-in-progress of release.yml (a release is never cancelled)", conc["cancel-in-progress"], false)...)
	jobs := asObj(doc["jobs"])
	problems = append(problems, exactKeys("release.yml jobs", jobs, "gate", "release")...)
	gate, release := asObj(jobs["gate"]), asObj(jobs["release"])
	problems = append(problems, exactKeys(`job "gate"`, gate, "uses", "permissions")...)
	problems = append(problems, same(`the "gate" job calls`, str(gate["uses"]), gateUses)...)
	problems = append(problems, same(`the "gate" job permissions`, asObj(gate["permissions"]), obj{"contents": "read"})...)
	problems = append(problems, exactKeys(`job "release"`, release, "needs", "if", "uses", "permissions", "with", "secrets")...)
	problems = append(problems, same(`the "release" job needs`, release["needs"], "gate")...)
	problems = append(problems, same(`the "release" job condition`, str(release["if"]), releaseIf)...)
	problems = append(problems, same(`the "release" job calls`, str(release["uses"]), sharedRef)...)
	problems = append(problems, same(`the "release" job permissions`, asObj(release["permissions"]), obj{"contents": "write"})...)
	problems = append(problems, same(`the "release" job inputs`, asObj(release["with"]), obj{"go_version": "1.27.0", "allow_major_version_bump": false, "artifact_smoke_test_homebrew_cask": false})...)
	problems = append(problems, same(`the "release" job secrets`, asObj(release["secrets"]), releaseSecrets)...)
	return problems
}

// checkWorkflowFiles refuses any file under .github/workflows that is not in the
// allow-list, and any allow-list entry without a stated reason.
func checkWorkflowFiles(names []string, allowed map[string]string) []string {
	var problems []string
	for _, n := range names {
		reason, ok := allowed[n]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf(".github/workflows/%s is not in the allow-list: a new workflow must be added to allowedWorkflows with the reason it may exist (one that calls the shared release workflow or is named CI could release without the gate)", n))
		case strings.TrimSpace(reason) == "":
			problems = append(problems, fmt.Sprintf("the allow-list entry for %s has no reason", n))
		}
	}
	for n := range allowed {
		if !slices.Contains(names, n) {
			problems = append(problems, fmt.Sprintf("the allow-list names %s, which does not exist", n))
		}
	}
	sort.Strings(problems)
	return problems
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRepositoryWorkflows(t *testing.T) {
	t.Parallel()
	ci, release := readWorkflow(t, "ci.yml"), readWorkflow(t, "release.yml")
	if p := checkCI(ci); len(p) != 0 {
		t.Errorf("ci.yml: %v", p)
	}
	if p := checkRelease(ci, release); len(p) != 0 {
		t.Errorf("release.yml: %v", p)
	}
}

func TestOnlyTheKnownWorkflows(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(filepath.Join("..", "..", ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if p := checkWorkflowFiles(names, allowedWorkflows); len(p) != 0 {
		t.Errorf("%v", p)
	}
	for _, tc := range []struct {
		name    string
		names   []string
		allowed map[string]string
		want    string
	}{
		{"a third workflow", []string{"ci.yml", "release.yml", "extra.yml"}, allowedWorkflows, "extra.yml is not in the allow-list"},
		{"an allowed file without a reason", []string{"ci.yml", "release.yml"}, map[string]string{"ci.yml": "ok", "release.yml": " "}, "no reason"},
		{"an allowed file that is gone", []string{"ci.yml"}, allowedWorkflows, "release.yml, which does not exist"},
	} {
		if p := checkWorkflowFiles(tc.names, tc.allowed); len(p) != 1 || !strings.Contains(p[0], tc.want) {
			t.Errorf("%s: %v", tc.name, p)
		}
	}
}

// edit replaces old with new in text and fails the test if old is not there, so
// that a mutation can never silently turn into a no-op.
func edit(t *testing.T, text, old, new string) string {
	t.Helper()
	if strings.Count(text, old) != 1 {
		t.Fatalf("expected exactly one %q in the workflow", old)
	}
	return strings.Replace(text, old, new, 1)
}

// Each edit below leaves a text search green and must be a failing case here.
func TestEditsThatWeakenTheGateAreCaught(t *testing.T) {
	t.Parallel()
	ci, release := readWorkflow(t, "ci.yml"), readWorkflow(t, "release.yml")
	gateStep := "        run: " + gateCommand
	testStep := "        run: " + testCommand
	const testJob = "    name: " + testJobName + "\n"
	const packJob = "    name: " + packJobName + "\n"
	const triggers = "on:\n  workflow_call:\n  pull_request:\n"
	const releaseJob = "  release:\n    needs: gate\n"
	const sharedLine = "    uses: " + sharedRef + " # v1.21.0\n"
	const pushTrigger = "    branches:\n      - main\n"
	checkoutLine := "      - uses: " + checkoutRef + " # v7.0.1\n"
	type mutation func(t *testing.T) (ciText, releaseText string)
	inCI := func(old, new string) mutation {
		return func(t *testing.T) (string, string) { return edit(t, ci, old, new), release }
	}
	inRelease := func(old, new string) mutation {
		return func(t *testing.T) (string, string) { return ci, edit(t, release, old, new) }
	}
	tests := []struct {
		name    string
		mutate  mutation
		problem string
	}{
		// The gate job and its steps.
		{"if: false on the gate step", inCI(gateStep, "        if: false\n"+gateStep), `step 8 has "if"`},
		{"both run lines kept as comments, other commands run", func(t *testing.T) (string, string) {
			c := edit(t, ci, testStep, "        # run: "+testCommand+"\n        run: go test ./pkg/...")
			c = edit(t, c, gateStep, "        # run: "+gateCommand+"\n        run: echo skipped")
			return c, release
		}, "no step runs exactly"},
		{"if: false on the whole test job", inCI(testJob, testJob+"    if: false\n"), `job "test" has "if"`},
		{"a step before the gate filters the profile", inCI("      - name: Coverage gate (every statement)\n", "      - name: Tidy the profile\n        run: grep -v ' 0$' cover.out > cover2.out && mv cover2.out cover.out\n\n      - name: Coverage gate (every statement)\n"), "touches the cover profile"},
		{"continue-on-error on the gate step", inCI(gateStep, "        continue-on-error: true\n"+gateStep), `"continue-on-error"`},
		{"continue-on-error on the job", inCI(testJob, testJob+"    continue-on-error: true\n"), `job "test" has "continue-on-error"`},
		{"an env on the test step that narrows the run", inCI(testStep, "        env:\n          GOFLAGS: -run=NoSuchTest\n"+testStep), `has "env"`},
		{"a job-level env", inCI(testJob+"    runs-on: ubuntu-latest\n", testJob+"    runs-on: ubuntu-latest\n    env:\n      GOFLAGS: -run=x\n"), `job "test" has "env"`},
		{"a step that exports GOFLAGS for later steps", inCI("      - name: gofmt\n", "      - run: echo GOFLAGS=-run=x >> $GITHUB_ENV\n\n      - name: gofmt\n"), "sets the environment or the path of later steps"},
		{"a step that adds a directory with its own go to GITHUB_PATH", inCI("      - name: gofmt\n", "      - run: echo /tmp/fake >> $GITHUB_PATH\n\n      - name: gofmt\n"), "sets the environment or the path of later steps"},
		{"a step between test and gate", inCI("      - name: Coverage gate (every statement)\n", "      - run: echo between\n\n      - name: Coverage gate (every statement)\n"), "must directly follow"},
		{"the gate step before the test step", inCI("      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n\n      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n", "      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n\n      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n"), "must directly follow"},
		{"the gate step removed", inCI("      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n", ""), "no step runs exactly: " + gateCommand},
		{"the test step removed", inCI("      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n\n", ""), "no step runs exactly: " + testCommand},
		{"the test narrowed to one package", inCI(testCommand, "go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./pkg/..."), "no step runs exactly"},
		{"the gate given an option", inCI(gateStep, gateStep+" -min 90"), "no step runs exactly"},
		{"the test command twice", inCI(gateStep, gateStep+"\n      - run: "+testCommand), "the test command appears twice"},
		{"the gate command twice", inCI(gateStep, gateStep+"\n      - run: "+gateCommand), "the gate command appears twice"},
		{"a working directory on the gate step", inCI(gateStep, "        working-directory: cmd\n"+gateStep), `"working-directory"`},
		{"a shell on the test step", inCI(testStep, "        shell: bash {0} || true\n"+testStep), `"shell"`},
		{"the test job renamed", inCI("\n  test:\n", "\n  tests:\n"), "ci.yml jobs must have exactly the keys"},
		{"the test job's name changed, so the required check is another", inCI(testJob, "    name: Tests\n"), "the name of the test job must be"},
		{"a container on the test job", inCI(testJob+"    runs-on: ubuntu-latest\n", testJob+"    runs-on: ubuntu-latest\n    container: alpine\n"), `job "test" has "container"`},
		{"services on the test job", inCI(testJob, testJob+"    services:\n      x:\n        image: alpine\n"), `job "test" has "services"`},
		{"the test job on a self-hosted runner", inCI(testJob+"    runs-on: ubuntu-latest", testJob+"    runs-on: self-hosted"), "must run on ubuntu-latest"},
		{"another ref on the test job's checkout", inCI(checkoutLine+"\n      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt", checkoutLine+"        with:\n          ref: v0.0.1\n\n      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt"), "the first step of the test job must be"},
		{"another setup action on the test job", inCI("      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt", "      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version: '1.20'\n\n      - name: gofmt"), "the first step of the test job must be"},

		// The triggers of ci.yml.
		{"no pull request trigger", inCI(triggers, "on:\n  workflow_call:\n"), "ci.yml triggers must have exactly the keys"},
		{"ci runs on a push as well, so main would run the gate twice and a hand push would run it alone", inCI(triggers, "on:\n  workflow_call:\n  pull_request:\n  push:\n    branches: [main]\n"), "ci.yml triggers must have exactly the keys"},
		{"ci cannot be called", inCI(triggers, "on:\n  pull_request:\n"), "ci.yml triggers must have exactly the keys"},
		{"a manual dispatch on ci", inCI(triggers, triggers+"  workflow_dispatch:\n"), "ci.yml triggers must have exactly the keys"},
		{"a schedule on ci", inCI(triggers, triggers+"  schedule:\n    - cron: '0 0 * * *'\n"), "ci.yml triggers must have exactly the keys"},
		{"a paths filter on the pull request trigger", inCI(triggers, "on:\n  workflow_call:\n  pull_request:\n    paths: ['pkg/**']\n"), "the pull_request trigger of ci.yml must be bare"},
		{"an input on workflow_call", inCI(triggers, "on:\n  workflow_call:\n    inputs:\n      skip:\n        type: boolean\n  pull_request:\n"), "the workflow_call trigger of ci.yml must be bare"},
		{"ci renamed", inCI("name: CI\n", "name: Checks\n"), `the workflow must be named "CI"`},
		{"cancel-in-progress true", inCI("cancel-in-progress: "+cancelExpr, "cancel-in-progress: true"), "cancel-in-progress must be"},
		{"a concurrency group shared by runs for main", inCI("github.event_name == 'pull_request' && github.ref || github.sha", "github.ref"), "the concurrency group must be"},
		{"workflow-level defaults.run.shell makes every run step a no-op", inCI("permissions:\n  contents: read\n", "permissions:\n  contents: read\n\ndefaults:\n  run:\n    shell: \"sh -c 'exit 0' {0}\"\n"), `workflow-level "defaults"`},
		{"workflow-level env GOFLAGS", inCI("permissions:\n  contents: read\n", "permissions:\n  contents: read\n\nenv:\n  GOFLAGS: -overlay=/tmp/x.json\n"), `workflow-level "env"`},
		{"write permissions at the top of ci", inCI("permissions:\n  contents: read\n", "permissions:\n  contents: write\n"), "ci.yml permissions must be"},
		{"no permissions at the top of ci", inCI("permissions:\n  contents: read\n\n", ""), "ci.yml permissions must be"},

		// The other jobs of ci.yml may not release.
		{"the packaging job with contents: write", inCI(packJob, packJob+"    permissions:\n      contents: write\n"), `has "permissions"`},
		{"the packaging job tags the commit", inCI("      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - uses: "+goreleaseRef+" # v7.2.3\n        with:\n          distribution: goreleaser\n          version: v2.18.2\n          args: check\n", "      - uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - run: git tag v9.9.9 && git push origin v9.9.9\n\n      - uses: "+goreleaseRef+" # v7.2.3\n        with:\n          distribution: goreleaser\n          version: v2.18.2\n          args: check\n"), `mentions "git tag"`},
		{"the packaging job releases", inCI("          args: build --snapshot --clean", "          args: release --clean"), "the steps of the packaging job must be"},
		{"the packaging job releases through gh", inCI("          args: build --snapshot --clean", "          args: build --snapshot --clean\n      - run: gh release create v9 --generate-notes"), `mentions "gh release"`},
		{"a third job that calls the shared release workflow", func(t *testing.T) (string, string) {
			return ci + "\n  publish:\n    uses: " + sharedRef + " # v1.21.0\n    permissions:\n      contents: write\n", release
		}, "ci.yml jobs must have exactly the keys"},
		{"a third job that runs gh release", func(t *testing.T) (string, string) {
			return ci + "\n  publish:\n    runs-on: ubuntu-latest\n    steps:\n      - run: gh release create v9\n", release
		}, "ci.yml jobs must have exactly the keys"},
		{"the packaging job waits for another", inCI(packJob, packJob+"    needs: test\n"), `has "needs"`},
		{"the packaging job on a self-hosted runner", inCI(packJob+"    runs-on: ubuntu-latest", packJob+"    runs-on: self-hosted"), "must run on ubuntu-latest"},
		{"the packaging job's checkout of another ref", inCI("          fetch-depth: 0\n", "          fetch-depth: 0\n          ref: v0.0.1\n"), "the steps of the packaging job must be"},
		{"the packaging job's name changed, so the required check is another", inCI(packJob, "    name: Packaging\n"), "the name of the packaging job must be"},
		{"the packaging job uses a secret", inCI("          args: check\n", "          args: check\n        env:\n          TOKEN: ${{ secrets.GITHUB_TOKEN }}\n"), `mentions "secrets."`},

		// The pins.
		{"an action pinned to a tag", inCI(checkoutRef+" # v7.0.1\n        with:\n          fetch-depth: 0", "actions/checkout@v7 # v7.0.1\n        with:\n          fetch-depth: 0"), "which is not one of the pinned actions"},
		{"an action pinned to another commit", inCI("uses: "+setupGoRef+" # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt", "uses: actions/setup-go@0000000000000000000000000000000000000000 # v7.0.0\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt"), "which is not one of the pinned actions"},
		{"a pinned action with the wrong version in the comment", inCI(checkoutRef+" # v7.0.1\n        with:\n          fetch-depth: 0", checkoutRef+" # v7.0.0\n        with:\n          fetch-depth: 0"), `without the comment "# v7.0.1"`},
		{"a pinned action with no version comment", inCI(checkoutRef+" # v7.0.1\n        with:\n          fetch-depth: 0", checkoutRef+"\n        with:\n          fetch-depth: 0"), `without the comment "# v7.0.1"`},
		{"an action that is not in the table", inCI("      - name: gofmt\n", "      - uses: some/action@3d3c42e5aac5ba805825da76410c181273ba90b1\n\n      - name: gofmt\n"), "which is not one of the pinned actions"},
		{"the shared workflow pinned to its tag", inRelease(sharedRef+" # v1.21.0", "strongo/cicd/.github/workflows/release.yml@v1.21.0 # v1.21.0"), "which is not one of the pinned actions"},
		{"the shared workflow pinned to a branch", inRelease(sharedRef+" # v1.21.0", "strongo/cicd/.github/workflows/release.yml@main"), "which is not one of the pinned actions"},
		{"the shared workflow pinned to another commit", inRelease(sharedRef+" # v1.21.0", "strongo/cicd/.github/workflows/release.yml@0000000000000000000000000000000000000000 # v1.21.0"), "which is not one of the pinned actions"},
		{"the shared workflow without its version comment", inRelease(sharedRef+" # v1.21.0", sharedRef), `without the comment "# v1.21.0"`},

		// release.yml: the shape that makes the gate and the release one run.
		{"the release job does not need the gate", inRelease(releaseJob, "  release:\n"), `job "release" must have exactly the keys`},
		{"the release job needs another job", inRelease(releaseJob, "  release:\n    needs: other\n"), `the "release" job needs must be gate`},
		{"the release job needs the gate and a list", inRelease(releaseJob, "  release:\n    needs: [gate, gate]\n"), `the "release" job needs must be gate`},
		{"the release job is conditional on something else", inRelease("    if: "+releaseIf+"\n", "    if: always()\n"), `the "release" job condition must be`},
		{"the release job runs whatever the gate did", inRelease("    if: "+releaseIf+"\n", "    if: ${{ always() && github.ref == 'refs/heads/main' }}\n"), `the "release" job condition must be`},
		{"the release job without a condition", inRelease("    if: "+releaseIf+"\n", ""), `job "release" must have exactly the keys`},
		{"the gate job removed", inRelease("  gate:\n    uses: "+gateUses+"\n    permissions:\n      contents: read\n\n", ""), "release.yml jobs must have exactly the keys"},
		{"the gate job calls something else", inRelease("uses: "+gateUses, "uses: ./.github/workflows/mine.yml"), `the "gate" job calls must be`},
		{"the gate job made conditional", inRelease("  gate:\n", "  gate:\n    if: false\n"), `job "gate" must have exactly the keys`},
		{"the gate job allowed to fail", inRelease("  gate:\n", "  gate:\n    continue-on-error: true\n"), `job "gate" must have exactly the keys`},
		{"the gate job with write permissions", inRelease("  gate:\n    uses: "+gateUses+"\n    permissions:\n      contents: read\n", "  gate:\n    uses: "+gateUses+"\n    permissions:\n      contents: write\n"), `the "gate" job permissions must be`},
		{"the release job allowed to fail", inRelease(releaseJob, releaseJob+"    continue-on-error: true\n"), `job "release" must have exactly the keys`},
		{"the shared workflow told to wait for CI as well", inRelease("      allow_major_version_bump: false\n", "      allow_major_version_bump: false\n      require_workflow_success: 'CI'\n"), `the "release" job inputs must be`},
		{"another input on the release", inRelease("      allow_major_version_bump: false\n", "      allow_major_version_bump: true\n"), `the "release" job inputs must be`},
		{"the release job with actions: read", inRelease("      contents: write\n", "      contents: write\n      actions: read\n"), `the "release" job permissions must be`},
		{"a secret dropped", inRelease("      NOTARIZE_KEY: ${{ secrets.NOTARIZE_KEY }}\n", ""), `the "release" job secrets must be`},
		{"a secret that is another secret", inRelease("NOTARIZE_KEY: ${{ secrets.NOTARIZE_KEY }}", "NOTARIZE_KEY: ${{ secrets.OTHER }}"), `the "release" job secrets must be`},
		{"secrets: inherit", inRelease("    secrets:\n      MACOS_SIGN_P12: ${{ secrets.MACOS_SIGN_P12 }}\n      MACOS_SIGN_PASSWORD: ${{ secrets.MACOS_SIGN_PASSWORD }}\n      NOTARIZE_ISSUER_ID: ${{ secrets.NOTARIZE_ISSUER_ID }}\n      NOTARIZE_KEY_ID: ${{ secrets.NOTARIZE_KEY_ID }}\n      NOTARIZE_KEY: ${{ secrets.NOTARIZE_KEY }}\n", "    secrets: inherit\n"), `the "release" job secrets must be`},
		{"the release job calls something else", inRelease(sharedLine, "    uses: ./.github/workflows/mine.yml\n"), "which is not one of the pinned actions"},
		{"a third job in release.yml that skips the gate", func(t *testing.T) (string, string) {
			return ci, release + "\n  other:\n    uses: " + sharedRef + " # v1.21.0\n    permissions:\n      contents: write\n"
		}, "release.yml jobs must have exactly the keys"},
		{"release job renamed", inRelease("\n  release:\n", "\n  publish:\n"), "release.yml jobs must have exactly the keys"},
		{"release on every branch", inRelease(pushTrigger, pushTrigger+"      - dev\n"), "must run on push to main only"},
		{"a manual dispatch trigger on the release", inRelease(pushTrigger, pushTrigger+"  workflow_dispatch:\n"), "release.yml triggers must have exactly the keys"},
		{"a tag trigger on the release", inRelease(pushTrigger, pushTrigger+"    tags:\n      - 'v*'\n"), "the push trigger of release.yml must have exactly the keys"},
		{"a pull request trigger on the release", inRelease(pushTrigger, pushTrigger+"  pull_request:\n"), "release.yml triggers must have exactly the keys"},
		{"a paths filter on the release push", inRelease(pushTrigger, pushTrigger+"    paths:\n      - 'pkg/**'\n"), "the push trigger of release.yml must have exactly the keys"},
		{"release.yml without its push trigger", inRelease("on:\n  push:\n    branches:\n      - main\n", "on:\n  workflow_call:\n"), "release.yml triggers must have exactly the keys"},
		{"release.yml named CI", inRelease("name: Release\n", "name: CI\n"), "must have a name of its own"},
		{"release.yml without a name", inRelease("name: Release\n", ""), "must have a name of its own"},
		{"a release that can be cancelled", inRelease("  cancel-in-progress: false\n", "  cancel-in-progress: true\n"), "cancel-in-progress of release.yml"},
		{"a release with no concurrency control", inRelease("  cancel-in-progress: false\n", ""), "cancel-in-progress of release.yml"},
		{"a release concurrency group of another name", inRelease("group: release-${{ github.ref }}", "group: other"), "the concurrency group of release.yml"},
		{"workflow-level permissions on the release", inRelease("permissions: {}\n", "permissions:\n  contents: write\n"), "release.yml permissions must be"},
		{"workflow-level env on the release", inRelease("permissions: {}\n", "permissions: {}\n\nenv:\n  GOFLAGS: -x\n"), `workflow-level "env"`},
		{"workflow-level defaults on the release", inRelease("permissions: {}\n", "permissions: {}\n\ndefaults:\n  run:\n    shell: 'true {0}'\n"), `workflow-level "defaults"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, r := tc.mutate(t)
			if c == ci && r == release {
				t.Fatal("the mutation changed nothing")
			}
			problems := append(checkCI(c), checkRelease(c, r)...)
			if len(problems) == 0 {
				t.Fatalf("the weakened workflow passes the checks")
			}
			if !strings.Contains(strings.Join(problems, "\n"), tc.problem) {
				t.Fatalf("problems = %v, want one containing %q", problems, tc.problem)
			}
		})
	}
}

// The pinned table itself: full commit SHAs only, each with a version.
func TestPinnedActionsAreFullSHAs(t *testing.T) {
	t.Parallel()
	for ref, version := range pinned {
		if !fullSHA.MatchString(ref) || !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(version) {
			t.Errorf("pinned %q %q is not a full SHA and an exact version", ref, version)
		}
	}
	// The workflows use the whole table, and nothing outside it.
	var used []string
	for _, name := range []string{"ci.yml", "release.yml"} {
		doc, err := parse(readWorkflow(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range usesOf(doc["jobs"]) {
			if ref != gateUses && !slices.Contains(used, ref) {
				used = append(used, ref)
			}
		}
	}
	if len(used) != len(pinned) {
		t.Errorf("the workflows use %v; the table has %d entries", used, len(pinned))
	}
}

func TestCheckersRejectBrokenInput(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"jobs: [", "", "# only a comment\n"} {
		if p := checkCI(text); len(p) != 1 || !strings.Contains(p[0], "ci.yml") {
			t.Errorf("checkCI(%q) = %v", text, p)
		}
		if p := checkRelease(readWorkflow(t, "ci.yml"), text); len(p) != 1 || !strings.Contains(p[0], "release.yml does not parse") {
			t.Errorf("checkRelease(_, %q) = %v", text, p)
		}
	}
	if p := checkRelease("jobs: [", readWorkflow(t, "release.yml")); len(p) != 1 || !strings.Contains(p[0], "ci.yml does not parse") {
		t.Errorf("checkRelease with a broken ci = %v", p)
	}
	if mentions(42, "x") || mentions(nil, "x") || !mentions(obj{"x": []any{1, "has cover.out"}}, "cover.out") || !mentions(obj{"cover.out": 1}, "cover.out") {
		t.Error("mentions wrong")
	}
	if len(branches(nil)) != 0 || str(3) != "" || asObj(3) != nil || asList(3) != nil {
		t.Error("accessors wrong")
	}
}
