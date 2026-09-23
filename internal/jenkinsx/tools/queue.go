// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

const queueToolset = "queue"

type queueTools struct {
	client *jenkinsx.Client
}

// RegisterQueue registers the build queue toolset.
func RegisterQueue(s *server.Server, c *jenkinsx.Client) {
	t := &queueTools{client: c}
	server.Register(s, server.ToolDef{
		Name:  "jenkins_list_queue",
		Title: "List Jenkins build queue",
		Description: "List builds waiting to start, with why each is still queued. Use this to see whether a " +
			"triggered build is blocked or merely waiting for an executor. Each item's id is what " +
			"jenkins_cancel_queue_item takes.",
	}, t.listQueue)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_cancel_queue_item",
		Title: "Cancel a queued Jenkins build",
		Description: "Cancel a queued build before it starts, by the id from jenkins_list_queue or " +
			"jenkins_trigger_build. Safe to retry: canceling an item that is already gone (it started building, " +
			"or was already canceled) still succeeds. To stop a build that has already started, use " +
			"jenkins_stop_build instead.",
		Write:       true,
		Destructive: true,
		Idempotent:  true,
	}, t.cancelQueueItem)
	s.NoteToolset(queueToolset)
}

// QueueItem describes one item waiting in the build queue.
type QueueItem struct {
	ID           int    `json:"id"`
	TaskName     string `json:"taskName"`
	TaskURL      string `json:"taskUrl"`
	Why          string `json:"why,omitempty" jsonschema:"reason the item is still waiting"`
	Blocked      bool   `json:"blocked"`
	Buildable    bool   `json:"buildable"`
	Stuck        bool   `json:"stuck"`
	InQueueSince int64  `json:"inQueueSince" jsonschema:"epoch milliseconds"`
}

type jenkinsQueueItem struct {
	ID   int `json:"id"`
	Task struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"task"`
	Why          string `json:"why"`
	Blocked      bool   `json:"blocked"`
	Buildable    bool   `json:"buildable"`
	Stuck        bool   `json:"stuck"`
	InQueueSince int64  `json:"inQueueSince"`
}

// ListQueueInput is the input to jenkins_list_queue.
type ListQueueInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"maximum queue items to return (default 50, maximum 200)"`
	Offset int `json:"offset,omitempty" jsonschema:"number of queue items to skip, for paging"`
}

// RefineSchema bounds the paging arguments.
func (ListQueueInput) RefineSchema(s *jsonschema.Schema) { refinePaging(s) }

func (t *queueTools) listQueue(ctx context.Context, _ *mcp.CallToolRequest, in ListQueueInput) (*mcp.CallToolResult, PageResult[QueueItem], error) {
	limit, offset := effectiveLimit(in.Limit), effectiveOffset(in.Offset)

	var raw struct {
		Items []jenkinsQueueItem `json:"items"`
	}
	query := url.Values{"tree": {"items[id,task[name,url],why,blocked,buildable,stuck,inQueueSince]" + treeRange(offset, limit)}}
	if err := t.client.Get(ctx, "/queue/api/json", query, &raw); err != nil {
		return nil, PageResult[QueueItem]{}, err
	}

	items := make([]QueueItem, 0, len(raw.Items))
	for _, it := range raw.Items {
		items = append(items, QueueItem{
			ID: it.ID, TaskName: it.Task.Name, TaskURL: it.Task.URL, Why: it.Why,
			Blocked: it.Blocked, Buildable: it.Buildable, Stuck: it.Stuck, InQueueSince: it.InQueueSince,
		})
	}
	return nil, newPage(items, offset, limit), nil
}

// CancelQueueItemInput is the input to jenkins_cancel_queue_item.
type CancelQueueItemInput struct {
	ID int `json:"id" jsonschema:"queue item ID, as returned by jenkins_list_queue or jenkins_trigger_build"`
}

// RefineSchema states the positive-ID requirement the handler enforces, so
// a client rejects id=0 before the call is made.
func (CancelQueueItemInput) RefineSchema(s *jsonschema.Schema) {
	if p, ok := s.Properties["id"]; ok {
		p.Minimum = ptr(1.0)
	}
}

// CancelQueueItemOutput is the output of jenkins_cancel_queue_item.
type CancelQueueItemOutput struct {
	Canceled bool `json:"canceled"`
}

func (t *queueTools) cancelQueueItem(ctx context.Context, _ *mcp.CallToolRequest, in CancelQueueItemInput) (*mcp.CallToolResult, CancelQueueItemOutput, error) {
	if in.ID <= 0 {
		return nil, CancelQueueItemOutput{}, fmt.Errorf("id must be a positive queue item ID")
	}

	query := url.Values{"id": {strconv.Itoa(in.ID)}}
	if _, err := t.client.PostForm(ctx, "/queue/cancelItem", query, url.Values{}, nil); err != nil {
		// A gone item (already started building, or already canceled) is
		// reported by Jenkins as 404; treat that as the idempotent success
		// this tool advertises rather than surfacing it as a failure.
		if jenkinsx.IsNotFound(err) {
			return nil, CancelQueueItemOutput{Canceled: true}, nil
		}
		return nil, CancelQueueItemOutput{}, err
	}
	return nil, CancelQueueItemOutput{Canceled: true}, nil
}
