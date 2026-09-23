// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, "alice", "tok_123", srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestNewClientRequiresBaseURL(t *testing.T) {
	if _, err := NewClient("", "alice", "tok", nil); err == nil {
		t.Fatal("NewClient(\"\") should return an error")
	}
}

func TestNewClientRejectsInvalidURL(t *testing.T) {
	if _, err := NewClient("http://[::1]:namedport", "alice", "tok", nil); err == nil {
		t.Fatal("NewClient with a malformed URL should return an error")
	}
}

func TestGetSendsBasicAuthAndDecodesJSON(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		if r.URL.Path != "/api/json" {
			t.Errorf("path = %q, want /api/json", r.URL.Path)
		}
		if got := r.URL.Query().Get("tree"); got != "jobs[name]" {
			t.Errorf("tree query = %q, want %q", got, "jobs[name]")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[{"name":"demo"}]}`))
	})

	var out struct {
		Jobs []struct {
			Name string `json:"name"`
		} `json:"jobs"`
	}
	query := url.Values{"tree": {"jobs[name]"}}
	if err := c.Get(context.Background(), "/api/json", query, &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !gotOK || gotUser != "alice" || gotPass != "tok_123" {
		t.Errorf("BasicAuth = (%q, %q, %v), want (alice, tok_123, true)", gotUser, gotPass, gotOK)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Name != "demo" {
		t.Errorf("decoded = %+v, want one job named demo", out)
	}
}

func TestGetMapsErrorStatus(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("No such job"))
	})

	err := c.Get(context.Background(), "/job/missing/api/json", nil, nil)
	if err == nil {
		t.Fatal("Get: expected an error")
	}
	if !IsNotFound(err) {
		t.Errorf("Get error = %v, want IsNotFound", err)
	}
}

func TestTextReturnsBodyAndHeaders(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Text-Size", "42")
		w.Header().Set("X-More-Data", "true")
		_, _ = w.Write([]byte("build output"))
	})

	body, headers, err := c.Text(context.Background(), "/job/demo/1/logText/progressiveText", nil)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if body != "build output" {
		t.Errorf("body = %q, want %q", body, "build output")
	}
	if headers.Get("X-Text-Size") != "42" || headers.Get("X-More-Data") != "true" {
		t.Errorf("headers = %v, missing expected values", headers)
	}
}

func TestPostFormAttachesCrumbAndReturnsHeaders(t *testing.T) {
	var sawCrumb string
	var sawContentType string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"crumb":"abc123","crumbRequestField":"Jenkins-Crumb"}`))
		case "/job/demo/buildWithParameters":
			sawCrumb = r.Header.Get("Jenkins-Crumb")
			sawContentType = r.Header.Get("Content-Type")
			w.Header().Set("Location", "http://example.com/queue/item/7/")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	form := url.Values{"FOO": {"bar"}}
	headers, err := c.PostForm(context.Background(), "/job/demo/buildWithParameters", nil, form, nil)
	if err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if sawCrumb != "abc123" {
		t.Errorf("Jenkins-Crumb header = %q, want abc123", sawCrumb)
	}
	if sawContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", sawContentType)
	}
	if got := headers.Get("Location"); got != "http://example.com/queue/item/7/" {
		t.Errorf("Location header = %q", got)
	}
}

func TestPostFormWithoutCrumbIssuer(t *testing.T) {
	var sawCrumbHeader bool
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/crumbIssuer/api/json":
			w.WriteHeader(http.StatusNotFound)
		case "/job/demo/build":
			if r.Header.Get("Jenkins-Crumb") != "" {
				sawCrumbHeader = true
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	})

	if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if sawCrumbHeader {
		t.Error("crumb header was sent even though crumbIssuer 404'd (CSRF disabled)")
	}
}

func TestPostFormDecodesJSONBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"canceled": true})
	})

	var out struct {
		Canceled bool `json:"canceled"`
	}
	if _, err := c.PostForm(context.Background(), "/queue/cancelItem", url.Values{"id": {"7"}}, url.Values{}, &out); err != nil {
		t.Fatalf("PostForm: %v", err)
	}
	if !out.Canceled {
		t.Error("out.Canceled = false, want true")
	}
}
