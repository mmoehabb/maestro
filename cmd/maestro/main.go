// Command maestro is a terminal multiplexer for coding agents.
package main

import (
	"os"

	"github.com/mmoehabb/maestro/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
