package term

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestActivity(t *testing.T) {
	now := time.Now()
	a := Activity{State: Starting, IdleAfter: time.Second, Hints: []string{"Allow command?"}}
	a.Tick(now.Add(time.Hour))
	if a.State != Starting {
		t.Fatal("untouched pane completed")
	}
	a.Output([]byte("thinking"), now)
	if a.State != Working {
		t.Fatal(a.State)
	}
	a.Tick(now.Add(time.Second))
	if a.State != Done {
		t.Fatal(a.State)
	}
	a.Input(now)
	a.Output([]byte("Allow com"), now)
	a.Output([]byte("mand?"), now)
	if a.State != NeedsInput {
		t.Fatal(a.State)
	}
	a.Tick(now.Add(time.Hour))
	if a.State != NeedsInput {
		t.Fatal("approval dismissed by idle")
	}
	a.Input(now)
	a.Complete()
	if a.State != Done {
		t.Fatal(a.State)
	}
}

func TestEmulatorInputModes(t *testing.T) {
	e := NewEmulator(80, 24, func() {})
	var output bytes.Buffer
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 1024)
		for {
			n, err := e.Read(b)
			if n > 0 {
				mu.Lock()
				output.Write(b[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() { _ = e.Close(); <-done }()
	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(e, s); err != nil {
			t.Fatal(err)
		}
	}
	write("\x1b[?1h")
	e.Key(uv.Key{Code: uv.KeyUp}, false)
	e.Key(uv.Key{Code: uv.KeyEnter}, false)
	e.Key(uv.Key{Code: 'c', Mod: uv.ModCtrl, Text: "c"}, false)
	write("\x1b[>3u\x1b[?u")
	e.Key(uv.Key{Code: 'm', Mod: uv.ModCtrl}, false)
	e.Key(uv.Key{Code: 'm', Mod: uv.ModCtrl}, true)
	e.Key(uv.Key{Code: uv.KeyEnter}, false)
	write("\x1b[<u\x1b[?2004h")
	e.Paste("one\ntwo")
	// Each pipe write is read before it returns, but the reader may still be
	// appending the last chunk. Closing then joining makes capture deterministic.
	_ = e.Close()
	<-done
	mu.Lock()
	got := output.String()
	mu.Unlock()
	want := "\x1bOA\r\x03\x1b[?3u\x1b[109;5:1u\x1b[109;5:3u\r\x1b[200~one\ntwo\x1b[201~"
	if got != want {
		t.Fatalf("input encoding\ngot  %q\nwant %q", got, want)
	}
}

func TestPrefixFallback(t *testing.T) {
	if ActivePrefix("ctrl+m", "ctrl+b", false) != "ctrl+b" || ActivePrefix("ctrl+m", "ctrl+b", true) != "ctrl+m" {
		t.Fatal("bad prefix negotiation")
	}
}

func TestAgentProcess(t *testing.T) {
	if os.Getenv("MAESTRO_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(testutil.FakeAgent(os.Args[i+1:]))
		}
	}
	os.Exit(2)
}

func TestPane(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestAgentProcess", "--", "new", "pane-test")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "MAESTRO_TEST_HELPER=1")
	p, err := Start(cmd, 80, 24, 80*time.Millisecond, []string{"Allow command?"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	waitFor := func(state State, text string) {
		t.Helper()
		until := time.Now().Add(5 * time.Second)
		for time.Now().Before(until) {
			s := p.Snapshot(true)
			if s.State == state && strings.Contains(s.Screen, text) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("wanted %s %q; got %+v", state, text, p.Snapshot(true))
	}
	waitFor(Done, "ready new pane-test")
	if err := p.Resize(100, 32); err != nil {
		t.Fatal(err)
	}
	// Submit with Enter: LF in pasted text does not submit cooked input on Windows.
	p.Paste("approve")
	p.Key(uv.Key{Code: uv.KeyEnter}, false)
	waitFor(NeedsInput, "Allow command?")
	p.Paste("work")
	p.Key(uv.Key{Code: uv.KeyEnter}, false)
	waitFor(Done, "done")
	if len(p.Scrollback()) == 0 {
		t.Fatal("no scrollback")
	}
	p.Paste("exit")
	p.Key(uv.Key{Code: uv.KeyEnter}, false)
	select {
	case <-p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
	if s := p.Snapshot(false); s.State != Exited || s.ExitCode != 0 {
		t.Fatal(s)
	}
}
