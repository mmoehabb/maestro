package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type Repo struct{ Owner, Name string }

// ParseRemote supports GitHub HTTPS, SSH and scp-style origin URLs.
func ParseRemote(remote string) (Repo, error) {
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	u, err := url.Parse(remote)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") || (u.Scheme != "https" && u.Scheme != "ssh" && u.Scheme != "git") {
		return Repo{}, fmt.Errorf("origin must be a github.com repository URL")
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[1] == "." || parts[0] == ".." || parts[1] == ".." {
		return Repo{}, fmt.Errorf("origin must identify a GitHub owner and repository")
	}
	return Repo{parts[0], parts[1]}, nil
}

type PR struct {
	Number                                    int
	URL, State, CI, Review, HeadSHA, MergeSHA string
	Mergeable                                 *bool
}

type NewPR struct{ Title, Body, Head, Base string }

type Provider interface {
	PRForBranch(context.Context, Repo, string) (*PR, error)
	CreatePR(context.Context, Repo, NewPR) (*PR, error)
	Merge(context.Context, Repo, int, string) error
	WebURL(*PR) string
}

// PRReader and HeadMerger are optional extensions used by the GitHub provider
// for polling details and optimistic expected-head merges.
type PRReader interface {
	GetPR(context.Context, Repo, int) (*PR, error)
}
type HeadMerger interface {
	MergeWithHead(context.Context, Repo, int, string, string) (*PR, error)
}
