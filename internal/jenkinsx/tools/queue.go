// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

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
		Name:        "jenkins_list_queue",
		Title:       "List Jenkins build queue",
		Description: "List items currently waiting in the build queue.",
	}, t.listQueue)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_cancel_queue_item",
		Title: "Cancel a queued Jenkins build",
		Description: "Cancel a pending queue item before it starts building. Safe to retry: canceling an item " +
			"that's already gone (started building or already canceled) still succeeds.",
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

func (t *queueTools) listQueue(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, server.ListResult[QueueItem], error) {
	var raw struct {
		Items []jenkinsQueueItem `json:"items"`
	}
	query := url.Values{"tree": {"items[id,task[name,url],why,blocked,buildable,stuck,inQueueSince]"}}
	if err := t.client.Get(ctx, "/queue/api/json", query, &raw); err != nil {
		return nil, server.ListResult[QueueItem]{}, err
	}

	items := make([]QueueItem, 0, len(raw.Items))
	for _, it := range raw.Items {
		items = append(items, QueueItem{
			ID: it.ID, TaskName: it.Task.Name, TaskURL: it.Task.URL, Why: it.Why,
			Blocked: it.Blocked, Buildable: it.Buildable, Stuck: it.Stuck, InQueueSince: it.InQueueSince,
		})
	}
	return nil, server.List(items), nil
}

// CancelQueueItemInput is the input to jenkins_cancel_queue_item.
type CancelQueueItemInput struct {
	ID int `json:"id" jsonschema:"queue item ID, as returned by jenkins_list_queue or jenkins_trigger_build"`
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
