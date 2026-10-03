package modelspec

import (
	"errors"
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
	// Canonical returns the absolute, symlink-free form of a path that exists,
	// so the same file reached by two names can be recognised.
	Canonical(name string) (string, error)
}

// OSFS is the operating system's filesystem. The zero value is ready to use.
type OSFS struct {
	getwd func() (string, error) // os.Getwd when nil; a seam for tests
}

func (OSFS) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }
func (OSFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }
func (OSFS) Stat(name string) (fs.FileInfo, error)      { return os.Stat(name) }
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (o OSFS) Canonical(name string) (string, error) {
	if !filepath.IsAbs(name) {
		getwd := o.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		wd, err := getwd()
		if err != nil {
			return "", err
		}
		name = filepath.Join(wd, name)
	}
	return filepath.EvalSymlinks(name)
}

// IsModelFile reports whether a file name is a standalone ModelSpec model file.
func IsModelFile(name string) bool {
	return strings.HasSuffix(name, HCLSuffix) || strings.HasSuffix(name, JSONSuffix)
}

// layoutModule recognises the SpecScore layout: a file directly inside a
// directory …/modules/<id>/models/ belongs to module <id>, whatever it is called
// (spec/hcl-authoring.md, "Open Questions"). canon is a canonical path.
func layoutModule(canon string) (id, dir string, ok bool) {
	p := filepath.ToSlash(canon)
	parts := strings.Split(p, "/")
	n := len(parts)
	if n < 4 || parts[n-2] != "models" || parts[n-4] != "modules" || !strings.HasSuffix(parts[n-1], ".hcl") {
		return "", "", false
	}
	return parts[n-3], strings.Join(parts[:n-1], "/"), true
}

// isModelPath reports whether a file is a model file: a standalone model file by
// name, or any .hcl file in a SpecScore layout models directory.
func isModelPath(name, canon string) bool {
	if IsModelFile(name) {
		return true
	}
	_, _, ok := layoutModule(canon)
	return ok
}

// skipDir reports whether a directory is left out of a search: hidden
// directories and node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Source is a model file to read: Path as given or found (used in findings) and
// its canonical form (used to recognise duplicates and the module layout).
type Source struct {
	Path  string
	Canon string
}

// Discover expands paths into model files. A file is taken as given (it must be
// a model file); a directory is searched recursively for *.modelspec.hcl and
// *.modelspec.json, and in a SpecScore layout models directory for any *.hcl.
// Hidden directories and node_modules are skipped, and symlinked directories are
// not followed; a symlink to a file is read through. A file reached by two names
// is returned once, under the first name. The result is sorted by path. The
// findings are warnings for files a search found but cannot read.
func Discover(fsys FS, paths []string) ([]Source, []Finding, error) {
	return discover(fsys, paths, false)
}

// discover is Discover; anyHCL also accepts any .hcl file, as a --module
// assignment does.
func discover(fsys FS, paths []string, anyHCL bool) ([]Source, []Finding, error) {
	d := &discovery{fsys: fsys, seen: map[string]bool{}, anyHCL: anyHCL}
	for _, p := range paths {
		info, err := fsys.Stat(p)
		if err != nil {
			return nil, nil, err
		}
		canon, err := fsys.Canonical(p)
		if err != nil {
			return nil, nil, err
		}
		if info.IsDir() {
			if err := d.walk(p); err != nil {
				return nil, nil, err
			}
			continue
		}
		if !d.isModel(p, canon) {
			return nil, nil, fmt.Errorf("%s: not a ModelSpec file (expected a name ending in %s or %s, or a .hcl file in a SpecScore models directory)", p, HCLSuffix, JSONSuffix)
		}
		d.add(Source{Path: filepath.Clean(p), Canon: canon})
	}
	sort.Slice(d.out, func(i, j int) bool { return d.out[i].Path < d.out[j].Path })
	SortFindings(d.findings)
	return d.out, d.findings, nil
}

type discovery struct {
	anyHCL   bool
	fsys     FS
	seen     map[string]bool
	out      []Source
	findings []Finding
}

func (d *discovery) isModel(name, canon string) bool {
	return isModelPath(name, canon) || (d.anyHCL && strings.HasSuffix(name, ".hcl"))
}

func (d *discovery) add(s Source) {
	if !d.seen[s.Canon] {
		d.seen[s.Canon] = true
		d.out = append(d.out, s)
	}
}

func (d *discovery) walk(dir string) error {
	entries, err := d.fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.Type()&fs.ModeSymlink != 0 {
			d.link(full, e.Name())
			continue
		}
		if e.IsDir() {
			if !skipDir(e.Name()) {
				if err := d.walk(full); err != nil {
					return err
				}
			}
			continue
		}
		d.file(full, e.Name())
	}
	return nil
}

// file adds a regular file when it is a model file.
func (d *discovery) file(full, name string) {
	canon, err := d.fsys.Canonical(full)
	if err != nil {
		d.findings = append(d.findings, Finding{File: full, Rule: RuleIO, Severity: SeverityWarning, Message: "cannot be read: " + err.Error()})
		return
	}
	if d.isModel(name, canon) {
		d.add(Source{Path: full, Canon: canon})
	}
}

// link follows a symlink to a file; a link to a directory is not followed, and a
// dangling link is a warning, not the end of the run.
func (d *discovery) link(full, name string) {
	info, err := d.fsys.Stat(full)
	switch {
	case err != nil:
		if IsModelFile(name) || strings.HasSuffix(name, ".hcl") {
			d.findings = append(d.findings, Finding{File: full, Rule: RuleIO, Severity: SeverityWarning, Message: "symbolic link cannot be followed, so the file was not checked: " + err.Error()})
		}
	case !info.IsDir():
		d.file(full, name)
	}
}

// Assignment places the model files under Path (a file or a directory) in module
// Module (--module Module=Path). It wins over every other module rule.
type Assignment struct {
	Module string
	Path   string
}

// LintOptions configures Lint.
type LintOptions struct {
	Profile Profile
	// Modules assigns files to modules explicitly. The assigned files are linted
	// even when no path names them.
	Modules []Assignment
}

// Parse reads one model file by its extension.
func Parse(file string, src []byte) (*Model, []Finding) {
	if strings.HasSuffix(file, JSONSuffix) {
		return ParseJSON(file, src)
	}
	return ParseHCL(file, src)
}

// Load reads and parses the files and assigns each to a module:
//
//  1. --module assignments (assign) win;
//  2. a .hcl file directly inside …/modules/<id>/models/ belongs to module <id>,
//     together with every other file there;
//  3. otherwise an HCL file <name>.modelspec.hcl is module <name> by itself, and a
//     JSON file is module.name, or the file name's stem when it has none.
//
// A JSON file beside the HCL file of the same name (X.modelspec.json beside
// X.modelspec.hcl, both in the files) is that module's interchange copy: a Twin.
// A file that cannot be read is an error, not a finding.
func Load(fsys FS, files []Source, assign []Assignment) ([]*Model, []Finding, error) {
	explicit, err := explicitModules(fsys, assign)
	if err != nil {
		return nil, nil, err
	}
	var models []*Model
	var findings []Finding
	byCanon := map[string]*Model{}
	for _, f := range files {
		src, err := fsys.ReadFile(f.Path)
		if err != nil {
			return nil, nil, err
		}
		m, fs := Parse(f.Path, src)
		m.Group = f.Canon
		models = append(models, m)
		byCanon[f.Canon] = m
		findings = append(findings, fs...)
		if name, ok := explicit[f.Canon]; ok {
			m.Name = name
			if m.Form == FormHCL {
				m.Group = "--module " + name
			}
		} else if id, dir, ok := layoutModule(f.Canon); ok && m.Form == FormHCL {
			m.Name, m.Group = id, dir
		}
	}
	for _, m := range models {
		if m.Form != FormJSON || !strings.HasSuffix(m.Group, JSONSuffix) {
			continue
		}
		twin, ok := byCanon[strings.TrimSuffix(m.Group, JSONSuffix)+HCLSuffix]
		if ok && twin.Form == FormHCL {
			m.Twin, m.Name = true, twin.Name
		}
	}
	SortFindings(findings)
	return models, findings, nil
}

// explicitModules reads the --module assignments: the canonical path of every
// assigned file and the module it is assigned to. A file assigned to two
// different modules is an error.
func explicitModules(fsys FS, assign []Assignment) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range assign {
		files, _, err := discover(fsys, []string{a.Path}, true)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if prev, ok := out[f.Canon]; ok && prev != a.Module {
				return nil, fmt.Errorf("%s is assigned to module %q and to module %q", f.Path, prev, a.Module)
			}
			out[f.Canon] = a.Module
		}
	}
	return out, nil
}

// Result is the outcome of Lint.
type Result struct {
	Findings []Finding
	Files    int
	Models   []*Model
}

// Lint discovers, loads and checks the paths together with the files assigned by
// opts.Modules: the parse findings and the semantic findings, sorted. With no
// paths and no assignments it is an error, as is finding no model file.
func Lint(fsys FS, paths []string, opts LintOptions) (Result, error) {
	var res Result
	sources, warnings, err := Discover(fsys, paths)
	if err != nil {
		return res, err
	}
	for _, a := range opts.Modules {
		more, w, err := discover(fsys, []string{a.Path}, true)
		if err != nil {
			return res, err
		}
		warnings = append(warnings, w...)
		sources = mergeSources(sources, more)
	}
	if len(sources) == 0 {
		return res, errors.New("no ModelSpec files (" + HCLSuffix + ", " + JSONSuffix + ") found in " + strings.Join(paths, ", "))
	}
	models, parse, err := Load(fsys, sources, opts.Modules)
	if err != nil {
		return res, err
	}
	res.Models = models
	res.Files = len(sources)
	res.Findings = append(append(warnings, parse...), Check(models, Options{Profile: opts.Profile})...)
	SortFindings(res.Findings)
	return res, nil
}

// mergeSources adds the sources of more that a does not already have, by
// canonical path, keeping the result sorted by path.
func mergeSources(a, more []Source) []Source {
	have := map[string]bool{}
	for _, s := range a {
		have[s.Canon] = true
	}
	for _, s := range more {
		if !have[s.Canon] {
			have[s.Canon] = true
			a = append(a, s)
		}
	}
	sort.Slice(a, func(i, j int) bool { return a[i].Path < a[j].Path })
	return a
}

// LayoutModule reports the SpecScore layout module a file belongs to: its id and
// every .hcl file of the module (the file's own directory). The id is "" and
// the files nil for a file outside the layout.
func LayoutModule(fsys FS, file string) (id string, files []string, err error) {
	canon, err := fsys.Canonical(file)
	if err != nil {
		return "", nil, err
	}
	id, dir, ok := layoutModule(canon)
	if !ok {
		return "", nil, nil
	}
	entries, err := fsys.ReadDir(filepath.FromSlash(dir))
	if err != nil {
		return "", nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
			files = append(files, filepath.Join(filepath.FromSlash(dir), e.Name()))
		}
	}
	return id, files, nil
}
