package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// DiffResult compares the base commit with the tracked working tree, including
// both staged and unstaged edits. Untracked paths are listed separately.
type DiffResult struct {
	Patch     string
	Untracked string
	Truncated bool
}

const MaxDiffBytes = 2 << 20

type limitedOutput struct {
	bytes.Buffer
	truncated bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := MaxDiffBytes - b.Len()
	if n > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func boundedGit(ctx context.Context, path string, args ...string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	var out, stderr limitedOutput
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("git diff: %w: %s", err, stderr.String())
	}
	return out.String(), out.truncated, nil
}

func WorktreeDiff(ctx context.Context, path, base string) (DiffResult, error) {
	var result DiffResult
	// Resolve the revision first, so config cannot inject options into diff.
	revision, err := run(ctx, path, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return result, err
	}
	result.Patch, result.Truncated, err = boundedGit(ctx, path, "--no-pager", "diff", "--no-ext-diff", "--no-textconv", "--no-color", revision, "--")
	if err != nil {
		return result, err
	}
	paths, truncated, err := boundedGit(ctx, path, "-c", "core.quotePath=true", "ls-files", "--others", "--exclude-standard")
	result.Untracked, result.Truncated = paths, result.Truncated || truncated
	return result, err
}
