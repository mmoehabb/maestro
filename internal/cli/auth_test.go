package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAuthLoginUsesGitHubCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	out, err := run(t, "auth", "login")
	if err != nil {
		t.Fatal(err)
	}
	if out != "auth\nlogin\n--hostname\ngithub.com\n" {
		t.Fatalf("unexpected login args %q", out)
	}
}

func TestAuthLoginMissingCLI(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	_, err := run(t, "auth", "login")
	if err == nil || !strings.Contains(err.Error(), "install") {
		t.Fatal(err)
	}
}
