package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

// lintReport is the --format json output.
type lintReport struct {
	Files    int                 `json:"files"`
	Errors   int                 `json:"errors"`
	Warnings int                 `json:"warnings"`
	Findings []modelspec.Finding `json:"findings"`
}

func lintCommand(env *Env) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "lint [path...]",
		Short: "Check ModelSpec models",
		Long: `Check ModelSpec models in HCL (*.modelspec.hcl) and JSON (*.modelspec.json).

Each path is a file or a directory; a directory is searched recursively
(hidden directories and node_modules are skipped). With no path, lint checks
the current directory. Files given together are checked together, so a
module-qualified reference such as core.Space resolves against the other
files by module name: the file name without .modelspec.hcl, or module.name
in JSON.

Output is text by default, or one JSON object with --format json. Findings
are sorted by file, line and rule.`,
		Example: `  modelspec lint
  modelspec lint model/chinook.modelspec.hcl model/chinook.modelspec.json
  modelspec lint --format json models/`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "text" && format != "json" {
				return usageErrorf("invalid --format %q: expected text or json", format)
			}
			if len(args) == 0 {
				args = []string{"."}
			}
			findings, files, err := modelspec.Lint(env.FS, args)
			if err != nil {
				return ioError(err)
			}
			rep := lintReport{Files: files, Findings: findings}
			for _, f := range findings {
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
	return cmd
}

func writeReport(env *Env, format string, rep lintReport) error {
	if format == "json" {
		if rep.Findings == nil {
			rep.Findings = []modelspec.Finding{}
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
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
