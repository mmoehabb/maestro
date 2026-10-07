package forge

import "testing"

func TestParseRemote(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git"} {
		repo, err := ParseRemote(remote)
		if err != nil || repo.Owner != "owner" || repo.Name != "repo" {
			t.Fatal(repo, err)
		}
	}
	for _, remote := range []string{"/tmp/repo", "https://gitlab.com/owner/repo", "https://github.com/owner", "https://github.com/../repo", "https://github.com/owner/repo/extra"} {
		if _, err := ParseRemote(remote); err == nil {
			t.Fatalf("accepted %s", remote)
		}
	}
}
