//go:build !windows

package term

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	xterm "github.com/charmbracelet/x/term"
)

func TestBlockedInputProcess(t *testing.T) {
	if os.Getenv("MAESTRO_BLOCKED_INPUT_HELPER") != "1" {
		return
	}
	if _, err := xterm.MakeRaw(os.Stdin.Fd()); err != nil {
		os.Exit(2)
	}
	fmt.Print("ready")
	time.Sleep(30 * time.Second) // Deliberately never drain the raw input buffer.
	os.Exit(0)
}

func TestLargePasteKeepsPaneResponsiveAndShutdownBounded(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestBlockedInputProcess$")
	cmd.Env = append(os.Environ(), "MAESTRO_BLOCKED_INPUT_HELPER=1")
	p, err := Start(cmd, 80, 24, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(p.Snapshot(true).Screen, "ready") {
		if time.Now().After(deadline) {
			t.Fatal("helper did not initialize raw mode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	responsive := make(chan error, 1)
	go func() {
		p.Paste(strings.Repeat("a", 1<<20))
		p.Snapshot(true)
		responsive <- p.Resize(100, 30)
	}()
	select {
	case err = <-responsive:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("paste blocked the pane")
	}
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked input prevented shutdown")
	}
}
