package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLayers(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{ConfigFile: filepath.Join(dir, "global.toml"), DataDir: dir}
	if err := os.WriteFile(paths.ConfigFile, []byte("default_agent = 'custom'\neditor = 'nvim'\n[agents.custom]\ncmd = 'custom-agent'\nnew = ['{{.Prompt}}']\n[agents.codex]\ncmd = 'global-codex'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".maestro.toml"), []byte("[agents.codex]\ncmd = 'local-codex'\n[worktree]\ncopy = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(paths, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultAgent != "custom" || c.Editor != "nvim" || c.Agents["codex"].Cmd != "local-codex" || len(c.Agents["codex"].Resume) != 3 {
		t.Fatalf("bad merge: %+v", c)
	}
	if len(c.Worktree.Copy) != 0 || c.Worktree.Root != filepath.Join(dir, "worktrees") {
		t.Fatalf("bad worktree defaults: %+v", c.Worktree)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, data := range []string{
		"default_agent = 'missing'", "prefix_fallback = 'ctrl+m'", "prefix_fallback = 'ctrl+j'", "prefix = 'enter'", "icons = 'typo'", "unknown = true",
		"[activity]\nidle_after = '0s'", "[worktree]\ncopy = ['../secret']", "[worktree]\ncopy = ['.git/config']",
		"[agents.codex]\nnew = ['{{']", "[git]\ncleanup = 'force'", "[worktree]\nroot = 'relative'",
		"[agents.custom]\ncmd = 'custom'\nsession_create = ['create']",
		"[agents.claude]\nsession_create = ['create']",
		"[agents.cursor-agent]\nsession_file = 'session-id'",
	} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(Paths{path, dir}, ""); err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}
}

func TestMissingConfigUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(Paths{filepath.Join(dir, "missing"), dir}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultAgent != "codex" || c.Activity.IdleAfter != "2s" || c.Worktree.BranchPrefix != "" {
		t.Fatalf("bad defaults: %+v", c)
	}
}
