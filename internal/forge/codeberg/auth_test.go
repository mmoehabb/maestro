package codeberg

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTokenPrecedence(t *testing.T) {
	for _, name := range []string{"CODEBERG_TOKEN", "GITEA_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("PATH", t.TempDir())
	token, source, err := Token(context.Background(), "codeberg.org", "configured")
	if err != nil || token != "configured" || source != "codeberg.token" {
		t.Fatal(source, err)
	}
	t.Setenv("GITEA_TOKEN", "gitea")
	t.Setenv("CODEBERG_TOKEN", "codeberg")
	token, source, err = Token(context.Background(), "codeberg.org", "configured")
	if err != nil || token != "codeberg" || source != "CODEBERG_TOKEN" {
		t.Fatal(source, err)
	}
	if _, err = LoginCommand(context.Background(), "codeberg.org"); err == nil {
		t.Fatal("login ignored environment override")
	}
}

func TestTeaTokenCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, name := range []string{"CODEBERG_TOKEN", "GITEA_TOKEN"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_TEA_ARGS\"\nprintf 'stored-token\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "tea"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TEST_TEA_ARGS", args)
	token, source, err := Token(context.Background(), "codeberg.org", "configured")
	if err != nil || token != "stored-token" || source != "tea login export" {
		t.Fatal(source, err)
	}
	data, err := os.ReadFile(args)
	if err != nil || strings.TrimSpace(string(data)) != "login\nexport\ncodeberg.org" {
		t.Fatal(string(data), err)
	}
}
