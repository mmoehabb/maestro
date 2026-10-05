package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// FakeAgent exercises ANSI, alternate screen, native resume, prompts and exit.
// Args are new|resume, native ID, and an optional first prompt.
func FakeAgent(args []string) int {
	if len(args) < 2 {
		return 2
	}
	if err := os.MkdirAll(".maestro", 0o700); err != nil {
		return 2
	}
	state := filepath.Join(".maestro", "fake-"+filepath.Base(args[1]))
	if args[0] == "resume" {
		if _, err := os.Stat(state); err != nil {
			fmt.Println("unknown session")
			return 3
		}
	} else {
		if err := os.WriteFile(state, []byte("session"), 0o600); err != nil {
			return 2
		}
	}
	f, err := os.OpenFile(filepath.Join(".maestro", "launches.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	_ = json.NewEncoder(f).Encode(args)
	_ = f.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	go func() { <-signals; os.Exit(0) }()
	fmt.Printf("\x1b[?1049h\x1b[2J\x1b[H\x1b[32mready %s %s\x1b[0m\r\n", args[0], args[1])
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		switch input.Text() {
		case "exit":
			return 0
		case "crash":
			return 7
		case "approve":
			fmt.Print("Allow command?")
		default:
			fmt.Printf("working: %s\r\n", input.Text())
			time.Sleep(80 * time.Millisecond)
			fmt.Print("done\r\n\a")
		}
	}
	return 0
}
