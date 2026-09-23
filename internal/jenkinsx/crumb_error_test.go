// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// TestPostFormPropagatesCrumbIssuerFailure checks a crumb-issuer failure that
// is NOT a 404 propagates instead of being silently treated as "CSRF is
// disabled". Only a 404 means the endpoint isn't there; a 500 or a 401 means
// something is wrong, and posting without a crumb would just fail again with
// a confusing 403.
func TestPostFormPropagatesCrumbIssuerFailure(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var buildCalls int32
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/crumbIssuer/api/json":
					w.WriteHeader(status)
					_, _ = w.Write([]byte("crumb issuer unavailable"))
				case "/job/demo/build":
					atomic.AddInt32(&buildCalls, 1)
					w.WriteHeader(http.StatusCreated)
				default:
					t.Errorf("unexpected path %q", r.URL.Path)
				}
			})

			_, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil)
			if err == nil {
				t.Fatal("PostForm should fail when the crumb issuer errors")
			}
			var se *StatusError
			if !errors.As(err, &se) || se.StatusCode != status {
				t.Errorf("error = %v, want a *StatusError with status %d", err, status)
			}
			if buildCalls != 0 {
				t.Errorf("build endpoint was called %d times, want 0 (the crumb fetch failed first)", buildCalls)
			}
		})
	}
}

// TestCrumbDisabledIsCachedAcrossCalls checks the 404 ("CSRF protection is
// off") answer is cached like a real crumb, so every subsequent POST doesn't
// re-probe the crumb issuer.
func TestCrumbDisabledIsCachedAcrossCalls(t *testing.T) {
	var issuerCalls int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			atomic.AddInt32(&issuerCalls, 1)
			w.WriteHeader(http.StatusNotFound)
		case "/job/demo/build":
			if got := r.Header.Get("Jenkins-Crumb"); got != "" {
				t.Errorf("Jenkins-Crumb = %q, want no crumb header when CSRF is disabled", got)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	for i := range 3 {
		if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err != nil {
			t.Fatalf("PostForm call %d: %v", i, err)
		}
	}
	if issuerCalls != 1 {
		t.Errorf("crumbIssuer called %d times, want 1 (the disabled answer should be cached)", issuerCalls)
	}
}

// TestPostFormSendsFormBody confirms the form values actually reach Jenkins
// as an encoded body — the mechanism jenkins_trigger_build relies on to pass
// build parameters.
func TestPostFormSendsFormBody(t *testing.T) {
	var gotBody string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	})

	form := url.Values{"BRANCH": {"main"}, "DEBUG": {"true"}}
	if _, err := c.PostForm(context.Background(), "/job/demo/buildWithParameters", nil, form, nil); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	for _, want := range []string{"BRANCH=main", "DEBUG=true"} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("request body %q does not contain %q", gotBody, want)
		}
	}
}
