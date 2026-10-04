package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMainExitCodes runs main with a log on standard input and captures the exit
// code. It is not parallel because it replaces os.Args, os.Stdin and the exit.
func TestMainExitCodes(t *testing.T) {
	dir := t.TempDir()
	sound := filepath.Join(dir, "sound.log")
	stalled := filepath.Join(dir, "stalled.log")
	line := func(sec, execs int) string {
		return "fuzz: elapsed: " + itoa(sec) + "s, execs: " + itoa(execs) + " (1/sec), new interesting: 0 (total: 1)\n"
	}
	soundLog, stalledLog := "", ""
	for sec := 3; sec <= 90; sec += 3 {
		soundLog += line(sec, sec*1000)
		stalledLog += line(sec, min(sec, 30)*1000)
	}
	for path, content := range map[string]string{sound: soundLog, stalled: stalledLog} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldArgs, oldExit, oldStdin := os.Args, exit, os.Stdin
	t.Cleanup(func() { os.Args, exit, os.Stdin = oldArgs, oldExit, oldStdin })
	for _, tc := range []struct {
		log  string
		want int
	}{{sound, 0}, {stalled, 1}} {
		in, err := os.Open(tc.log)
		if err != nil {
			t.Fatal(err)
		}
		defer in.Close()
		got := -1
		exit = func(code int) { got = code }
		os.Args, os.Stdin = []string{"fuzzjudge", "FuzzHCL"}, in
		main()
		if got != tc.want {
			t.Errorf("main(%s) exit code = %d, want %d", filepath.Base(tc.log), got, tc.want)
		}
	}
}

func itoa(n int) string {
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
