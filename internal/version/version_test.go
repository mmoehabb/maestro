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

func TestModuleVersion(t *testing.T) {
	for _, tc := range []struct{ linked, module, want string }{
		{"dev", "v1.2.3", "v1.2.3"},
		{"dev", "v1.2.4-0.20260101000000-abcdef123456", "v1.2.4-0.20260101000000-abcdef123456"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
		{"v2.0.0", "v1.2.3", "v2.0.0"},
	} {
		if got := moduleVersion(tc.linked, tc.module); got != tc.want {
			t.Errorf("moduleVersion(%q, %q) = %q, want %q", tc.linked, tc.module, got, tc.want)
		}
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
