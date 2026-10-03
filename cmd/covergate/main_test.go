package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMainExitCodes runs main with a real profile on disk and captures the exit code.
// It is not parallel because it replaces os.Args and the package's exit.
func TestMainExitCodes(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.out")
	bad := filepath.Join(dir, "bad.out")
	if err := os.WriteFile(good, []byte("mode: set\na.go:1.1,2.2 1 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("mode: set\na.go:1.1,2.2 1 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldArgs, oldExit := os.Args, exit
	t.Cleanup(func() { os.Args, exit = oldArgs, oldExit })
	for _, tc := range []struct {
		profile string
		want    int
	}{{good, 0}, {bad, 1}} {
		got := -1
		exit = func(code int) { got = code }
		os.Args = []string{"covergate", tc.profile}
		main()
		if got != tc.want {
			t.Errorf("main(%s) exit code = %d, want %d", filepath.Base(tc.profile), got, tc.want)
		}
	}
}
