package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"

	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestThemeCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	out, err := run(t, "theme")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Forest", "Paper", "Catppuccin", "Tokyo Night"} {
		if !strings.Contains(out, name) {
			t.Fatal(out)
		}
	}
	if _, err = run(t, "theme", "catppuccin"); err != nil {
		t.Fatal(err)
	}
	if name, e := config.FileTheme(config.DefaultPaths().ConfigFile); e != nil || name != "catppuccin" {
		t.Fatal(name, e)
	}
	if _, err = run(t, "theme", "unknown"); err == nil {
		t.Fatal("invalid name accepted")
	}
	if _, err = run(t, "theme", "dark", "--local"); err == nil {
		t.Fatal("local accepted outside git")
	}
	root := testutil.Repo(t)
	t.Chdir(root)
	local := filepath.Join(root, ".maestro.toml")
	if err = os.WriteFile(local, []byte("# keep me\ntheme = 'light'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, "theme", "dark")
	if err != nil || !strings.Contains(out, "overrides") {
		t.Fatal(out, err)
	}
	if _, err = run(t, "theme", "tokyo-night", "--local"); err != nil {
		t.Fatal(err)
	}
	if name, e := config.FileTheme(local); e != nil || name != "tokyo-night" {
		t.Fatal(name, e)
	}
	out, err = run(t, "theme")
	if err != nil || !strings.Contains(out, "Current selection: tokyo-night") {
		t.Fatal(out, err)
	}
	if _, err = run(t, "theme", "auto", "--local"); err != nil {
		t.Fatal(err)
	}
}
