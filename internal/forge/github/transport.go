package github

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type cachedResponse struct {
	etag   string
	body   []byte
	header http.Header
}

// transport caches each representation independently: PR metadata can remain
// unchanged while its checks and reviews change. Mutations invalidate the cache.
type transport struct {
	base       http.RoundTripper
	configured string
	mu         sync.Mutex
	token      string
	cache      map[string]cachedResponse
	retryAt    time.Time
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	token := t.token
	cached := t.cache[req.URL.String()]
	retryAt := t.retryAt
	t.mu.Unlock()
	if time.Now().Before(retryAt) {
		return nil, fmt.Errorf("GitHub rate limit: retry after %s", retryAt.Format(time.RFC3339))
	}
	if token == "" {
		var err error
		token, _, err = Token(req.Context(), t.configured)
		if err != nil {
			return nil, err
		}
		t.mu.Lock()
		t.token = token
		t.mu.Unlock()
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)
	if req.Method == http.MethodGet && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.mu.Lock()
		if t.token == token {
			t.token = ""
			clear(t.cache)
		}
		t.mu.Unlock()
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GitHub rejected the credentials; run maestro auth login or update GH_TOKEN, GITHUB_TOKEN or github.token")
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden &&
		(resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		delay := time.Minute
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil {
			delay = max(delay, time.Duration(seconds)*time.Second)
		}
		if reset, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			delay = max(delay, time.Until(time.Unix(reset, 0)))
		}
		t.mu.Lock()
		t.retryAt = time.Now().Add(delay)
		t.mu.Unlock()
	}
	if req.Method != http.MethodGet {
		t.mu.Lock()
		clear(t.cache)
		t.mu.Unlock()
		return resp, nil
	}
	if resp.StatusCode == http.StatusNotModified && cached.etag != "" {
		_ = resp.Body.Close()
		resp.StatusCode = http.StatusOK
		resp.Status = "200 OK"
		resp.Body = io.NopCloser(bytes.NewReader(cached.body))
		resp.ContentLength = int64(len(cached.body))
		// Preserve pagination links from the original representation.
		resp.Header = cached.header.Clone()
		return resp, nil
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") == "" {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > 8<<20 {
		return nil, fmt.Errorf("GitHub response exceeds 8 MiB")
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	t.mu.Lock()
	if len(t.cache) >= 256 {
		clear(t.cache)
	}
	t.cache[req.URL.String()] = cachedResponse{resp.Header.Get("ETag"), body, resp.Header.Clone()}
	t.mu.Unlock()
	return resp, nil
}
