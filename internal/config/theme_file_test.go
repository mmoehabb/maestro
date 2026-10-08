package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveThemePreservesConfiguration(t *testing.T) {
	for _, input := range []string{
		"# preferences\ntheme = 'dark' # personal\n[agents.custom]\ncmd = 'my-agent'\n",
		"# preferences\n\"theme\" = \"\"\"dark\"\"\" # multiline\n[git]\ncleanup = 'ask'\n",
		"# no root theme\n[agents.custom]\ncmd = '''\ntheme = 'literal content'\n'''\n",
		"theme = 'dark'\r\n[git]\r\ncleanup = 'ask'\r\n",
	} {
		t.Run(strings.ReplaceAll(input[:min(20, len(input))], "\n", "_"), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := SaveTheme(path, "tokyo-night"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			name, err := FileTheme(path)
			if err != nil || name != "tokyo-night" {
				t.Fatalf("theme %q, %v; %s", name, err, got)
			}
			if strings.Contains(input, "# personal") && !strings.Contains(string(got), "# personal") {
				t.Fatal("lost comment")
			}
			start := strings.Index(input, "[")
			if start < 0 {
				t.Fatal("fixture has no table")
			}
			tail := input[start:]
			if !strings.Contains(string(got), tail) {
				t.Fatalf("changed unrelated tables: %s", got)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := SaveTheme(path, "light"); err != nil {
		t.Fatal(err)
	}
	if name, err := FileTheme(path); err != nil || name != "light" {
		t.Fatal(name, err)
	}
	before, _ := os.ReadFile(path)
	if err := SaveTheme(path, "unknown"); err == nil {
		t.Fatal("invalid theme accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("invalid choice changed file")
	}
	if err := os.WriteFile(path, []byte("invalid = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveTheme(path, "dark"); err == nil {
		t.Fatal("overwrote malformed configuration")
	}
}

func TestFourBuiltinThemes(t *testing.T) {
	themes := BuiltinThemes()
	if len(themes) != 4 {
		t.Fatal(len(themes))
	}
	seen := map[string]bool{}
	for _, theme := range themes {
		if seen[theme.ID] || theme.Name == "" {
			t.Fatal("invalid theme catalog")
		}
		seen[theme.ID] = true
		for _, color := range []string{theme.Palette.Background, theme.Palette.Foreground, theme.Palette.Accent} {
			if !hexColor.MatchString(color) {
				t.Fatal(color)
			}
		}
	}
}
