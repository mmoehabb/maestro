package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitEmptyPRBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		set  bool
		body string
	}{
		{name: "omitted"},
		{name: "empty flag", args: []string{"--body", ""}, set: true},
		{name: "empty file", args: []string{"--body-file", path}, set: true},
		{name: "provided", args: []string{"--body", "reviewed"}, set: true, body: "reviewed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newForgeCmd("pr")
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			body, set, err := readPRBody(cmd)
			if err != nil || body != tc.body || set != tc.set {
				t.Fatalf("body=%q set=%v err=%v", body, set, err)
			}
		})
	}
}
