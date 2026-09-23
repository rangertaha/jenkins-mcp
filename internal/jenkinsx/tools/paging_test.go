// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

func TestEffectiveLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, defaultPageLimit},  // omitted
		{-1, defaultPageLimit}, // nonsense
		{10, 10},               // honored
		{maxPageLimit, maxPageLimit},
		{maxPageLimit + 1, maxPageLimit}, // clamped
	}
	for _, c := range cases {
		if got := effectiveLimit(c.in); got != c.want {
			t.Errorf("effectiveLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	if got := effectiveOffset(-5); got != 0 {
		t.Errorf("effectiveOffset(-5) = %d, want 0", got)
	}
}

// TestTreeRangeRequestsOneExtra documents why the range is limit+1: the
// extra element is the probe that reveals whether more data exists, since
// Jenkins exposes no total count.
func TestTreeRangeRequestsOneExtra(t *testing.T) {
	if got, want := treeRange(0, 50), "{0,51}"; got != want {
		t.Errorf("treeRange(0, 50) = %q, want %q", got, want)
	}
	if got, want := treeRange(100, 10), "{100,111}"; got != want {
		t.Errorf("treeRange(100, 10) = %q, want %q", got, want)
	}
}

func TestNewPageTrimsProbeElement(t *testing.T) {
	// Three items returned for a limit of 2 means the probe came back, so
	// the page is trimmed and HasMore set.
	got := newPage([]int{1, 2, 3}, 0, 2)
	if got.Count != 2 || len(got.Items) != 2 {
		t.Errorf("Count = %d, Items = %v, want 2 items", got.Count, got.Items)
	}
	if !got.HasMore {
		t.Error("HasMore = false, want true when the probe element came back")
	}

	got = newPage([]int{1, 2}, 10, 2)
	if got.HasMore {
		t.Error("HasMore = true, want false when exactly one page came back")
	}
	if got.Offset != 10 {
		t.Errorf("Offset = %d, want the requested offset echoed back", got.Offset)
	}

	// A nil slice must serialize as [] rather than null.
	if got := newPage[int](nil, 0, 5); got.Items == nil {
		t.Error("Items = nil, want an empty slice")
	}
}

func TestTruncate(t *testing.T) {
	if got, cut := truncate("hello", 10); got != "hello" || cut {
		t.Errorf("truncate short string = %q, %v", got, cut)
	}
	if got, cut := truncate("hello", 3); got != "hel" || !cut {
		t.Errorf("truncate = %q, %v, want \"hel\", true", got, cut)
	}
	// Cutting mid-rune must not leave invalid UTF-8: "é" is two bytes, so a
	// 1-byte cut drops it entirely.
	got, cut := truncate("aé", 2)
	if !cut {
		t.Fatal("expected truncation")
	}
	if got != "a" {
		t.Errorf("truncate(\"aé\", 2) = %q, want %q (partial rune dropped)", got, "a")
	}
}

// TestListJobsPaging drives the whole paging path through a real handler:
// the request must carry the range suffix, and a returned probe element
// must be trimmed and reported as HasMore.
func TestListJobsPaging(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		tree := r.URL.Query().Get("tree")
		if !strings.Contains(tree, "{5,8}") {
			t.Errorf("tree = %q, want the range {5,8} for offset=5 limit=2", tree)
		}
		var entries []string
		for i := 0; i < 3; i++ { // limit + probe
			entries = append(entries, fmt.Sprintf(`{"name":"j%d","fullName":"j%d","url":"u","buildable":true}`, i, i))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[` + strings.Join(entries, ",") + `]}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.listJobs(context.Background(), nil, ListJobsInput{Limit: 2, Offset: 5})
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	if out.Count != 2 {
		t.Errorf("Count = %d, want 2 (probe element trimmed)", out.Count)
	}
	if !out.HasMore {
		t.Error("HasMore = false, want true")
	}
	if out.Offset != 5 {
		t.Errorf("Offset = %d, want 5", out.Offset)
	}
}

// TestListToolsRequestRangeSuffix checks every paged list tool actually
// sends a range, so a new list tool that forgets to page is caught here.
func TestListToolsRequestRangeSuffix(t *testing.T) {
	check := func(t *testing.T, body string, call func(c *jenkinsx.Client) error) {
		t.Helper()
		var sawRange bool
		c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Query().Get("tree"), "{0,51}") {
				sawRange = true
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
		if err := call(c); err != nil {
			t.Fatalf("call: %v", err)
		}
		if !sawRange {
			t.Error("request carried no {0,51} range suffix; this list tool does not page")
		}
	}

	t.Run("list_jobs", func(t *testing.T) {
		check(t, `{"jobs":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&jobTools{client: c}).listJobs(context.Background(), nil, ListJobsInput{})
			return err
		})
	})
	t.Run("list_queue", func(t *testing.T) {
		check(t, `{"items":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&queueTools{client: c}).listQueue(context.Background(), nil, ListQueueInput{})
			return err
		})
	})
	t.Run("list_nodes", func(t *testing.T) {
		check(t, `{"computer":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&nodeTools{client: c}).listNodes(context.Background(), nil, ListNodesInput{})
			return err
		})
	})
	t.Run("list_views", func(t *testing.T) {
		check(t, `{"views":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&viewTools{client: c}).listViews(context.Background(), nil, ListViewsInput{})
			return err
		})
	})
	t.Run("list_plugins", func(t *testing.T) {
		check(t, `{"plugins":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&systemTools{client: c}).listPlugins(context.Background(), nil, ListPluginsInput{})
			return err
		})
	})
	t.Run("list_artifacts", func(t *testing.T) {
		check(t, `{"artifacts":[]}`, func(c *jenkinsx.Client) error {
			_, _, err := (&buildTools{client: c}).listArtifacts(context.Background(), nil,
				ListArtifactsInput{Job: "demo", Build: "1"})
			return err
		})
	})
}

func TestGetBuildConsoleRespectsMaxBytes(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Text-Size", "5000")
		w.Header().Set("X-More-Data", "false")
		_, _ = w.Write([]byte(strings.Repeat("y", 5000)))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildConsole(context.Background(), nil, GetBuildConsoleInput{
		Job: "demo", Build: "42", Start: 100, MaxBytes: 64,
	})
	if err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if len(out.Text) != 64 {
		t.Errorf("len(Text) = %d, want 64", len(out.Text))
	}
	if !out.Truncated || !out.HasMore {
		t.Errorf("Truncated = %v, HasMore = %v; want both true", out.Truncated, out.HasMore)
	}
	// Resuming must continue from the end of what was actually returned,
	// not from Jenkins' end-of-log offset, or the unread remainder is lost.
	if want := int64(100 + 64); out.NextStart != want {
		t.Errorf("NextStart = %d, want %d (start + bytes returned)", out.NextStart, want)
	}
}
