// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

// mockServer stands up an httptest.Server implementing handler and returns a
// jenkinsx.Client pointed at it. Requests to /crumbIssuer/api/json get a
// canned 404 (CSRF disabled) unless handler itself serves that path.
func mockServer(t *testing.T, handler http.HandlerFunc) *jenkinsx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	c, err := jenkinsx.NewClient(srv.URL, "alice", "tok_123", srv.Client())
	if err != nil {
		t.Fatalf("jenkinsx.NewClient: %v", err)
	}
	return c
}
