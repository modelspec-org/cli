// Command modelspec validates ModelSpec models and exports them to JSON.
package main

import (
	"os"

	"github.com/modelspec-org/cli/internal/cli"
)

var exit = os.Exit

func main() {
	exit(cli.Run(os.Args[1:], cli.OSEnv()))
}
