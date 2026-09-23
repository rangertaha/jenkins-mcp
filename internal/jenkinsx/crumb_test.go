// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
)

// TestPostFormRetriesOnceOnStaleCrumb simulates a crumb that was valid when
// fetched but rejected by the server (e.g. because Jenkins rotated it), and
// checks the client clears its cache and retries exactly once with a fresh
// crumb rather than failing or retrying forever.
func TestPostFormRetriesOnceOnStaleCrumb(t *testing.T) {
	var crumbIssuerCalls int32
	var buildAttempts int32

	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			n := atomic.AddInt32(&crumbIssuerCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"crumb":"crumb-` + strconv.Itoa(int(n)) + `","crumbRequestField":"Jenkins-Crumb"}`))
		case "/job/demo/build":
			n := atomic.AddInt32(&buildAttempts, 1)
			if n == 1 {
				// First attempt: reject the (first) crumb as stale.
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("No valid crumb was included in the request"))
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if buildAttempts != 2 {
		t.Errorf("build endpoint called %d times, want 2 (initial + one retry)", buildAttempts)
	}
	if crumbIssuerCalls != 2 {
		t.Errorf("crumbIssuer called %d times, want 2 (cache cleared before retry)", crumbIssuerCalls)
	}
}

// TestPostFormGivesUpAfterOneRetry checks a persistently-rejected crumb
// fails after exactly one retry, not an infinite loop.
func TestPostFormGivesUpAfterOneRetry(t *testing.T) {
	var buildAttempts int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"crumb":"stale","crumbRequestField":"Jenkins-Crumb"}`))
		case "/job/demo/build":
			atomic.AddInt32(&buildAttempts, 1)
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("No valid crumb was included in the request"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	_, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil)
	if err == nil {
		t.Fatal("PostForm: expected an error")
	}
	if buildAttempts != 2 {
		t.Errorf("build endpoint called %d times, want exactly 2 (no infinite retry loop)", buildAttempts)
	}
}

// TestPostFormRetriesOn403RegardlessOfBodyWording checks the retry-once
// path triggers on any 403, not just one whose body happens to mention
// "crumb" — Jenkins' wording varies by version, plugin, and locale.
func TestPostFormRetriesOn403RegardlessOfBodyWording(t *testing.T) {
	var crumbIssuerCalls, buildAttempts int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			n := atomic.AddInt32(&crumbIssuerCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"crumb":"crumb-` + strconv.Itoa(int(n)) + `","crumbRequestField":"Jenkins-Crumb"}`))
		case "/job/demo/build":
			n := atomic.AddInt32(&buildAttempts, 1)
			if n == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("Forbidden")) // no mention of "crumb"
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if buildAttempts != 2 {
		t.Errorf("build endpoint called %d times, want 2 (retried once despite crumb-less error body)", buildAttempts)
	}
	if crumbIssuerCalls != 2 {
		t.Errorf("crumbIssuer called %d times, want 2 (cache cleared before retry)", crumbIssuerCalls)
	}
}

// TestCrumbCachedAcrossCalls checks the crumb is fetched once and reused,
// not re-fetched on every POST.
func TestCrumbCachedAcrossCalls(t *testing.T) {
	var crumbIssuerCalls int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			atomic.AddInt32(&crumbIssuerCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`))
		case "/job/demo/build":
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	for i := 0; i < 3; i++ {
		if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err != nil {
			t.Fatalf("PostForm call %d: %v", i, err)
		}
	}
	if crumbIssuerCalls != 1 {
		t.Errorf("crumbIssuer called %d times, want 1 (crumb should be cached)", crumbIssuerCalls)
	}
}
