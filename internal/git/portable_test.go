package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mmoehabb/maestro/internal/portable"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestPortableDiscoveryOwnershipAndValidation(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	r, err := Discover(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Head(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "checkout", "-b", "task")
	c := portable.Checkpoint{Manifest: portable.Manifest{Version: 1, ID: uuid.NewString(), Slug: "task", Title: "Task", Agent: "fake", Branch: "task", BaseBranch: "main", BaseCommit: base}, Handoff: "Keep the context.\n"}
	c.Seal()
	if err = portable.Write(root, c); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", ".maestro/tasks")
	testutil.Git(t, root, "commit", "-m", "checkpoint")
	testutil.Git(t, root, "checkout", "-b", "inherited")
	checkpoints, err := r.TaskCheckpoints(ctx, "")
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Ref != "refs/heads/task" {
		t.Fatal("inherited task duplicated", checkpoints, err)
	}
	testutil.Git(t, root, "checkout", "task")
	if err = os.WriteFile(filepath.Join(root, c.Directory(), "handoff.md"), []byte("torn write"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", ".maestro/tasks")
	testutil.Git(t, root, "commit", "-m", "broken checkpoint")
	if _, err = r.TaskCheckpoints(ctx, "task"); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatal("accepted corrupt checkpoint", err)
	}
}

func TestPortableIgnoreMigration(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	r, err := Discover(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(r.CommonDir, "info", "exclude")
	if err = os.WriteFile(exclude, []byte("# user rule\n/private.txt\n\n# Maestro task handoffs\n/.maestro/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = r.EnablePortable(root); err != nil {
		t.Fatal(err)
	}
	if err = r.excludeHandoff(); err != nil {
		t.Fatal(err)
	}
	if err = r.EnablePortable(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exclude)
	if err != nil || !strings.Contains(string(b), "/private.txt") || strings.Contains(string(b), "/.maestro/\n") {
		t.Fatal("ignore migration", string(b), err)
	}
	for _, name := range []string{".maestro/local/handoff.md", ".maestro/session-id", ".maestro/handoff.md"} {
		if _, err = run(ctx, root, "check-ignore", "--", name); err != nil {
			t.Fatal("runtime file exposed", name, err)
		}
	}
	for _, name := range []string{".maestro/tasks/id/task.json", ".maestro/.gitignore"} {
		if _, err = run(ctx, root, "check-ignore", "--", name); err == nil {
			t.Fatal("portable file ignored", name)
		}
	}
}

func TestPortableWriterRejectsTrackedSymlinks(t *testing.T) {
	root := testutil.Repo(t)
	r, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	base, err := Head(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	c := portable.Checkpoint{Manifest: portable.Manifest{Version: 1, ID: uuid.NewString(), Slug: "main", Title: "Main", Agent: "fake", Branch: "main", BaseBranch: "main", BaseCommit: base}, Handoff: "context"}
	c.Seal()
	if err = portable.Write(root, c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, c.Directory(), "handoff.md")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(t.TempDir(), "outside"), path); err != nil {
		t.Skip(err)
	}
	testutil.Git(t, root, "add", ".maestro/tasks")
	testutil.Git(t, root, "commit", "-m", "symlink checkpoint")
	if _, err = r.TaskCheckpoints(context.Background(), "main"); err == nil {
		t.Fatal("accepted tracked symlink")
	}
}

func TestPortableCheckoutRejectsReplacedRepository(t *testing.T) {
	ctx := context.Background()
	root := testutil.Repo(t)
	r, err := Discover(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateTaskCheckout(ctx, root, "main"); err != nil {
		t.Fatal("main checkout rejected", err)
	}
	path := filepath.Join(t.TempDir(), "task")
	if err = r.CreateWorktree(ctx, WorktreeSpec{Path: path, Branch: "task", Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "init", "-b", "task")
	testutil.Git(t, path, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "unrelated repository")
	if err = r.ValidateTaskCheckout(ctx, path, "task"); err == nil {
		t.Fatal("accepted another repository at registered worktree path")
	}
}
