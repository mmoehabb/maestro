package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmoehabb/maestro/internal/forge"
)

func testProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	t.Setenv("GITLAB_TOKEN", "test-token")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := New("gitlab.example", "")
	p.base = server.URL + "/api/v4"
	p.client = server.Client()
	return p
}

func TestMergeRequestWorkflow(t *testing.T) {
	repo := forge.Repo{Owner: "group/subgroup", Name: "project"}
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		if !strings.HasPrefix(r.URL.EscapedPath(), "/api/v4/projects/group%2Fsubgroup%2Fproject/merge_requests") {
			t.Error(r.URL.EscapedPath())
		}
		switch {
		case r.Method == http.MethodPost:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["description"] != "" || in["source_branch"] != "feature" || in["remove_source_branch"] != false {
				t.Errorf("bad create: %+v", in)
			}
			fmt.Fprint(w, `{"iid":7,"state":"opened","sha":"head","web_url":"https://gitlab.example/group/subgroup/project/-/merge_requests/7"}`)
		case r.Method == http.MethodPut:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["sha"] != "head" || in["squash"] != true || in["should_remove_source_branch"] != false {
				t.Errorf("unsafe merge: %+v", in)
			}
			fmt.Fprint(w, `{"iid":7,"state":"merged","sha":"head","squash_commit_sha":"merged","web_url":"https://gitlab.example/mr/7"}`)
		case strings.HasSuffix(r.URL.Path, "/approvals"):
			fmt.Fprint(w, `{"approvals_left":0,"approvals_required":1,"approved_by":[{}]}`)
		case strings.HasSuffix(r.URL.Path, "/7"):
			fmt.Fprint(w, `{"iid":7,"state":"opened","sha":"head","detailed_merge_status":"mergeable","head_pipeline":{"sha":"head","status":"success"}}`)
		default:
			if r.URL.Query().Get("source_branch") != "feature" {
				t.Error("branch filter missing")
			}
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("X-Next-Page", "2")
				fmt.Fprint(w, `[{"iid":1,"source_project_id":2,"target_project_id":3}]`)
			} else {
				fmt.Fprint(w, `[{"iid":7,"source_project_id":3,"target_project_id":3}]`)
			}
		}
	})
	ctx := context.Background()
	pr, err := p.PRForBranch(ctx, repo, "feature")
	if err != nil || pr.Number != 7 || pr.CI != "success" || pr.Review != "approved" || pr.Mergeable == nil || !*pr.Mergeable {
		t.Fatal(pr, err)
	}
	pr, err = p.CreatePR(ctx, repo, forge.NewPR{Title: "Fix", Head: "feature", Base: "main"})
	if err != nil || pr.State != "open" {
		t.Fatal(pr, err)
	}
	pr, err = p.MergeWithHead(ctx, repo, 7, "squash", "head")
	if err != nil || pr.State != "merged" || pr.MergeSHA != "merged" {
		t.Fatal(pr, err)
	}
	if _, err = p.MergeWithHead(ctx, repo, 7, "rebase", "head"); err == nil {
		t.Fatal("unsupported merge accepted")
	}
}

func TestGitLabFailuresAndUnknownStates(t *testing.T) {
	for _, status := range []int{401, 409, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			p := testProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret response")
			})
			_, err := p.MergeWithHead(context.Background(), forge.Repo{Owner: "g", Name: "p"}, 1, "merge", "old-head")
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			if status == 429 {
				_ = p.CheckAuth(context.Background())
				if calls != 1 {
					t.Fatal("backoff ignored")
				}
			}
		})
	}
	stale := basic(mergeRequest{SHA: "new", Pipeline: &struct {
		Status string `json:"status"`
		SHA    string `json:"sha"`
	}{"success", "old"}})
	if stale.CI != "pending" || stale.Mergeable != nil {
		t.Fatal(stale)
	}
}

func TestUnavailableApprovals(t *testing.T) {
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/approvals") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"iid":1,"state":"opened","sha":"head"}`)
	})
	pr, err := p.GetPR(context.Background(), forge.Repo{Owner: "g", Name: "p"}, 1)
	if err != nil || pr.Review != "none" {
		t.Fatal(pr, err)
	}
}
