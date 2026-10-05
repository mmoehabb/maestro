package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	i := Get()
	if i.Version == "" {
		t.Error("empty version")
	}
	if i.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("platform = %q", i.Platform)
	}
	if len(i.Commit) > 12 {
		t.Errorf("commit not shortened: %q", i.Commit)
	}
}

func TestString(t *testing.T) {
	s := Info{Version: "1.2.3", Commit: "abc", Date: "d", GoVersion: "go1", Platform: "x/y"}.String()
	if want := "maestro 1.2.3 (abc, d) go1 x/y"; s != want {
		t.Errorf("got %q, want %q", s, want)
	}
	if s := (Info{Version: "dev", GoVersion: "go1", Platform: "x/y"}).String(); strings.Contains(s, "(") {
		t.Errorf("unexpected parens without commit: %q", s)
	}
}
