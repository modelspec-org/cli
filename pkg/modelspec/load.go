package modelspec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FS is the filesystem the loader reads and writes through. OSFS is the real
// one; tests use in-memory implementations.
type FS interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
}

// OSFS is the operating system's filesystem.
type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }
func (OSFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }
func (OSFS) Stat(name string) (fs.FileInfo, error)      { return os.Stat(name) }
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// IsModelFile reports whether a file name is a ModelSpec model file.
func IsModelFile(name string) bool {
	return strings.HasSuffix(name, HCLSuffix) || strings.HasSuffix(name, JSONSuffix)
}

// skipDir reports whether a directory is left out of a search: hidden
// directories and node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Discover expands paths into model files. A file is taken as given (it must be
// a model file); a directory is searched recursively for *.modelspec.hcl and
// *.modelspec.json. The result is sorted and has no duplicates.
func Discover(fsys FS, paths []string) ([]string, error) {
	set := map[string]bool{}
	for _, p := range paths {
		info, err := fsys.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !IsModelFile(p) {
				return nil, fmt.Errorf("%s: not a ModelSpec file (expected a name ending in %s or %s)", p, HCLSuffix, JSONSuffix)
			}
			set[filepath.Clean(p)] = true
			continue
		}
		if err := walk(fsys, p, set); err != nil {
			return nil, err
		}
	}
	files := make([]string, 0, len(set))
	for f := range set {
		files = append(files, f)
	}
	sort.Strings(files)
	return files, nil
}

func walk(fsys FS, dir string, set map[string]bool) error {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if skipDir(e.Name()) {
				continue
			}
			if err := walk(fsys, full, set); err != nil {
				return err
			}
		case IsModelFile(e.Name()):
			set[full] = true
		}
	}
	return nil
}

// Parse reads one model file by its extension.
func Parse(file string, src []byte) (*Model, []Finding) {
	if strings.HasSuffix(file, JSONSuffix) {
		return ParseJSON(file, src)
	}
	return ParseHCL(file, src)
}

// Load reads and parses the files. A file that cannot be read is an error, not
// a finding. The findings returned are the parse findings, sorted.
func Load(fsys FS, files []string) ([]*Model, []Finding, error) {
	var models []*Model
	var findings []Finding
	for _, f := range files {
		src, err := fsys.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		m, fs := Parse(f, src)
		models = append(models, m)
		findings = append(findings, fs...)
	}
	SortFindings(findings)
	return models, findings, nil
}

// Lint discovers, loads and checks the paths: the parse findings and the
// semantic findings, sorted, with the number of files read.
func Lint(fsys FS, paths []string) (findings []Finding, files int, err error) {
	names, err := Discover(fsys, paths)
	if err != nil {
		return nil, 0, err
	}
	if len(names) == 0 {
		return nil, 0, fmt.Errorf("no ModelSpec files (%s, %s) found in %s", HCLSuffix, JSONSuffix, strings.Join(paths, ", "))
	}
	models, parse, err := Load(fsys, names)
	if err != nil {
		return nil, 0, err
	}
	findings = append(parse, Check(models)...)
	SortFindings(findings)
	return findings, len(names), nil
}
