// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

const jobsToolset = "jobs"

type jobTools struct {
	client *jenkinsx.Client
}

// RegisterJobs registers the jobs & folders toolset.
func RegisterJobs(s *server.Server, c *jenkinsx.Client) {
	t := &jobTools{client: c}
	server.Register(s, server.ToolDef{
		Name:  "jenkins_list_jobs",
		Title: "List Jenkins jobs",
		Description: "List jobs at the top level or within a folder. Folders appear as entries too " +
			`(class ending in "Folder"); recurse by calling again with folder set to the folder's full name.`,
	}, t.listJobs)
	server.Register(s, server.ToolDef{
		Name:        "jenkins_get_job",
		Title:       "Get Jenkins job detail",
		Description: "Get one job's description, health, buildability, last-build pointers, and parameter definitions.",
	}, t.getJob)
	s.NoteToolset(jobsToolset)
}

// JobSummary describes one job, as listed by jenkins_list_jobs or embedded
// in a view.
type JobSummary struct {
	Name      string `json:"name" jsonschema:"short job name"`
	FullName  string `json:"fullName,omitempty" jsonschema:"full job path, e.g. team-a/service-b"`
	URL       string `json:"url" jsonschema:"job URL"`
	Class     string `json:"class,omitempty" jsonschema:"Jenkins internal class, e.g. a Folder or a FreeStyleProject"`
	Buildable bool   `json:"buildable" jsonschema:"whether the job currently accepts builds"`
	Color     string `json:"color,omitempty" jsonschema:"Jenkins status ball color (e.g. blue, red, notbuilt, disabled; an _anime suffix means a build is in progress)"`
}

// jenkinsJob is the raw shape decoded from Jenkins' job-list JSON. Every
// tree= string that embeds a jobs[...] sub-selector (listJobs below, and
// views.go's getView) must request all six fields below by name —
// name,fullName,url,color,buildable,_class — or the corresponding
// JobSummary field silently decodes as its zero value instead of erroring,
// since every field but Name/URL/Buildable is `omitempty` (see
// views.go's getView, which shipped without fullName/_class and always
// returned an empty FullName/Class for every job in a view until fixed).
type jenkinsJob struct {
	Name      string `json:"name"`
	FullName  string `json:"fullName"`
	URL       string `json:"url"`
	Class     string `json:"_class"`
	Buildable bool   `json:"buildable"`
	Color     string `json:"color"`
}

func (j jenkinsJob) summary() JobSummary {
	return JobSummary(j)
}

// ListJobsInput is the input to jenkins_list_jobs.
type ListJobsInput struct {
	Folder string `json:"folder,omitempty" jsonschema:"folder path to list within, e.g. team-a/sub-folder (job names joined by /); omit for the top level"`
}

func (t *jobTools) listJobs(ctx context.Context, _ *mcp.CallToolRequest, in ListJobsInput) (*mcp.CallToolResult, server.ListResult[JobSummary], error) {
	var raw struct {
		Jobs []jenkinsJob `json:"jobs"`
	}
	path := jenkinsx.JobPath(in.Folder) + "/api/json"
	query := url.Values{"tree": {"jobs[name,fullName,url,color,buildable,_class]"}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, server.ListResult[JobSummary]{}, err
	}

	items := make([]JobSummary, 0, len(raw.Jobs))
	for _, j := range raw.Jobs {
		items = append(items, j.summary())
	}
	return nil, server.List(items), nil
}

// GetJobInput is the input to jenkins_get_job.
type GetJobInput struct {
	Job string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
}

// BuildRef points to a single build.
type BuildRef struct {
	Number int    `json:"number" jsonschema:"build number"`
	URL    string `json:"url" jsonschema:"build URL"`
}

// ParamDef describes one of a job's declared build parameters.
type ParamDef struct {
	Name    string `json:"name" jsonschema:"parameter name"`
	Type    string `json:"type" jsonschema:"parameter type, e.g. StringParameterDefinition"`
	Default string `json:"default,omitempty" jsonschema:"default value, if any"`
}

// JobDetail is the output of jenkins_get_job.
type JobDetail struct {
	Name                string     `json:"name"`
	FullName            string     `json:"fullName"`
	URL                 string     `json:"url"`
	Description         string     `json:"description,omitempty"`
	Buildable           bool       `json:"buildable"`
	InQueue             bool       `json:"inQueue"`
	NextBuildNumber     int        `json:"nextBuildNumber"`
	HealthScore         int        `json:"healthScore" jsonschema:"0-100 health score, or -1 if unavailable"`
	LastBuild           *BuildRef  `json:"lastBuild,omitempty"`
	LastSuccessfulBuild *BuildRef  `json:"lastSuccessfulBuild,omitempty"`
	LastFailedBuild     *BuildRef  `json:"lastFailedBuild,omitempty"`
	LastStableBuild     *BuildRef  `json:"lastStableBuild,omitempty"`
	LastCompletedBuild  *BuildRef  `json:"lastCompletedBuild,omitempty"`
	Parameters          []ParamDef `json:"parameters,omitempty"`
}

type jenkinsBuildRef struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

func toBuildRef(b *jenkinsBuildRef) *BuildRef {
	if b == nil {
		return nil
	}
	return &BuildRef{Number: b.Number, URL: b.URL}
}

type jenkinsJobDetail struct {
	Name            string `json:"name"`
	FullName        string `json:"fullName"`
	URL             string `json:"url"`
	Description     string `json:"description"`
	Buildable       bool   `json:"buildable"`
	InQueue         bool   `json:"inQueue"`
	NextBuildNumber int    `json:"nextBuildNumber"`
	HealthReport    []struct {
		Score int `json:"score"`
	} `json:"healthReport"`
	LastBuild           *jenkinsBuildRef `json:"lastBuild"`
	LastSuccessfulBuild *jenkinsBuildRef `json:"lastSuccessfulBuild"`
	LastFailedBuild     *jenkinsBuildRef `json:"lastFailedBuild"`
	LastStableBuild     *jenkinsBuildRef `json:"lastStableBuild"`
	LastCompletedBuild  *jenkinsBuildRef `json:"lastCompletedBuild"`
	Property            []struct {
		ParameterDefinitions []struct {
			Name                  string `json:"name"`
			Type                  string `json:"type"`
			DefaultParameterValue *struct {
				Value any `json:"value"`
			} `json:"defaultParameterValue"`
		} `json:"parameterDefinitions"`
	} `json:"property"`
}

func (t *jobTools) getJob(ctx context.Context, _ *mcp.CallToolRequest, in GetJobInput) (*mcp.CallToolResult, JobDetail, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, JobDetail{}, err
	}

	var raw jenkinsJobDetail
	path := jenkinsx.JobPath(in.Job) + "/api/json"
	query := url.Values{"tree": {
		"name,fullName,url,description,buildable,inQueue,nextBuildNumber," +
			"healthReport[score]," +
			"lastBuild[number,url],lastSuccessfulBuild[number,url],lastFailedBuild[number,url]," +
			"lastStableBuild[number,url],lastCompletedBuild[number,url]," +
			"property[parameterDefinitions[name,type,defaultParameterValue[value]]]",
	}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, JobDetail{}, err
	}

	out := JobDetail{
		Name: raw.Name, FullName: raw.FullName, URL: raw.URL, Description: raw.Description,
		Buildable: raw.Buildable, InQueue: raw.InQueue, NextBuildNumber: raw.NextBuildNumber,
		HealthScore:         -1,
		LastBuild:           toBuildRef(raw.LastBuild),
		LastSuccessfulBuild: toBuildRef(raw.LastSuccessfulBuild),
		LastFailedBuild:     toBuildRef(raw.LastFailedBuild),
		LastStableBuild:     toBuildRef(raw.LastStableBuild),
		LastCompletedBuild:  toBuildRef(raw.LastCompletedBuild),
	}
	if len(raw.HealthReport) > 0 {
		out.HealthScore = raw.HealthReport[0].Score
	}
	for _, prop := range raw.Property {
		for _, pd := range prop.ParameterDefinitions {
			def := ParamDef{Name: pd.Name, Type: pd.Type}
			if pd.DefaultParameterValue != nil && pd.DefaultParameterValue.Value != nil {
				def.Default = fmt.Sprint(pd.DefaultParameterValue.Value)
			}
			out.Parameters = append(out.Parameters, def)
		}
	}
	return nil, out, nil
}
