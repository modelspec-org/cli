// Command fuzzjudge reads the log of a fuzz run on standard input and fails when
// the run stalled. See package fuzzjudge.
package main

import (
	"os"

	"github.com/modelspec-org/cli/internal/fuzzjudge"
)

var exit = os.Exit

func main() {
	exit(fuzzjudge.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
