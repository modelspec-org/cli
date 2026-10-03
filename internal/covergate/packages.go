package covergate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The gate reads every .go file of every package directory of the module, whatever
// its name or build constraint, because go/build lists only the files that this
// platform and these tags would build: a TestMain behind `//go:build race`, or in
// a file named x_linux_test.go, would not be listed, and would run only on the
// platform or with the tag that is not the gate's. So a TestMain is refused in any
// file, and so is any build constraint (a //go:build or // +build line, or a GOOS
// or GOARCH file-name suffix): a file with one can be left out of a test run, and
// so of the coverage profile, where the gate cannot see it.
//
// A TestMain runs the tests itself, so it can run them and then exit 0, and every
// test of the package can fail with the run green: the gate counts statements that
// ran, not tests that passed. No package may declare one. (If a package needs
// set-up and tear-down, do it in the tests that need it, with t.Cleanup, or in a
// helper called from them; a seam for a test is a variable or a parameter in the
// code it tests.)

// Package is a package of the module, as `go list ./...` would list it: its
// import path, and whether its non-test Go files hold any statement. A package
// with statements must appear in the cover profile with a statement count above
// zero: a test binary whose TestMain exits 0 before it runs, or a package the
// profile was written without, must not make the package vanish from the count.
type Package struct {
	Path          string
	HasStatements bool
	TestMains     []string // the test files that declare a TestMain
	Constraints   []string // a message for each build constraint of a Go file
}

// Packages lists the packages of the module. Run takes it as a parameter so that
// tests can supply a fixed list.
type Packages func() ([]Package, error)

// OSPackages lists the packages of the module rooted at root (the directory of
// its go.mod) the way `go list ./...` does, without running it: every directory
// with Go files for this platform, except those whose name begins with "." or
// "_", testdata directories, and nested modules. A directory with only test files
// is a package without statements.
func OSPackages(root string) Packages { return osPackages(root, os.ReadFile) }

// osPackages is OSPackages reading files with readFile, a seam for tests.
func osPackages(root string, readFile func(string) ([]byte, error)) Packages {
	return func() ([]Package, error) {
		var dirs []string
		goFiles := map[string][]string{} // the .go files of each directory
		err := filepath.WalkDir(root, func(dir string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				if strings.HasSuffix(d.Name(), ".go") {
					goFiles[filepath.Dir(dir)] = append(goFiles[filepath.Dir(dir)], d.Name())
				}
				return nil
			}
			if dir != root {
				name := d.Name()
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
					return filepath.SkipDir // a nested module
				}
			}
			dirs = append(dirs, dir)
			return nil
		})
		if err != nil {
			return nil, err
		}
		modPath, err := modulePath(root)
		if err != nil {
			return nil, err
		}
		var out []Package
		for _, dir := range dirs {
			pkg, err := build.Default.ImportDir(dir, 0)
			var none *build.NoGoError
			if err != nil && !errors.As(err, &none) {
				return nil, err
			}
			rel, _ := filepath.Rel(root, dir)
			p := Package{Path: path.Join(modPath, filepath.ToSlash(rel))}
			for _, f := range pkg.GoFiles {
				has, err := hasStatements(filepath.Join(dir, f))
				if err != nil {
					return nil, err
				}
				p.HasStatements = p.HasStatements || has
			}
			// Every Go file of the directory, not only the ones go/build lists.
			if len(goFiles[dir]) == 0 {
				continue
			}
			for _, name := range goFiles[dir] {
				src, err := readFile(filepath.Join(dir, name))
				if err != nil {
					return nil, err
				}
				for _, problem := range constraintProblems(name, src) {
					p.Constraints = append(p.Constraints, p.Path+"/"+problem)
				}
				if !strings.HasSuffix(name, "_test.go") {
					continue
				}
				has, err := declaresTestMain(name, src)
				if err != nil {
					return nil, err
				}
				if has {
					p.TestMains = append(p.TestMains, path.Join(p.Path, name))
				}
			}
			out = append(out, p)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out, nil
	}
}

// modulePath reads the module path from the go.mod in root.
func modulePath(root string) (string, error) {
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("the gate must run in the module root, with a go.mod: %w", err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`), nil
		}
	}
	return "", errors.New("go.mod has no module line")
}

// hasStatements reports whether the Go file has a function body with a statement:
// what the cover tool counts.
func hasStatements(file string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		return false, err
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStmt); ok && len(b.List) > 0 {
			found = true
		}
		return !found
	})
	return found, nil
}

var knownOS = strings.Fields("aix android darwin dragonfly freebsd hurd illumos ios js linux nacl netbsd openbsd plan9 solaris wasip1 windows zos")
var knownArch = strings.Fields("386 amd64 amd64p32 arm armbe arm64 arm64be loong64 mips mipsle mips64 mips64le mips64p32 mips64p32le ppc ppc64 ppc64le riscv riscv64 s390 s390x sparc sparc64 wasm")

// constraintProblems reports the build constraint, if any, of one Go file: the
// name of the file with its reason.
func constraintProblems(name string, src []byte) []string {
	var problems []string
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	parts := strings.Split(stem, "_")
	if n := len(parts); n >= 2 {
		last := parts[n-1]
		if slices.Contains(knownOS, last) || slices.Contains(knownArch, last) {
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

// declaresTestMain reports whether the Go file, named file, with the source src,
// declares a function TestMain.
func declaresTestMain(file string, src []byte) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		return false, err
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "TestMain" {
			return true, nil
		}
	}
	return false, nil
}

// Missing returns a message for each Go file with a build constraint, for each test file that declares a TestMain, and for each package that has statements and is absent
// from the profile or contributes no statements to it. perPackage is the statement
// count of each package path in the profile.
func Missing(pkgs []Package, perPackage map[string]int) []string {
	var out []string
	for _, p := range pkgs {
		for _, c := range p.Constraints {
			out = append(out, fmt.Sprintf("%s: a Go file with a build constraint can be left out of a test run and so of the coverage profile, where the gate cannot see it; no file may have one", c))
		}
		for _, file := range p.TestMains {
			out = append(out, fmt.Sprintf("%s declares TestMain, which can hide a failing test (it may run the tests and exit 0); no package may have one", file))
		}
		switch n, in := perPackage[p.Path]; {
		case !p.HasStatements:
		case !in:
			out = append(out, fmt.Sprintf("package %s has statements and is not in the cover profile", p.Path))
		case n == 0:
			out = append(out, fmt.Sprintf("package %s has statements and contributes none to the cover profile", p.Path))
		}
	}
	return out
}
