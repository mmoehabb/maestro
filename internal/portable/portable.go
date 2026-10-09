// Package portable defines the versioned task checkpoint exchanged through Git.
package portable

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const MaxBytes = 1 << 20

type Manifest struct {
	Version    int    `json:"version"`
	ID         string `json:"id"`
	Checkpoint string `json:"checkpoint"`
	Parent     string `json:"parent,omitempty"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Goal       string `json:"goal"`
	Notes      string `json:"notes"`
	Agent      string `json:"agent"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"base_branch"`
	BaseCommit string `json:"base_commit"`
	Archived   bool   `json:"archived"`
}

type Checkpoint struct {
	Manifest
	Handoff string
}

func (c Checkpoint) digest() string {
	c.Checkpoint = ""
	b, _ := json.Marshal(c.Manifest)
	return fmt.Sprintf("%x", sha256.Sum256(append(append(b, '\n'), []byte(c.Handoff)...)))
}

func (c *Checkpoint) Seal() { c.Checkpoint = c.digest() }

func (c Checkpoint) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported task checkpoint version %d", c.Version)
	}
	id, err := uuid.Parse(c.ID)
	if err != nil || id.String() != c.ID {
		return fmt.Errorf("invalid portable task UUID")
	}
	if c.Slug == "" || strings.Trim(c.Slug, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || len(c.Slug) > 60 {
		return fmt.Errorf("invalid task slug")
	}
	if c.Title == "" || c.Agent == "" || c.Branch == "" || c.BaseBranch == "" || !hexID(c.BaseCommit, 40, 64) {
		return fmt.Errorf("incomplete task checkpoint")
	}
	if !hexID(c.Checkpoint, 64, 64) || (c.Parent != "" && !hexID(c.Parent, 64, 64)) || c.Checkpoint != c.digest() {
		return fmt.Errorf("checkpoint files do not match; restore both task.json and handoff.md from the same checkpoint")
	}
	b, _ := json.Marshal(c.Manifest)
	if len(b) > MaxBytes || len(c.Handoff) > MaxBytes || len(c.Notes) > 64<<10 || !utf8.ValidString(c.Handoff) {
		return fmt.Errorf("task checkpoint exceeds size limits or contains invalid UTF-8")
	}
	return nil
}

func hexID(s string, min, max int) bool {
	return (len(s) == min || len(s) == max) && strings.Trim(s, "0123456789abcdef") == ""
}

func Decode(manifest, handoff []byte) (Checkpoint, error) {
	var c Checkpoint
	if len(manifest) > MaxBytes || len(handoff) > MaxBytes || !utf8.Valid(manifest) {
		return c, fmt.Errorf("invalid or oversized task checkpoint")
	}
	if err := json.Unmarshal(manifest, &c.Manifest); err != nil {
		return c, fmt.Errorf("decode task checkpoint: %w", err)
	}
	c.Handoff = string(handoff)
	return c, c.Validate()
}

func (c Checkpoint) Directory() string { return filepath.Join(".maestro", "tasks", c.ID) }

func Write(root string, c Checkpoint) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c.Manifest, "", "  ")
	if err != nil {
		return err
	}
	// Install the manifest last. Its digest detects interrupted two-file writes.
	if err = WriteFile(root, filepath.Join(c.Directory(), "handoff.md"), []byte(c.Handoff)); err != nil {
		return err
	}
	return WriteFile(root, filepath.Join(c.Directory(), "task.json"), append(b, '\n'))
}

// WriteFile installs a file atomically and rejects symlinks at every component.
func WriteFile(root, name string, content []byte) error {
	if !filepath.IsLocal(name) {
		return fmt.Errorf("invalid checkpoint path")
	}
	dir := root
	parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err = os.Mkdir(dir, 0o700); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("checkpoint directory must not be a symlink: %s", dir)
		}
	}
	path := filepath.Join(root, name)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint destination must be a regular file: %s", path)
		}
		if old, err := os.ReadFile(path); err == nil && string(old) == string(content) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".maestro-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
