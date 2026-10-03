package covergate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodCI = `name: CI
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    steps:
      - run: go test -race -covermode=atomic -coverpkg=./... -coverprofile=cover.out ./...
      - run: go run ./cmd/covergate cover.out
`

func TestCheckCI(t *testing.T) {
	t.Parallel()
	if p := CheckCI(goodCI); len(p) != 0 {
		t.Fatalf("good workflow reported problems: %v", p)
	}
	tests := []struct {
		name string
		ci   string
		want string
	}{
		{"gate step removed", strings.Replace(goodCI, "go run ./cmd/covergate cover.out", "echo skip", 1), "lacks"},
		{"no pull request trigger", strings.Replace(goodCI, "  pull_request:\n", "", 1), "pull_request"},
		{"threshold env", goodCI + "      - run: go run ./cmd/covergate cover.out\n        env:\n          COVERAGE_THRESHOLD: 90\n", "threshold"},
		{"percentage", goodCI + "      - run: awk '$1 < 95 %' cover.out\n", "threshold"},
		{"flag on the gate", strings.Replace(goodCI, "covergate cover.out", "covergate -min 90 cover.out", 1), "lacks"},
		{"continue on error", goodCI + "        continue-on-error: true\n", "continue-on-error"},
		{"min coverage word", goodCI + "      - run: echo min-cov\n", "threshold"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := CheckCI(tc.ci)
			if len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), tc.want) {
				t.Fatalf("problems = %v, want one containing %q", p, tc.want)
			}
		})
	}
	commented := goodCI + "# threshold 90% is not allowed, and this comment is fine\n"
	if p := CheckCI(commented); len(p) != 0 {
		t.Fatalf("comment lines must be ignored: %v", p)
	}
}

func TestCheckRelease(t *testing.T) {
	t.Parallel()
	good := "jobs:\n  release:\n    with:\n      require_workflow_success: 'CI'\n"
	if p := CheckRelease(goodCI, good); len(p) != 0 {
		t.Fatalf("good release reported problems: %v", p)
	}
	if p := CheckRelease(goodCI, "jobs:\n  release:\n"); len(p) != 1 || !strings.Contains(p[0], "require_workflow_success: 'CI'") {
		t.Fatalf("release without the guard: %v", p)
	}
	if p := CheckRelease(goodCI, good+"      x: continue-on-error\n"); len(p) != 1 {
		t.Fatalf("release with override: %v", p)
	}
	if p := CheckRelease("on: push\n", good); len(p) != 1 || !strings.Contains(p[0], "no name") {
		t.Fatalf("ci without a name: %v", p)
	}
}

func TestRepositoryWorkflows(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	ci, release := read("ci.yml"), read("release.yml")
	if p := CheckCI(ci); len(p) != 0 {
		t.Fatalf("the repository's ci.yml: %v", p)
	}
	if p := CheckRelease(ci, release); len(p) != 0 {
		t.Fatalf("the repository's release.yml: %v", p)
	}
}
