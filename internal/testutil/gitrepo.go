// Package testutil provides isolated repositories for integration tests.
package testutil

import (
	"os/exec"
	"testing"
)

func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func Repo(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-b", "main")
	Git(t, dir, "config", "user.name", "Maestro Test")
	Git(t, dir, "config", "user.email", "test@example.invalid")
	Git(t, dir, "config", "commit.gpgsign", "false")
	Git(t, dir, "config", "core.hooksPath", t.TempDir())
	Git(t, dir, "commit", "--allow-empty", "-m", "initial")
	return dir
}
