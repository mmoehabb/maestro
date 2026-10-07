package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mmoehabb/maestro/internal/forge"
)

func serverProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := New("")
	p.client.BaseURL, _ = url.Parse(srv.URL + "/")
	return p
}

func TestPRPollingCachesEachEndpointAndPaginatesReviews(t *testing.T) {
	var conditional atomic.Int32
	p := serverProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		path := r.URL.Path
		if r.Header.Get("If-None-Match") == "v1" {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "v1")
		switch {
		case strings.HasSuffix(path, "/pulls/7"):
			fmt.Fprint(w, `{"number":7,"state":"open","head":{"sha":"abc"},"html_url":"https://github.com/o/r/pull/7"}`)
		case strings.HasSuffix(path, "/status"):
			fmt.Fprint(w, `{"statuses":[{"state":"success"}]}`)
		case strings.HasSuffix(path, "/check-runs"):
			fmt.Fprint(w, `{"check_runs":[{"status":"completed","conclusion":"failure"}]}`)
		case strings.HasSuffix(path, "/reviews"):
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"user":{"login":"alice"},"state":"APPROVED"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=2>; rel="next"`, r.Host, path))
			fmt.Fprint(w, `[{"user":{"login":"alice"},"state":"CHANGES_REQUESTED"}]`)
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	for range 2 {
		pr, err := p.GetPR(context.Background(), forge.Repo{Owner: "o", Name: "r"}, 7)
		if err != nil {
			t.Fatal(err)
		}
		if pr.CI != "failure" || pr.Review != "approved" || pr.HeadSHA != "abc" {
			t.Fatalf("%+v", pr)
		}
	}
	if conditional.Load() != 5 {
		t.Fatalf("conditional requests = %d", conditional.Load())
	}
}

func TestCreateAndMergeUsesExpectedHead(t *testing.T) {
	p := serverProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		switch r.Method {
		case http.MethodPost:
			if in["base"] != "main" || in["body"] != "reviewed" {
				t.Errorf("%v", in)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":8,"state":"open","head":{"sha":"abc"}}`)
		case http.MethodPut:
			if in["sha"] != "abc" || in["merge_method"] != "squash" {
				t.Errorf("%v", in)
			}
			fmt.Fprint(w, `{"merged":true,"sha":"merged"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	repo := forge.Repo{Owner: "o", Name: "r"}
	if _, err := p.CreatePR(context.Background(), repo, forge.NewPR{Title: "Title", Body: "reviewed", Base: "main", Head: "task"}); err != nil {
		t.Fatal(err)
	}
	pr, err := p.MergeWithHead(context.Background(), repo, 8, "squash", "abc")
	if err != nil || pr.State != "merged" || pr.MergeSHA != "merged" {
		t.Fatal(pr, err)
	}
}

func TestRateLimitBackoff(t *testing.T) {
	var calls atomic.Int32
	p := serverProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"message":"slow down"}`)
	})
	for range 2 {
		if _, err := p.GetPR(context.Background(), forge.Repo{Owner: "o", Name: "r"}, 1); err == nil {
			t.Fatal("missing rate error")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("did not back off: %d calls", calls.Load())
	}
}

func TestTokenEnvironmentPrecedence(t *testing.T) {
	t.Setenv("GH_TOKEN", "first")
	t.Setenv("GITHUB_TOKEN", "second")
	token, source, err := Token(context.Background(), "third")
	if err != nil || token != "first" || source != "GH_TOKEN" {
		t.Fatal(source, err)
	}
	t.Setenv("GH_TOKEN", "")
	token, source, err = Token(context.Background(), "third")
	if err != nil || token != "second" || source != "GITHUB_TOKEN" {
		t.Fatal(source, err)
	}
}
