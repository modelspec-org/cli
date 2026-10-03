package covergate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// These tests parse the repository's workflow files and assert on their
// structure, so that the coverage gate cannot be made optional by an edit that a
// text search would miss: a comment, an `if:`, a step that rewrites the profile,
// a guard value set to the empty string. They are test code: the checkers below
// are not part of the binary.

const (
	testCommand = "go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./..."
	gateCommand = "go run ./cmd/covergate cover.out"
	ciName      = "CI"
	cancelExpr  = "${{ github.event_name == 'pull_request' }}"
	// A push to main gets a concurrency group of its own, so that a newer pending
	// run can never replace its CI run; only pull requests share a group per ref.
	ciGroup = "ci-${{ github.workflow }}-${{ github.event_name == 'pull_request' && github.ref || github.sha }}"
)

// allowedWorkflows is every file that may exist under .github/workflows, each
// with the reason it may. A new workflow file fails TestOnlyTheKnownWorkflows
// until it is added here with a stated reason: a workflow that calls the shared
// release workflow, or is named CI, could release without the gate.
var allowedWorkflows = map[string]string{
	"ci.yml":      "runs the exact coverage gate and the packaging checks; the only workflow named CI",
	"release.yml": "releases from a push to main through the shared workflow, which waits for the CI workflow",
}

// releasePin is what the shared release workflow reference must end in: an exact
// version tag, never a branch.
var releasePin = regexp.MustCompile(`@v\d+\.\d+\.\d+$`)

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

// keys lists the keys of a mapping, sorted.
func keys(v any) []string {
	var out []string
	for k := range asObj(v) {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// triggerProblems checks the triggers of a workflow. Both must run on pushes to
// main, with nothing but the branch list on the push trigger (a paths filter
// would let a main commit go without a CI run, and a tags list would run the
// workflow on a tag). The release workflow has no other trigger at all: a
// hand-pushed tag or a manual dispatch on a tag would release a commit that has
// no CI run, which the shared release workflow's guard lets through after 180
// seconds. CI needs pull_request, and may have others.
func triggerProblems(what string, doc obj, isCI bool) []string {
	on := asObj(doc["on"])
	var problems []string
	if got := branches(on["push"]); len(got) != 1 || got[0] != "main" {
		problems = append(problems, fmt.Sprintf("%s must run on push to main only, has branches %v", what, got))
	}
	if got := keys(on["push"]); strings.Join(got, ",") != "branches" {
		problems = append(problems, fmt.Sprintf("the push trigger of %s may carry only a branch list, it has %v (a paths or tags filter lets a commit go without a CI run)", what, got))
	}
	want := "push"
	if isCI {
		want = "pull_request,push"
		if !has(on, "pull_request") {
			problems = append(problems, what+" must run on pull requests")
		}
	}
	if got := strings.Join(keys(on), ","); got != want {
		problems = append(problems, fmt.Sprintf("%s must have exactly the triggers %q (no tags and no workflow_dispatch anywhere: a release must always have a CI run to wait for), it has %q", what, want, got))
	}
	return problems
}

// topLevelProblems refuses what changes the environment of every job of a
// workflow: a workflow-level env or defaults (a default shell can make every run
// step execute nothing and succeed).
func topLevelProblems(what string, doc obj) []string {
	var problems []string
	for _, key := range []string{"env", "defaults"} {
		if has(doc, key) {
			problems = append(problems, fmt.Sprintf("%s has a workflow-level %q, which changes every step", what, key))
		}
	}
	return problems
}

// checkCI returns the problems in the CI workflow's text: the gate job and its
// steps, in structure.
func checkCI(text string) []string {
	doc, err := parse(text)
	if err != nil {
		return []string{"ci.yml does not parse: " + err.Error()}
	}
	var problems []string
	if str(doc["name"]) != ciName {
		problems = append(problems, fmt.Sprintf("the workflow must be named %q, which the release waits for; it is %q", ciName, str(doc["name"])))
	}
	problems = append(problems, triggerProblems("ci.yml", doc, true)...)
	problems = append(problems, topLevelProblems("ci.yml", doc)...)
	if got := str(asObj(doc["concurrency"])["cancel-in-progress"]); got != cancelExpr {
		problems = append(problems, fmt.Sprintf("cancel-in-progress must be %q so that a run on main is never cancelled, it is %q", cancelExpr, got))
	}
	if got := str(asObj(doc["concurrency"])["group"]); got != ciGroup {
		problems = append(problems, fmt.Sprintf("the concurrency group must be %q so that a push to main never shares one (a pending run would be replaced), it is %q", ciGroup, got))
	}
	job := asObj(asObj(doc["jobs"])["test"])
	if job == nil {
		return append(problems, `ci.yml has no "test" job`)
	}
	problems = append(problems, plain(`job "test"`, job, "needs", "uses", "with", "container", "services", "permissions")...)
	if got := str(job["runs-on"]); got != "ubuntu-latest" {
		problems = append(problems, fmt.Sprintf("the test job must run on ubuntu-latest, not %q", got))
	}
	steps := asList(job["steps"])
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

// checkRelease returns the problems in the release workflow's text: it runs on
// pushes to main, calls the shared release workflow, and passes the literal name
// of the CI workflow as the workflow that must have succeeded.
func checkRelease(ci, text string) []string {
	doc, err := parse(text)
	if err != nil {
		return []string{"release.yml does not parse: " + err.Error()}
	}
	cidoc, err := parse(ci)
	if err != nil {
		return []string{"ci.yml does not parse: " + err.Error()}
	}
	problems := triggerProblems("release.yml", doc, false)
	problems = append(problems, topLevelProblems("release.yml", doc)...)
	if name := str(doc["name"]); name == "" || name == str(cidoc["name"]) {
		problems = append(problems, fmt.Sprintf("release.yml must have a name of its own, not %q: the release waits for the workflow named %q, and a second workflow of that name could stand in for it", name, str(cidoc["name"])))
	}
	jobs := asObj(doc["jobs"])
	if len(jobs) != 1 {
		problems = append(problems, fmt.Sprintf("release.yml must have exactly one job (the release, which goes through the guard); it has %v", keys(jobs)))
	}
	job := asObj(jobs["release"])
	if job == nil {
		return append(problems, `release.yml has no "release" job`)
	}
	for _, key := range []string{"if", "continue-on-error"} {
		if has(job, key) {
			problems = append(problems, fmt.Sprintf("the release job has %q; it must run on every push to main", key))
		}
	}
	uses := str(job["uses"])
	if !strings.HasPrefix(uses, "strongo/cicd/.github/workflows/release.yml@") {
		problems = append(problems, fmt.Sprintf("the release job must call the shared release workflow, it uses %q", uses))
	} else if !releasePin.MatchString(uses) {
		problems = append(problems, fmt.Sprintf("the shared release workflow must be pinned to an exact version tag, it uses %q", uses))
	}
	if got := str(asObj(job["with"])["require_workflow_success"]); got != ciName || got != str(cidoc["name"]) {
		problems = append(problems, fmt.Sprintf("require_workflow_success must be the literal %q, the name of the CI workflow; it is %q", ciName, got))
	}
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
		if !contains(names, n) {
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

// Each edit below leaves a text search green and must be a failing case here. The
// first five are the ones the review of the first version found.
func TestEditsThatWeakenTheGateAreCaught(t *testing.T) {
	t.Parallel()
	ci, release := readWorkflow(t, "ci.yml"), readWorkflow(t, "release.yml")
	gateStep := "        run: " + gateCommand
	testStep := "        run: " + testCommand
	tests := []struct {
		name    string
		mutate  func(t *testing.T) (ciText, releaseText string)
		problem string
	}{
		{"1. if: false on the gate step", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, "        if: false\n"+gateStep), release
		}, `step 8 has "if"`},
		{"2. both run lines kept as comments, other commands run", func(t *testing.T) (string, string) {
			c := edit(t, ci, testStep, "        # run: "+testCommand+"\n        run: go test ./pkg/...")
			c = edit(t, c, gateStep, "        # run: "+gateCommand+"\n        run: echo skipped")
			return c, release
		}, "no step runs exactly"},
		{"3. if: false on the whole test job", func(t *testing.T) (string, string) {
			return edit(t, ci, "    name: Test, vet, race, exact coverage\n", "    name: Test, vet, race, exact coverage\n    if: false\n"), release
		}, `job "test" has "if"`},
		{"4. a step before the gate filters the profile", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: Coverage gate (every statement)\n", "      - name: Tidy the profile\n        run: grep -v ' 0$' cover.out > cover2.out && mv cover2.out cover.out\n\n      - name: Coverage gate (every statement)\n"), release
		}, "touches the cover profile"},
		{"5. the guard kept as a comment, the real input empty", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "      require_workflow_success: 'CI'", "      # require_workflow_success: 'CI'\n      require_workflow_success: ''")
		}, "require_workflow_success must be the literal"},
		{"continue-on-error on the gate step", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, "        continue-on-error: true\n"+gateStep), release
		}, `"continue-on-error"`},
		{"continue-on-error on the job", func(t *testing.T) (string, string) {
			return edit(t, ci, "    name: Test, vet, race, exact coverage\n", "    name: Test, vet, race, exact coverage\n    continue-on-error: true\n"), release
		}, `job "test" has "continue-on-error"`},
		{"an env on the test step that narrows the run", func(t *testing.T) (string, string) {
			return edit(t, ci, testStep, "        env:\n          GOFLAGS: -run=NoSuchTest\n"+testStep), release
		}, `has "env"`},
		{"a job-level env", func(t *testing.T) (string, string) {
			return edit(t, ci, "    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v7\n\n      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt", "    runs-on: ubuntu-latest\n    env:\n      GOFLAGS: -run=x\n    steps:\n      - uses: actions/checkout@v7\n\n      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt"), release
		}, `job "test" has "env"`},
		{"a step that exports GOFLAGS for later steps", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: gofmt\n", "      - run: echo GOFLAGS=-run=x >> $GITHUB_ENV\n\n      - name: gofmt\n"), release
		}, "sets the environment or the path of later steps"},
		{"a step between test and gate", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: Coverage gate (every statement)\n", "      - run: echo between\n\n      - name: Coverage gate (every statement)\n"), release
		}, "must directly follow"},
		{"the gate step before the test step", func(t *testing.T) (string, string) {
			c := edit(t, ci, "      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n\n      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n", "      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n\n      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n")
			return c, release
		}, "must directly follow"},
		{"the gate step removed", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: Coverage gate (every statement)\n        run: "+gateCommand+"\n", ""), release
		}, "no step runs exactly: " + gateCommand},
		{"the test step removed", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: go test (race, coverage profile)\n        run: "+testCommand+"\n\n", ""), release
		}, "no step runs exactly: " + testCommand},
		{"the test narrowed to one package", func(t *testing.T) (string, string) {
			return edit(t, ci, testCommand, "go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./pkg/..."), release
		}, "no step runs exactly"},
		{"the gate given an option", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, gateStep+" -min 90"), release
		}, "no step runs exactly"},
		{"the test command twice", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, gateStep+"\n      - run: "+testCommand), release
		}, "the test command appears twice"},
		{"the gate command twice", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, gateStep+"\n      - run: "+gateCommand), release
		}, "the gate command appears twice"},
		{"a working directory on the gate step", func(t *testing.T) (string, string) {
			return edit(t, ci, gateStep, "        working-directory: cmd\n"+gateStep), release
		}, `"working-directory"`},
		{"a shell on the test step", func(t *testing.T) (string, string) {
			return edit(t, ci, testStep, "        shell: bash {0} || true\n"+testStep), release
		}, `"shell"`},
		{"the job renamed", func(t *testing.T) (string, string) {
			return edit(t, ci, "\n  test:\n", "\n  tests:\n"), release
		}, `no "test" job`},
		{"cancel-in-progress true", func(t *testing.T) (string, string) {
			return edit(t, ci, "cancel-in-progress: "+cancelExpr, "cancel-in-progress: true"), release
		}, "cancel-in-progress must be"},
		{"no pull request trigger", func(t *testing.T) (string, string) {
			return edit(t, ci, "  pull_request:\n", ""), release
		}, "must run on pull requests"},
		{"ci runs on other branches too", func(t *testing.T) (string, string) {
			return edit(t, ci, "    branches: [main]", "    branches: [main, dev]"), release
		}, "must run on push to main only"},
		{"ci renamed so the release waits for nothing", func(t *testing.T) (string, string) {
			return edit(t, ci, "name: CI\n", "name: Checks\n"), release
		}, `must be named "CI"`},
		{"the guard input removed", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "      require_workflow_success: 'CI'\n", "")
		}, "require_workflow_success must be the literal"},
		{"the guard names another workflow", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "require_workflow_success: 'CI'", "require_workflow_success: 'Go CI'")
		}, "require_workflow_success must be the literal"},
		{"the guard value built from an expression", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "require_workflow_success: 'CI'", "require_workflow_success: ${{ vars.GUARD }}")
		}, "require_workflow_success must be the literal"},
		{"release made conditional", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "  release:\n    # Pinned", "  release:\n    if: false\n    # Pinned")
		}, `the release job has "if"`},
		{"release continue-on-error", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "  release:\n    # Pinned", "  release:\n    continue-on-error: true\n    # Pinned")
		}, `the release job has "continue-on-error"`},
		{"release calls something else", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "uses: strongo/cicd/.github/workflows/release.yml@v1.21.0", "uses: ./.github/workflows/mine.yml")
		}, "must call the shared release workflow"},
		{"release on every branch", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "    branches:\n      - main\n", "    branches:\n      - main\n      - dev\n")
		}, "must run on push to main only"},
		{"a manual dispatch trigger on the release", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "    branches:\n      - main\n", "    branches:\n      - main\n  workflow_dispatch:\n")
		}, "must have exactly the triggers"},
		{"a tag trigger on the release", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "    branches:\n      - main\n", "    branches:\n      - main\n    tags:\n      - 'v*'\n")
		}, "may carry only a branch list"},
		{"a pull request trigger on the release", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "    branches:\n      - main\n", "    branches:\n      - main\n  pull_request:\n")
		}, "must have exactly the triggers"},
		{"a paths filter on the release push", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "    branches:\n      - main\n", "    branches:\n      - main\n    paths:\n      - 'pkg/**'\n")
		}, "may carry only a branch list"},
		{"a paths filter on the CI push", func(t *testing.T) (string, string) {
			return edit(t, ci, "    branches: [main]\n", "    branches: [main]\n    paths: ['pkg/**']\n"), release
		}, "may carry only a branch list"},
		{"a tags trigger on CI push", func(t *testing.T) (string, string) {
			return edit(t, ci, "    branches: [main]\n", "    branches: [main]\n    tags: ['v*']\n"), release
		}, "may carry only a branch list"},
		{"CI without a push trigger", func(t *testing.T) (string, string) {
			return edit(t, ci, "  push:\n    branches: [main]\n", ""), release
		}, "must run on push to main only"},
		{"workflow-level defaults.run.shell makes every run step a no-op", func(t *testing.T) (string, string) {
			return edit(t, ci, "permissions:\n  contents: read\n", "permissions:\n  contents: read\n\ndefaults:\n  run:\n    shell: \"sh -c 'exit 0' {0}\"\n"), release
		}, `workflow-level "defaults"`},
		{"workflow-level env GOFLAGS", func(t *testing.T) (string, string) {
			return edit(t, ci, "permissions:\n  contents: read\n", "permissions:\n  contents: read\n\nenv:\n  GOFLAGS: -overlay=/tmp/x.json\n"), release
		}, `workflow-level "env"`},
		{"a step that adds a directory with its own go to GITHUB_PATH", func(t *testing.T) (string, string) {
			return edit(t, ci, "      - name: gofmt\n", "      - run: echo /tmp/fake >> $GITHUB_PATH\n\n      - name: gofmt\n"), release
		}, "sets the environment or the path of later steps"},
		{"a second job in release.yml that skips the guard", func(t *testing.T) (string, string) {
			return ci, release + "\n  other:\n    uses: strongo/cicd/.github/workflows/release.yml@v1.21.0\n    permissions:\n      contents: write\n"
		}, "must have exactly one job"},
		{"release.yml named CI", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "name: Release\n", "name: CI\n")
		}, "must have a name of its own"},
		{"release.yml without a name", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "name: Release\n", "")
		}, "must have a name of its own"},
		{"a container on the test job", func(t *testing.T) (string, string) {
			return edit(t, ci, "    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v7\n\n      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt", "    runs-on: ubuntu-latest\n    container: alpine\n    steps:\n      - uses: actions/checkout@v7\n\n      - uses: actions/setup-go@v7\n        with:\n          go-version-file: go.mod\n          cache: true\n\n      - name: gofmt"), release
		}, `job "test" has "container"`},
		{"services on the test job", func(t *testing.T) (string, string) {
			return edit(t, ci, "    name: Test, vet, race, exact coverage\n", "    name: Test, vet, race, exact coverage\n    services:\n      x:\n        image: alpine\n"), release
		}, `job "test" has "services"`},
		{"the test job on a self-hosted runner", func(t *testing.T) (string, string) {
			return edit(t, ci, "  test:\n    name: Test, vet, race, exact coverage\n    runs-on: ubuntu-latest", "  test:\n    name: Test, vet, race, exact coverage\n    runs-on: self-hosted"), release
		}, "must run on ubuntu-latest"},
		{"the shared workflow pinned to a branch", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "release.yml@v1.21.0", "release.yml@main")
		}, "pinned to an exact version tag"},
		{"the shared workflow pinned to a major tag", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "release.yml@v1.21.0", "release.yml@v1")
		}, "pinned to an exact version tag"},
		{"a workflow_dispatch trigger on CI", func(t *testing.T) (string, string) {
			return edit(t, ci, "  pull_request:\n", "  pull_request:\n  workflow_dispatch:\n"), release
		}, "must have exactly the triggers"},
		{"a schedule trigger on CI", func(t *testing.T) (string, string) {
			return edit(t, ci, "  pull_request:\n", "  pull_request:\n  schedule:\n    - cron: '0 0 * * *'\n"), release
		}, "must have exactly the triggers"},
		{"a concurrency group shared by pushes to main", func(t *testing.T) (string, string) {
			return edit(t, ci, "github.event_name == 'pull_request' && github.ref || github.sha", "github.ref"), release
		}, "the concurrency group must be"},
		{"release job missing", func(t *testing.T) (string, string) {
			return ci, edit(t, release, "\n  release:\n", "\n  publish:\n")
		}, `no "release" job`},
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
