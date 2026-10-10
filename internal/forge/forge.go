package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type Repo struct{ Owner, Name string }

// RemoteHost accepts HTTPS, SSH and scp-style remotes.
func RemoteHost(remote string) string {
	u, err := remoteURL(remote)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func remoteURL(remote string) (*url.URL, error) {
	if !strings.Contains(remote, "://") {
		if left, path, ok := strings.Cut(remote, ":"); ok && strings.Contains(left, "@") {
			remote = "ssh://" + left + "/" + path
		}
	}
	u, err := url.Parse(remote)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" && u.Scheme != "ssh" && u.Scheme != "git" {
		return nil, fmt.Errorf("unsupported Git remote URL")
	}
	return u, nil
}

// ParseRemote accepts standard hosts (GitHub, GitLab, Codeberg) or explicitly configured hosts.
func ParseRemote(remote string, extraHosts ...string) (Repo, error) {
	u, err := remoteURL(remote)
	if err != nil {
		return Repo{}, err
	}
	host := strings.ToLower(u.Hostname())
	allowed := host == "github.com" || host == "gitlab.com" || host == "codeberg.org"
	for _, configured := range extraHosts {
		if configured != "" && strings.EqualFold(host, configured) {
			allowed = true
		}
	}
	if !allowed {
		return Repo{}, fmt.Errorf("origin must identify GitHub, GitLab, Codeberg, or a configured forge host")
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"), "/")
	if len(parts) < 2 || (host == "github.com" && len(parts) != 2) {
		return Repo{}, fmt.Errorf("origin must identify a namespace and repository")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Repo{}, fmt.Errorf("invalid repository path")
		}
	}
	return Repo{Owner: strings.Join(parts[:len(parts)-1], "/"), Name: parts[len(parts)-1]}, nil
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
