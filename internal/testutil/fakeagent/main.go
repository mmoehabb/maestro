package main

import (
	"os"

	"github.com/mmoehabb/maestro/internal/testutil"
)

func main() { os.Exit(testutil.FakeAgent(os.Args[1:])) }
