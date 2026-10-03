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
	"sort"
	"strings"
)

// Package is a package of the module, as `go list ./...` would list it: its
// import path, and whether its non-test Go files hold any statement. A package
// with statements must appear in the cover profile with a statement count above
// zero: a test binary whose TestMain exits 0 before it runs, or a package the
// profile was written without, must not make the package vanish from the count.
type Package struct {
	Path          string
	HasStatements bool
}

// Packages lists the packages of the module. Run takes it as a parameter so that
// tests can supply a fixed list.
type Packages func() ([]Package, error)

// OSPackages lists the packages of the module rooted at root (the directory of
// its go.mod) the way `go list ./...` does, without running it: every directory
// with Go files for this platform, except those whose name begins with "." or
// "_", testdata directories, and nested modules. A directory with only test files
// is a package without statements.
func OSPackages(root string) Packages {
	return func() ([]Package, error) {
		var dirs []string
		err := filepath.WalkDir(root, func(dir string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
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
			if errors.As(err, &none) {
				continue
			}
			if err != nil {
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

// Missing returns a message for each package that has statements and is absent
// from the profile or contributes no statements to it. perPackage is the statement
// count of each package path in the profile.
func Missing(pkgs []Package, perPackage map[string]int) []string {
	var out []string
	for _, p := range pkgs {
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
