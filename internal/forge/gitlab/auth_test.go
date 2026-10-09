package gitlab

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTokenPrecedence(t *testing.T) {
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("PATH", t.TempDir())
	token, source, err := Token(context.Background(), "git.example", "configured")
	if err != nil || token != "configured" || source != "gitlab.token" {
		t.Fatal(source, err)
	}
	t.Setenv("OAUTH_TOKEN", "oauth")
	t.Setenv("GITLAB_ACCESS_TOKEN", "access")
	t.Setenv("GITLAB_TOKEN", "first")
	token, source, err = Token(context.Background(), "git.example", "configured")
	if err != nil || token != "first" || source != "GITLAB_TOKEN" {
		t.Fatal(source, err)
	}
	if _, err = LoginCommand(context.Background(), "git.example"); err == nil {
		t.Fatal("login ignored environment override")
	}
}

func TestGlabTokenCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, name := range []string{"GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_GLAB_ARGS\"\nprintf 'stored-token\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TEST_GLAB_ARGS", args)
	token, source, err := Token(context.Background(), "git.example", "configured")
	if err != nil || token != "stored-token" || source != "glab config get token" {
		t.Fatal(source, err)
	}
	data, err := os.ReadFile(args)
	if err != nil || strings.TrimSpace(string(data)) != "config\nget\ntoken\n--host\ngit.example" {
		t.Fatal(string(data), err)
	}
}
