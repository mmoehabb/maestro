package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestCreateAndDiscoverWorktree(t *testing.T) {
	ctx := context.Background()
	dir := testutil.Repo(t)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "space in path")
	if err := repo.CreateWorktree(ctx, WorktreeSpec{Path: path, Branch: "maestro/task", Base: "main", Copy: []string{".env", ".env.local"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(path, ".env"))
	if err != nil || string(b) != "SECRET=test\n" {
		t.Fatalf("copy failed: %q %v", b, err)
	}
	linked, err := Discover(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if linked.Root != repo.Root || linked.Key() != repo.Key() {
		t.Fatalf("linked checkout has different identity: %+v %+v", repo, linked)
	}
	if err := os.Mkdir(filepath.Join(path, ".maestro"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, ".maestro", "handoff.md"), []byte("history"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := testutil.Git(t, path, "status", "--porcelain"); strings.Contains(out, "maestro") {
		t.Fatalf("handoff not ignored: %s", out)
	}
	if err := repo.CreateWorktree(ctx, WorktreeSpec{Path: path, Branch: "maestro/duplicate", Base: "main"}); err == nil {
		t.Fatal("must not overwrite destination")
	}
}

func TestPreserveHeadMigratesLegacyRecoveryRef(t *testing.T) {
	ctx := context.Background()
	dir := testutil.Repo(t)
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	old := strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "HEAD"))
	testutil.Git(t, dir, "update-ref", "refs/maestro/archive/42", old)
	testutil.Git(t, dir, "commit", "--allow-empty", "-m", "next cycle")
	if _, err = r.PreserveHead(ctx, dir, 42); err != nil {
		t.Fatal(err)
	}
	ref := "refs/maestro/recovery/42/" + old
	if got := strings.TrimSpace(testutil.Git(t, dir, "rev-parse", ref)); got != old {
		t.Fatal(got)
	}
}

func TestCopyRefusesTraversalSymlinksAndOverwrite(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "secret"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "secret"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret", ".git/config", "secret"} {
		if err := copyFile(src, dst, name); err == nil {
			t.Fatalf("accepted unsafe copy %q", name)
		}
	}
	b, err := os.ReadFile(filepath.Join(dst, "secret"))
	if err != nil || string(b) != "existing" {
		t.Fatal("overwrote destination")
	}
	if err := os.Symlink(src, filepath.Join(src, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyFile(src, dst, filepath.Join("link", "secret")); err == nil {
		t.Fatal("followed source symlink")
	}
	if err := os.Mkdir(filepath.Join(src, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, filepath.Join(dst, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst, filepath.Join("nested", "secret")); err == nil {
		t.Fatal("followed destination symlink")
	}
}

func TestSetupFailurePreservesCheckout(t *testing.T) {
	repo, err := Discover(context.Background(), testutil.Repo(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "failed")
	err = repo.CreateWorktree(context.Background(), WorktreeSpec{Path: path, Branch: "maestro/failed", Base: "main", Setup: []string{"exit 7"}})
	if err == nil || !strings.Contains(err.Error(), "worktree retained at") {
		t.Fatalf("wrong failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatal("failed checkout was removed", err)
	}
}

func TestWorktreeStatus(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Git(t, dir, "checkout", "-b", "maestro/status")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "file")
	testutil.Git(t, dir, "commit", "-m", "add file")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("one\nnew\nthird\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := WorktreeStatus(context.Background(), dir, "main")
	if err != nil || s.Dirty != 2 || s.Added != 2 || s.Deleted != 1 || s.Ahead != 1 || s.Commits != 1 || s.Upstream {
		t.Fatalf("status %+v: %v", s, err)
	}
}
