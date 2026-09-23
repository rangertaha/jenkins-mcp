// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/server"
)

func TestListQueue(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queue/api/json" {
			t.Errorf("path = %q, want /queue/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"id":7,"task":{"name":"demo","url":"http://x/job/demo/"},"why":"waiting for agent","blocked":true,"buildable":false,"stuck":false,"inQueueSince":1700000000000}
		]}`))
	})
	tls := &queueTools{client: c}

	_, out, err := tls.listQueue(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("listQueue: %v", err)
	}
	if out.Count != 1 || out.Items[0].ID != 7 || out.Items[0].TaskName != "demo" {
		t.Errorf("out = %+v", out)
	}
}

func TestCancelQueueItem(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queue/cancelItem" {
			t.Errorf("path = %q, want /queue/cancelItem", r.URL.Path)
		}
		if got := r.URL.Query().Get("id"); got != "7" {
			t.Errorf("id query = %q, want 7", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	tls := &queueTools{client: c}

	_, out, err := tls.cancelQueueItem(context.Background(), nil, CancelQueueItemInput{ID: 7})
	if err != nil {
		t.Fatalf("cancelQueueItem: %v", err)
	}
	if !out.Canceled {
		t.Error("Canceled = false, want true")
	}
}

func TestCancelQueueItemRejectsNonPositiveID(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; cancelQueueItem should reject id<=0 before calling Jenkins", r.URL.Path)
	})
	tls := &queueTools{client: c}

	for _, id := range []int{0, -1} {
		if _, _, err := tls.cancelQueueItem(context.Background(), nil, CancelQueueItemInput{ID: id}); err == nil {
			t.Errorf("cancelQueueItem(ID:%d) succeeded, want an error", id)
		}
	}
}

// TestCancelQueueItemAlreadyGone confirms canceling an item Jenkins no
// longer knows about (already started building, or already canceled —
// reported as 404) is treated as the idempotent success this tool
// advertises, not an error.
func TestCancelQueueItemAlreadyGone(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	tls := &queueTools{client: c}

	_, out, err := tls.cancelQueueItem(context.Background(), nil, CancelQueueItemInput{ID: 7})
	if err != nil {
		t.Fatalf("cancelQueueItem: %v", err)
	}
	if !out.Canceled {
		t.Error("Canceled = false, want true for an already-gone queue item")
	}
}

// TestRegisterQueueSkipsWriteToolsWhenReadOnly confirms
// jenkins_cancel_queue_item (the only Write tool in this toolset) is
// suppressed in read-only mode.
func TestRegisterQueueSkipsWriteToolsWhenReadOnly(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	s := server.New("test", "0.0.0", true)
	RegisterQueue(s, c)

	if s.ToolCount() != 1 {
		t.Errorf("ToolCount() = %d, want 1 (cancel_queue_item suppressed)", s.ToolCount())
	}
}

func TestRegisterQueueRegistersAllToolsWhenNotReadOnly(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	s := server.New("test", "0.0.0", false)
	RegisterQueue(s, c)

	if s.ToolCount() != 2 {
		t.Errorf("ToolCount() = %d, want 2", s.ToolCount())
	}
}
