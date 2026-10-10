package forge

import "testing"

func TestParseRemote(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repo.git", "https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git"} {
		repo, err := ParseRemote(remote)
		if err != nil || repo.Owner != "owner" || repo.Name != "repo" {
			t.Fatal(repo, err)
		}
	}
	for _, remote := range []string{"/tmp/repo", "https://unknown.example/owner/repo", "https://github.com/owner", "https://github.com/../repo", "https://github.com/owner/repo/extra"} {
		if _, err := ParseRemote(remote); err == nil {
			t.Fatalf("accepted %s", remote)
		}
	}
}

func TestGitLabRemoteNamespaces(t *testing.T) {
	for _, remote := range []string{"git@gitlab.com:group/subgroup/repo.git", "ssh://git@gitlab.com/group/subgroup/repo.git", "https://gitlab.com/group/subgroup/repo"} {
		repo, err := ParseRemote(remote)
		if err != nil || repo.Owner != "group/subgroup" || repo.Name != "repo" {
			t.Fatal(repo, err)
		}
	}
	repo, err := ParseRemote("git@git.example:team/nested/repo.git", "git.example")
	if err != nil || repo.Owner != "team/nested" {
		t.Fatal(repo, err)
	}
	if _, err = ParseRemote("https://git.example/team/repo"); err == nil {
		t.Fatal("unconfigured host accepted")
	}
}

func TestCodebergRemote(t *testing.T) {
	for _, remote := range []string{"git@codeberg.org:owner/repo.git", "https://codeberg.org/owner/repo.git", "ssh://git@codeberg.org/owner/repo.git"} {
		repo, err := ParseRemote(remote)
		if err != nil || repo.Owner != "owner" || repo.Name != "repo" {
			t.Fatal(repo, err)
		}
	}
	repo, err := ParseRemote("git@forgejo.example:owner/repo.git", "forgejo.example")
	if err != nil || repo.Owner != "owner" || repo.Name != "repo" {
		t.Fatal(repo, err)
	}
}
