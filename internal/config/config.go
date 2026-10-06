package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

//go:embed defaults.toml
var defaults []byte

type Agent struct {
	Cmd               string   `toml:"cmd"`
	New               []string `toml:"new"`
	Resume            []string `toml:"resume"`
	GenerateSessionID bool     `toml:"generate_session_id"`
	SessionCreate     []string `toml:"session_create"`
	ManualPrompt      bool     `toml:"manual_prompt"`
	SessionFile       string   `toml:"session_file"`
	InputHints        []string `toml:"input_hints"`
}

type Config struct {
	Prefix         string `toml:"prefix"`
	PrefixFallback string `toml:"prefix_fallback"`
	Icons          string `toml:"icons"`
	Theme          string `toml:"theme"`
	DefaultAgent   string `toml:"default_agent"`
	Activity       struct {
		IdleAfter string   `toml:"idle_after"`
		NotifyOn  []string `toml:"notify_on"`
	} `toml:"activity"`
	Worktree struct {
		Root         string   `toml:"root"`
		BranchPrefix string   `toml:"branch_prefix"`
		Copy         []string `toml:"copy"`
		Setup        []string `toml:"setup"`
	} `toml:"worktree"`
	Git struct {
		MergeMethod string `toml:"merge_method"`
		Cleanup     string `toml:"cleanup"`
	} `toml:"git"`
	Handoff struct {
		TokenBudget int `toml:"token_budget"`
	} `toml:"handoff"`
	Agents map[string]Agent `toml:"agents"`
}

type Paths struct{ ConfigFile, DataDir string }

func DefaultPaths() Paths {
	return Paths{filepath.Join(xdg.ConfigHome, "maestro", "config.toml"), filepath.Join(xdg.DataHome, "maestro")}
}

// Load overlays tables recursively, so overriding an agent command preserves its
// argument templates. Missing files are optional; unreadable/invalid files are not.
func Load(paths Paths, repo string) (Config, error) {
	var values map[string]any
	if err := toml.Unmarshal(defaults, &values); err != nil {
		return Config{}, err
	}
	files := []string{paths.ConfigFile}
	if repo != "" {
		files = append(files, filepath.Join(repo, ".maestro.toml"))
	}
	for _, path := range files {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		var overlay map[string]any
		if err := toml.Unmarshal(b, &overlay); err != nil {
			return Config{}, fmt.Errorf("config %s: %w", path, err)
		}
		merge(values, overlay)
	}
	b, err := toml.Marshal(values)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(&c); err != nil {
		return c, err
	}
	if c.Worktree.Root == "" {
		c.Worktree.Root = filepath.Join(paths.DataDir, "worktrees")
	}
	if !filepath.IsAbs(c.Worktree.Root) {
		return c, fmt.Errorf("worktree.root must be an absolute path")
	}
	return c, c.Validate()
}

func merge(dst, src map[string]any) {
	for k, v := range src {
		a, aok := dst[k].(map[string]any)
		b, bok := v.(map[string]any)
		if aok && bok {
			merge(a, b)
		} else {
			dst[k] = v
		}
	}
}

func (c Config) Validate() error {
	if c.Handoff.TokenBudget <= 0 {
		return fmt.Errorf("handoff.token_budget must be positive")
	}
	if _, ok := c.Agents[c.DefaultAgent]; !ok {
		return fmt.Errorf("default_agent %q is not configured", c.DefaultAgent)
	}
	d, err := time.ParseDuration(c.Activity.IdleAfter)
	if err != nil || d <= 0 {
		return fmt.Errorf("activity.idle_after must be a positive duration")
	}
	if c.Prefix == "" || c.Prefix == "enter" || c.PrefixFallback == "" || c.PrefixFallback == "ctrl+m" || c.PrefixFallback == "ctrl+j" || c.PrefixFallback == "enter" {
		return fmt.Errorf("prefixes must be nonempty and prefix_fallback must not capture Enter")
	}
	if !oneOf(c.Icons, "nerd", "unicode", "ascii") {
		return fmt.Errorf("invalid icons %q", c.Icons)
	}
	if !oneOf(c.Git.MergeMethod, "squash", "merge", "rebase") {
		return fmt.Errorf("invalid git.merge_method %q", c.Git.MergeMethod)
	}
	if !oneOf(c.Git.Cleanup, "ask", "auto", "never") {
		return fmt.Errorf("invalid git.cleanup %q", c.Git.Cleanup)
	}
	for _, path := range c.Worktree.Copy {
		if !filepath.IsLocal(path) || strings.Contains(path, "\\") || strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")[0] == ".git" {
			return fmt.Errorf("worktree.copy must contain safe relative paths: %q", path)
		}
	}
	for id, a := range c.Agents {
		if a.SessionFile != "" && (!filepath.IsLocal(a.SessionFile) || strings.Contains(a.SessionFile, "\\")) {
			return fmt.Errorf("agent %s session_file must be a local relative path", id)
		}
		if a.GenerateSessionID && len(a.Resume) == 0 {
			return fmt.Errorf("agent %s generates session IDs but has no resume arguments", id)
		}
		if len(a.SessionCreate) > 0 && (a.GenerateSessionID || a.SessionFile != "" || len(a.Resume) == 0) {
			return fmt.Errorf("agent %s session_create requires resume arguments and cannot be combined with other session ID sources", id)
		}
		if strings.TrimSpace(id) == "" || strings.TrimSpace(a.Cmd) == "" {
			return fmt.Errorf("agent %q needs a command", id)
		}
		for _, arg := range append(append([]string{}, a.New...), a.Resume...) {
			if _, err := template.New("arg").Option("missingkey=error").Parse(arg); err != nil {
				return fmt.Errorf("agent %s: %w", id, err)
			}
		}
	}
	return nil
}

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
