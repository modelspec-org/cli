package covergate

import (
	"regexp"
	"strings"
)

// The functions below inspect workflow files as text. They exist so that a test
// can fail when someone edits CI to run the gate differently, add a threshold,
// or let the release proceed without it.

var forbiddenInWorkflow = regexp.MustCompile(`(?i)threshold|min[-_ ]?cov|coverage[-_]?(min|floor|target)|covergate[^\n]*(-|\$|\{)|continue-on-error|\b\d+(\.\d+)?\s*%`)

// requiredInCI is what the CI workflow must contain verbatim: it runs on pull
// requests and on main, writes a profile covering every package, and runs the
// gate on that profile.
var requiredInCI = []string{
	"\n  pull_request:\n",
	"\n    branches: [main]\n",
	"go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./...",
	"run: go run ./cmd/covergate cover.out\n",
}

var workflowName = regexp.MustCompile(`(?m)^name: (.+)$`)

// CheckCI returns the problems in a CI workflow's text: a missing piece of the
// gate, or anything that could lower it.
func CheckCI(ci string) []string {
	var problems []string
	for _, want := range requiredInCI {
		if !strings.Contains(ci, want) {
			problems = append(problems, "ci workflow lacks "+quote(want))
		}
	}
	return append(problems, forbidden("ci workflow", ci)...)
}

// CheckRelease returns the problems in a release workflow's text: it must name
// the CI workflow in require_workflow_success, so a commit CI did not pass is
// never tagged or published, and it must not carry a threshold override.
func CheckRelease(ci, release string) []string {
	m := workflowName.FindStringSubmatch(ci)
	if m == nil {
		return []string{"ci workflow has no name"}
	}
	var problems []string
	want := "require_workflow_success: '" + strings.TrimSpace(m[1]) + "'"
	if !strings.Contains(release, want) {
		problems = append(problems, "release workflow lacks "+quote(want))
	}
	return append(problems, forbidden("release workflow", release)...)
}

func forbidden(what, text string) []string {
	var problems []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if forbiddenInWorkflow.MatchString(line) {
			problems = append(problems, what+" must not carry a coverage threshold, override or continue-on-error: "+quote(strings.TrimSpace(line)))
		}
	}
	return problems
}

func quote(s string) string {
	return "\"" + strings.ReplaceAll(strings.TrimSpace(s), "\n", `\n`) + "\""
}
