// Command covergate fails unless every statement in a Go cover profile is
// covered. See package covergate.
package main

import (
	"os"

	"github.com/modelspec-org/cli/internal/covergate"
)

var exit = os.Exit

func main() {
	exit(covergate.Run(os.Args[1:], os.Stdout, os.Stderr, covergate.OSOpen))
}
