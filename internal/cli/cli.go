// Package cli is the modelspec command line: lint, export, rewrite, version and
// self-update. Everything the commands touch (filesystem, output streams,
// build information, the update source) comes in through Env, so tests run
// them in memory.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	vercmd "github.com/strongo/buildinfo/cobracmd"
	"github.com/strongo/cli-helpers/selfupdate"
	"github.com/strongo/cli-helpers/selfupdate/cobracmd"

	"github.com/modelspec-org/cli/pkg/modelspec"
)

// Exit codes. Lint and export use 0, 1 and 2; 10 is reserved for
// `self-update --check` finding a newer release.
const (
	ExitOK              = 0
	ExitFindings        = 1
	ExitUsage           = 2
	ExitUpdateAvailable = 10
)

// Env is everything a command touches outside its own arguments.
type Env struct {
	Stdout, Stderr io.Writer
	FS             modelspec.FS
	Build          buildinfo.Info
	Update         selfupdate.Config
	// Interactive reports whether stdin is a terminal; nil means the
	// self-update library's own check.
	Interactive func() bool
	// MaxTwinBytes is the largest JSON twin export writes; zero means
	// modelspec.MaxInputBytes, the largest file lint reads. A seam for tests.
	MaxTwinBytes int
}

// OSEnv is the environment of the real process.
func OSEnv() *Env {
	info := buildinfo.Get("modelspec")
	return &Env{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		FS:     modelspec.OSFS{},
		Build:  info,
		Update: selfupdate.Config{
			BinaryName:     "modelspec",
			Repository:     "modelspec-org/cli",
			CurrentVersion: info.Version,
		},
	}
}

// exitError carries a process exit code out of a command. err, when non-nil,
// is printed to stderr; findings are printed by the command itself and carry
// none.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return &exitError{code: ExitUsage, err: fmt.Errorf(format, args...)}
}

func ioError(err error) error { return &exitError{code: ExitUsage, err: err} }

// Run executes the command line (without the program name) and returns the
// process exit code.
func Run(args []string, env *Env) int {
	root := newRoot(env)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if !errors.As(err, &ee) {
		ee = &exitError{code: ExitUsage, err: err}
	}
	if ee.err != nil {
		fmt.Fprintf(env.Stderr, "modelspec: %v\n", ee.err)
	}
	// A script that asked for JSON gets JSON on every exit-2 path, never an
	// empty or a success-looking standard output.
	if ee.code == ExitUsage && wantsJSON(args) {
		enc := json.NewEncoder(env.Stdout)
		_ = enc.Encode(struct {
			Error string `json:"error"`
			Exit  int    `json:"exit"`
		}{ee.Error(), ExitUsage})
	}
	return ee.code
}

// wantsJSON reports whether the command line asks for --format json.
func wantsJSON(args []string) bool {
	for i, a := range args {
		if a == "--format=json" || (a == "--format" && i+1 < len(args) && args[i+1] == "json") {
			return true
		}
	}
	return false
}

func newRoot(env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "modelspec",
		Short: "Validate, export and rewrite ModelSpec models",
		Long: `modelspec validates ModelSpec models, exports them to their JSON form and
rewrites them from the old spelling to the new one.

Exit codes: 0 clean, 1 findings at error severity (or export drift, or a file
rewrite would change under --check, or one it cannot rewrite), 2 usage or I/O
error. modelspec makes no network request except
self-update, and sends no telemetry.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{code: ExitUsage, err: err} })
	root.AddCommand(lintCommand(env), exportCommand(env), rewriteCommand(env))
	vercmd.WireCobra(root, env.Build)
	root.AddCommand(cobracmd.New(env.Update, cobracmd.CommandOptions{
		Short:       "Update modelspec to the latest release",
		JSONFormat:  true,
		Errors:      updateErrors{},
		Interactive: env.Interactive,
	}))
	return root
}

// updateErrors maps self-update outcomes to this CLI's exit codes.
type updateErrors struct{}

func (updateErrors) Failure(err error) error { return &exitError{code: ExitUsage, err: err} }

func (updateErrors) UpdateAvailable(selfupdate.CheckResult) error {
	return &exitError{code: ExitUpdateAvailable}
}
