// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDoRejectsPathTraversal covers a real vulnerability: url.URL.JoinPath
// resolves ".." itself, so a caller-supplied segment could rewrite the
// target of an authenticated request — reading another job's config.xml, or
// with a base URL that has a path prefix, leaving Jenkins entirely while
// still sending the Basic auth header, handing the API token to whatever
// else is hosted there.
func TestDoRejectsPathTraversal(t *testing.T) {
	var reached string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, "u", "tok", srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	cases := []string{
		"/job/demo/1/artifact/../../../../job/other/config.xml",
		"/job/demo/1/artifact/..",
		"/job/../../etc/passwd",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			reached = ""
			err := c.Get(context.Background(), path, nil, nil)
			if err == nil {
				t.Fatalf("request succeeded and reached %q, want it refused", reached)
			}
			if !strings.Contains(err.Error(), "..") {
				t.Errorf("error should explain the %q segment was refused, got: %v", "..", err)
			}
			if reached != "" {
				t.Errorf("request still reached the server at %q; it must not be sent at all", reached)
			}
		})
	}
}

// TestDoAllowsDotsInsideSegments guards against over-blocking: a job or
// artifact legitimately named with dots must still work.
func TestDoAllowsDotsInsideSegments(t *testing.T) {
	var reached string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(srv.URL, "u", "tok", srv.Client())
	if err := c.Get(context.Background(), "/job/my..build/1/artifact/a.b..c.txt", nil, nil); err != nil {
		t.Fatalf("a path with dots inside segments should be allowed: %v", err)
	}
	if reached == "" {
		t.Error("request never reached the server")
	}
}

// TestStatusErrorSummarizesHTMLBody covers what an LLM actually receives on
// a failure. Jenkins answers most errors with a full HTML page; returning it
// verbatim spent kilobytes of context on markup, buried the real signal, and
// — because Jenkins embeds a CSRF crumb in the page head — copied a live
// session token into the conversation on every failed call.
func TestStatusErrorSummarizesHTMLBody(t *testing.T) {
	const crumb = "4d477dbf20d1117474356900d20e23df"
	body := `<!DOCTYPE html><html data-crumb-value="` + crumb + `"><head>` +
		`<title>Not Found - Jenkins</title></head><body>` +
		strings.Repeat("<div>padding</div>", 200) + `</body></html>`

	err := &StatusError{StatusCode: 404, Status: "404 Not Found", Body: body}
	got := err.Error()

	if strings.Contains(got, crumb) {
		t.Errorf("error leaks the CSRF crumb into the model's context: %s", got)
	}
	if strings.Contains(got, "<div>") || strings.Contains(got, "<!DOCTYPE") {
		t.Errorf("error still contains raw HTML markup: %s", got)
	}
	if !strings.Contains(got, "Not Found - Jenkins") {
		t.Errorf("error should keep the page title as the useful signal, got: %s", got)
	}
	if len(got) > 200 {
		t.Errorf("error is %d bytes, want a short summary: %s", len(got), got)
	}
}

// TestStatusErrorKeepsPlainTextBody pins that Jenkins' genuinely useful
// plain-text errors are not thrown away with the HTML.
func TestStatusErrorKeepsPlainTextBody(t *testing.T) {
	err := &StatusError{StatusCode: 400, Status: "400 Bad Request", Body: "Nothing is submitted"}
	if got := err.Error(); !strings.Contains(got, "Nothing is submitted") {
		t.Errorf("plain-text body should be preserved, got: %s", got)
	}
}

// TestStatusErrorHTMLWithoutTitleKeepsText checks an HTML page with no
// <title> is reduced to its readable text rather than to nothing.
func TestStatusErrorHTMLWithoutTitleKeepsText(t *testing.T) {
	err := &StatusError{StatusCode: 500, Status: "500 Internal Server Error", Body: "<html><body>oops</body></html>"}
	got := err.Error()
	if strings.Contains(got, "<html>") {
		t.Errorf("error still contains markup: %s", got)
	}
	if !strings.Contains(got, "500") {
		t.Errorf("error should still report the status: %s", got)
	}
	if !strings.Contains(got, "oops") {
		t.Errorf("error should keep the page's readable text: %s", got)
	}
}

// TestStatusErrorKeepsPermissionDiagnostic is the case that matters most for
// a misconfigured token. Jenkins' 403 page has no <title>, and its actual
// diagnostic sits in an HTML comment — reducing the body to "" would leave
// the model a bare "403 Forbidden" and throw away the answer.
func TestStatusErrorKeepsPermissionDiagnostic(t *testing.T) {
	body := `<html><head><meta http-equiv='refresh' content='1;url=/login'/></head><body>` +
		`Authentication required<!-- You are authenticated as: anonymous. ` +
		`Permission you need to have (but didn't): hudson.model.Item.Read --></body></html>`

	err := &StatusError{StatusCode: 403, Status: "403 Forbidden", Body: body}
	got := err.Error()

	if !strings.Contains(got, "hudson.model.Item.Read") {
		t.Errorf("error dropped the permission diagnostic, the one thing worth reporting: %s", got)
	}
	if strings.Contains(got, "<meta") || strings.Contains(got, "<body>") {
		t.Errorf("error still contains markup: %s", got)
	}
}

// TestStatusErrorWithoutTitleStillHidesCrumb: the title path is not the only
// way a body reaches the model, so the markup-stripping path must keep the
// CSRF crumb out too — it lives in a tag attribute.
func TestStatusErrorWithoutTitleStillHidesCrumb(t *testing.T) {
	const crumb = "4d477dbf20d1117474356900d20e23df"
	body := `<html data-crumb-value="` + crumb + `"><body>Authentication required</body></html>`

	got := (&StatusError{StatusCode: 403, Status: "403 Forbidden", Body: body}).Error()
	if strings.Contains(got, crumb) {
		t.Errorf("error leaks the CSRF crumb via the no-title path: %s", got)
	}
	if !strings.Contains(got, "Authentication required") {
		t.Errorf("error should keep the readable text: %s", got)
	}
}

// TestDoRejectsDotSegments covers the sibling of the ".." hole: url.JoinPath
// resolves "." too, which makes a segment VANISH rather than climb — so a
// job path of "." turned /job/./lastBuild/api/json into
// /job/lastBuild/api/json, the detail of a job named "lastBuild", returned
// as a success instead of an input error.
func TestDoRejectsDotSegments(t *testing.T) {
	var reached string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c, _ := NewClient(srv.URL, "u", "tok", srv.Client())
	for _, path := range []string{"/job/./lastBuild/api/json", "/job/team-a/job/./api/json"} {
		t.Run(path, func(t *testing.T) {
			reached = ""
			if err := c.Get(context.Background(), path, nil, nil); err == nil {
				t.Fatalf("request succeeded and reached %q, want it refused", reached)
			}
			if reached != "" {
				t.Errorf("request reached the server at %q; it must not be sent", reached)
			}
		})
	}
}

// TestHasJobSegmentsRejectsDotOnlyPaths pins the tool-level check so the
// caller gets a clear input error rather than the client's refusal.
func TestHasJobSegmentsRejectsDotOnlyPaths(t *testing.T) {
	for _, bad := range []string{".", "..", "/", "//", "team-a/.", "./team-a"} {
		if HasJobSegments(bad) {
			t.Errorf("HasJobSegments(%q) = true, want false", bad)
		}
	}
	for _, ok := range []string{"demo", "team-a/service-b", "my..build", "v1.2"} {
		if !HasJobSegments(ok) {
			t.Errorf("HasJobSegments(%q) = false, want true", ok)
		}
	}
}
