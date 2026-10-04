package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

// lintReport is the --format json output.
type lintReport struct {
	Files    int                 `json:"files"`
	Notes    []string            `json:"notes"`
	Errors   int                 `json:"errors"`
	Warnings int                 `json:"warnings"`
	Findings []modelspec.Finding `json:"findings"`
}

// parseAssignments reads repeated --module name=path flags.
func parseAssignments(values []string) ([]modelspec.Assignment, error) {
	out := make([]modelspec.Assignment, 0, len(values))
	for _, v := range values {
		name, path, ok := strings.Cut(v, "=")
		if !ok || name == "" || path == "" || strings.Contains(name, ".") {
			return nil, fmt.Errorf("invalid --module %q: expected <name>=<path>, a module name without a dot and a file or directory", v)
		}
		out = append(out, modelspec.Assignment{Module: name, Path: path})
	}
	return out, nil
}

func lintCommand(env *Env) *cobra.Command {
	var format, profile string
	var modules []string
	cmd := &cobra.Command{
		Use:   "lint [path...]",
		Short: "Check ModelSpec models",
		Long: `Check ModelSpec models in HCL (*.modelspec.hcl) and JSON (*.modelspec.json).

Each path is a file or a directory; a directory is searched recursively
(hidden directories and node_modules are skipped; a search follows no symbolic
link, and a model file or a SpecScore models directory that is one is a skipped-file
error: name it on the command line to read it). With no path, lint checks the current
directory. A file reached by two names is read once.

Modules. A module is a set of files, and the module is the unit of checking: if
a file you give belongs to a module with more files on disk, the whole module is
loaded and checked, findings in the other files are reported with their paths,
and a note says so. Concept names are unique per module and references resolve
across all its files:
  - in the SpecScore layout, every .hcl file directly inside
    .../modules/<id>/models/ belongs to module <id>, whatever it is called;
  - otherwise <name>.modelspec.hcl is module <name>, and a JSON file is
    module.name (or its file name without .modelspec.json when it has none);
  - --module <name>=<path> (repeatable; a file or a directory) assigns files to
    a module explicitly and wins over both rules. Assigned files are linted too.
    Assigning only part of a layout module's directory is refused: it would split
    the module.
  - X.modelspec.json beside X.modelspec.hcl is the interchange copy of the same
    module, not a second module (a stale copy is a warning). A JSON file in a
    layout module's models directory, or in a directory assigned to a module that
    has HCL files, is that module's interchange copy too. Two different sources
    claiming one module name are an error where the module is referenced.
A module-qualified reference such as core.Space resolves against the modules in
the files linted together.

Profiles. The default profile checks the standard (spec/core-model.md,
spec/hcl-authoring.md, spec/json-format.md and the decisions) and nothing else.
--profile publish adds what a model needs to be listed in public catalogues
(the requirements of the Directory's JSON reader): a module.name that is an
identifier, at least one entity, properties on every entity, identifier names
for entities and properties, no component-valued properties, and entity
references only within the module.

Output is text by default, or one JSON object with --format json; with
--format json an I/O or usage error (exit 2) is also JSON on standard output:
{"error": "...", "exit": 2}. Findings are sorted by file, line and rule.`,
		Example: `  modelspec lint
  modelspec lint model/chinook.modelspec.hcl model/chinook.modelspec.json
  modelspec lint spec/                                  # a SpecScore tree
  modelspec lint sales.modelspec.hcl --module core=shared/core/
  modelspec lint --profile publish --format json models/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "text" && format != "json" {
				return usageErrorf("invalid --format %q: expected text or json", format)
			}
			prof, err := modelspec.ParseProfile(profile)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			assign, err := parseAssignments(modules)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			if len(args) == 0 && len(assign) == 0 {
				args = []string{"."}
			}
			res, err := modelspec.Lint(env.FS, args, modelspec.LintOptions{Profile: prof, Modules: assign})
			if err != nil {
				return ioError(err)
			}
			rep := lintReport{Files: res.Files, Findings: res.Findings, Notes: res.Notes}
			for _, f := range res.Findings {
				if f.Severity == modelspec.SeverityError {
					rep.Errors++
				} else {
					rep.Warnings++
				}
			}
			if err := writeReport(env, format, rep); err != nil {
				return ioError(err)
			}
			if rep.Errors > 0 {
				return &exitError{code: ExitFindings}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "output format: text or json")
	cmd.Flags().StringVar(&profile, "profile", string(modelspec.ProfileDefault), "rules to apply: default (the standard) or publish (the standard plus what public catalogues require)")
	cmd.Flags().StringArrayVar(&modules, "module", nil, "assign files to a module: <name>=<path> (repeatable)")
	return cmd
}

func writeReport(env *Env, format string, rep lintReport) error {
	if format == "json" {
		if rep.Findings == nil {
			rep.Findings = []modelspec.Finding{}
		}
		if rep.Notes == nil {
			rep.Notes = []string{}
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	for _, n := range rep.Notes {
		if _, err := fmt.Fprintf(env.Stdout, "note: %s\n", n); err != nil {
			return err
		}
	}
	for _, f := range rep.Findings {
		if _, err := fmt.Fprintln(env.Stdout, f); err != nil {
			return err
		}
	}
	verdict := "ok"
	if rep.Errors > 0 {
		verdict = "failed"
	}
	_, err := fmt.Fprintf(env.Stdout, "%s: %s checked, %s, %s\n", verdict, count(rep.Files, "file"), count(rep.Errors, "error"), count(rep.Warnings, "warning"))
	return err
}

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
