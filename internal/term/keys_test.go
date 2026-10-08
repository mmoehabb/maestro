package term

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestPrintableKeysReachAgent(t *testing.T) {
	for _, flags := range []int{0, 1, 3} {
		e := NewEmulator(80, 24, func() {})
		if flags != 0 {
			_, _ = io.WriteString(e, "\x1b[>"+strconv.Itoa(flags)+"u")
		}
		var captured strings.Builder
		done := make(chan struct{})
		go func() { _, _ = io.Copy(&captured, e); close(done) }()
		var want strings.Builder
		for code := rune(' '); code <= '~'; code++ {
			e.Key(uv.Key{Code: code}, false)
			want.WriteRune(code)
		}
		for _, k := range []uv.Key{
			{Code: 'p', ShiftedCode: 'P', Mod: uv.ModShift},
			{Code: '7', ShiftedCode: '&', Mod: uv.ModShift},
			{Code: '/', ShiftedCode: '?', Mod: uv.ModShift},
			{Code: 'a', Mod: uv.ModCapsLock},
			{Code: 'é', Text: "é"},
			{Code: uv.KeyExtended, Text: "👩‍💻"},
		} {
			e.Key(k, false)
		}
		want.WriteString("P&?Aé👩‍💻")
		_ = e.Close()
		<-done
		if captured.String() != want.String() {
			t.Errorf("flags %d: got %q, want %q", flags, captured.String(), want.String())
		}
	}
}

func TestLegacyAltShiftedText(t *testing.T) {
	e := NewEmulator(80, 24, func() {})
	var captured strings.Builder
	done := make(chan struct{})
	go func() { _, _ = io.Copy(&captured, e); close(done) }()
	e.Key(uv.Key{Code: 'p', ShiftedCode: 'P', Mod: uv.ModAlt | uv.ModShift}, false)
	_ = e.Close()
	<-done
	if captured.String() != "\x1bP" {
		t.Fatalf("got %q", captured.String())
	}
}

func TestEnhancedPrintableEvents(t *testing.T) {
	for code := rune(' '); code <= '~'; code++ {
		k := NormalizeKey(uv.Key{Code: code})
		for _, release := range []bool{false, true} {
			kind, text := 1, fmt.Sprintf(";%d", code)
			if release {
				kind, text = 3, ""
			}
			got, handled := encodeEnhanced(k, release, 31)
			want := fmt.Sprintf("\x1b[%d;1:%d%su", code, kind, text)
			if !handled || got != want {
				t.Fatalf("%c release=%v: got %q, want %q", code, release, got, want)
			}
		}
	}
}

func TestEnhancedComposedTextAndRelease(t *testing.T) {
	k := uv.Key{Code: uv.KeyExtended, Text: "é🙂"}
	for _, tc := range []struct {
		flags int
		want  string
	}{
		{3, "é🙂"}, {8, "é🙂"}, {31, "\x1b[0;1:1;233:128578u"},
	} {
		got, handled := encodeEnhanced(k, false, tc.flags)
		if !handled || got != tc.want {
			t.Fatalf("flags %d: got %q, want %q", tc.flags, got, tc.want)
		}
		if got, _ := encodeEnhanced(k, true, tc.flags); got != "" {
			t.Fatalf("composed text release: %q", got)
		}
	}
	if got, _ := encodeEnhanced(uv.Key{Code: 'a', Text: "a"}, true, 3); got != "" {
		t.Fatalf("UTF-8 release: %q", got)
	}
}
