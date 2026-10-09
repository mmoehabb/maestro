package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mmoehabb/maestro/internal/portable"
)

type TaskCheckpoint struct {
	portable.Checkpoint
	Ref, Commit string
}

func (r Repo) Commit(ctx context.Context, ref string) (string, error) {
	return run(ctx, r.Root, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}

func (r Repo) IsAncestor(ctx context.Context, before, after string) bool {
	_, err := run(ctx, r.Root, "merge-base", "--is-ancestor", before, after)
	return err == nil
}

// HasCheckpoint proves checkpoint ancestry from published file history, not
// from a local code commit that may predate an unpublished checkpoint.
func (r Repo) HasCheckpoint(ctx context.Context, tip string, c portable.Checkpoint) bool {
	path := filepath.ToSlash(filepath.Join(c.Directory(), "task.json"))
	commits, err := run(ctx, r.Root, "log", "-256", "--format=%H", tip, "--", path)
	if err != nil {
		return false
	}
	for _, commit := range strings.Fields(commits) {
		blob, err := run(ctx, r.Root, "rev-parse", commit+":"+path)
		if err != nil {
			continue
		}
		b, err := r.checkpointBlob(ctx, blob)
		if err != nil {
			continue
		}
		var m portable.Manifest
		if json.Unmarshal(b, &m) == nil && m.Checkpoint == c.Checkpoint {
			return true
		}
	}
	return false
}

// TaskCheckpoints only reads fetched/local branch tips, never contacting remotes.
// A manifest inherited by another branch is not that branch's task.
func (r Repo) TaskCheckpoints(ctx context.Context, only string) ([]TaskCheckpoint, error) {
	refs, err := run(ctx, r.Root, "for-each-ref", "--format=%(refname) %(objectname) %(symref)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil, err
	}
	var result []TaskCheckpoint
	matched := false
	for _, line := range strings.Split(refs, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ref, commit := fields[0], fields[1]
		short := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/")
		if only != "" && only != ref && only != short {
			continue
		}
		matched = true
		branch := short
		if strings.HasPrefix(ref, "refs/remotes/") {
			_, branch, _ = strings.Cut(short, "/")
		}
		tree, err := run(ctx, r.Root, "ls-tree", "-r", "-z", commit, "--", ".maestro/tasks/")
		if err != nil {
			return nil, err
		}
		blobs := map[string]string{}
		for _, entry := range strings.Split(tree, "\x00") {
			meta, name, ok := strings.Cut(entry, "\t")
			if !ok {
				continue
			}
			f := strings.Fields(meta)
			if len(f) == 3 && f[0] == "100644" && f[1] == "blob" {
				blobs[name] = f[2]
			}
		}
		if len(blobs) > 2000 {
			return nil, fmt.Errorf("too many task checkpoint files in %s", ref)
		}
		for _, entry := range strings.Split(tree, "\x00") {
			_, name, ok := strings.Cut(entry, "\t")
			if !ok || !strings.HasSuffix(name, "/task.json") {
				continue
			}
			parts := strings.Split(name, "/")
			if len(parts) != 4 {
				continue
			}
			manifest, err := r.checkpointBlob(ctx, blobs[name])
			if err != nil {
				return nil, fmt.Errorf("%s:%s: %w", ref, name, err)
			}
			handoff, err := r.checkpointBlob(ctx, blobs[strings.TrimSuffix(name, "task.json")+"handoff.md"])
			if err != nil {
				return nil, fmt.Errorf("%s:%s: %w", ref, name, err)
			}
			c, err := portable.Decode(manifest, handoff)
			if err != nil {
				return nil, fmt.Errorf("%s:%s: %w", ref, name, err)
			}
			if c.ID != parts[2] {
				return nil, fmt.Errorf("task UUID does not match directory in %s", ref)
			}
			if c.Branch != branch {
				continue
			}
			if _, err = run(ctx, r.Root, "check-ref-format", "refs/heads/"+c.Branch); err != nil {
				return nil, err
			}
			result = append(result, TaskCheckpoint{Checkpoint: c, Ref: ref, Commit: commit})
		}
	}
	if only != "" && (!matched || len(result) == 0) {
		return nil, fmt.Errorf("no task checkpoint on branch %q; fetch its published branch first", only)
	}
	return result, nil
}

func (r Repo) checkpointBlob(ctx context.Context, id string) ([]byte, error) {
	if id == "" {
		return nil, fmt.Errorf("checkpoint must contain regular task.json and handoff.md files")
	}
	size, err := run(ctx, r.Root, "cat-file", "-s", id)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(size)
	if err != nil || n > portable.MaxBytes {
		return nil, fmt.Errorf("checkpoint file exceeds 1 MiB")
	}
	// Unlike run, preserve exact whitespace for the checkpoint digest.
	return exec.CommandContext(ctx, "git", "-C", r.Root, "cat-file", "blob", id).Output()
}

// TaskWorktree returns an existing checkout for this branch, including the main
// checkout. Destructive cleanup continues to require ValidateWorktree.
func (r Repo) TaskWorktree(ctx context.Context, branch string) (string, error) {
	out, err := run(ctx, r.Root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", err
	}
	var path string
	for _, field := range strings.Split(out, "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			path = strings.TrimPrefix(field, "worktree ")
		}
		if field == "branch refs/heads/"+branch {
			return path, nil
		}
	}
	return "", nil
}

func (r Repo) ValidateTaskCheckout(ctx context.Context, path, branch string) error {
	return r.validateCheckout(ctx, path, branch, true)
}

// RestoreWorktree attaches an existing branch or creates it from the fetched
// commit. Never resets branches or runs repository setup commands on discovery.
func (r Repo) RestoreWorktree(ctx context.Context, path, branch, commit, ref string) error {
	if existing, err := r.TaskWorktree(ctx, branch); err != nil {
		return err
	} else if existing != "" {
		if err = r.ValidateTaskCheckout(ctx, path, branch); err != nil {
			return err
		}
		head, err := Head(ctx, path)
		if err != nil {
			return err
		}
		if head != commit && !r.IsAncestor(ctx, commit, head) {
			return fmt.Errorf("checkout %s is behind or diverges from the imported checkpoint; update it with Git before continuing", path)
		}
		return nil
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("restore destination already exists or is inaccessible: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	local, err := r.Commit(ctx, "refs/heads/"+branch)
	if err == nil {
		if local != commit && !r.IsAncestor(ctx, commit, local) {
			return fmt.Errorf("local branch %s differs from fetched checkpoint; reconcile it with Git first", branch)
		}
		_, err = run(ctx, r.Root, "worktree", "add", "--", path, branch)
		return err
	}
	_, err = run(ctx, r.Root, "worktree", "add", "-b", branch, "--", path, commit)
	if err != nil {
		return err
	}
	if strings.HasPrefix(ref, "refs/remotes/") {
		_, err = run(ctx, path, "branch", "--set-upstream-to="+ref, "--", branch)
	}
	return err
}

// EnablePortable replaces only Maestro's exact generated legacy ignore block.
// Legacy custom-agent files remain ignored; only checkpoint paths are exposed.
func (r Repo) EnablePortable(worktree string) error {
	if info, err := os.Lstat(filepath.Join(worktree, ".maestro")); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(".maestro must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	path := filepath.Join(r.CommonDir, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const legacy = "# Maestro task handoffs\n/.maestro/"
	const current = "# Maestro local task files\n/.maestro/*\n!/.maestro/tasks/\n!/.maestro/.gitignore"
	next := strings.ReplaceAll(string(b), legacy+"\n", current+"\n")
	if strings.HasSuffix(next, legacy) {
		next = strings.TrimSuffix(next, legacy) + current
	}
	if next != string(b) {
		if err = portable.WriteFile(r.CommonDir, filepath.Join("info", "exclude"), []byte(next)); err != nil {
			return err
		}
	}
	ignore := filepath.Join(worktree, ".maestro", ".gitignore")
	if info, e := os.Lstat(ignore); e == nil && !info.Mode().IsRegular() {
		return fmt.Errorf(".maestro/.gitignore must be a regular file")
	}
	old, err := os.ReadFile(ignore)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const rules = "# Maestro: publish checkpoints only\n/*\n!/tasks/\n!/.gitignore\n.maestro-write-*\n"
	if !strings.Contains(string(old), rules) {
		if len(old) > 0 && old[len(old)-1] != '\n' {
			old = append(old, '\n')
		}
		if err = portable.WriteFile(worktree, filepath.Join(".maestro", ".gitignore"), append(old, rules...)); err != nil {
			return err
		}
	}
	return nil
}

func (r Repo) CheckpointCommitted(ctx context.Context, path string, c portable.Checkpoint) error {
	var data [][]byte
	for _, name := range []string{"task.json", "handoff.md"} {
		file := filepath.ToSlash(filepath.Join(c.Directory(), name))
		out, err := run(ctx, path, "status", "--porcelain", "--", file)
		if err != nil {
			return err
		}
		if out != "" {
			return fmt.Errorf("checkpoint has uncommitted changes; review, commit and push .maestro/tasks")
		}
		if _, err = run(ctx, path, "cat-file", "-e", "HEAD:"+file); err != nil {
			return fmt.Errorf("checkpoint is not committed; add and commit .maestro/tasks first")
		}
		blob, err := run(ctx, path, "rev-parse", "HEAD:"+file)
		if err != nil {
			return err
		}
		b, err := r.checkpointBlob(ctx, blob)
		if err != nil {
			return err
		}
		data = append(data, b)
	}
	published, err := portable.Decode(data[0], data[1])
	if err != nil {
		return err
	}
	if published.Checkpoint != c.Checkpoint {
		return fmt.Errorf("committed checkpoint differs from local task context; checkpoint and commit again")
	}
	return nil
}
