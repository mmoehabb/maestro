package core

import "testing"

func TestSlug(t *testing.T) {
	for title, want := range map[string]string{" Fix AUTH! ": "fix-auth", "../../escape": "escape", "---": "", "a   b___c": "a-b-c"} {
		if got := Slug(title); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}
}
