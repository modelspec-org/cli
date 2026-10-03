package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

func exportCommand(env *Env) *cobra.Command {
	var (
		out   string
		check bool
		id    modelspec.ModuleIdentity
	)
	cmd := &cobra.Command{
		Use:   "export <file.modelspec.hcl> [--out <file>]",
		Short: "Write the JSON interchange form of an HCL model, or check a committed copy",
		Long: `Write the JSON interchange form (spec/json-format.md) of an HCL model to
standard output, or to --out.

The JSON form carries a module identity (id, name, version) that the HCL has
no place for, so export needs --module-id, --module-name and --module-version.

With --check, export compares a committed JSON file with what the HCL exports
to and exits 1 when they differ, so CI can catch a stale copy:

  modelspec export --check model.modelspec.hcl model.modelspec.json

The comparison is of the JSON document, with key and array order significant
and whitespace not. The module identity is read from the committed file unless
the --module-* flags are given, so a check covers everything except an
identity nobody states.

export does not run the semantic checks of lint; it refuses only a model that
does not parse, or one that contains an index or projection block, which the
JSON form does not define.`,
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
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := readHCL(env, args[0])
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
	cmd.Flags().StringVar(&id.Name, "module-name", "", "module.name of the JSON form")
	cmd.Flags().StringVar(&id.Version, "module-version", "", "module.version of the JSON form")
	return cmd
}

func ioErrorOrNil(err error) error {
	if err != nil {
		return ioError(err)
	}
	return nil
}

// readHCL reads and parses an HCL file for export, printing parse findings.
func readHCL(env *Env, file string) (*modelspec.Model, error) {
	if !strings.HasSuffix(file, modelspec.HCLSuffix) {
		return nil, usageErrorf("%s: export reads %s files", file, modelspec.HCLSuffix)
	}
	src, err := env.FS.ReadFile(file)
	if err != nil {
		return nil, ioError(err)
	}
	m, findings := modelspec.ParseHCL(file, src)
	for _, f := range findings {
		fmt.Fprintln(env.Stderr, f)
	}
	if modelspec.HasErrors(findings) {
		return nil, &exitError{code: ExitFindings, err: fmt.Errorf("%s does not parse as ModelSpec HCL; fix it (modelspec lint shows the findings) before exporting", file)}
	}
	return m, nil
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
