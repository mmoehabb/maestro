package portable

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func sample() Checkpoint {
	c := Checkpoint{Manifest: Manifest{Version: 1, ID: uuid.NewString(), Slug: "fix-login", Title: "Fix login", Agent: "codex", Branch: "fix-login", BaseBranch: "main", BaseCommit: strings.Repeat("a", 40)}, Handoff: "Continue with the regression test.\n"}
	c.Seal()
	return c
}

func TestCheckpointValidation(t *testing.T) {
	c := sample()
	b, _ := json.Marshal(c.Manifest)
	got, err := Decode(b, []byte(c.Handoff))
	if err != nil || got != c {
		t.Fatalf("roundtrip: %#v %v", got, err)
	}
	if _, err = Decode(b, []byte("interrupted write")); err == nil {
		t.Fatal("accepted mismatched files")
	}
	for _, mutate := range []func(*Checkpoint){
		func(c *Checkpoint) { c.Version = 2 },
		func(c *Checkpoint) { c.ID = "../../escape" },
		func(c *Checkpoint) { c.Slug = "../escape" },
		func(c *Checkpoint) { c.BaseCommit = "HEAD" },
		func(c *Checkpoint) { c.Notes = strings.Repeat("x", 65537) },
		func(c *Checkpoint) { c.Handoff = strings.Repeat("x", MaxBytes+1) },
	} {
		bad := c
		mutate(&bad)
		bad.Seal()
		if bad.Validate() == nil {
			t.Fatal("accepted invalid checkpoint")
		}
	}
}

func TestWriteRefusesSymlinkDirectories(t *testing.T) {
	for _, component := range []string{".maestro", ".maestro/tasks"} {
		t.Run(component, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			path := filepath.Join(root, component)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skip(err)
			}
			if err := Write(root, sample()); err == nil {
				t.Fatal("followed symlink")
			}
			files, err := os.ReadDir(outside)
			if err != nil || len(files) != 0 {
				t.Fatal("modified outside directory", err)
			}
		})
	}
}

func TestCheckpointAtomicRewrite(t *testing.T) {
	root, c := t.TempDir(), sample()
	for _, text := range []string{"first\n", "second\n"} {
		c.Handoff = text
		c.Seal()
		if err := Write(root, c); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(root, c.Directory(), "task.json"))
		if err != nil {
			t.Fatal(err)
		}
		h, err := os.ReadFile(filepath.Join(root, c.Directory(), "handoff.md"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Decode(b, h); err != nil {
			t.Fatal(err)
		}
	}
}
