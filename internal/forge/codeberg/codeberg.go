// Package codeberg implements Codeberg/Forgejo/Gitea's v1 pull request API.
package codeberg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/mmoehabb/maestro/internal/forge"
)

type Provider struct {
	base, host, configured string
	client                 *http.Client
	mu                     sync.Mutex
	retryAt                time.Time
}

func New(host, token string) *Provider {
	if host == "" {
		host = "codeberg.org"
	}
	return &Provider{
		host:       host,
		base:       "https://" + host + "/api/v1",
		configured: token,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *Provider) ResetAuth() {
	p.mu.Lock()
	p.retryAt = time.Time{}
	p.mu.Unlock()
}

type apiError struct{ status int }

func (e *apiError) Error() string { return fmt.Sprintf("Codeberg API returned HTTP %d", e.status) }

func (p *Provider) request(ctx context.Context, method, path string, body, out any) error {
	p.mu.Lock()
	retry := p.retryAt
	p.mu.Unlock()
	if time.Now().Before(retry) {
		return fmt.Errorf("codeberg rate limited; retry after %s", retry.Format(time.RFC3339))
	}
	token, _, err := Token(ctx, p.host, p.configured)
	if err != nil {
		return err
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		delay := time.Minute
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 {
			delay = time.Duration(min(seconds, 3600)) * time.Second
		}
		p.mu.Lock()
		p.retryAt = time.Now().Add(delay)
		p.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{resp.StatusCode}
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	return err
}

func repoPath(repo forge.Repo) string {
	return "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

func prPath(repo forge.Repo, number int) string {
	return repoPath(repo) + "/pulls/" + strconv.Itoa(number)
}

type pullRequest struct {
	Index    int    `json:"number"`
	State    string `json:"state"`
	URL      string `json:"html_url"`
	Merged   bool   `json:"merged"`
	MergeSHA string `json:"merged_commit_sha"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Mergeable *bool `json:"mergeable"`
}

func basic(pr pullRequest) *forge.PR {
	state := pr.State
	if pr.Merged {
		state = "merged"
	} else if state == "open" || state == "opened" {
		state = "open"
	}
	result := &forge.PR{
		Number:    pr.Index,
		URL:       pr.URL,
		State:     state,
		HeadSHA:   pr.Head.SHA,
		MergeSHA:  pr.MergeSHA,
		Mergeable: pr.Mergeable,
		CI:        "none",
		Review:    "none",
	}
	return result
}

func (p *Provider) PRForBranch(ctx context.Context, repo forge.Repo, branch string) (*forge.PR, error) {
	page := 1
	for {
		var list []pullRequest
		query := url.Values{
			"state": {"open"},
			"page":  {strconv.Itoa(page)},
			"limit": {"50"},
		}
		err := p.request(ctx, "GET", repoPath(repo)+"/pulls?"+query.Encode(), nil, &list)
		if err != nil {
			return nil, err
		}
		for _, pr := range list {
			if pr.Head.Ref == branch {
				return p.GetPR(ctx, repo, pr.Index)
			}
		}
		if len(list) < 50 {
			return nil, nil
		}
		page++
	}
}

func (p *Provider) GetPR(ctx context.Context, repo forge.Repo, number int) (*forge.PR, error) {
	var pr pullRequest
	err := p.request(ctx, "GET", prPath(repo, number), nil, &pr)
	if err != nil {
		return nil, err
	}
	result := basic(pr)
	if result.State != "open" {
		return result, nil
	}

	if pr.Head.SHA != "" {
		var statuses []struct {
			Status string `json:"status"`
		}
		err = p.request(ctx, "GET", repoPath(repo)+"/commits/"+url.PathEscape(pr.Head.SHA)+"/statuses", nil, &statuses)
		if err == nil && len(statuses) > 0 {
			hasPending := false
			hasFailure := false
			for _, st := range statuses {
				switch st.Status {
				case "pending":
					hasPending = true
				case "failure", "error":
					hasFailure = true
				}
			}
			switch {
			case hasFailure:
				result.CI = "failure"
			case hasPending:
				result.CI = "pending"
			default:
				result.CI = "success"
			}
		}
	}

	var reviews []struct {
		State string `json:"state"`
	}
	err = p.request(ctx, "GET", prPath(repo, number)+"/reviews", nil, &reviews)
	if err == nil {
		approved := false
		for _, rev := range reviews {
			if rev.State == "APPROVED" {
				approved = true
				break
			}
		}
		if approved {
			result.Review = "approved"
		}
	}

	return result, nil
}

func (p *Provider) CreatePR(ctx context.Context, repo forge.Repo, in forge.NewPR) (*forge.PR, error) {
	var pr pullRequest
	body := map[string]any{
		"title": in.Title,
		"body":  in.Body,
		"head":  in.Head,
		"base":  in.Base,
	}
	err := p.request(ctx, "POST", repoPath(repo)+"/pulls", body, &pr)
	if err != nil {
		return nil, err
	}
	return basic(pr), nil
}

func (p *Provider) Merge(ctx context.Context, repo forge.Repo, number int, method string) error {
	_, err := p.MergeWithHead(ctx, repo, number, method, "")
	return err
}

func (p *Provider) MergeWithHead(ctx context.Context, repo forge.Repo, number int, method, head string) (*forge.PR, error) {
	var doMethod string
	switch method {
	case "merge":
		doMethod = "merge"
	case "rebase":
		doMethod = "rebase"
	case "squash":
		doMethod = "squash"
	default:
		return nil, fmt.Errorf("unsupported merge method %q", method)
	}

	body := map[string]any{
		"Do": doMethod,
	}
	if head != "" {
		body["head_commit_id"] = head
	}
	err := p.request(ctx, "POST", prPath(repo, number)+"/merge", body, nil)
	if err != nil {
		return nil, err
	}
	return p.GetPR(ctx, repo, number)
}

func (*Provider) WebURL(pr *forge.PR) string {
	if pr == nil {
		return ""
	}
	return pr.URL
}

func (p *Provider) CheckAuth(ctx context.Context) error {
	var user struct{ ID int64 }
	return p.request(ctx, "GET", "/user", nil, &user)
}
