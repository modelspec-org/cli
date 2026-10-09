package cli

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

func rewriteCommand(env *Env) *cobra.Command {
	var write, check bool
	var modules []string
	cmd := &cobra.Command{
		Use:   "rewrite [path...]",
		Short: "Rewrite models from the old spelling to the new one",
		Long: `Rewrite ModelSpec models from the old spelling to the new one (decisions 0018,
0020 and 0022):

  HCL   the block type entity becomes record, the block type property directly
        inside a record becomes field, and the attribute entity directly inside a
        member becomes record
  JSON  the format identifier 1.0-draft becomes 1.0-draft-2, and the keys
        entities, properties (of a record) and entity (of a member) become records,
        fields and record

Each path is a file or a directory, searched as lint searches it (hidden
directories and node_modules are skipped; a search follows no symbolic link, and
a model file that is one is reported and not written; a link named on the
command line is followed, and its target is rewritten). With no path, the
current directory. --module <name>=<path>, as for lint, names files that are not
called *.modelspec.hcl or *.modelspec.json (any .hcl file under the path); the
name plays no part. Every file is rewritten on its own: an HCL file and its JSON
copy are each rewritten when both are named or found, and rewriting one of the
two leaves the other as lint will report it.

By default nothing is written: rewrite prints, for each file that would change,
the file and the number of replacements, and exits 0. --write applies the
changes. --check writes nothing and exits 1 when any file would change, so CI can
see that a repository is still in the old spelling.

Only the old spellings are replaced, as byte ranges: every other byte of a file
is as it was (comments, blank lines, alignment, key order, line endings, the final
newline or its absence). A file already in the new spelling is left alone.
Before a file is written, the rewritten text is read again and must be the same
model; the write goes through a temporary file in the same directory and a rename,
and the file keeps its permissions.

A file that rewrite cannot rewrite safely is named, with the reason, on standard
error, and the exit code is 1; nothing is written for it. That is a file that
does not parse, one with a removed construct (collection, recordset) or a
reserved word (projection, index, migration), which no rewriting fixes, and one
that mixes the two vocabularies. A file with other mistakes is rewritten: the
rewrite is syntactic, and lint reports the mistakes.`,
		Example: `  modelspec rewrite                       # what would change in this directory
  modelspec rewrite --write model/        # rewrite the files under model/
  modelspec rewrite --check .             # in CI: fail while a file is in the old spelling`,
		Args: func(_ *cobra.Command, _ []string) error {
			if write && check {
				return usageErrorf("--write and --check cannot be combined: --check writes nothing")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			assign, err := parseAssignments(modules)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			if len(args) == 0 && len(assign) == 0 {
				args = []string{"."}
			}
			return runRewrite(env, args, assign, write, check)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "write the rewritten files (the default is to print what would change)")
	cmd.Flags().BoolVar(&check, "check", false, "write nothing, and exit 1 when any file would change")
	cmd.Flags().StringArrayVar(&modules, "module", nil, "also rewrite the files under a path assigned to a module, whatever they are called: <name>=<path> (repeatable, as for lint)")
	return cmd
}

func runRewrite(env *Env, paths []string, assign []modelspec.Assignment, write, check bool) error {
	files, found, err := modelspec.DiscoverModules(env.FS, paths, assign)
	if err != nil {
		return ioError(err)
	}
	if len(files) == 0 && len(found) == 0 {
		return ioError(fmt.Errorf("no ModelSpec files (%s, %s) found in %v", modelspec.HCLSuffix, modelspec.JSONSuffix, append(paths, assigned(assign)...)))
	}
	// A model file a search met and did not read (a link, a pipe) is not rewritten,
	// and neither is one it could not look at.
	refused := len(found)
	for _, f := range found {
		fmt.Fprintf(env.Stderr, "%s: not rewritten: %s\n", f.File, f.Message)
	}
	var changed, unchanged, replacements int
	for _, f := range files {
		src, err := modelspec.ReadSource(env.FS, f.Path)
		var tooLarge *modelspec.TooLargeError
		if errors.As(err, &tooLarge) {
			refused++
			fmt.Fprintf(env.Stderr, "%s: not rewritten: %v\n", f.Path, err)
			continue
		}
		if err != nil {
			return ioError(err)
		}
		out, n, err := modelspec.Rewrite(f.Path, src)
		if err != nil {
			refused++
			fmt.Fprintf(env.Stderr, "%s: not rewritten: %v\n", f.Path, err)
			continue
		}
		if n == 0 {
			unchanged++
			continue
		}
		changed++
		replacements += n
		verb := "would change"
		if write {
			if err := writeRewritten(env.FS, f.Path, out); err != nil {
				return ioError(err)
			}
			verb = "rewritten"
		}
		if _, err := fmt.Fprintf(env.Stdout, "%s: %s, %s\n", f.Path, verb, count(n, "replacement")); err != nil {
			return ioError(err)
		}
	}
	summary := fmt.Sprintf("%s would change, %d unchanged", count(changed, "file"), unchanged)
	if write {
		summary = fmt.Sprintf("%s rewritten (%s), %d unchanged", count(changed, "file"), count(replacements, "replacement"), unchanged)
	}
	if refused > 0 {
		summary += fmt.Sprintf(", %d not rewritten", refused)
	}
	if _, err := fmt.Fprintln(env.Stdout, summary); err != nil {
		return ioError(err)
	}
	if refused > 0 || (check && changed > 0) {
		return &exitError{code: ExitFindings}
	}
	return nil
}

// writeRewritten writes the rewritten file. A file named on the command line
// that is a symbolic link is followed, and its target is the file written, so
// the link stays a link.
func writeRewritten(fsys modelspec.FS, name string, data []byte) error {
	target := name
	if info, err := fsys.Lstat(name); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		resolved, err := fsys.EvalSymlinks(name)
		if err != nil {
			return err
		}
		target = resolved
	}
	return fsys.WriteFile(target, data, 0o644)
}

// assigned lists the paths of module assignments, for a message.
func assigned(assign []modelspec.Assignment) []string {
	var out []string
	for _, a := range assign {
		out = append(out, a.Path)
	}
	return out
}
