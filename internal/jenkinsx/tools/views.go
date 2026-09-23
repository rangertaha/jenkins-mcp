// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/url"

	"github.com/google/jsonschema-go/jsonschema"

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
		Name:  "jenkins_list_views",
		Title: "List Jenkins views",
		Description: "List the views configured on this controller. A view is a saved, named subset of jobs; " +
			"use jenkins_get_view to see which jobs one contains.",
	}, t.listViews)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_view",
		Title: "Get Jenkins view detail",
		Description: "Get a view's description and the jobs it contains. Each job's fullName is what the job " +
			"and build tools take as their job argument.",
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

// ListViewsInput is the input to jenkins_list_views.
type ListViewsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"maximum views to return (default 50, maximum 200)"`
	Offset int `json:"offset,omitempty" jsonschema:"number of views to skip, for paging"`
}

// RefineSchema bounds the paging arguments.
func (ListViewsInput) RefineSchema(s *jsonschema.Schema) { refinePaging(s) }

func (t *viewTools) listViews(ctx context.Context, _ *mcp.CallToolRequest, in ListViewsInput) (*mcp.CallToolResult, PageResult[ViewSummary], error) {
	limit, offset := effectiveLimit(in.Limit), effectiveOffset(in.Offset)

	var raw struct {
		Views []jenkinsView `json:"views"`
	}
	query := url.Values{"tree": {"views[name,url,_class]" + treeRange(offset, limit)}}
	if err := t.client.Get(ctx, "/api/json", query, &raw); err != nil {
		return nil, PageResult[ViewSummary]{}, err
	}

	items := make([]ViewSummary, 0, len(raw.Views))
	for _, v := range raw.Views {
		items = append(items, ViewSummary(v))
	}
	return nil, newPage(items, offset, limit), nil
}

// GetViewInput is the input to jenkins_get_view.
type GetViewInput struct {
	Name string `json:"name" jsonschema:"view name, as reported by jenkins_list_views"`
}

// RefineSchema requires a non-blank view name.
func (GetViewInput) RefineSchema(s *jsonschema.Schema) { refineRequiredString(s, "name") }

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
	if err := requireNonEmpty("name", in.Name); err != nil {
		return nil, ViewDetail{}, err
	}

	var raw jenkinsViewDetail
	path := "/view/" + url.PathEscape(in.Name) + "/api/json"
	query := url.Values{"tree": {"name,url,description,jobs[name,fullName,url,color,buildable,_class]"}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, ViewDetail{}, err
	}

	out := ViewDetail{Name: raw.Name, URL: raw.URL, Description: raw.Description}
	for _, j := range raw.Jobs {
		out.Jobs = append(out.Jobs, j.summary())
	}
	return nil, out, nil
}
