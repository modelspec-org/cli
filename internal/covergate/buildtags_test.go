package covergate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file with a build constraint can be left out of the test run and so of the
// coverage profile, and the gate cannot see it. This test refuses any Go file
// with a constraint, whether written as a //go:build or // +build line or as a
// GOOS or GOARCH file name suffix, so that nothing can hide from the gate. The
// release builds windows binaries, and a file for windows only would be
// invisible to a Linux run.

var knownOS = strings.Fields("aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos")
var knownArch = strings.Fields("386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm")

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// constraintProblems reports the build constraint, if any, of one Go file.
func constraintProblems(name string, src []byte) []string {
	var problems []string
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	parts := strings.Split(stem, "_")
	if n := len(parts); n >= 2 {
		last := parts[n-1]
		if contains(knownOS, last) || contains(knownArch, last) {
			problems = append(problems, name+": the file name carries a GOOS or GOARCH build constraint ("+last+")")
		}
	}
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:build") || strings.HasPrefix(line, "// +build") {
			problems = append(problems, name+": "+line)
		}
	}
	return problems
}

func TestConstraintProblems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want int
	}{
		{"plain.go", "package p\n", 0},
		{"plain_test.go", "package p\n", 0},
		{"hidden_windows.go", "package p\n", 1},
		{"hidden_windows_test.go", "package p\n", 1},
		{"hidden_linux_amd64.go", "package p\n", 1},
		{"hidden_arm64.go", "package p\n", 1},
		{"windows.go", "package p\n", 0}, // a bare GOOS name is not a constraint
		{"my_file.go", "package p\n", 0},
		{"a.go", "//go:build windows\n\npackage p\n", 1},
		{"a.go", "// +build ignore\n\npackage p\n", 1},
		{"a.go", "package p\n\n  //go:build linux\n", 1},
		{"a.go", "// go:build is mentioned in prose here\npackage p\n", 0},
	}
	for _, tc := range tests {
		if got := constraintProblems(tc.name, []byte(tc.src)); len(got) != tc.want {
			t.Errorf("constraintProblems(%q, %q) = %v, want %d problems", tc.name, tc.src, got, tc.want)
		}
	}
}

func TestNoGoFileHasABuildConstraint(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "dist", "testdata", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		count++
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, p := range constraintProblems(filepath.Base(path), src) {
			t.Errorf("%s", strings.Replace(p, filepath.Base(path), path, 1))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count < 20 {
		t.Fatalf("found only %d Go files; is the walk looking in the right place?", count)
	}
}
