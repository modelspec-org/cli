package cli

import (
	"fmt"
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
for lint; those files are used to resolve references and are not exported.
export reads HCL: any .hcl file of a SpecScore models directory is accepted, whatever
it is called, and a JSON file is a usage error.

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
			m, err := lintForExport(env, args[0], assign)
			if err != nil {
				return err
			}
			if check {
				return runCheck(env, m, args[1], id)
			}
			node, err := m.JSON(id)
			if err != nil {
				return &exitError{code: ExitFindings, err: err}
			}
			if out == "" {
				_, err = env.Stdout.Write(node.Encode())
				return ioErrorOrNil(err)
			}
			return ioErrorOrNil(env.FS.WriteFile(out, node.Encode(), 0o644))
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

func ioErrorOrNil(err error) error {
	if err != nil {
		return ioError(err)
	}
	return nil
}

// lintForExport lints the file with the loader lint uses (so the module is
// checked whole, and the modules it needs are supplied) and returns its model when
// it has no error. The findings for the file go to standard error.
func lintForExport(env *Env, file string, assign []modelspec.Assignment) (*modelspec.Model, error) {
	info, err := env.FS.Stat(file)
	if err != nil {
		return nil, ioError(err)
	}
	if info.IsDir() {
		return nil, usageErrorf("%s is a directory; export takes one HCL file (lint takes directories)", file)
	}
	res, err := modelspec.Lint(env.FS, []string{file}, modelspec.LintOptions{Modules: assign})
	if err != nil {
		return nil, ioError(err)
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
		return nil, usageErrorf("%s: export reads HCL files, and this is the JSON form", file)
	}
	var siblings []string
	for _, m := range res.Models {
		if m.Form == modelspec.FormHCL && !m.Twin && m != model && m.Group == model.Group {
			siblings = append(siblings, m.File)
		}
	}
	if len(siblings) > 0 {
		return nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s is one of %d files of module %s (%s); the JSON form is one document per module and the standard does not say how a module's files are merged, so there is nothing to export", file, len(siblings)+1, model.Name, strings.Join(siblings, ", "))}
	}
	var mine []modelspec.Finding
	for _, f := range res.Findings {
		if f.File == model.File {
			mine = append(mine, f)
			fmt.Fprintln(env.Stderr, f)
		}
	}
	if modelspec.HasErrors(mine) {
		return nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s has errors; fix them (modelspec lint shows the same findings) before exporting", file)}
	}
	return model, nil
}

func runCheck(env *Env, m *modelspec.Model, jsonFile string, id modelspec.ModuleIdentity) error {
	committed, err := env.FS.ReadFile(jsonFile)
	if err != nil {
		return ioError(err)
	}
	if problem := m.ExportDrift(committed, id); problem != "" {
		return &exitError{code: ExitFindings, err: fmt.Errorf("%s", problem)}
	}
	_, err = fmt.Fprintf(env.Stdout, "ok: %s is what %s exports to\n", jsonFile, m.File)
	return ioErrorOrNil(err)
}
