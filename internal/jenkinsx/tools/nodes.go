// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

const nodesToolset = "nodes"

type nodeTools struct {
	client *jenkinsx.Client
}

// RegisterNodes registers the nodes/agents toolset.
func RegisterNodes(s *server.Server, c *jenkinsx.Client) {
	t := &nodeTools{client: c}
	server.Register(s, server.ToolDef{
		Name:        "jenkins_list_nodes",
		Title:       "List Jenkins nodes",
		Description: "List build agents/nodes and their online/idle status.",
	}, t.listNodes)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_node",
		Title: "Get Jenkins node detail",
		Description: "Get one node's status and per-executor activity. name should be passed through verbatim " +
			`from jenkins_list_nodes' displayName field (e.g. "(built-in)" for the controller).`,
	}, t.getNode)
	s.NoteToolset(nodesToolset)
}

// NodeSummary describes one build agent/node.
type NodeSummary struct {
	DisplayName        string   `json:"displayName"`
	Offline            bool     `json:"offline"`
	TemporarilyOffline bool     `json:"temporarilyOffline"`
	Idle               bool     `json:"idle"`
	NumExecutors       int      `json:"numExecutors"`
	OfflineCauseReason string   `json:"offlineCauseReason,omitempty"`
	Labels             []string `json:"labels,omitempty"`
}

type jenkinsNode struct {
	DisplayName        string `json:"displayName"`
	Offline            bool   `json:"offline"`
	TemporarilyOffline bool   `json:"temporarilyOffline"`
	Idle               bool   `json:"idle"`
	NumExecutors       int    `json:"numExecutors"`
	OfflineCauseReason string `json:"offlineCauseReason"`
	AssignedLabels     []struct {
		Name string `json:"name"`
	} `json:"assignedLabels"`
}

func (n jenkinsNode) summary() NodeSummary {
	labels := make([]string, 0, len(n.AssignedLabels))
	for _, l := range n.AssignedLabels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	return NodeSummary{
		DisplayName: n.DisplayName, Offline: n.Offline, TemporarilyOffline: n.TemporarilyOffline,
		Idle: n.Idle, NumExecutors: n.NumExecutors, OfflineCauseReason: n.OfflineCauseReason, Labels: labels,
	}
}

func (t *nodeTools) listNodes(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, server.ListResult[NodeSummary], error) {
	var raw struct {
		Computer []jenkinsNode `json:"computer"`
	}
	query := url.Values{"tree": {"computer[displayName,offline,temporarilyOffline,idle,numExecutors,offlineCauseReason,assignedLabels[name]]"}}
	if err := t.client.Get(ctx, "/computer/api/json", query, &raw); err != nil {
		return nil, server.ListResult[NodeSummary]{}, err
	}

	items := make([]NodeSummary, 0, len(raw.Computer))
	for _, n := range raw.Computer {
		items = append(items, n.summary())
	}
	return nil, server.List(items), nil
}

// GetNodeInput is the input to jenkins_get_node.
type GetNodeInput struct {
	Name string `json:"name" jsonschema:"node display name, as returned by jenkins_list_nodes"`
}

// ExecutorStatus describes one executor slot on a node.
type ExecutorStatus struct {
	Idle            bool   `json:"idle"`
	Progress        int    `json:"progress" jsonschema:"percent complete of the current build, or -1 if idle/unknown"`
	CurrentBuildURL string `json:"currentBuildUrl,omitempty"`
}

// NodeDetail is the output of jenkins_get_node.
type NodeDetail struct {
	NodeSummary
	Executors []ExecutorStatus `json:"executors,omitempty"`
}

type jenkinsNodeDetail struct {
	jenkinsNode
	Executors []struct {
		Idle              bool `json:"idle"`
		Progress          int  `json:"progress"`
		CurrentExecutable *struct {
			URL string `json:"url"`
		} `json:"currentExecutable"`
	} `json:"executors"`
}

func (t *nodeTools) getNode(ctx context.Context, _ *mcp.CallToolRequest, in GetNodeInput) (*mcp.CallToolResult, NodeDetail, error) {
	if err := requireNonEmpty("name", in.Name); err != nil {
		return nil, NodeDetail{}, err
	}

	var raw jenkinsNodeDetail
	path := "/computer/" + url.PathEscape(in.Name) + "/api/json"
	query := url.Values{"tree": {
		"displayName,offline,temporarilyOffline,idle,numExecutors,offlineCauseReason,assignedLabels[name]," +
			"executors[idle,progress,currentExecutable[url]]",
	}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, NodeDetail{}, err
	}

	out := NodeDetail{NodeSummary: raw.summary()}
	for _, e := range raw.Executors {
		es := ExecutorStatus{Idle: e.Idle, Progress: e.Progress}
		if e.CurrentExecutable != nil {
			es.CurrentBuildURL = e.CurrentExecutable.URL
		}
		out.Executors = append(out.Executors, es)
	}
	return nil, out, nil
}
