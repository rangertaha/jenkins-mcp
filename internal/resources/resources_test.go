// SPDX-License-Identifier: GPL-3.0-or-later

package resources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// mockServer stands up an httptest.Server implementing handler and returns a
// jenkinsx.Client pointed at it.
func mockServer(t *testing.T, handler http.HandlerFunc) *jenkinsx.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := jenkinsx.NewClient(srv.URL, "alice", "tok_123", srv.Client())
	if err != nil {
		t.Fatalf("jenkinsx.NewClient: %v", err)
	}
	return c
}

// isNotFoundResource reports whether err is the protocol-level
// "resource not found" rather than a generic failure.
func isNotFoundResource(err error) bool {
	var jerr *jsonrpc.Error
	if errors.As(err, &jerr) {
		return jerr.Code == mcp.CodeResourceNotFound
	}
	return false
}

func TestInfo(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" {
			t.Errorf("path = %q, want /api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Jenkins reports its version only in this header.
		w.Header().Set("X-Jenkins", "2.568.3")
		_, _ = w.Write([]byte(`{"mode":"NORMAL","nodeDescription":"the controller","nodeName":"",
			"numExecutors":2,"url":"http://ci.example.com/","useSecurity":true,"quietingDown":false}`))
	})
	r := &jenkinsResources{client: c}

	body, err := r.info(context.Background(), InfoURI)
	if err != nil {
		t.Fatalf("info: %v", err)
	}

	var got info
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("info body is not valid JSON: %v\n%s", err, body)
	}
	if got.Version != "2.568.3" {
		t.Errorf("Version = %q, want it taken from the X-Jenkins header", got.Version)
	}
	if got.Mode != "NORMAL" || got.NumExecutors != 2 || !got.UseSecurity {
		t.Errorf("info = %+v, want the body fields decoded", got)
	}
}

func TestInfoPropagatesError(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	r := &jenkinsResources{client: c}

	if _, err := r.info(context.Background(), InfoURI); err == nil {
		t.Error("info succeeded against a 500, want an error")
	}
}

func TestJobConfig(t *testing.T) {
	const wantXML = `<?xml version='1.1' encoding='UTF-8'?><project><description>demo</description></project>`

	cases := []struct {
		name     string
		uri      string
		wantPath string
	}{
		{"top-level job", "jenkins://job/demo/config.xml", "/job/demo/config.xml"},
		{"nested job", "jenkins://job/team-a/service-b/config.xml", "/job/team-a/job/service-b/config.xml"},
		{"job name with a space", "jenkins://job/my%20job/config.xml", "/job/my job/config.xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, c.wantPath)
				}
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(wantXML))
			})
			res := &jenkinsResources{client: client}

			body, err := res.jobConfig(context.Background(), c.uri)
			if err != nil {
				t.Fatalf("jobConfig: %v", err)
			}
			if body != wantXML {
				t.Errorf("body = %q, want the config XML", body)
			}
		})
	}
}

// TestJobConfigEscapesExactlyOnce guards the percent-decode step: matched URI
// values arrive decoded, and jenkinsx.JobPath re-escapes each segment, so
// skipping the decode would send "my%2520job" to Jenkins.
func TestJobConfigEscapesExactlyOnce(t *testing.T) {
	var gotRaw string
	client := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.EscapedPath()
		_, _ = w.Write([]byte("<project/>"))
	})
	res := &jenkinsResources{client: client}

	if _, err := res.jobConfig(context.Background(), "jenkins://job/my%20job/config.xml"); err != nil {
		t.Fatalf("jobConfig: %v", err)
	}
	if gotRaw != "/job/my%20job/config.xml" {
		t.Errorf("escaped path = %q, want /job/my%%20job/config.xml (escaped exactly once)", gotRaw)
	}
}

func TestJobConfigNotFound(t *testing.T) {
	client := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	res := &jenkinsResources{client: client}

	_, err := res.jobConfig(context.Background(), "jenkins://job/missing/config.xml")
	if err == nil {
		t.Fatal("jobConfig succeeded for a missing job, want an error")
	}
	if !isNotFoundResource(err) {
		t.Errorf("err = %v, want a protocol-level resource-not-found", err)
	}
}

func TestBuildConsole(t *testing.T) {
	const wantLog = "Started by user alice\nFinished: SUCCESS\n"

	cases := []struct {
		name     string
		uri      string
		wantPath string
	}{
		{"numeric build", "jenkins://build/demo/42/console", "/job/demo/42/consoleText"},
		{"nested job", "jenkins://build/team-a/service-b/7/console", "/job/team-a/job/service-b/7/consoleText"},
		{"permalink", "jenkins://build/demo/lastFailedBuild/console", "/job/demo/lastFailedBuild/consoleText"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, c.wantPath)
				}
				_, _ = w.Write([]byte(wantLog))
			})
			res := &jenkinsResources{client: client}

			body, err := res.buildConsole(context.Background(), c.uri)
			if err != nil {
				t.Fatalf("buildConsole: %v", err)
			}
			if body != wantLog {
				t.Errorf("body = %q, want the console log", body)
			}
		})
	}
}

func TestBuildConsoleNotFound(t *testing.T) {
	client := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	res := &jenkinsResources{client: client}

	_, err := res.buildConsole(context.Background(), "jenkins://build/demo/999/console")
	if err == nil {
		t.Fatal("buildConsole succeeded for a missing build, want an error")
	}
	if !isNotFoundResource(err) {
		t.Errorf("err = %v, want a protocol-level resource-not-found", err)
	}
}

func TestParseJobConfigURIRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty job path", "jenkins://job//config.xml"},
		{"whitespace job path", "jenkins://job/%20/config.xml"},
		{"wrong suffix", "jenkins://job/demo/config.json"},
		{"wrong prefix", "jenkins://builds/demo/config.xml"},
		{"invalid escape", "jenkins://job/%zz/config.xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := parseJobConfigURI(c.uri); err == nil {
				t.Errorf("parseJobConfigURI(%q) = %q, want an error", c.uri, got)
			}
		})
	}
}

func TestParseBuildConsoleURI(t *testing.T) {
	cases := []struct {
		name       string
		uri        string
		wantJob    string
		wantBuild  string
		wantErrStr string
	}{
		{name: "numeric", uri: "jenkins://build/demo/42/console", wantJob: "demo", wantBuild: "42"},
		{name: "nested", uri: "jenkins://build/a/b/c/9/console", wantJob: "a/b/c", wantBuild: "9"},
		{name: "permalink", uri: "jenkins://build/demo/lastBuild/console", wantJob: "demo", wantBuild: "lastBuild"},
		{name: "decoded job", uri: "jenkins://build/my%20job/3/console", wantJob: "my job", wantBuild: "3"},

		{name: "empty build", uri: "jenkins://build/demo//console", wantErrStr: "neither a positive build number"},
		{name: "zero build", uri: "jenkins://build/demo/0/console", wantErrStr: "neither a positive build number"},
		{name: "negative build", uri: "jenkins://build/demo/-1/console", wantErrStr: "neither a positive build number"},
		{name: "unknown permalink", uri: "jenkins://build/demo/lastGreenBuild/console", wantErrStr: "neither a positive build number"},
		{name: "no job", uri: "jenkins://build/42/console", wantErrStr: "missing a job path or build number"},
		{name: "blank job", uri: "jenkins://build/%20/42/console", wantErrStr: "empty job path"},
		{name: "wrong prefix", uri: "jenkins://job/demo/42/console", wantErrStr: "is not"},
		{name: "wrong suffix", uri: "jenkins://build/demo/42/log", wantErrStr: "is not"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			job, build, err := parseBuildConsoleURI(c.uri)
			if c.wantErrStr != "" {
				if err == nil {
					t.Fatalf("parseBuildConsoleURI(%q) = (%q, %q), want an error", c.uri, job, build)
				}
				if !strings.Contains(err.Error(), c.wantErrStr) {
					t.Errorf("err = %v, want it to mention %q", err, c.wantErrStr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBuildConsoleURI(%q): %v", c.uri, err)
			}
			if job != c.wantJob || build != c.wantBuild {
				t.Errorf("= (%q, %q), want (%q, %q)", job, build, c.wantJob, c.wantBuild)
			}
		})
	}
}

// TestBadURIsNeverReachJenkins confirms validation happens before any HTTP
// call, so a malformed URI fails locally with a clear message instead of as
// an opaque Jenkins error.
func TestBadURIsNeverReachJenkins(t *testing.T) {
	client := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; a malformed URI should be rejected first", r.URL.Path)
	})
	res := &jenkinsResources{client: client}

	if _, err := res.jobConfig(context.Background(), "jenkins://job//config.xml"); err == nil {
		t.Error("jobConfig succeeded for an empty job path, want an error")
	}
	if _, err := res.buildConsole(context.Background(), "jenkins://build/demo/nope/console"); err == nil {
		t.Error("buildConsole succeeded for a bad build reference, want an error")
	}
}

// TestRegisteredURIsRouteToHandlers reads each resource through a real MCP
// session against the templates Register actually declares. The handler-level
// tests above call the read functions directly, so they would not catch a
// template whose shape disagrees with the parser it routes to — the failure
// mode being that a perfectly good handler is simply never reached.
func TestRegisteredURIsRouteToHandlers(t *testing.T) {
	client := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/json":
			w.Header().Set("X-Jenkins", "2.568.3")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mode":"NORMAL","numExecutors":2}`))
		case strings.HasSuffix(r.URL.Path, "/config.xml"):
			_, _ = w.Write([]byte("<project/>"))
		case strings.HasSuffix(r.URL.Path, "/consoleText"):
			_, _ = w.Write([]byte("console output"))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	srv := server.New("test", "1.0.0", false)
	Register(srv, client)
	if got := srv.ResourceCount(); got != 3 {
		t.Fatalf("ResourceCount() = %d, want 3", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- srv.Run(ctx, serverTransport) }()

	mc := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := mc.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = session.Close(); cancel(); <-serverErrCh }()

	cases := []struct {
		name     string
		uri      string
		wantText string
		wantMIME string
	}{
		{"info", InfoURI, `"version": "2.568.3"`, "application/json"},
		{"job config", "jenkins://job/demo/config.xml", "<project/>", "text/xml"},
		{"nested job config", "jenkins://job/team-a/service-b/config.xml", "<project/>", "text/xml"},
		{"build console", "jenkins://build/demo/42/console", "console output", "text/plain"},
		{"nested build console", "jenkins://build/team-a/service-b/lastBuild/console", "console output", "text/plain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: c.uri})
			if err != nil {
				t.Fatalf("ReadResource(%q): %v", c.uri, err)
			}
			if len(res.Contents) != 1 {
				t.Fatalf("Contents = %d entries, want 1", len(res.Contents))
			}
			got := res.Contents[0]
			if !strings.Contains(got.Text, c.wantText) {
				t.Errorf("Text = %q, want it to contain %q", got.Text, c.wantText)
			}
			if got.MIMEType != c.wantMIME {
				t.Errorf("MIMEType = %q, want %q", got.MIMEType, c.wantMIME)
			}
			if got.URI != c.uri {
				t.Errorf("URI = %q, want %q", got.URI, c.uri)
			}
		})
	}
}
