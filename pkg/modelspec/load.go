package modelspec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// FS is the filesystem the loader reads and writes through. OSFS is the real
// one; tests use in-memory implementations.
type FS interface {
	// Open opens a file for reading. It is read only through ReadSource, which
	// refuses what is not a regular file and bounds how much is read.
	Open(name string) (fs.File, error)
	ReadDir(name string) ([]fs.DirEntry, error)
	// Stat follows symbolic links; Lstat does not.
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	// Abs returns the absolute form of a path, cleaned, with symbolic links left
	// as they are: the path as the file was given or found. The SpecScore layout
	// is read from it.
	Abs(name string) (string, error)
	// SameFile reports whether two FileInfos from Stat describe one file, however
	// it was reached: by a relative or an absolute path, through a symbolic link,
	// under another spelling of the same name on a case-insensitive filesystem.
	SameFile(a, b fs.FileInfo) bool
}

// TooLargeError is the error of a file over MaxInputBytes, which is not read
// (further than the limit). Partial is set when the size is not known: the file
// grew after it was checked, and Size is how much was read before reading stopped.
type TooLargeError struct {
	File    string
	Size    int64
	Partial bool
}

func (e *TooLargeError) Error() string {
	if e.Partial {
		return fmt.Sprintf("%s: file is more than %d bytes; the limit is %d bytes", e.File, e.Size-1, MaxInputBytes)
	}
	return fmt.Sprintf("%s: file is %d bytes; the limit is %d bytes", e.File, e.Size, MaxInputBytes)
}

// NotRegularError is the error of a file that is not a regular file: a device, a
// named pipe, a socket or a directory, which has no size and may never end.
type NotRegularError struct {
	File string
	What string // what it is: "a named pipe", "a symbolic link to a character device"
}

func (e *NotRegularError) Error() string {
	return fmt.Sprintf("%s is %s, not a regular file; modelspec reads regular files only", e.File, e.What)
}

// FileKind says what a file that is not a regular file is: "a directory", "a named
// pipe", "a symbolic link", "a character device".
func FileKind(mode fs.FileMode) string { return fileKind(mode) }

// fileKind says what a file that is not a regular file is.
func fileKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeCharDevice != 0:
		return "a character device"
	case mode&fs.ModeDevice != 0:
		return "a device"
	default:
		return "not an ordinary file"
	}
}

// notRegular returns the error for a path whose target is not a regular file
// (nil when it is), saying whether the path is a symbolic link to it.
func notRegular(fsys FS, name string, target fs.FileInfo) error {
	if target.Mode().IsRegular() {
		return nil
	}
	what := fileKind(target.Mode())
	if li, err := fsys.Lstat(name); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		what = "a symbolic link to " + what
	}
	return &NotRegularError{File: name, What: what}
}

// ReadSource reads a file, which every command that reads a model goes through,
// for every kind of file (a model, the JSON operand of export --check, a --module
// path). A symbolic link is followed to its target, which must be a regular file:
// that is checked before the file is opened (opening a named pipe waits for a
// writer), and again on the opened file, and what is read is at most
// MaxInputBytes+1, so a file that grows after the checks, or that reports a size
// of zero and never ends, is still bounded. One over the limit is refused with a
// *TooLargeError, and one that is not regular with a *NotRegularError.
func ReadSource(fsys FS, name string) ([]byte, error) {
	info, err := fsys.Stat(name)
	if err != nil {
		return nil, err
	}
	if err := notRegular(fsys, name, info); err != nil {
		return nil, err
	}
	if info.Size() > MaxInputBytes {
		return nil, &TooLargeError{File: name, Size: info.Size()}
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := notRegular(fsys, name, opened); err != nil {
		return nil, err
	}
	src, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(src) > MaxInputBytes {
		return nil, &TooLargeError{File: name, Size: int64(len(src)), Partial: true}
	}
	return src, nil
}

// OSFS is the operating system's filesystem. The zero value is ready to use.
type OSFS struct {
	getwd func() (string, error) // os.Getwd when nil; a seam for tests
}

// Open opens without waiting: opening a named pipe for reading otherwise blocks
// until a writer comes, and ReadSource must be able to refuse it.
func (OSFS) Open(name string) (fs.File, error) {
	return os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
func (OSFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }
func (OSFS) Stat(name string) (fs.FileInfo, error)      { return os.Stat(name) }
func (OSFS) Lstat(name string) (fs.FileInfo, error)     { return os.Lstat(name) }
func (OSFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (OSFS) SameFile(a, b fs.FileInfo) bool { return os.SameFile(a, b) }
func (o OSFS) Abs(name string) (string, error) {
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
	return filepath.Clean(name), nil
}

// IsModelFile reports whether a file name is a standalone ModelSpec model file.
func IsModelFile(name string) bool {
	return strings.HasSuffix(name, HCLSuffix) || strings.HasSuffix(name, JSONSuffix)
}

// layoutDir recognises the SpecScore layout from a file's absolute path as it was
// given or found, symbolic links not resolved: a file directly inside a directory
// …/modules/<id>/models/ belongs to module <id> (spec/hcl-authoring.md, "Open
// Questions"). dir is that models directory.
func layoutDir(abs string) (id, dir string, ok bool) {
	parts := strings.Split(filepath.ToSlash(abs), "/")
	n := len(parts)
	if n < 4 || parts[n-2] != "models" || parts[n-4] != "modules" {
		return "", "", false
	}
	return parts[n-3], strings.Join(parts[:n-1], "/"), true
}

// layoutModule is layoutDir for an HCL file: any .hcl file there is a model of
// the module, whatever it is called.
func layoutModule(abs string) (id, dir string, ok bool) {
	if !strings.HasSuffix(abs, ".hcl") {
		return "", "", false
	}
	return layoutDir(abs)
}

// skipDir reports whether a directory is left out of a search: hidden
// directories and node_modules.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules"
}

// Source is a model file to read: Path as given or found (used in findings) and
// its absolute form, symbolic links not resolved (used for the module layout).
type Source struct {
	Path string
	Abs  string
}

// discovery collects the model files of a run, once each.
type discovery struct {
	warned   map[string]bool // a warning is given once for a file
	fsys     FS
	seen     map[string]bool          // by Abs
	infos    map[string][]fs.FileInfo // by size and time: candidates for SameFile
	out      []Source
	findings []Finding
	skipped  []skipped // files met and not read, see skip
	module   string    // the module being assigned (--module), while its path is searched
}

// skipped is a file a search did not read, and the module a --module assignment
// gave it ("" when it was not found through one).
type skipped struct {
	Source
	module string
}

func newDiscovery(fsys FS) *discovery {
	return &discovery{fsys: fsys, warned: map[string]bool{}, seen: map[string]bool{}, infos: map[string][]fs.FileInfo{}}
}

// add records a file unless the same file is already there; it reports whether
// the file was new.
func (d *discovery) add(s Source, info fs.FileInfo) bool {
	if d.seen[s.Abs] {
		return false
	}
	key := fmt.Sprintf("%d/%d", info.Size(), info.ModTime().UnixNano())
	for _, other := range d.infos[key] {
		if d.fsys.SameFile(other, info) {
			return false
		}
	}
	d.seen[s.Abs] = true
	d.infos[key] = append(d.infos[key], info)
	d.out = append(d.out, s)
	return true
}

func (d *discovery) sorted() []Source {
	sort.Slice(d.out, func(i, j int) bool { return d.out[i].Path < d.out[j].Path })
	return d.out
}

// isModel reports whether a file is a model file: a standalone model file by
// name, or any .hcl file in a SpecScore layout models directory; with anyHCL (a
// --module assignment) any .hcl file.
func isModel(name, abs string, anyHCL bool) bool {
	if IsModelFile(name) || (anyHCL && strings.HasSuffix(name, ".hcl")) {
		return true
	}
	_, _, ok := layoutModule(abs)
	return ok
}

// addPath adds one path given on the command line: a file, or a directory searched
// recursively.
func (d *discovery) addPath(p string, anyHCL bool) error {
	info, err := d.fsys.Stat(p)
	if err != nil {
		return err
	}
	abs, err := d.fsys.Abs(p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return d.walk(p, anyHCL)
	}
	if !isModel(p, abs, anyHCL) {
		return fmt.Errorf("%s: not a ModelSpec file (expected a name ending in %s or %s, or a .hcl file in a SpecScore models directory)", p, HCLSuffix, JSONSuffix)
	}
	// A file named on the command line may be a link to a regular file.
	if err := notRegular(d.fsys, p, info); err != nil {
		return err
	}
	d.add(Source{Path: filepath.Clean(p), Abs: abs}, info)
	return nil
}

func (d *discovery) walk(dir string, anyHCL bool) error {
	entries, err := d.fsys.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if !skipDir(e.Name()) {
				if err := d.walk(full, anyHCL); err != nil {
					return err
				}
			}
			continue
		}
		d.found(full, e.Name(), anyHCL)
	}
	return nil
}

// warn records a warning about a file, once.
func (d *discovery) warn(file, message string) {
	if !d.warned[file+message] {
		d.warned[file+message] = true
		d.findings = append(d.findings, Finding{File: file, Rule: RuleIO, Severity: SeverityWarning, Message: message})
	}
}

// skip records a model-named file that a search does not read, once: an error
// (rule skipped-file), because the run did not check what it was asked to, and a
// note of the file, whose module is not checked (markIncomplete).
func (d *discovery) skip(s Source, what string) {
	message := "is " + what + ", which a search does not read, so this file was not checked and neither is its module; replace it with the file, or name it on the command line"
	if !d.warned[s.Path+message] {
		d.warned[s.Path+message] = true
		d.findings = append(d.findings, Finding{File: s.Path, Rule: RuleSkipped, Severity: SeverityError, Message: message})
		d.skipped = append(d.skipped, skipped{Source: s, module: d.module})
	}
}

// found adds a file a search met when it is a model file; a file that cannot be
// read is a warning, not the end of the run. A search does not follow symbolic
// links: a link that would have been a model file is a warning that names it
// (a repository can point one at a device, or at a file outside it), and so is a
// model file that is not a regular file.
func (d *discovery) found(full, name string, anyHCL bool) bool {
	info, err := d.fsys.Lstat(full)
	if err != nil {
		d.warn(full, "cannot be read, so the file was not checked: "+err.Error())
		return false
	}
	abs, err := d.fsys.Abs(full)
	if err != nil {
		d.warn(full, "cannot be read, so the file was not checked: "+err.Error())
		return false
	}
	if d.seen[abs] || !isModel(name, abs, anyHCL) {
		return false // met already (a link named on the command line, then met in its directory)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		d.skip(Source{Path: full, Abs: abs}, "a symbolic link")
		return false
	case !info.Mode().IsRegular():
		d.skip(Source{Path: full, Abs: abs}, fileKind(info.Mode()))
		return false
	}
	return d.add(Source{Path: full, Abs: abs}, info)
}

// Discover expands paths into model files. A file is taken as given (it must be
// a model file); a directory is searched recursively for *.modelspec.hcl and
// *.modelspec.json, and in a SpecScore layout models directory for any *.hcl.
// Hidden directories and node_modules are skipped. A search follows no symbolic
// link, to a directory or to a file: a link that would have been a model file is
// a warning that names it. A file named on the command line may be a link to a
// regular file, and is read through. A file reached by two names
// is returned once, under the first name. The result is sorted by path. The
// findings are warnings for files a search found but cannot read.
func Discover(fsys FS, paths []string) ([]Source, []Finding, error) {
	return discover(fsys, paths, false)
}

// discover is Discover; anyHCL also accepts any .hcl file, as a --module
// assignment does.
func discover(fsys FS, paths []string, anyHCL bool) ([]Source, []Finding, error) {
	d := newDiscovery(fsys)
	for _, p := range paths {
		if err := d.addPath(p, anyHCL); err != nil {
			return nil, nil, err
		}
	}
	SortFindings(d.findings)
	return d.sorted(), d.findings, nil
}

// expand adds the files that a module's files imply, so that a module is always
// checked whole: for a file in a SpecScore layout models directory, the other .hcl
// files of that directory; for X.modelspec.hcl or X.modelspec.json, its partner of
// the same name beside it. It returns a note for each module it completed. A
// module directory that cannot be listed is an error: the module cannot be checked
// whole, and a quiet partial check would pass what it did not see.
func (d *discovery) expand() ([]string, error) {
	var notes []string
	doneDir := map[string]bool{}
	for _, s := range append([]Source(nil), d.sorted()...) {
		dir := filepath.Dir(s.Path)
		if id, absDir, ok := layoutDir(s.Abs); ok && !doneDir[absDir] {
			doneDir[absDir] = true
			entries, err := d.fsys.ReadDir(dir)
			if err != nil {
				return nil, err
			}
			added := 0
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") && d.found(filepath.Join(dir, e.Name()), e.Name(), false) {
					added++
				}
			}
			if added > 0 {
				notes = append(notes, fmt.Sprintf("a module is the unit of checking: module %q has more files in %s than were given, so the whole module was checked", id, filepath.ToSlash(dir)))
			}
		}
		base := filepath.Base(s.Path)
		var partner string
		switch {
		case strings.HasSuffix(base, JSONSuffix):
			partner = strings.TrimSuffix(base, JSONSuffix) + HCLSuffix
		case strings.HasSuffix(base, HCLSuffix):
			partner = strings.TrimSuffix(base, HCLSuffix) + JSONSuffix
		default:
			continue
		}
		path := filepath.Join(dir, partner)
		if info, err := d.fsys.Lstat(path); err == nil && !info.IsDir() && d.found(path, partner, false) {
			notes = append(notes, fmt.Sprintf("%s and %s are one module (the JSON is the interchange copy of the HCL), so both were checked", filepath.ToSlash(s.Path), filepath.ToSlash(path)))
		}
	}
	SortFindings(d.findings)
	return notes, nil
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
// A JSON file is the interchange copy (a Twin) of the module it sits with, and
// not a second module, when it is assigned to a module that has HCL files, sits in
// the models directory of a layout module, or is X.modelspec.json beside
// X.modelspec.hcl (both among the files); an explicit assignment of the JSON file
// itself to another module wins. A file larger than MaxInputBytes is not read. A
// file that cannot be read is an error, not a finding.
func Load(fsys FS, files []Source, assign []Assignment) ([]*Model, []Finding, error) {
	explicit, err := explicitModules(fsys, assign)
	if err != nil {
		return nil, nil, err
	}
	var models []*Model
	var findings []Finding
	hclAt := map[string]*Model{} // by Abs
	for _, f := range files {
		var m *Model
		src, err := ReadSource(fsys, f.Path)
		var tooLarge *TooLargeError
		switch {
		case errors.As(err, &tooLarge):
			m = &Model{File: f.Path, Form: FormHCL, Name: moduleNameFromFile(f.Path), Broken: true}
			if strings.HasSuffix(f.Path, JSONSuffix) {
				m.Form = FormJSON
			}
			findings = append(findings, oversize(f.Path, tooLarge.Size, tooLarge.Partial))
		case err != nil:
			return nil, nil, err
		default:
			var parse []Finding
			m, parse = Parse(f.Path, src)
			findings = append(findings, parse...)
		}
		m.Group = f.Abs
		models = append(models, m)
		if name, ok := explicit[f.Abs]; ok {
			m.Name = name
			if m.Form == FormHCL {
				m.Group = "--module " + name
			}
		} else if id, dir, ok := layoutModule(f.Abs); ok && m.Form == FormHCL {
			m.Name, m.Group = id, dir
		}
		if m.Form == FormHCL {
			hclAt[f.Abs] = m
		}
	}
	groupSize := map[string]int{}
	for _, m := range models {
		if m.Form == FormHCL {
			groupSize[m.Group]++
		}
	}
	for i, m := range models {
		if m.Form != FormJSON {
			continue
		}
		abs := files[i].Abs
		stem := strings.TrimSuffix(abs, JSONSuffix) + HCLSuffix
		switch name, isExplicit := explicit[abs]; {
		case isExplicit:
			m.Twin = groupSize["--module "+name] > 0
		default:
			if id, dir, ok := layoutDir(abs); ok && groupSize[dir] > 0 {
				m.Twin, m.Name = true, id
				if h := hclAt[stem]; h != nil && groupSize[dir] == 1 {
					m.TwinOf = h
				}
			} else if h := hclAt[stem]; h != nil && strings.HasSuffix(abs, JSONSuffix) {
				m.Twin, m.Name, m.TwinOf = true, h.Name, h
			}
		}
	}
	SortFindings(findings)
	return models, findings, nil
}

// markIncomplete marks the models of every module that has a skipped file: the
// files of the models directory of a layout module (the skipped one is in it), the
// other form of X.modelspec.hcl and X.modelspec.json, and the files assigned to
// the same module with --module. models[i] was read from files[i].
func markIncomplete(models []*Model, files []Source, skips []skipped, explicit map[string]string) {
	for _, s := range skips {
		_, layout, inLayout := layoutDir(s.Abs)
		stem := strings.TrimSuffix(strings.TrimSuffix(s.Abs, HCLSuffix), JSONSuffix)
		for i, f := range files {
			_, fileLayout, fileInLayout := layoutDir(f.Abs)
			sameStem := (strings.HasSuffix(f.Abs, HCLSuffix) || strings.HasSuffix(f.Abs, JSONSuffix)) && strings.TrimSuffix(strings.TrimSuffix(f.Abs, HCLSuffix), JSONSuffix) == stem
			if sameStem || (inLayout && fileInLayout && fileLayout == layout) || (s.module != "" && explicit[f.Abs] == s.module) {
				models[i].Incomplete = true
			}
		}
	}
}

// explicitModules reads the --module assignments: the absolute path of every
// assigned file and the module it is assigned to. A file assigned to two
// different modules is an error, and so is assigning part of a layout module's
// directory, which would split the module.
func explicitModules(fsys FS, assign []Assignment) (map[string]string, error) {
	out := map[string]string{}
	paths := map[string]string{}
	for _, a := range assign {
		files, _, err := discover(fsys, []string{a.Path}, true)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if prev, ok := out[f.Abs]; ok && prev != a.Module {
				return nil, fmt.Errorf("%s is assigned to module %q and to module %q", f.Path, prev, a.Module)
			}
			out[f.Abs] = a.Module
			paths[f.Abs] = f.Path
		}
	}
	checked := map[string]bool{}
	for abs, name := range out {
		_, dir, ok := layoutModule(abs)
		if !ok || checked[dir] {
			continue
		}
		checked[dir] = true
		entries, err := fsys.ReadDir(filepath.FromSlash(dir))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") && out[filepath.ToSlash(filepath.Join(filepath.FromSlash(dir), e.Name()))] != name {
				return nil, fmt.Errorf("%s is in the models directory of a SpecScore layout module, whose files are one module; assigning part of it to %q would split it, so assign the whole directory: --module %s=%s", paths[abs], name, name, filepath.Dir(paths[abs]))
			}
		}
	}
	return out, nil
}

// Result is the outcome of Lint.
type Result struct {
	Findings []Finding
	Files    int
	Models   []*Model
	// Notes say what Lint did beyond the paths given: a module is the unit of
	// checking, so the files of a module that were not given were checked too.
	Notes []string
}

// Lint discovers, loads and checks the paths together with the files assigned by
// opts.Modules: the parse findings and the semantic findings, sorted. A module is
// always checked whole: when a file given belongs to a module with more files on
// disk (a SpecScore layout models directory, or the HCL and JSON of one module),
// the others are loaded too, and their findings are reported with their paths.
// With no paths and no assignments it is an error, as is finding no model file.
func Lint(fsys FS, paths []string, opts LintOptions) (Result, error) {
	var res Result
	var err error
	d := newDiscovery(fsys)
	for _, p := range paths {
		if err := d.addPath(p, false); err != nil {
			return res, err
		}
	}
	for _, a := range opts.Modules {
		d.module = a.Module
		if err := d.addPath(a.Path, true); err != nil {
			return res, err
		}
	}
	d.module = ""
	if len(d.out) == 0 && len(d.skipped) == 0 {
		return res, errors.New("no ModelSpec files (" + HCLSuffix + ", " + JSONSuffix + ") found in " + strings.Join(paths, ", "))
	}
	if res.Notes, err = d.expand(); err != nil {
		return res, err
	}
	sources := d.sorted()
	models, parse, err := Load(fsys, sources, opts.Modules)
	if err != nil {
		return res, err
	}
	if len(d.skipped) > 0 {
		explicit, _ := explicitModules(fsys, opts.Modules) // Load has just read them without error
		markIncomplete(models, sources, d.skipped, explicit)
	}
	res.Models = models
	res.Files = len(sources)
	res.Findings = append(append(d.findings, parse...), Check(models, Options{Profile: opts.Profile})...)
	SortFindings(res.Findings)
	res.Findings = capFindings(res.Findings)
	return res, nil
}
