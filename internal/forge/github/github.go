// Package github implements the forge provider using GitHub's REST API.
package github

import (
	"context"
	"fmt"
	"net/http"
	"time"

	gh "github.com/google/go-github/v74/github"

	"github.com/mmoehabb/maestro/internal/forge"
)

type Provider struct{ client *gh.Client }

func New(token string) *Provider {
	tr := &transport{base: http.DefaultTransport, configured: token, cache: map[string]cachedResponse{}}
	return &Provider{client: gh.NewClient(&http.Client{Transport: tr, Timeout: 30 * time.Second})}
}

func (p *Provider) CheckAuth(ctx context.Context) error {
	_, _, err := p.client.Users.Get(ctx, "")
	return err
}

func basic(pr *gh.PullRequest) *forge.PR {
	state := pr.GetState()
	if pr.GetMerged() || pr.MergedAt != nil {
		state = "merged"
	}
	return &forge.PR{Number: pr.GetNumber(), URL: pr.GetHTMLURL(), State: state, HeadSHA: pr.GetHead().GetSHA(), MergeSHA: pr.GetMergeCommitSHA(), Mergeable: pr.Mergeable}
}

func (p *Provider) PRForBranch(ctx context.Context, repo forge.Repo, branch string) (*forge.PR, error) {
	list, _, err := p.client.PullRequests.List(ctx, repo.Owner, repo.Name, &gh.PullRequestListOptions{State: "open", Head: repo.Owner + ":" + branch, ListOptions: gh.ListOptions{PerPage: 100}})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return p.GetPR(ctx, repo, list[0].GetNumber())
}

func (p *Provider) GetPR(ctx context.Context, repo forge.Repo, number int) (*forge.PR, error) {
	pr, _, err := p.client.PullRequests.Get(ctx, repo.Owner, repo.Name, number)
	if err != nil {
		return nil, err
	}
	result := basic(pr)
	if result.State != "open" {
		return result, nil
	}
	result.CI, err = p.checks(ctx, repo, result.HeadSHA)
	if err != nil {
		return nil, err
	}
	result.Review, err = p.reviews(ctx, repo, number)
	return result, err
}

func (p *Provider) CreatePR(ctx context.Context, repo forge.Repo, in forge.NewPR) (*forge.PR, error) {
	pr, _, err := p.client.PullRequests.Create(ctx, repo.Owner, repo.Name, &gh.NewPullRequest{Title: &in.Title, Body: &in.Body, Head: &in.Head, Base: &in.Base})
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
	if head == "" {
		pr, _, err := p.client.PullRequests.Get(ctx, repo.Owner, repo.Name, number)
		if err != nil {
			return nil, err
		}
		head = pr.GetHead().GetSHA()
	}
	result, _, err := p.client.PullRequests.Merge(ctx, repo.Owner, repo.Name, number, "", &gh.PullRequestOptions{MergeMethod: method, SHA: head})
	if err != nil {
		return nil, err
	}
	if !result.GetMerged() {
		return nil, fmt.Errorf("PR was not merged: %s", result.GetMessage())
	}
	// Do not depend on another request succeeding after the remote mutation.
	return &forge.PR{Number: number, State: "merged", HeadSHA: head, MergeSHA: result.GetSHA(), URL: fmt.Sprintf("https://github.com/%s/%s/pull/%d", repo.Owner, repo.Name, number)}, nil
}

func (*Provider) WebURL(pr *forge.PR) string {
	if pr == nil {
		return ""
	}
	return pr.URL
}

func combine(a, b string) string {
	rank := map[string]int{"none": 0, "success": 1, "pending": 2, "failure": 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func (p *Provider) checks(ctx context.Context, repo forge.Repo, sha string) (string, error) {
	result := "none"
	opts := &gh.ListOptions{PerPage: 100}
	for {
		statuses, response, err := p.client.Repositories.GetCombinedStatus(ctx, repo.Owner, repo.Name, sha, opts)
		if err != nil {
			return "", err
		}
		for _, status := range statuses.Statuses {
			state := status.GetState()
			switch state {
			case "error", "failure":
				state = "failure"
			case "success":
			default:
				state = "pending"
			}
			result = combine(result, state)
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	checks := &gh.ListCheckRunsOptions{Filter: gh.Ptr("latest"), ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		runs, response, err := p.client.Checks.ListCheckRunsForRef(ctx, repo.Owner, repo.Name, sha, checks)
		if err != nil {
			return "", err
		}
		for _, run := range runs.CheckRuns {
			state := "pending"
			if run.GetStatus() == "completed" {
				switch run.GetConclusion() {
				case "success", "neutral", "skipped":
					state = "success"
				default:
					state = "failure"
				}
			}
			result = combine(result, state)
		}
		if response.NextPage == 0 {
			break
		}
		checks.Page = response.NextPage
	}
	return result, nil
}

func (p *Provider) reviews(ctx context.Context, repo forge.Repo, number int) (string, error) {
	latest := map[string]string{}
	opts := &gh.ListOptions{PerPage: 100}
	for {
		reviews, response, err := p.client.PullRequests.ListReviews(ctx, repo.Owner, repo.Name, number, opts)
		if err != nil {
			return "", err
		}
		for _, review := range reviews {
			switch review.GetState() {
			case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
				latest[review.GetUser().GetLogin()] = review.GetState()
			}
		}
		if response.NextPage == 0 {
			break
		}
		opts.Page = response.NextPage
	}
	result := "none"
	for _, state := range latest {
		if state == "CHANGES_REQUESTED" {
			return "changes_requested", nil
		}
		if state == "APPROVED" {
			result = "approved"
		}
	}
	return result, nil
}
