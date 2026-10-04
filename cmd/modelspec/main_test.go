package main

import (
	"os"
	"testing"
)

// TestMainExitsWithTheCommandsCode runs main, which prints the version to the
// test's own stdout, and captures the exit code. It is not parallel because it
// replaces os.Args and exit.
func TestMainExitsWithTheCommandsCode(t *testing.T) {
	oldArgs, oldExit := os.Args, exit
	t.Cleanup(func() { os.Args, exit = oldArgs, oldExit })
	for _, tc := range []struct {
		args []string
		want int
	}{{[]string{"modelspec", "version"}, 0}, {[]string{"modelspec", "nonsense"}, 2}} {
		got := -1
		exit = func(code int) { got = code }
		os.Args = tc.args
		main()
		if got != tc.want {
			t.Errorf("main(%v) exit code = %d, want %d", tc.args[1:], got, tc.want)
		}
	}
}
