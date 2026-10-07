package term

import (
	"bytes"
	"errors"
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

func TestEmulatorTracksMouseReporting(t *testing.T) {
	e := NewEmulator(80, 24, func() {})
	defer e.Close()
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"\x1b[?1006h", false}, // Encoding alone does not request mouse reporting.
		{"\x1b[?1000", false},
		{";1002h", true}, // A split sequence with combined parameters.
		{"\x1b[?1000l", true},
		{"\x1b[?1002l", false},
		{"\x1b[?9h", true},
		{"\x1b[?9l", false},
		{"\x1b[?1001h", true},
		{"\x1b[?1001l", false},
		{"\x1b[?1003h", true},
		{"\x1bc", false}, // A full reset also resets reporting.
	} {
		if _, err := io.WriteString(e, tc.output); err != nil {
			t.Fatal(err)
		}
		if got := e.MouseReporting(); got != tc.want {
			t.Fatalf("after %q: reporting=%v, want %v", tc.output, got, tc.want)
		}
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

func TestNativeActivityPrecedence(t *testing.T) {
	now := time.Now()
	a := Activity{State: Starting, IdleAfter: time.Millisecond, Hints: []string{"approve?"}}
	a.NativeEvent("started")
	a.Output([]byte("tool working"), now)
	a.Tick(now.Add(time.Hour))
	a.Complete()
	if a.State != Working {
		t.Fatal("silence/BEL completed native turn", a.State)
	}
	a.Output([]byte("approve?"), now)
	if a.State != NeedsInput {
		t.Fatal("approval lost")
	}
	a.NativeEvent("done")
	if a.State != Done {
		t.Fatal("native completion ignored")
	}
	a.Output([]byte("repaint"), now)
	if a.State != Done {
		t.Fatal("repaint restarted completed turn")
	}
	a.NativeEvent("started")
	a.NativeEvent("unavailable")
	a.Tick(time.Now().Add(time.Hour))
	if a.State != Done {
		t.Fatal("fallback not restored")
	}
	a.State = Exited
	a.NativeEvent("started")
	if a.State != Exited {
		t.Fatal("native event resurrected exited process")
	}
}

func TestRecoveredNativeActivityPreservesApproval(t *testing.T) {
	for _, state := range []State{Working, Done, NeedsInput, Exited, Crashed} {
		a := Activity{State: state, IdleAfter: time.Millisecond}
		a.NativeEvent("restored_started")
		a.Tick(time.Now().Add(time.Hour))
		want := state
		if state == Done {
			want = Working
		}
		if a.State != want {
			t.Fatalf("%s recovered as %s", state, a.State)
		}
		if state != Exited && state != Crashed && !a.NativeWorking {
			t.Fatal("native activity not restored")
		}
	}
}

func TestRecoveredIdleDoesNotCompleteNewInput(t *testing.T) {
	a := Activity{State: Working}
	a.NativeEvent("restored_idle")
	if a.State != Working || !a.Native || a.NativeWorking {
		t.Fatal(a)
	}
	a.State = Done
	a.NativeEvent("restored_idle")
	a.Output([]byte("repaint"), time.Now())
	if a.State != Done {
		t.Fatal("recovered completion lost on repaint", a)
	}
}

func TestDelayedNativeStartPreservesApproval(t *testing.T) {
	now := time.Now()
	a := Activity{State: Working, IdleAfter: time.Millisecond, Hints: []string{"approve?"}}
	a.Output([]byte("Approve?"), now)
	prompt := a.tail
	a.NativeEvent("started")
	a.Tick(now.Add(time.Hour))
	if a.State != NeedsInput || a.tail != prompt || !a.NativeWorking {
		t.Fatalf("delayed start lost approval: %+v", a)
	}
	a.Input(now)
	if a.State != Working || a.tail != "" {
		t.Fatalf("approval response did not resume work: %+v", a)
	}
	a.NativeEvent("done")
	if a.State != Done {
		t.Fatal("native completion ignored")
	}
}

func TestAlternateScreenHistorySurvivesTeardown(t *testing.T) {
	for _, exit := range []string{"\x1b[?1049l", "\x1b[?25;1049l", "\x1b[2J\x1b[?1049l"} {
		e := NewEmulator(80, 24, func() {})
		// Write bytewise to exercise sequences split across PTY reads.
		for _, b := range []byte("\x1b[?1049himportant conversation" + exit) {
			_, _ = e.Write([]byte{b})
		}
		if !strings.Contains(strings.Join(e.Scrollback(), "\n"), "important conversation") {
			t.Errorf("conversation lost on %q", exit)
		}
		if strings.Contains(e.Render(), "important conversation") {
			t.Error("alternate screen remains visible after exit")
		}
		_ = e.Close()
	}
}

func TestScrollbackRetainsOriginalLineWidth(t *testing.T) {
	e := NewEmulator(80, 2, func() {})
	defer e.Close()
	line := strings.Repeat("a", 40) + "IMPORTANT_END"
	_, _ = io.WriteString(e, line+"\r\nnext\r\nlast\r\n")
	e.Resize(20, 2)
	if !strings.Contains(strings.Join(e.Scrollback(), "\n"), line) {
		t.Fatal("narrow resize truncated history")
	}
}

func TestInputQueueOrderingAndOverflow(t *testing.T) {
	q := newInputQueue()
	if _, err := q.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 11)
	if _, err := io.ReadFull(q, out); err != nil || string(out) != "firstsecond" {
		t.Fatal(string(out), err)
	}
	if _, err := q.Write(make([]byte, maxPendingInput+1)); err == nil {
		t.Fatal("unbounded pending input")
	}
	if q.Err() == nil {
		t.Fatal("overflow was not reported")
	}
	if _, err := q.Read(out); !errors.Is(err, io.EOF) {
		t.Fatal("overflow reader remains blocked", err)
	}
	q.Close()
}
