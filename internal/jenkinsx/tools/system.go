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

const systemToolset = "plugins"

type systemTools struct {
	client *jenkinsx.Client
}

// RegisterSystem registers the plugins & system-info toolset.
func RegisterSystem(s *server.Server, c *jenkinsx.Client) {
	t := &systemTools{client: c}
	server.Register(s, server.ToolDef{
		Name:  "jenkins_list_plugins",
		Title: "List installed Jenkins plugins",
		Description: "List installed plugins with version, enabled/active status, and whether an update is " +
			"available. Useful for checking whether a capability a job depends on is actually installed. " +
			"Results are paged: when hasMore is true, call again with offset set to offset+count.",
	}, t.listPlugins)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_system_info",
		Title: "Get Jenkins system info",
		Description: "Get the controller's version, mode, executor count, and whether it is quieting down " +
			"(preparing to shut down, so new builds will not start). Use jenkins_whoami instead to check " +
			"who the configured credentials authenticate as.",
	}, t.systemInfo)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_whoami",
		Title: "Check Jenkins authentication",
		Description: "Report the identity Jenkins resolves the configured credentials to. Start here when a " +
			"tool fails with a permission error, to confirm whether the server is authenticated at all or " +
			"acting as anonymous.",
	}, t.whoami)
	s.NoteToolset(systemToolset)
}

// PluginSummary describes one installed plugin.
type PluginSummary struct {
	ShortName string `json:"shortName"`
	LongName  string `json:"longName"`
	Version   string `json:"version"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	HasUpdate bool   `json:"hasUpdate"`
}

// ListPluginsInput is the input to jenkins_list_plugins.
type ListPluginsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"maximum plugins to return (default 50, maximum 200)"`
	Offset int `json:"offset,omitempty" jsonschema:"number of plugins to skip, for paging"`
}

// RefineSchema bounds the paging arguments.
func (ListPluginsInput) RefineSchema(s *jsonschema.Schema) { refinePaging(s) }

func (t *systemTools) listPlugins(ctx context.Context, _ *mcp.CallToolRequest, in ListPluginsInput) (*mcp.CallToolResult, PageResult[PluginSummary], error) {
	limit, offset := effectiveLimit(in.Limit), effectiveOffset(in.Offset)

	var raw struct {
		Plugins []PluginSummary `json:"plugins"`
	}
	query := url.Values{
		"depth": {"1"},
		"tree":  {"plugins[shortName,longName,version,enabled,active,hasUpdate]" + treeRange(offset, limit)},
	}
	if err := t.client.Get(ctx, "/pluginManager/api/json", query, &raw); err != nil {
		return nil, PageResult[PluginSummary]{}, err
	}
	return nil, newPage(raw.Plugins, offset, limit), nil
}

// SystemInfo is the output of jenkins_system_info.
type SystemInfo struct {
	Version         string `json:"version" jsonschema:"Jenkins version, from the X-Jenkins response header"`
	Mode            string `json:"mode,omitempty"`
	NodeDescription string `json:"nodeDescription,omitempty"`
	NodeName        string `json:"nodeName,omitempty"`
	NumExecutors    int    `json:"numExecutors"`
	URL             string `json:"url,omitempty"`
	UseSecurity     bool   `json:"useSecurity"`
	QuietingDown    bool   `json:"quietingDown"`
}

func (t *systemTools) systemInfo(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, SystemInfo, error) {
	var raw struct {
		Mode            string `json:"mode"`
		NodeDescription string `json:"nodeDescription"`
		NodeName        string `json:"nodeName"`
		NumExecutors    int    `json:"numExecutors"`
		URL             string `json:"url"`
		UseSecurity     bool   `json:"useSecurity"`
		QuietingDown    bool   `json:"quietingDown"`
	}
	query := url.Values{"tree": {"mode,nodeDescription,nodeName,numExecutors,url,useSecurity,quietingDown"}}
	headers, err := t.client.GetWithHeaders(ctx, "/api/json", query, &raw)
	if err != nil {
		return nil, SystemInfo{}, err
	}

	return nil, SystemInfo{
		Version: headers.Get("X-Jenkins"),
		Mode:    raw.Mode, NodeDescription: raw.NodeDescription, NodeName: raw.NodeName,
		NumExecutors: raw.NumExecutors, URL: raw.URL, UseSecurity: raw.UseSecurity, QuietingDown: raw.QuietingDown,
	}, nil
}

func (t *systemTools) whoami(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, jenkinsx.Identity, error) {
	id, err := jenkinsx.Check(ctx, t.client)
	if err != nil {
		return nil, jenkinsx.Identity{}, err
	}
	return nil, *id, nil
}
