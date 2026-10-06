package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"

	"github.com/mmoehabb/maestro/internal/app"
	"github.com/mmoehabb/maestro/internal/config"
	"github.com/mmoehabb/maestro/internal/store"
	"github.com/mmoehabb/maestro/internal/testutil"
)

func TestNotesCommands(t *testing.T) {
	t.Chdir(testutil.Repo(t))
	oldConfig, oldData := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = t.TempDir(), t.TempDir()
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = oldConfig, oldData })
	if _, err := run(t, "new", "notes"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one\ntwo", "", "TODO: verify 中文"} {
		if _, err := run(t, "notes", "notes", "--set", text); err != nil {
			t.Fatal(err)
		}
		if out, err := run(t, "notes", "notes"); err != nil || out != text+"\n" {
			t.Fatal(out, err)
		}
	}
	file := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(file, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "notes", "notes", "--file", file); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := NewRootCmd(&out, &out)
	cmd.SetIn(strings.NewReader("from stdin"))
	cmd.SetArgs([]string{"notes", "notes", "--file", "-"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, err := run(t, "notes", "notes"); err != nil || got != "from stdin\n" {
		t.Fatal(got, err)
	}
	for _, args := range [][]string{
		{"notes", "notes", "--set", "x", "--file", file},
		{"notes", "missing", "--set", "x"},
		{"notes", "notes", "--set", strings.Repeat("a", store.MaxNotesBytes+1)},
		{"notes", "notes", "--set", string([]byte{0xff})},
	} {
		if _, err := run(t, args...); err == nil {
			t.Fatal("invalid notes accepted")
		}
	}
	s, err := app.Open(context.Background(), ".", config.DefaultPaths())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Store.Close()
	r, err := s.OpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = run(t, "notes", "notes", "--set", "blocked"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatal("notes writer bypassed lock", err)
	}
	if got, err := run(t, "notes", "notes"); err != nil || got != "from stdin\n" {
		t.Fatal("read-only notes failed while locked", got, err)
	}
}
