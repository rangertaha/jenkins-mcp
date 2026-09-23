// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

const buildsToolset = "builds"

type buildTools struct {
	client *jenkinsx.Client
}

// RegisterBuilds registers the builds toolset.
func RegisterBuilds(s *server.Server, c *jenkinsx.Client) {
	t := &buildTools{client: c}
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_build",
		Title: "Get Jenkins build detail",
		Description: "Get one build's status, timing, parameters, and change set. build may be a number or a " +
			"permalink: lastBuild, lastSuccessfulBuild, lastFailedBuild, lastStableBuild, or lastCompletedBuild.",
	}, t.getBuild)
	server.Register(s, server.ToolDef{
		Name:        "jenkins_trigger_build",
		Title:       "Trigger a Jenkins build",
		Description: "Enqueue a new build for a job, optionally with build parameters.",
		Write:       true,
	}, t.triggerBuild)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_build_console",
		Title: "Get Jenkins build console output",
		Description: "Get a slice of a build's console log starting at a byte offset. Follow nextStart while " +
			"hasMore is true to read the whole log.",
	}, t.getBuildConsole)
	s.NoteToolset(buildsToolset)
}

// GetBuildInput is the input to jenkins_get_build.
type GetBuildInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink: lastBuild, lastSuccessfulBuild, lastFailedBuild, lastStableBuild, lastCompletedBuild"`
}

// ParamValue is one build's resolved parameter value.
type ParamValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ChangeSetItem is one source control change included in a build.
type ChangeSetItem struct {
	Message  string `json:"message"`
	Author   string `json:"author"`
	CommitID string `json:"commitId,omitempty"`
}

// BuildDetail is the output of jenkins_get_build.
type BuildDetail struct {
	Number                  int             `json:"number"`
	URL                     string          `json:"url"`
	Result                  string          `json:"result,omitempty" jsonschema:"SUCCESS, FAILURE, UNSTABLE, ABORTED, or empty while building"`
	DisplayName             string          `json:"displayName"`
	Description             string          `json:"description,omitempty"`
	BuiltOn                 string          `json:"builtOn,omitempty" jsonschema:"node the build ran on"`
	Building                bool            `json:"building"`
	Timestamp               int64           `json:"timestamp" jsonschema:"start time, epoch milliseconds"`
	DurationMillis          int64           `json:"durationMillis"`
	EstimatedDurationMillis int64           `json:"estimatedDurationMillis"`
	Parameters              []ParamValue    `json:"parameters,omitempty"`
	Changes                 []ChangeSetItem `json:"changes,omitempty"`
}

type jenkinsBuildDetail struct {
	Number            int    `json:"number"`
	URL               string `json:"url"`
	Result            string `json:"result"`
	DisplayName       string `json:"displayName"`
	Description       string `json:"description"`
	Building          bool   `json:"building"`
	Timestamp         int64  `json:"timestamp"`
	Duration          int64  `json:"duration"`
	EstimatedDuration int64  `json:"estimatedDuration"`
	BuiltOn           string `json:"builtOn"`
	Actions           []struct {
		Parameters []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"parameters"`
	} `json:"actions"`
	ChangeSet struct {
		Items []struct {
			Msg    string `json:"msg"`
			Author struct {
				FullName string `json:"fullName"`
			} `json:"author"`
			CommitID string `json:"commitId"`
		} `json:"items"`
	} `json:"changeSet"`
}

func (t *buildTools) getBuild(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildInput) (*mcp.CallToolResult, BuildDetail, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, BuildDetail{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, BuildDetail{}, err
	}

	var raw jenkinsBuildDetail
	path := jenkinsx.JobPath(in.Job) + "/" + url.PathEscape(in.Build) + "/api/json"
	query := url.Values{"tree": {
		"number,url,result,building,timestamp,duration,estimatedDuration,description,displayName,builtOn," +
			"actions[parameters[name,value]],changeSet[items[msg,author[fullName],commitId]]",
	}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, BuildDetail{}, err
	}

	out := BuildDetail{
		Number: raw.Number, URL: raw.URL, Result: raw.Result, DisplayName: raw.DisplayName,
		Description: raw.Description, BuiltOn: raw.BuiltOn, Building: raw.Building,
		Timestamp: raw.Timestamp, DurationMillis: raw.Duration, EstimatedDurationMillis: raw.EstimatedDuration,
	}
	for _, a := range raw.Actions {
		for _, p := range a.Parameters {
			if p.Name == "" {
				continue
			}
			out.Parameters = append(out.Parameters, ParamValue{Name: p.Name, Value: fmt.Sprint(p.Value)})
		}
	}
	for _, item := range raw.ChangeSet.Items {
		out.Changes = append(out.Changes, ChangeSetItem{Message: item.Msg, Author: item.Author.FullName, CommitID: item.CommitID})
	}
	return nil, out, nil
}

// TriggerBuildInput is the input to jenkins_trigger_build.
type TriggerBuildInput struct {
	Job        string            `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Parameters map[string]string `json:"parameters,omitempty" jsonschema:"build parameters as name/value pairs; omit for a parameterless job"`
}

// TriggerBuildOutput is the output of jenkins_trigger_build.
type TriggerBuildOutput struct {
	QueueURL string `json:"queueUrl" jsonschema:"URL of the created queue item"`
	QueueID  int    `json:"queueId,omitempty" jsonschema:"numeric ID of the created queue item, parsed from the queue URL"`
}

func (t *buildTools) triggerBuild(ctx context.Context, _ *mcp.CallToolRequest, in TriggerBuildInput) (*mcp.CallToolResult, TriggerBuildOutput, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, TriggerBuildOutput{}, err
	}

	endpoint := "/build"
	form := url.Values{}
	if len(in.Parameters) > 0 {
		endpoint = "/buildWithParameters"
		for k, v := range in.Parameters {
			form.Set(k, v)
		}
	}

	path := jenkinsx.JobPath(in.Job) + endpoint
	headers, err := t.client.PostForm(ctx, path, nil, form, nil)
	if err != nil {
		return nil, TriggerBuildOutput{}, err
	}

	loc := headers.Get("Location")
	return nil, TriggerBuildOutput{QueueURL: loc, QueueID: parseQueueID(loc)}, nil
}

// parseQueueID extracts the numeric ID from a queue item URL such as
// ".../queue/item/123/".
func parseQueueID(location string) int {
	trimmed := strings.TrimSuffix(location, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return 0
	}
	id, err := strconv.Atoi(trimmed[idx+1:])
	if err != nil {
		return 0
	}
	return id
}

// GetBuildConsoleInput is the input to jenkins_get_build_console.
type GetBuildConsoleInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	Start int64  `json:"start,omitempty" jsonschema:"byte offset to resume from; 0 for the beginning"`
}

// ConsoleOutput is the output of jenkins_get_build_console.
type ConsoleOutput struct {
	Text      string `json:"text"`
	NextStart int64  `json:"nextStart" jsonschema:"pass as start on the next call to continue reading"`
	HasMore   bool   `json:"hasMore" jsonschema:"true while the build is still running and more output may follow"`
}

func (t *buildTools) getBuildConsole(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildConsoleInput) (*mcp.CallToolResult, ConsoleOutput, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, ConsoleOutput{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, ConsoleOutput{}, err
	}

	path := jenkinsx.JobPath(in.Job) + "/" + url.PathEscape(in.Build) + "/logText/progressiveText"
	query := url.Values{"start": {strconv.FormatInt(in.Start, 10)}}

	body, headers, err := t.client.Text(ctx, path, query)
	if err != nil {
		return nil, ConsoleOutput{}, err
	}

	out := ConsoleOutput{Text: body, HasMore: headers.Get("X-More-Data") == "true"}
	if v := headers.Get("X-Text-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out.NextStart = n
		}
	}
	return nil, out, nil
}
