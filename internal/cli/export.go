package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

func exportCommand(env *Env) *cobra.Command {
	var (
		out     string
		check   bool
		id      modelspec.ModuleIdentity
		modules []string
	)
	cmd := &cobra.Command{
		Use:   "export <file.modelspec.hcl> [--out <file>]",
		Short: "Write the JSON interchange form of an HCL model, or check a committed copy",
		Long: `Write the JSON interchange form (spec/json-format.md) of an HCL model to
standard output, or to --out.

The JSON form carries a module identity that the HCL has no place for, so
export needs --module-id and --module-version (the format requires those two);
--module-name is written when given, and also when the model refers to its own
module by name (so the JSON lints clean whatever file it is saved as).

export lints the file first, as lint does under the default profile (so the module
the file belongs to is checked whole, as lint checks it), and refuses a file with
errors (exit 1, the findings for the file on standard error). A file that refers to
other modules needs them supplied with --module <name>=<path> (repeatable), as
for lint; those files are used to resolve references and are not exported. If one
of those modules has a file that was found and not read (a symbolic link, a pipe, a
device), references into it were not checked: the export is written and the exit code
is as before, and standard error has the skipped-file finding and a note.
export reads HCL: any .hcl file of a SpecScore models directory is accepted, whatever
it is called, and a JSON file is a usage error.

--out writes a regular file, through a temporary file in the same directory and a
rename, and refuses a path that is one of the inputs (the model, or a file supplied
with --module), which would replace it with its JSON.

With --check, export compares a committed JSON file with what the HCL exports to
and exits 1 when they differ, so CI can catch a stale copy:

  modelspec export --check model.modelspec.hcl model.modelspec.json

The comparison is of the JSON document: whitespace does not count, but the order
of keys in objects and of items in arrays does (the Directory compares a model with
its registered copy the same way). The module identity is read from the committed
file unless the --module-* flags are given. --check refuses an HCL file with
errors too, and cannot be combined with --out.

The JSON form is one document per module, and the standard does not say how the
files of a module are merged, so a file that is one of several .hcl files of a
module (the SpecScore layout, or --module) is refused, with the other files named.
A model that refers to its own module by name cannot be exported under another
module.name (the JSON would not lint clean on its own) and is refused. A file with an
index, projection or migration block is refused too: the standard does not define
their JSON form.`,
		Example: `  modelspec export model/chinook.modelspec.hcl --out model/chinook.modelspec.json \
    --module-id github.com/acme/chinook/model/chinook --module-name chinook --module-version 0.1.0
  modelspec export --check model/chinook.modelspec.hcl model/chinook.modelspec.json`,
		Args: func(_ *cobra.Command, args []string) error {
			if check && len(args) != 2 {
				return usageErrorf("export --check takes the HCL file and the committed JSON file")
			}
			if !check && len(args) != 1 {
				return usageErrorf("export takes one HCL file")
			}
			if check && out != "" {
				return usageErrorf("--out cannot be combined with --check, which writes nothing")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			assign, err := parseAssignments(modules)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			if out != "" {
				if err := checkOutput(env.FS, out); err != nil {
					return err
				}
			}
			m, loaded, err := lintForExport(env, args[0], assign)
			if err != nil {
				return err
			}
			if out != "" {
				if err := refuseInputAsOutput(env.FS, out, loaded); err != nil {
					return err
				}
			}
			if check {
				return runCheck(env, m, args[1], id)
			}
			node, err := m.JSON(id)
			if err != nil {
				return &exitError{code: ExitFindings, err: err}
			}
			twin := node.Encode()
			limit := env.MaxTwinBytes
			if limit == 0 {
				limit = modelspec.MaxInputBytes
			}
			if len(twin) > limit {
				return &exitError{code: ExitFindings, err: fmt.Errorf("the JSON form of %s is %d bytes, over the %d-byte limit on every file this tool reads, so lint and export --check would refuse it; nothing was written", args[0], len(twin), limit)}
			}
			if out == "" {
				_, err = env.Stdout.Write(twin)
				return ioErrorOrNil(err)
			}
			return ioErrorOrNil(env.FS.WriteFile(out, twin, 0o644))
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "write the JSON to this file instead of standard output")
	cmd.Flags().BoolVar(&check, "check", false, "compare a committed JSON file with the export instead of writing")
	cmd.Flags().StringVar(&id.ID, "module-id", "", "module.id of the JSON form")
	cmd.Flags().StringVar(&id.Name, "module-name", "", "module.name of the JSON form (optional)")
	cmd.Flags().StringVar(&id.Version, "module-version", "", "module.version of the JSON form")
	cmd.Flags().StringArrayVar(&modules, "module", nil, "assign files to a module, to resolve references: <name>=<path> (repeatable)")
	return cmd
}

// checkOutput refuses an output path that exists and is not a regular file, without
// following a link: --out writes a file, and a link would send the write to whatever it
// points to, a pipe would wait for a reader, and a device would take it. A path that
// does not exist, and a regular file that is overwritten, are as they were.
func checkOutput(fsys modelspec.FS, out string) error {
	info, err := fsys.Lstat(out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ioError(err)
	}
	if !info.Mode().IsRegular() {
		return ioError(fmt.Errorf("%s is %s, not a regular file; --out writes regular files only (to write elsewhere, leave --out out and redirect standard output)", out, modelspec.FileKind(info.Mode())))
	}
	return nil
}

// refuseInputAsOutput refuses an output path that is one of the files the run read as a
// source: the model itself, or a file supplied with --module. Writing it would replace the
// model with its JSON. The JSON copy of a module (a twin) is what export writes, so it is
// not a source.
func refuseInputAsOutput(fsys modelspec.FS, out string, loaded []*modelspec.Model) error {
	target, err := fsys.Stat(out)
	if err != nil {
		return nil // there is nothing at the path yet, so it is no input
	}
	for _, m := range loaded {
		if in, err := fsys.Stat(m.File); err == nil && !m.Twin && fsys.SameFile(target, in) {
			return usageErrorf("--out %s is the input %s, and writing it would replace the model with its JSON; write to another file", out, m.File)
		}
	}
	return nil
}

func ioErrorOrNil(err error) error {
	if err != nil {
		return ioError(err)
	}
	return nil
}

// lintForExport lints the file with the loader lint uses (so the module is
// checked whole, and the modules it needs are supplied) and returns its model when
// it has no error, with every model the run loaded. The findings for the file go to
// standard error.
func lintForExport(env *Env, file string, assign []modelspec.Assignment) (*modelspec.Model, []*modelspec.Model, error) {
	info, err := env.FS.Stat(file)
	if err != nil {
		return nil, nil, ioError(err)
	}
	if info.IsDir() {
		return nil, nil, usageErrorf("%s is a directory; export takes one HCL file (lint takes directories)", file)
	}
	res, err := modelspec.Lint(env.FS, []string{file}, modelspec.LintOptions{Modules: assign})
	if err != nil {
		return nil, nil, ioError(err)
	}
	// A file that Stat found and is not a directory is the one file Lint loaded
	// under the name it was given, so exactly one model has it for its File.
	var model *modelspec.Model
	for _, m := range res.Models {
		if m.File == filepath.Clean(file) {
			model = m
		}
	}
	if model.Form != modelspec.FormHCL {
		return nil, nil, usageErrorf("%s: export reads HCL files, and this is the JSON form", file)
	}
	// Export writes whole modules: a file of the module that a search met and did not
	// read (a link, a pipe, a device) makes what was read a part of it. That is decided
	// from the loaded state (the module's models are marked Incomplete), not from the
	// findings, which are capped and can have lost the one about the file.
	if model.Incomplete {
		var files []string
		for _, s := range res.Skipped {
			for _, affected := range s.Affected {
				if affected == model {
					fmt.Fprintln(env.Stderr, s.Finding)
					name := s.Finding.File
					if s.Module != "" {
						name += fmt.Sprintf(" (assigned to module %q with --module)", s.Module)
					}
					files = append(files, name)
					break
				}
			}
		}
		return nil, nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s: module %s has %d file(s) found and not read (%s), so what was read is not the whole module; export writes and checks whole modules only. Replace the link with the file, or name it on the command line", file, model.Name, len(files), strings.Join(files, ", "))}
	}
	var siblings []string
	for _, m := range res.Models {
		if m.Form == modelspec.FormHCL && !m.Twin && m != model && m.Group == model.Group {
			siblings = append(siblings, m.File)
		}
	}
	if len(siblings) > 0 {
		return nil, nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s is one of %d files of module %s (%s); the JSON form is one document per module and the standard does not say how a module's files are merged, so there is nothing to export", file, len(siblings)+1, model.Name, strings.Join(siblings, ", "))}
	}
	var mine []modelspec.Finding
	for _, f := range res.Findings {
		if f.File == model.File {
			mine = append(mine, f)
			fmt.Fprintln(env.Stderr, f)
		}
	}
	if modelspec.HasErrors(mine) {
		return nil, nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s has errors; fix them (modelspec lint shows the same findings) before exporting", file)}
	}
	// What was skipped now belongs to other modules, supplied with --module: the model's own
	// export is whole and stays as it is, but a reference into them was not checked.
	noted := map[string]bool{}
	for _, s := range res.Skipped {
		fmt.Fprintln(env.Stderr, s.Finding)
		if !noted[s.Module] {
			noted[s.Module] = true
			fmt.Fprintf(env.Stderr, "note: module %s was not read whole (see the skipped-file error above), so references into it were not checked\n", s.Module)
		}
	}
	return model, res.Models, nil
}

func runCheck(env *Env, m *modelspec.Model, jsonFile string, id modelspec.ModuleIdentity) error {
	committed, err := modelspec.ReadSource(env.FS, jsonFile)
	var tooLarge *modelspec.TooLargeError
	if errors.As(err, &tooLarge) {
		return &exitError{code: ExitFindings, err: err}
	}
	if err != nil {
		return ioError(err)
	}
	if problem := m.ExportDrift(committed, id); problem != "" {
		return &exitError{code: ExitFindings, err: fmt.Errorf("%s", problem)}
	}
	_, err = fmt.Fprintf(env.Stdout, "ok: %s is what %s exports to\n", jsonFile, m.File)
	return ioErrorOrNil(err)
}
