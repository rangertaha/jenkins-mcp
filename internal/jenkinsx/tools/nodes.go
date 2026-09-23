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
		Description: "Get one node's status and per-executor activity. Pass the name field from " +
			"jenkins_list_nodes, not displayName: for the controller those differ " +
			`("(built-in)" vs "Built-In Node") and only name resolves.`,
	}, t.getNode)
	s.NoteToolset(nodesToolset)
}

// NodeSummary describes one build agent/node.
type NodeSummary struct {
	Name               string   `json:"name" jsonschema:"identifier to pass to jenkins_get_node; for the controller this is (built-in), which differs from its display name"`
	DisplayName        string   `json:"displayName" jsonschema:"human-readable name; NOT usable as a URL segment for the controller"`
	Offline            bool     `json:"offline"`
	TemporarilyOffline bool     `json:"temporarilyOffline"`
	Idle               bool     `json:"idle"`
	NumExecutors       int      `json:"numExecutors"`
	OfflineCauseReason string   `json:"offlineCauseReason,omitempty"`
	Labels             []string `json:"labels,omitempty"`
}

// masterComputerClass is the _class Jenkins reports for the controller's own
// built-in node.
const masterComputerClass = "hudson.model.Hudson$MasterComputer"

// builtInNodeSegment is the URL path segment for the controller's built-in
// node. Jenkins does not expose it as a field anywhere in /computer/api/json
// — the controller reports displayName "Built-In Node", which 404s as a path
// segment — so it has to be reconstructed from _class. Verified against
// Jenkins 2.568.3: /computer/(built-in)/api/json and the legacy
// /computer/(master)/api/json both return 200, /computer/Built-In%20Node
// returns 404.
const builtInNodeSegment = "(built-in)"

type jenkinsNode struct {
	Class              string `json:"_class"`
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

// urlSegment returns the path segment identifying this node under /computer,
// which is the display name for an agent but a fixed alias for the
// controller (see builtInNodeSegment).
func (n jenkinsNode) urlSegment() string {
	if n.Class == masterComputerClass {
		return builtInNodeSegment
	}
	return n.DisplayName
}

func (n jenkinsNode) summary() NodeSummary {
	labels := make([]string, 0, len(n.AssignedLabels))
	for _, l := range n.AssignedLabels {
		if l.Name != "" {
			labels = append(labels, l.Name)
		}
	}
	return NodeSummary{
		Name: n.urlSegment(), DisplayName: n.DisplayName,
		Offline: n.Offline, TemporarilyOffline: n.TemporarilyOffline,
		Idle: n.Idle, NumExecutors: n.NumExecutors, OfflineCauseReason: n.OfflineCauseReason, Labels: labels,
	}
}

func (t *nodeTools) listNodes(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, server.ListResult[NodeSummary], error) {
	var raw struct {
		Computer []jenkinsNode `json:"computer"`
	}
	query := url.Values{"tree": {"computer[_class,displayName,offline,temporarilyOffline,idle,numExecutors,offlineCauseReason,assignedLabels[name]]"}}
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
	Name string `json:"name" jsonschema:"node identifier from jenkins_list_nodes' name field (not displayName); the controller is (built-in)"`
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
		"_class,displayName,offline,temporarilyOffline,idle,numExecutors,offlineCauseReason,assignedLabels[name]," +
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
