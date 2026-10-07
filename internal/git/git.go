package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Repo struct{ Root, CommonDir, Remote, DefaultBranch string }

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Discover resolves linked worktrees to the main checkout, so all tabs in the
// same repository share a project identity and lock.
func Discover(ctx context.Context, dir string) (Repo, error) {
	var r Repo
	bare, err := run(ctx, dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return r, err
	}
	if bare == "true" {
		return r, fmt.Errorf("maestro requires a non-bare checkout")
	}
	r.CommonDir, err = run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return r, err
	}
	r.CommonDir, err = filepath.EvalSymlinks(r.CommonDir)
	if err != nil {
		return r, err
	}
	worktrees, err := run(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return r, err
	}
	first, _, _ := strings.Cut(worktrees, "\x00")
	if !strings.HasPrefix(first, "worktree ") {
		return r, fmt.Errorf("cannot find main worktree")
	}
	r.Root, err = filepath.EvalSymlinks(strings.TrimPrefix(first, "worktree "))
	if err != nil {
		return r, err
	}
	r.Remote, _ = run(ctx, r.Root, "remote", "get-url", "origin")
	r.DefaultBranch, _ = run(ctx, r.Root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if r.DefaultBranch == "" {
		r.DefaultBranch, _ = run(ctx, r.Root, "symbolic-ref", "--short", "HEAD")
	}
	if r.DefaultBranch == "" {
		r.DefaultBranch = "HEAD"
	}
	return r, nil
}

// Key avoids collisions between checkouts with the same directory name.
func (r Repo) Key() string {
	sum := sha256.Sum256([]byte(r.CommonDir))
	return fmt.Sprintf("repo-%x", sum[:12])
}

type WorktreeSpec struct {
	Path, Branch, Base string
	Copy, Setup        []string
}

// StableBase keeps named branches usable as comparison targets, while resolving
// checkout-relative revisions such as HEAD and HEAD~1 in the source checkout.
func (r Repo) StableBase(ctx context.Context, base string) (string, error) {
	commit, err := run(ctx, r.Root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("invalid base %q (the repository needs an initial commit): %w", base, err)
	}
	ref, err := run(ctx, r.Root, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", base)
	if err == nil && (ref == "refs/heads/"+base || ref == "refs/remotes/"+base || (ref == base && (strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, "refs/remotes/")))) {
		return base, nil
	}
	return commit, nil
}

func (r Repo) CreateWorktree(ctx context.Context, spec WorktreeSpec) error {
	if _, err := run(ctx, r.Root, "check-ref-format", "--branch", spec.Branch); err != nil {
		return err
	}
	if strings.HasPrefix(spec.Branch, "-") {
		return fmt.Errorf("invalid branch %q", spec.Branch)
	}
	commit, err := run(ctx, r.Root, "rev-parse", "--verify", "--end-of-options", spec.Base+"^{commit}")
	if err != nil {
		return fmt.Errorf("invalid base %q (the repository needs an initial commit): %w", spec.Base, err)
	}
	if _, err := os.Lstat(spec.Path); !os.IsNotExist(err) {
		return fmt.Errorf("worktree destination already exists or is inaccessible: %s", spec.Path)
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o700); err != nil {
		return err
	}
	if err := r.excludeHandoff(); err != nil {
		return err
	}
	if _, err := run(ctx, r.Root, "worktree", "add", "-b", spec.Branch, "--", spec.Path, commit); err != nil {
		return err
	}
	// Once creation succeeds, failures intentionally retain the checkout. Setup
	// commands may have made valuable changes, so rollback must never delete it.
	for _, name := range spec.Copy {
		if err := copyFile(r.Root, spec.Path, name); err != nil {
			return fmt.Errorf("copy %q: %w; worktree retained at %s (branch %s)", name, err, spec.Path, spec.Branch)
		}
	}
	for _, command := range spec.Setup {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
		cmd.Dir = spec.Path
		// Do not include output in errors: setup may print copied credentials.
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("setup %q: %w; worktree retained at %s (branch %s)", command, err, spec.Path, spec.Branch)
		}
	}
	return nil
}

func (r Repo) excludeHandoff() error {
	path := filepath.Join(r.CommonDir, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "/.maestro/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := io.WriteString(f, "\n# Maestro task handoffs\n/.maestro/\n")
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func copyFile(srcRoot, dstRoot, name string) error {
	if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("not a safe relative path")
	}
	name = filepath.Clean(name)
	if strings.Split(filepath.ToSlash(name), "/")[0] == ".git" {
		return fmt.Errorf("cannot copy Git metadata")
	}
	src, err := regularPath(srcRoot, name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("only regular files can be copied")
	}
	// Inspect existing destination components before creating directories.
	if _, err := regularPath(dstRoot, name); err != nil && !os.IsNotExist(err) {
		return err
	}
	dst := filepath.Join(dstRoot, name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()&0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func regularPath(root, name string) (string, error) {
	path := root
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks are not allowed: %s", path)
		}
	}
	return path, nil
}
