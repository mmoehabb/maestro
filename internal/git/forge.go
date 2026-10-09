package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Head(ctx context.Context, path string) (string, error) {
	return run(ctx, path, "rev-parse", "--verify", "HEAD^{commit}")
}

func Push(ctx context.Context, path, branch string) error {
	actual, err := run(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if actual != branch {
		return fmt.Errorf("worktree is on %s, expected %s", actual, branch)
	}
	_, err = run(ctx, path, "push", "--set-upstream", "origin", "HEAD:refs/heads/"+branch)
	return err
}

func Commits(ctx context.Context, path, base string) (string, error) {
	return run(ctx, path, "log", "--oneline", base+"..HEAD", "--")
}

// PRBase resolves named local/remote bases; commit-only tasks must supply a base.
func (r Repo) PRBase(ctx context.Context, base string) (string, error) {
	ref, err := run(ctx, r.Root, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", base)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimPrefix(ref, "refs/heads/"), nil
	}
	if strings.HasPrefix(ref, "refs/remotes/origin/") {
		return strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
	}
	return "", fmt.Errorf("task base is a commit; specify the PR base branch")
}

func (r Repo) ValidateWorktree(ctx context.Context, path, branch string) error {
	return r.validateCheckout(ctx, path, branch, false)
}

func (r Repo) validateCheckout(ctx context.Context, path, branch string, allowMain bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("worktree must be a directory, not a symlink")
	}
	root, err := run(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	main, err := filepath.EvalSymlinks(r.Root)
	if err != nil {
		return err
	}
	if root != expected || (!allowMain && root == main) {
		return fmt.Errorf("refusing cleanup outside the task's linked worktree")
	}
	common, err := run(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return err
	}
	if common != r.CommonDir {
		return fmt.Errorf("worktree belongs to another repository")
	}
	actual, err := run(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return err
	}
	if actual != branch {
		return fmt.Errorf("worktree branch changed: expected %s, found %s", branch, actual)
	}
	return nil
}

// CleanupCheck checks against the actual remote branch, rather than a potentially
// stale upstream tracking ref. A merged PR's exact head is also proof of upload.
func (r Repo) CleanupCheck(ctx context.Context, path, branch, base, mergedHead string) error {
	if err := r.ValidateWorktree(ctx, path, branch); err != nil {
		return err
	}
	status, err := WorktreeStatus(ctx, path, base)
	if err != nil {
		return err
	}
	if status.Dirty > 0 {
		return fmt.Errorf("worktree has %d uncommitted files; explicit force confirmation required", status.Dirty)
	}
	head, err := Head(ctx, path)
	if err != nil {
		return err
	}
	if mergedHead != "" && head == mergedHead {
		return nil
	}
	if status.Commits == 0 {
		return nil
	}
	out, err := run(ctx, r.Root, "ls-remote", "--exit-code", "origin", "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("cannot prove task commits are pushed; push first or explicitly confirm force cleanup: %w", err)
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != head {
		return fmt.Errorf("task HEAD differs from the remote branch; push first or explicitly confirm force cleanup")
	}
	return nil
}

// PreserveHead protects the local commits from garbage collection even after a
// squash merge, force cleanup, or deletion of the remote task branch.
func (r Repo) PreserveHead(ctx context.Context, path string, id int64) (string, error) {
	head, err := Head(ctx, path)
	if err != nil {
		return "", err
	}
	latest := "refs/maestro/archive/" + strconv.FormatInt(id, 10)
	// Keep immutable snapshots outside the legacy latest-ref namespace. Protect
	// the legacy tip too, before updating it during the first upgraded cleanup.
	previous, err := run(ctx, r.Root, "for-each-ref", "--format=%(objectname)", latest)
	if err != nil {
		return "", err
	}
	for _, commit := range append(strings.Fields(previous), head) {
		ref := "refs/maestro/recovery/" + strconv.FormatInt(id, 10) + "/" + commit
		if _, err = run(ctx, r.Root, "update-ref", ref, commit); err != nil {
			return "", err
		}
	}
	_, err = run(ctx, r.Root, "update-ref", latest, head)
	return head, err
}

func (r Repo) RemoveWorktree(ctx context.Context, path, branch, head string, force, merged bool) error {
	if err := r.ValidateWorktree(ctx, path, branch); err != nil {
		return err
	}
	current, err := Head(ctx, path)
	if err != nil {
		return err
	}
	if current != head {
		return fmt.Errorf("task HEAD changed during cleanup; retry")
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--", path)
	if _, err = run(ctx, r.Root, args...); err != nil {
		return err
	}
	// -d cannot recognize squash merges. The exact merged head plus a recovery
	// ref makes deletion safe without weakening the dirty/unpushed preflight.
	flag := "-d"
	if force || merged {
		flag = "-D"
	}
	if _, err = run(ctx, r.Root, "branch", flag, "--", branch); err != nil {
		return fmt.Errorf("worktree removed; branch retained: %w", err)
	}
	_, err = run(ctx, r.Root, "worktree", "prune")
	return err
}

// FinishCleanup resumes a removal interrupted between Git and SQLite updates.
func (r Repo) FinishCleanup(ctx context.Context, branch string, id int64, force bool, mergedHead string) error {
	if _, err := run(ctx, r.Root, "check-ref-format", "--branch", branch); err != nil {
		return err
	}
	head, err := run(ctx, r.Root, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		_, err = run(ctx, r.Root, "worktree", "prune")
		return err
	}
	saved, err := run(ctx, r.Root, "rev-parse", "--verify", "refs/maestro/archive/"+strconv.FormatInt(id, 10))
	if err != nil {
		return err
	}
	if head != saved {
		return fmt.Errorf("branch changed after cleanup began; retained for recovery")
	}
	flag := "-d"
	if force || head == mergedHead {
		flag = "-D"
	}
	if _, err = run(ctx, r.Root, "branch", flag, "--", branch); err != nil {
		return err
	}
	_, err = run(ctx, r.Root, "worktree", "prune")
	return err
}

func (r Repo) RecreateWorktree(ctx context.Context, path, branch, commit string) error {
	if _, err := os.Lstat(path); err == nil {
		return r.ValidateWorktree(ctx, path, branch)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := run(ctx, r.Root, "check-ref-format", "--branch", branch); err != nil {
		return err
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := run(ctx, r.Root, "worktree", "prune"); err != nil {
		return err
	}
	if _, err := run(ctx, r.Root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = run(ctx, r.Root, "worktree", "add", "--", path, branch)
		return err
	}
	if commit == "" {
		return fmt.Errorf("no saved commit available for reopening")
	}
	resolved, err := run(ctx, r.Root, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		if _, err = run(ctx, r.Root, "fetch", "origin", commit); err != nil {
			return err
		}
		resolved, err = run(ctx, r.Root, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
		if err != nil {
			return err
		}
	}
	_, err = run(ctx, r.Root, "worktree", "add", "-b", branch, "--", path, resolved)
	return err
}
