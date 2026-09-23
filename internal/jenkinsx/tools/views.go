// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

const viewsToolset = "views"

type viewTools struct {
	client *jenkinsx.Client
}

// RegisterViews registers the views/folders toolset.
func RegisterViews(s *server.Server, c *jenkinsx.Client) {
	t := &viewTools{client: c}
	server.Register(s, server.ToolDef{
		Name:        "jenkins_list_views",
		Title:       "List Jenkins views",
		Description: "List the views configured on this Jenkins controller.",
	}, t.listViews)
	server.Register(s, server.ToolDef{
		Name:        "jenkins_get_view",
		Title:       "Get Jenkins view detail",
		Description: "Get a view's description and the jobs it contains.",
	}, t.getView)
	s.NoteToolset(viewsToolset)
}

// ViewSummary describes one view.
type ViewSummary struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Class string `json:"class,omitempty"`
}

type jenkinsView struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Class string `json:"_class"`
}

func (t *viewTools) listViews(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, server.ListResult[ViewSummary], error) {
	var raw struct {
		Views []jenkinsView `json:"views"`
	}
	query := url.Values{"tree": {"views[name,url,_class]"}}
	if err := t.client.Get(ctx, "/api/json", query, &raw); err != nil {
		return nil, server.ListResult[ViewSummary]{}, err
	}

	items := make([]ViewSummary, 0, len(raw.Views))
	for _, v := range raw.Views {
		items = append(items, ViewSummary(v))
	}
	return nil, server.List(items), nil
}

// GetViewInput is the input to jenkins_get_view.
type GetViewInput struct {
	Name string `json:"name" jsonschema:"view name"`
}

// ViewDetail is the output of jenkins_get_view.
type ViewDetail struct {
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Description string       `json:"description,omitempty"`
	Jobs        []JobSummary `json:"jobs,omitempty"`
}

type jenkinsViewDetail struct {
	Name        string       `json:"name"`
	URL         string       `json:"url"`
	Description string       `json:"description"`
	Jobs        []jenkinsJob `json:"jobs"`
}

func (t *viewTools) getView(ctx context.Context, _ *mcp.CallToolRequest, in GetViewInput) (*mcp.CallToolResult, ViewDetail, error) {
	var raw jenkinsViewDetail
	path := "/view/" + url.PathEscape(in.Name) + "/api/json"
	query := url.Values{"tree": {"name,url,description,jobs[name,url,color,buildable]"}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, ViewDetail{}, err
	}

	out := ViewDetail{Name: raw.Name, URL: raw.URL, Description: raw.Description}
	for _, j := range raw.Jobs {
		out.Jobs = append(out.Jobs, j.summary())
	}
	return nil, out, nil
}
