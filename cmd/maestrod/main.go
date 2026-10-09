// maestrod runs the same daemon implementation used by maestro's auto-start.
package main

import (
	"os"

	"github.com/mmoehabb/maestro/internal/cli"
	"github.com/mmoehabb/maestro/internal/git"
)

func main() {
	if handled, code := git.RunAskpass(os.Args[1:], os.Stdout); handled {
		os.Exit(code)
	}
	os.Args = append([]string{os.Args[0], "daemon", "serve"}, os.Args[1:]...)
	os.Exit(cli.Execute())
}
