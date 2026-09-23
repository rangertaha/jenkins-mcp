// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

// handlerCall names a tool handler and invokes it with a valid (non-blank)
// input, so the only thing that can fail is the Jenkins call itself.
type handlerCall struct {
	name string
	call func(*jenkinsx.Client) error
}

// allHandlers is every tool handler that talks to Jenkins, each invoked with
// inputs that pass local validation. Keeping them in one table means a new
// handler gets its Jenkins-failure path covered by adding a single line.
var allHandlers = []handlerCall{
	{"listJobs", func(c *jenkinsx.Client) error {
		_, _, err := (&jobTools{client: c}).listJobs(context.Background(), nil, ListJobsInput{})
		return err
	}},
	{"getJob", func(c *jenkinsx.Client) error {
		_, _, err := (&jobTools{client: c}).getJob(context.Background(), nil, GetJobInput{Job: "demo"})
		return err
	}},
	{"getBuild", func(c *jenkinsx.Client) error {
		_, _, err := (&buildTools{client: c}).getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "1"})
		return err
	}},
	{"triggerBuild", func(c *jenkinsx.Client) error {
		_, _, err := (&buildTools{client: c}).triggerBuild(context.Background(), nil, TriggerBuildInput{Job: "demo"})
		return err
	}},
	{"triggerBuildWithParameters", func(c *jenkinsx.Client) error {
		_, _, err := (&buildTools{client: c}).triggerBuild(context.Background(), nil,
			TriggerBuildInput{Job: "demo", Parameters: map[string]string{"BRANCH": "main"}})
		return err
	}},
	{"getBuildConsole", func(c *jenkinsx.Client) error {
		_, _, err := (&buildTools{client: c}).getBuildConsole(context.Background(), nil, GetBuildConsoleInput{Job: "demo", Build: "1"})
		return err
	}},
	{"listQueue", func(c *jenkinsx.Client) error {
		_, _, err := (&queueTools{client: c}).listQueue(context.Background(), nil, EmptyInput{})
		return err
	}},
	{"cancelQueueItem", func(c *jenkinsx.Client) error {
		_, _, err := (&queueTools{client: c}).cancelQueueItem(context.Background(), nil, CancelQueueItemInput{ID: 7})
		return err
	}},
	{"listNodes", func(c *jenkinsx.Client) error {
		_, _, err := (&nodeTools{client: c}).listNodes(context.Background(), nil, EmptyInput{})
		return err
	}},
	{"getNode", func(c *jenkinsx.Client) error {
		_, _, err := (&nodeTools{client: c}).getNode(context.Background(), nil, GetNodeInput{Name: "agent-1"})
		return err
	}},
	{"listViews", func(c *jenkinsx.Client) error {
		_, _, err := (&viewTools{client: c}).listViews(context.Background(), nil, EmptyInput{})
		return err
	}},
	{"getView", func(c *jenkinsx.Client) error {
		_, _, err := (&viewTools{client: c}).getView(context.Background(), nil, GetViewInput{Name: "All"})
		return err
	}},
	{"listPlugins", func(c *jenkinsx.Client) error {
		_, _, err := (&systemTools{client: c}).listPlugins(context.Background(), nil, EmptyInput{})
		return err
	}},
	{"systemInfo", func(c *jenkinsx.Client) error {
		_, _, err := (&systemTools{client: c}).systemInfo(context.Background(), nil, EmptyInput{})
		return err
	}},
	{"whoami", func(c *jenkinsx.Client) error {
		_, _, err := (&systemTools{client: c}).whoami(context.Background(), nil, EmptyInput{})
		return err
	}},
}

// TestHandlersPropagateJenkinsErrors checks every handler surfaces a Jenkins
// failure as a tool error rather than returning a zero-valued success. A
// handler that swallowed the error would hand the model an empty result that
// looks like a legitimately empty Jenkins.
func TestHandlersPropagateJenkinsErrors(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Jenkins is having a bad day"))
	})

	for _, h := range allHandlers {
		t.Run(h.name, func(t *testing.T) {
			if err := h.call(c); err == nil {
				t.Errorf("%s: returned nil error on a Jenkins 500, want the failure surfaced", h.name)
			}
		})
	}
}

// TestHandlersPropagateDecodeErrors checks a malformed JSON body is reported
// rather than silently yielding an empty result.
func TestHandlersPropagateDecodeErrors(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"this": "is", "truncated"`))
	})

	for _, h := range allHandlers {
		switch h.name {
		// These don't decode a JSON body on success: the console log is raw
		// text, and a build trigger reports its result via the Location
		// header, so neither can fail to decode.
		case "getBuildConsole", "triggerBuild", "triggerBuildWithParameters", "cancelQueueItem":
			continue
		}
		t.Run(h.name, func(t *testing.T) {
			if err := h.call(c); err == nil {
				t.Errorf("%s: returned nil error on a malformed JSON body, want a decode error", h.name)
			}
		})
	}
}

func TestParseQueueID(t *testing.T) {
	cases := []struct {
		name     string
		location string
		want     int
	}{
		{"standard queue URL", "http://ci.example.com/queue/item/123/", 123},
		{"no trailing slash", "http://ci.example.com/queue/item/45", 45},
		{"empty location", "", 0},
		{"no slash at all", "queue-item-7", 0},
		{"non-numeric trailing segment", "http://ci.example.com/queue/item/abc/", 0},
		{"trailing slash only", "/", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseQueueID(c.location); got != c.want {
				t.Errorf("parseQueueID(%q) = %d, want %d", c.location, got, c.want)
			}
		})
	}
}

// TestTriggerBuildWithoutLocationHeader covers the degraded case where
// Jenkins' response carries no Location header (e.g. a reverse proxy stripped
// it): the build was still triggered, so this is a success, but the queue ID
// is unknown and must not be reported as a real ID. Queue IDs start at 1, so
// the zero value is unambiguous — and jenkins_cancel_queue_item rejects id<=0
// rather than acting on it.
func TestTriggerBuildWithoutLocationHeader(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	_, out, err := (&buildTools{client: c}).triggerBuild(context.Background(), nil, TriggerBuildInput{Job: "demo"})
	if err != nil {
		t.Fatalf("triggerBuild: %v", err)
	}
	if out.QueueURL != "" {
		t.Errorf("QueueURL = %q, want empty when no Location header is returned", out.QueueURL)
	}
	if out.QueueID != 0 {
		t.Errorf("QueueID = %d, want 0 (unknown) when no Location header is returned", out.QueueID)
	}
}
