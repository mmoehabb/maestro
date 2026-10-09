// Package gitlab implements GitLab's v4 merge-request API.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		host = "gitlab.com"
	}
	return &Provider{host: host, base: "https://" + host + "/api/v4", configured: token, client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (p *Provider) ResetAuth() { p.mu.Lock(); p.retryAt = time.Time{}; p.mu.Unlock() }

type apiError struct{ status int }

func (e *apiError) Error() string { return fmt.Sprintf("GitLab API returned HTTP %d", e.status) }
func (p *Provider) request(ctx context.Context, method, path string, body, out any) (http.Header, error) {
	p.mu.Lock()
	retry := p.retryAt
	p.mu.Unlock()
	if time.Now().Before(retry) {
		return nil, fmt.Errorf("GitLab rate limited; retry after %s", retry.Format(time.RFC3339))
	}
	token, _, err := Token(ctx, p.host, p.configured)
	if err != nil {
		return nil, err
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
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
		return nil, &apiError{resp.StatusCode}
	}
	if out != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	return resp.Header, err
}
func project(repo forge.Repo) string { return "/projects/" + url.PathEscape(repo.Owner+"/"+repo.Name) }
func mrPath(repo forge.Repo, number int) string {
	return project(repo) + "/merge_requests/" + strconv.Itoa(number)
}

type mergeRequest struct {
	IID           int    `json:"iid"`
	State         string `json:"state"`
	URL           string `json:"web_url"`
	SHA           string `json:"sha"`
	MergeSHA      string `json:"merge_commit_sha"`
	SquashSHA     string `json:"squash_commit_sha"`
	Detailed      string `json:"detailed_merge_status"`
	SourceProject int    `json:"source_project_id"`
	TargetProject int    `json:"target_project_id"`
	Pipeline      *struct {
		Status string `json:"status"`
		SHA    string `json:"sha"`
	} `json:"head_pipeline"`
}

func basic(m mergeRequest) *forge.PR {
	state := m.State
	if state == "opened" || state == "locked" {
		state = "open"
	}
	result := &forge.PR{Number: m.IID, URL: m.URL, State: state, HeadSHA: m.SHA, MergeSHA: m.MergeSHA, CI: "none", Review: "none"}
	if result.MergeSHA == "" {
		result.MergeSHA = m.SquashSHA
	}
	if m.Detailed == "mergeable" {
		yes := true
		result.Mergeable = &yes
	} else if m.Detailed != "" && m.Detailed != "checking" && m.Detailed != "unchecked" && m.Detailed != "preparing" {
		no := false
		result.Mergeable = &no
	}
	if m.Pipeline != nil {
		result.CI = "pending"
		if m.Pipeline.SHA == m.SHA {
			switch m.Pipeline.Status {
			case "success", "skipped":
				result.CI = "success"
			case "failed", "canceled":
				result.CI = "failure"
			}
		}
	}
	return result
}

func (p *Provider) PRForBranch(ctx context.Context, repo forge.Repo, branch string) (*forge.PR, error) {
	page := "1"
	for {
		var list []mergeRequest
		query := url.Values{"state": {"opened"}, "source_branch": {branch}, "per_page": {"100"}, "page": {page}}
		headers, err := p.request(ctx, "GET", project(repo)+"/merge_requests?"+query.Encode(), nil, &list)
		if err != nil {
			return nil, err
		}
		for _, mr := range list {
			if mr.SourceProject == mr.TargetProject {
				return p.GetPR(ctx, repo, mr.IID)
			}
		}
		next := headers.Get("X-Next-Page")
		if next == "" {
			return nil, nil
		}
		n, err := strconv.Atoi(next)
		old, _ := strconv.Atoi(page)
		if err != nil || n <= old {
			return nil, fmt.Errorf("invalid GitLab pagination")
		}
		page = next
	}
}

func (p *Provider) GetPR(ctx context.Context, repo forge.Repo, number int) (*forge.PR, error) {
	var mr mergeRequest
	_, err := p.request(ctx, "GET", mrPath(repo, number), nil, &mr)
	if err != nil {
		return nil, err
	}
	result := basic(mr)
	if result.State != "open" {
		return result, nil
	}
	var approvals struct {
		Left     int               `json:"approvals_left"`
		Required int               `json:"approvals_required"`
		Approved []json.RawMessage `json:"approved_by"`
	}
	_, err = p.request(ctx, "GET", mrPath(repo, number)+"/approvals", nil, &approvals)
	var api *apiError
	if errors.As(err, &api) && (api.status == 404 || api.status == 403) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if approvals.Left > 0 {
		result.Review = "pending"
	} else if len(approvals.Approved) > 0 || approvals.Required > 0 {
		result.Review = "approved"
	}
	return result, nil
}

func (p *Provider) CreatePR(ctx context.Context, repo forge.Repo, in forge.NewPR) (*forge.PR, error) {
	var mr mergeRequest
	_, err := p.request(ctx, "POST", project(repo)+"/merge_requests", map[string]any{"title": in.Title, "description": in.Body, "source_branch": in.Head, "target_branch": in.Base, "remove_source_branch": false}, &mr)
	if err != nil {
		return nil, err
	}
	return basic(mr), nil
}

func (p *Provider) Merge(ctx context.Context, repo forge.Repo, number int, method string) error {
	_, err := p.MergeWithHead(ctx, repo, number, method, "")
	return err
}

func (p *Provider) MergeWithHead(ctx context.Context, repo forge.Repo, number int, method, head string) (*forge.PR, error) {
	if method != "squash" && method != "merge" {
		return nil, fmt.Errorf("GitLab supports squash or merge here; rebase is controlled by project settings")
	}
	if head == "" {
		pr, err := p.GetPR(ctx, repo, number)
		if err != nil {
			return nil, err
		}
		head = pr.HeadSHA
	}
	if head == "" {
		return nil, fmt.Errorf("GitLab merge request has no head SHA; refresh and retry")
	}
	var mr mergeRequest
	_, err := p.request(ctx, "PUT", mrPath(repo, number)+"/merge", map[string]any{"sha": head, "squash": method == "squash", "should_remove_source_branch": false}, &mr)
	if err != nil {
		return nil, err
	}
	result := basic(mr)
	if result.State != "merged" {
		return nil, fmt.Errorf("GitLab has not completed the merge; refresh before retrying")
	}
	return result, nil
}

func (*Provider) WebURL(pr *forge.PR) string {
	if pr == nil {
		return ""
	}
	return pr.URL
}

func (p *Provider) CheckAuth(ctx context.Context) error {
	var user struct{ ID int }
	_, err := p.request(ctx, "GET", "/user", nil, &user)
	return err
}
