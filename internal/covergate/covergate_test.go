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
		{"bad statement count", "mode: set\na.go:1.1,2.2 x 1\n", Result{}, `bad statement count "x"`},
		{"negative statement count", "mode: set\na.go:1.1,2.2 -1 1\n", Result{}, `bad statement count "-1"`},
		{"bad execution count", "mode: set\na.go:1.1,2.2 1 y\n", Result{}, `bad execution count "y"`},
		{"negative execution count", "mode: set\na.go:1.1,2.2 1 -2\n", Result{}, `bad execution count "-2"`},
		{"a profile with no mode line counts its first line", "a.go:1.1,2.2 2 0\na.go:3.1,4.2 1 1\n", Result{1, 3}, ""},
		{"a mode line anywhere but the first is not a block", "a.go:1.1,2.2 2 1\nmode: set\n", Result{}, "line 2"},
		{"a long line under the limit", "mode: set\na.go:1.1,2.2" + strings.Repeat(" ", 1048000) + "2 1\n", Result{2, 2}, ""},
		{"a line over the limit", "mode: set\na.go:1.1,2.2" + strings.Repeat(" ", 1048700) + "2 1\n", Result{}, "token too long"},
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

const twoPackages = "mode: set\nm/a/x.go:1.1,2.2 3 1\nm/b/y.go:1.1,2.2 2 4\n"

func pkg(path string, statements bool) Package { return Package{Path: path, HasStatements: statements} }

func listing(pkgs ...Package) Packages {
	return func() ([]Package, error) { return pkgs, nil }
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
		pkgs       Packages // nil: the module has no package with statements
	}{
		{"pass", []string{"cover.out"}, memOpen("mode: set\na.go:1.1,2.2 4 1\n"), 0, "coverage gate passed: 4 of 4", "", nil},
		{"fail by one statement", []string{"cover.out"}, memOpen(hundredAndOne), 1, "coverage gate FAILED: 9999 of 10000 statements covered (1 uncovered)", "", nil},
		{"no args", nil, memOpen(""), 2, "", "usage: covergate", nil},
		{"two args", []string{"cover.out", "x"}, memOpen(""), 2, "", "usage: covergate", nil},
		{"threshold flag is refused", []string{"-threshold=50", "cover.out"}, memOpen("mode: set\na.go:1.1,2.2 1 0\n"), 2, "", "takes no options", nil},
		{"single flag is refused", []string{"--min=1"}, memOpen("mode: set\na.go:1.1,2.2 1 0\n"), 2, "", "takes no options", nil},
		{"open error", []string{"missing.out"}, memOpen(""), 2, "", "no such profile missing.out", nil},
		{"parse error", []string{"cover.out"}, memOpen("mode: set\nbroken\n"), 2, "", "cover.out: line 2", nil},
		{"empty profile fails", []string{"cover.out"}, memOpen("mode: set\n"), 1, "FAILED: 0 of 0", "", nil},
		{"read error", []string{"cover.out"}, func(string) (io.ReadCloser, error) { return io.NopCloser(errReader{}), nil }, 2, "", "read failed", nil},
		// A package with statements must be in the profile with some. A test binary
		// whose TestMain exits 0 before running, or a profile written without a
		// package, must not let the package vanish from the count while the rest
		// is fully covered.
		{"every package with statements is in the profile", []string{"cover.out"}, memOpen(twoPackages), 0, "coverage gate passed: 5 of 5", "", listing(pkg("m/a", true), pkg("m/b", true), pkg("m/c", false))},
		{"a package vanished from the profile", []string{"cover.out"}, memOpen("mode: set\nm/a/x.go:1.1,2.2 3 1\n"), 1, "coverage gate FAILED: package m/b has statements and is not in the cover profile", "", listing(pkg("m/a", true), pkg("m/b", true))},
		{"a package contributes no statements", []string{"cover.out"}, memOpen("mode: set\nm/a/x.go:1.1,2.2 3 1\nm/b/y.go:1.1,2.2 0 1\n"), 1, "package m/b has statements and contributes none to the cover profile", "", listing(pkg("m/a", true), pkg("m/b", true))},
		{"a package with no statements may be absent", []string{"cover.out"}, memOpen("mode: set\nm/a/x.go:1.1,2.2 3 1\n"), 0, "passed: 3 of 3", "", listing(pkg("m/a", true), pkg("m/consts", false))},
		{"a TestMain in a package that is fully covered", []string{"cover.out"}, memOpen(twoPackages), 1, "coverage gate FAILED: m/a/x_test.go declares TestMain, which can hide a failing test", "", listing(Package{Path: "m/a", HasStatements: true, TestMains: []string{"m/a/x_test.go"}}, pkg("m/b", true))},
		{"a vanished package and an uncovered statement are both reported", []string{"cover.out"}, memOpen("mode: set\nm/a/x.go:1.1,2.2 3 1\nm/a/x.go:3.1,4.2 1 0\n"), 1, "package m/b has statements and is not in the cover profile", "", listing(pkg("m/a", true), pkg("m/b", true))},
		{"the package list cannot be read", []string{"cover.out"}, memOpen(twoPackages), 2, "", "no go.mod", func() ([]Package, error) { return nil, errors.New("no go.mod") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			pkgs := tc.pkgs
			if pkgs == nil {
				pkgs = listing()
			}
			code := Run(tc.args, &out, &errb, tc.open, pkgs)
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

func TestParseByPackage(t *testing.T) {
	t.Parallel()
	res, per, err := ParseByPackage(strings.NewReader("mode: atomic\nm/a/x.go:1.1,2.2 3 1\nm/a/y.go:1.1,2.2 2 0\nm/a/b/z.go:1.1,2.2 4 1\nm/a/x.go:1.1,2.2 3 5\nplain.go:1.1,2.2 1 1\n"))
	if err != nil || res != (Result{8, 10}) {
		t.Fatalf("result = %+v, %v", res, err)
	}
	want := map[string]int{"m/a": 5, "m/a/b": 4, ".": 1}
	if len(per) != len(want) {
		t.Fatalf("per package = %v, want %v", per, want)
	}
	for k, v := range want {
		if per[k] != v {
			t.Errorf("per package = %v, want %v", per, want)
		}
	}
	if _, _, err := ParseByPackage(strings.NewReader("mode: set\nbroken\n")); err == nil {
		t.Error("a broken profile was accepted")
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()
	got := Missing([]Package{pkg("m/a", true), pkg("m/b", true), pkg("m/c", true), pkg("m/d", false), {Path: "m/e", TestMains: []string{"m/e/e_test.go"}}}, map[string]int{"m/a": 3, "m/c": 0, "m/d": 0, "m/other": 5})
	if len(got) != 3 || !strings.Contains(got[0], "m/b has statements and is not in") || !strings.Contains(got[1], "m/c has statements and contributes none") || !strings.Contains(got[2], "m/e/e_test.go declares TestMain") {
		t.Fatalf("Missing = %v", got)
	}
}

// write creates the files of a small module in a temporary directory.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// OSPackages lists what `go list ./...` would: Go files for this platform, not
// testdata, hidden or underscore directories, nested modules.
func TestOSPackages(t *testing.T) {
	t.Parallel()
	root := write(t, map[string]string{
		"go.mod":                 "module example.com/m\n\ngo 1.27\n",
		"main.go":                "package m\n\nfunc F() int {\n\treturn 1\n}\n",
		"consts/c.go":            "package consts\n\nconst X = 1\n\nvar Y = func() {}\n\nfunc G()\n",
		"empty/e.go":             "package empty\n\ntype T struct{}\n\nfunc (T) M() {}\n",
		"onlytests/x_test.go":    "package onlytests\n",
		"testdata/t.go":          "package t\n\nfunc F() { println() }\n",
		".hidden/h.go":           "package h\n\nfunc F() { println() }\n",
		"_skip/s.go":             "package s\n\nfunc F() { println() }\n",
		"nested/go.mod":          "module example.com/nested\n",
		"nested/n.go":            "package nested\n\nfunc F() { println() }\n",
		"deep/er/d.go":           "package er\n\nfunc F() {\n\tprintln()\n}\n",
		"constrained/c.go":       "package constrained\n\nfunc F() { println() }\n",
		"constrained/other_x.go": "//go:build never_ever\n\npackage constrained\n\nfunc H() { println() }\n",
		"notes/readme.md":        "not go\n",
		// A package whose first file has statements and whose last has none, and the
		// package inside a nested module, which is the other module's.
		"mixed/a.go":         "package mixed\n\nfunc F() int {\n\treturn 1\n}\n",
		"mixed/z.go":         "package mixed\n\nconst X = 1\n",
		"nested/sub/deep.go": "package deep\n\nfunc F() { println() }\n",
		// A TestMain, in either form, in a test of the package or an external one.
		"tm/a.go":      "package tm\n\nfunc F() int {\n\treturn 1\n}\n",
		"tm/a_test.go": "package tm\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n\tos.Exit(0)\n}\n",
		"tm/b_test.go": "package tm_test\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n",
		"tm/c_test.go": "package tm\n\nimport \"testing\"\n\ntype T struct{}\n\nfunc (T) TestMain(m *testing.M) {}\n\nfunc TestOther(t *testing.T) {}\n\nvar TestMainValue = 1\n",
		"tm/main_x.go": "package tm\n\nfunc TestMain() {}\n",
	})
	got, err := OSPackages(root)()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, fmt.Sprintf("%s:%v", p.Path, p.HasStatements))
	}
	want := "example.com/m:true example.com/m/constrained:true example.com/m/consts:false example.com/m/deep/er:true example.com/m/empty:false example.com/m/mixed:true example.com/m/onlytests:false example.com/m/tm:true"
	if strings.Join(names, " ") != want {
		t.Fatalf("packages = %v\nwant %s", names, want)
	}
	// The files that declare a TestMain are named: a method, a variable and a
	// function of a non-test file with that name are not one.
	for _, p := range got {
		wantMains := ""
		if p.Path == "example.com/m/tm" {
			wantMains = "example.com/m/tm/a_test.go example.com/m/tm/b_test.go"
		}
		if gotMains := strings.Join(p.TestMains, " "); gotMains != wantMains {
			t.Errorf("%s declares TestMain in %q, want %q", p.Path, gotMains, wantMains)
		}
	}
}

func TestOSPackagesErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"no go.mod", map[string]string{"a.go": "package a\n"}, "go.mod"},
		{"no module line", map[string]string{"go.mod": "go 1.27\n"}, "no module line"},
		{"two packages in one directory", map[string]string{"go.mod": "module m\n", "a.go": "package a\n", "b.go": "package b\n"}, "found packages"},
		{"a Go file that does not parse", map[string]string{"go.mod": "module m\n", "a.go": "package a\n\nfunc {\n"}, "a.go"},
		{"a test file that does not parse", map[string]string{"go.mod": "module m\n", "a.go": "package a\n", "a_test.go": "package a\n\nfunc {\n"}, "a_test.go"},
		{"an external test file that does not parse", map[string]string{"go.mod": "module m\n", "a.go": "package a\n", "a_test.go": "package a_test\n\nfunc {\n"}, "a_test.go"},
	} {
		if _, err := OSPackages(write(t, tc.files))(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want containing %q", tc.name, err, tc.want)
		}
	}
	// A directory that cannot be read: the walk reports it.
	if _, err := OSPackages(filepath.Join(t.TempDir(), "absent"))(); err == nil {
		t.Error("a missing root was accepted")
	}
}

// The module's own packages: every one that has statements is one the gate
// requires, so a package added to the module without tests cannot be left out.
func TestOSPackagesOfThisModule(t *testing.T) {
	t.Parallel()
	got, err := OSPackages(filepath.Join("..", ".."))()
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range got {
		have[p.Path] = p.HasStatements
	}
	for _, name := range []string{"cmd/covergate", "cmd/modelspec", "internal/cli", "internal/covergate", "pkg/modelspec"} {
		if !have["github.com/modelspec-org/cli/"+name] {
			t.Errorf("package %s is not listed with statements: %v", name, have)
		}
	}
	if has, listed := have["github.com/modelspec-org/cli/scripts/fuzz"]; !listed || has {
		t.Errorf("the test-only package scripts/fuzz: listed %v, statements %v", listed, has)
	}
}
