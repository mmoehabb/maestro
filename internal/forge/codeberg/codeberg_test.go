package codeberg

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
	t.Setenv("CODEBERG_TOKEN", "test-token")
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := New("codeberg.org", "")
	p.base = server.URL + "/api/v1"
	p.client = server.Client()
	return p
}

func TestPullRequestWorkflow(t *testing.T) {
	repo := forge.Repo{Owner: "user", Name: "repo"}
	p := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token test-token" {
			t.Errorf("missing or invalid auth: %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["head"] != "feature" || in["base"] != "main" {
				t.Errorf("bad create: %+v", in)
			}
			fmt.Fprint(w, `{"number":12,"state":"open","html_url":"https://codeberg.org/user/repo/pulls/12","head":{"ref":"feature","sha":"sha123"}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["Do"] != "squash" || in["head_commit_id"] != "sha123" {
				t.Errorf("bad merge: %+v", in)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[{"status":"success"}]`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/reviews"):
			fmt.Fprint(w, `[{"state":"APPROVED"}]`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/12"):
			fmt.Fprint(w, `{"number":12,"state":"open","html_url":"https://codeberg.org/user/repo/pulls/12","head":{"ref":"feature","sha":"sha123"}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
			fmt.Fprint(w, `[{"number":12,"state":"open","html_url":"https://codeberg.org/user/repo/pulls/12","head":{"ref":"feature","sha":"sha123"}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			fmt.Fprint(w, `{"id":1}`)
		default:
			t.Errorf("unhandled request: %s %s", r.Method, r.URL.Path)
		}
	})

	ctx := context.Background()

	if err := p.CheckAuth(ctx); err != nil {
		t.Fatal(err)
	}

	pr, err := p.CreatePR(ctx, repo, forge.NewPR{Title: "Title", Body: "Body", Head: "feature", Base: "main"})
	if err != nil || pr.Number != 12 {
		t.Fatalf("unexpected pr: %+v, err: %v", pr, err)
	}

	pr, err = p.PRForBranch(ctx, repo, "feature")
	if err != nil || pr.Number != 12 || pr.CI != "success" || pr.Review != "approved" {
		t.Fatalf("unexpected pr: %+v, err: %v", pr, err)
	}

	pr, err = p.MergeWithHead(ctx, repo, 12, "squash", "sha123")
	if err != nil || pr.Number != 12 {
		t.Fatalf("unexpected merge: %+v, err: %v", pr, err)
	}

	if url := p.WebURL(pr); url != "https://codeberg.org/user/repo/pulls/12" {
		t.Fatalf("unexpected url: %s", url)
	}
}
