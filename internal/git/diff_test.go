package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestWorktreeDiff(t *testing.T) {
	dir := testutil.Repo(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "old\n")
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-m", "base")
	base := strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "HEAD"))
	write("tracked.txt", "committed\n")
	testutil.Git(t, dir, "commit", "-am", "change")
	write("staged.txt", "staged\n")
	testutil.Git(t, dir, "add", "staged.txt")
	write("tracked.txt", "unstaged\n")
	write("new file.txt", "untracked\n")
	write("binary.bin", "\x00binary")
	testutil.Git(t, dir, "add", "binary.bin")
	// External diff and textconv must never execute in the viewer.
	testutil.Git(t, dir, "config", "diff.external", "maestro-nonexistent-external-diff")
	result, err := WorktreeDiff(context.Background(), dir, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-old", "+unstaged", "+staged", "Binary files"} {
		if !strings.Contains(result.Patch, want) {
			t.Fatalf("missing %q in %s", want, result.Patch)
		}
	}
	if !strings.Contains(result.Untracked, "new file.txt") || strings.Contains(result.Patch, "untracked") {
		t.Fatalf("untracked handling: %+v", result)
	}
	if _, err = WorktreeDiff(context.Background(), dir, "--help"); err == nil {
		t.Fatal("invalid base accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = WorktreeDiff(ctx, dir, base); err == nil {
		t.Fatal("cancelled diff succeeded")
	}
}

func TestDiffOutputBound(t *testing.T) {
	var b limitedOutput
	content := strings.Repeat("x", MaxDiffBytes+100)
	n, err := b.Write([]byte(content))
	if err != nil || n != len(content) || b.Len() != MaxDiffBytes || !b.truncated {
		t.Fatalf("unbounded output: %d %d %v", n, b.Len(), err)
	}
	if _, err = b.Write([]byte("tail")); err != nil || b.Len() != MaxDiffBytes {
		t.Fatal("subsequent write exceeded cap")
	}
}
