// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewClientDefaultsHTTPClient(t *testing.T) {
	c, err := NewClient("https://ci.example.com", "alice", "tok", nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.http != http.DefaultClient {
		t.Errorf("http = %v, want http.DefaultClient when none is supplied", c.http)
	}
}

func TestNewClientTrimsTrailingSlash(t *testing.T) {
	c, err := NewClient("  https://ci.example.com/  ", "alice", "tok", nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := c.base.String(); got != "https://ci.example.com" {
		t.Errorf("base = %q, want the trimmed URL", got)
	}
}

// deadClient returns a Client pointed at a server that is already closed, so
// every request fails at the transport layer rather than with an HTTP status.
func deadClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	client := srv.Client()
	srv.Close()

	c, err := NewClient(addr, "alice", "tok", client)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestGetPropagatesTransportError(t *testing.T) {
	c := deadClient(t)

	err := c.Get(context.Background(), "/api/json", nil, nil)
	if err == nil {
		t.Fatal("Get against a closed server should fail")
	}
	if !strings.Contains(err.Error(), "jenkinsx: GET /api/json") {
		t.Errorf("error = %v, want it to name the method and path", err)
	}
}

func TestTextPropagatesTransportError(t *testing.T) {
	c := deadClient(t)

	if _, _, err := c.Text(context.Background(), "/job/demo/1/logText/progressiveText", nil); err == nil {
		t.Fatal("Text against a closed server should fail")
	}
}

func TestPostFormPropagatesTransportError(t *testing.T) {
	c := deadClient(t)

	if _, err := c.PostForm(context.Background(), "/job/demo/build", nil, url.Values{}, nil); err == nil {
		t.Fatal("PostForm against a closed server should fail")
	}
}

func TestGetWithNilOutDiscardsBody(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Jenkins", "2.440.1")
		_, _ = w.Write([]byte(`this is not valid json`))
	})

	headers, err := c.GetWithHeaders(context.Background(), "/api/json", nil, nil)
	if err != nil {
		t.Fatalf("GetWithHeaders with a nil out should ignore the body: %v", err)
	}
	if got := headers.Get("X-Jenkins"); got != "2.440.1" {
		t.Errorf("X-Jenkins = %q, want 2.440.1", got)
	}
}

func TestGetReportsDecodeError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs": [truncated`))
	})

	var out struct {
		Jobs []string `json:"jobs"`
	}
	err := c.Get(context.Background(), "/api/json", nil, &out)
	if err == nil {
		t.Fatal("Get with a malformed JSON body should fail")
	}
	if !strings.Contains(err.Error(), "decoding response from /api/json") {
		t.Errorf("error = %v, want it to name the decode failure and path", err)
	}
}

func TestPostFormReportsDecodeError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`not json at all`))
	})

	var out struct {
		Canceled bool `json:"canceled"`
	}
	if _, err := c.PostForm(context.Background(), "/queue/cancelItem", nil, url.Values{}, &out); err == nil {
		t.Fatal("PostForm with a malformed JSON body should fail when decoding into out")
	}
}

// TestTextReportsReadError covers the io.ReadAll failure path: the handler
// promises more bytes than it writes and then aborts the connection, so the
// client sees a short read rather than a clean EOF.
func TestTextReportsReadError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	})

	_, _, err := c.Text(context.Background(), "/job/demo/1/logText/progressiveText", nil)
	if err == nil {
		t.Fatal("Text should fail when the response body is truncated mid-read")
	}
	if !strings.Contains(err.Error(), "reading response from") {
		t.Errorf("error = %v, want it to name the read failure", err)
	}
}

// TestErrorBodyIsTruncated checks a huge error body can't be read into a
// StatusError unbounded: do caps it at maxErrorBodyBytes.
func TestErrorBodyIsTruncated(t *testing.T) {
	huge := strings.Repeat("x", maxErrorBodyBytes*4)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(huge))
	})

	err := c.Get(context.Background(), "/api/json", nil, nil)
	if err == nil {
		t.Fatal("expected a 500 to produce an error")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want a *StatusError", err)
	}
	if len(se.Body) > maxErrorBodyBytes {
		t.Errorf("StatusError body is %d bytes, want it capped at %d", len(se.Body), maxErrorBodyBytes)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
}

func TestContextCancellationAborts(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Get(ctx, "/api/json", nil, nil); err == nil {
		t.Fatal("Get with a cancelled context should fail")
	}
}

// TestHeadReturnsHeadersWithoutBody covers the size probe the console tail
// depends on: Jenkins reports a build log's length in X-Text-Size, and a
// GET cannot be used to read it cheaply because an out-of-range start
// returns the whole log rather than nothing.
func TestHeadReturnsHeadersWithoutBody(t *testing.T) {
	var gotMethod string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("X-Text-Size", "7184")
		_, _ = w.Write([]byte("this body must not be needed"))
	})

	headers, err := c.Head(context.Background(), "/job/demo/1/logText/progressiveText", nil)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if gotMethod != http.MethodHead {
		t.Errorf("method = %q, want HEAD", gotMethod)
	}
	if got := headers.Get("X-Text-Size"); got != "7184" {
		t.Errorf("X-Text-Size = %q, want 7184", got)
	}
}

func TestHeadPropagatesStatusError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	if _, err := c.Head(context.Background(), "/missing", nil); err == nil {
		t.Error("expected an error for a 404")
	} else if !IsNotFound(err) {
		t.Errorf("err = %v, want a not-found StatusError", err)
	}
}
