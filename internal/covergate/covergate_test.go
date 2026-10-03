package covergate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		profile string
		want    Result
		wantErr string
	}{
		{"all covered", "mode: atomic\na.go:1.1,2.2 3 1\na.go:3.1,4.2 2 7\n", Result{5, 5}, ""},
		{"one uncovered statement", "mode: set\na.go:1.1,2.2 3 1\na.go:3.1,4.2 1 0\n", Result{3, 4}, ""},
		{"duplicate block covered in any listing", "mode: set\na.go:1.1,2.2 3 0\na.go:1.1,2.2 3 4\n", Result{3, 3}, ""},
		{"duplicate block covered in the first listing only", "mode: set\na.go:1.1,2.2 3 5\na.go:1.1,2.2 3 0\n", Result{3, 3}, ""},
		{"duplicate block covered in the second listing only", "mode: set\na.go:1.1,2.2 3 0\na.go:1.1,2.2 3 5\n", Result{3, 3}, ""},
		{"duplicate block uncovered in every listing", "mode: set\na.go:1.1,2.2 3 0\na.go:1.1,2.2 3 0\n", Result{0, 3}, ""},
		{"blank lines ignored", "mode: set\n\na.go:1.1,2.2 1 1\n\n", Result{1, 1}, ""},
		{"mode only", "mode: set\n", Result{0, 0}, ""},
		{"empty", "", Result{0, 0}, ""},
		{"wrong field count", "mode: set\na.go:1.1,2.2 3\n", Result{}, "line 2"},
		{"bad statement count", "mode: set\na.go:1.1,2.2 x 1\n", Result{}, "bad statement count"},
		{"negative statement count", "mode: set\na.go:1.1,2.2 -1 1\n", Result{}, "bad statement count"},
		{"bad execution count", "mode: set\na.go:1.1,2.2 1 y\n", Result{}, "bad execution count"},
		{"negative execution count", "mode: set\na.go:1.1,2.2 1 -2\n", Result{}, "bad execution count"},
		{"line too long", "mode: set\n" + strings.Repeat("x", 2<<20) + "\n", Result{}, "token too long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(strings.NewReader(tc.profile))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	t.Parallel()
	tests := []struct {
		r    Result
		want bool
	}{
		{Result{10, 10}, true},
		{Result{9999, 10000}, false}, // 99.99%: a rounded gate would pass this
		{Result{0, 10}, false},
		{Result{0, 0}, false},
	}
	for _, tc := range tests {
		if got := tc.r.Complete(); got != tc.want {
			t.Errorf("%+v.Complete() = %v, want %v", tc.r, got, tc.want)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func memOpen(profile string) Open {
	return func(name string) (io.ReadCloser, error) {
		if name != "cover.out" {
			return nil, errors.New("no such profile " + name)
		}
		return io.NopCloser(strings.NewReader(profile)), nil
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	var big strings.Builder
	big.WriteString("mode: set\n")
	for i := 1; i <= 9999; i++ {
		fmt.Fprintf(&big, "a.go:%d.1,%d.2 1 1\n", i, i)
	}
	big.WriteString("a.go:99999.1,99999.2 1 0\n")
	hundredAndOne := big.String()
	tests := []struct {
		name       string
		args       []string
		open       Open
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"pass", []string{"cover.out"}, memOpen("mode: set\na.go:1.1,2.2 4 1\n"), 0, "coverage gate passed: 4 of 4", ""},
		{"fail by one statement", []string{"cover.out"}, memOpen(hundredAndOne), 1, "coverage gate FAILED: 9999 of 10000 statements covered (1 uncovered)", ""},
		{"no args", nil, memOpen(""), 2, "", "usage: covergate"},
		{"two args", []string{"cover.out", "x"}, memOpen(""), 2, "", "usage: covergate"},
		{"threshold flag is refused", []string{"-threshold=50", "cover.out"}, memOpen("mode: set\na.go:1.1,2.2 1 0\n"), 2, "", "takes no options"},
		{"single flag is refused", []string{"--min=1"}, memOpen("mode: set\na.go:1.1,2.2 1 0\n"), 2, "", "takes no options"},
		{"open error", []string{"missing.out"}, memOpen(""), 2, "", "no such profile missing.out"},
		{"parse error", []string{"cover.out"}, memOpen("mode: set\nbroken\n"), 2, "", "cover.out: line 2"},
		{"empty profile fails", []string{"cover.out"}, memOpen("mode: set\n"), 1, "FAILED: 0 of 0", ""},
		{"read error", []string{"cover.out"}, func(string) (io.ReadCloser, error) { return io.NopCloser(errReader{}), nil }, 2, "", "read failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code := Run(tc.args, &out, &errb, tc.open)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (stdout %q, stderr %q)", code, tc.wantCode, out.String(), errb.String())
			}
			if !strings.Contains(out.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want containing %q", out.String(), tc.wantStdout)
			}
			if !strings.Contains(errb.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want containing %q", errb.String(), tc.wantStderr)
			}
		})
	}
}

func TestOSOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cover.out")
	if err := os.WriteFile(path, []byte("mode: set\na.go:1.1,2.2 2 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := OSOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := Parse(f)
	if err != nil || res != (Result{2, 2}) {
		t.Fatalf("Parse(OSOpen) = %+v, %v", res, err)
	}
	if _, err := OSOpen(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("OSOpen of a missing file succeeded")
	}
}
