// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
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
		Description: "List jobs at the top level, or inside a folder. Use this to discover what exists; " +
			"use jenkins_get_job for one job's detail. Folders are listed as entries too (their class ends " +
			`in "Folder") — descend by calling again with folder set to that entry's fullName. Results are ` +
			"paged: when hasMore is true, call again with offset set to offset+count.",
	}, t.listJobs)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_job",
		Title: "Get Jenkins job detail",
		Description: "Get one job's description, health, buildability, parameter definitions, and pointers to " +
			"its recent builds. The lastBuild/lastSuccessfulBuild/... numbers here are what you pass as " +
			"jenkins_get_build's build argument. For the job's raw XML definition use jenkins_get_job_config.",
	}, t.getJob)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_job_config",
		Title: "Get Jenkins job config.xml",
		Description: "Get a job's raw config.xml — its full definition, including build steps, SCM, triggers " +
			"and publishers. Use this when jenkins_get_job's summary isn't enough to explain how a job is set up.",
	}, t.getJobConfig)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_search_jobs",
		Title: "Search Jenkins jobs by name",
		Description: "Find jobs whose name contains a substring, searching inside folders too. Use this instead " +
			"of walking folders with repeated jenkins_list_jobs calls when you know part of a job's name but not " +
			"where it lives. Searches to depth 3 by default (maximum 5) and returns the matches' fullName, which " +
			"is what every other job tool takes as its job argument.",
	}, t.searchJobs)
	s.NoteToolset(jobsToolset)
}

// JobSummary describes one job, as listed by jenkins_list_jobs or embedded
// in a view.
type JobSummary struct {
	Name      string `json:"name" jsonschema:"short job name"`
	FullName  string `json:"fullName,omitempty" jsonschema:"full job path, e.g. team-a/service-b; this is what other job tools take as their job argument"`
	URL       string `json:"url" jsonschema:"job URL"`
	Class     string `json:"class,omitempty" jsonschema:"Jenkins internal class, e.g. a Folder or a FreeStyleProject"`
	IsFolder  bool   `json:"isFolder" jsonschema:"true when this entry is a folder to descend into rather than a buildable job"`
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

// jobFieldsTree is the shared jobs[...] sub-selector, kept in one place so
// listJobs and views.go's getView cannot drift apart again.
const jobFieldsTree = "name,fullName,url,color,buildable,_class"

// folderClassSuffix identifies a folder. Jenkins reports the Cloudbees
// Folder plugin's class as com.cloudbees.hudson.plugins.folder.Folder, and
// organization folders as ...OrganizationFolder, so a suffix match covers
// both without hardcoding the plugin's full package path.
const folderClassSuffix = "Folder"

func (j jenkinsJob) summary() JobSummary {
	return JobSummary{
		Name: j.Name, FullName: j.FullName, URL: j.URL, Class: j.Class,
		IsFolder:  strings.HasSuffix(j.Class, folderClassSuffix),
		Buildable: j.Buildable, Color: j.Color,
	}
}

// ListJobsInput is the input to jenkins_list_jobs.
type ListJobsInput struct {
	Folder string `json:"folder,omitempty" jsonschema:"folder path to list within, e.g. team-a/sub-folder (job fullNames joined by /); omit for the top level"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum jobs to return (default 50, maximum 200)"`
	Offset int    `json:"offset,omitempty" jsonschema:"number of jobs to skip, for paging; use the previous response's offset+count"`
}

// RefineSchema bounds the paging arguments.
func (ListJobsInput) RefineSchema(s *jsonschema.Schema) { refinePaging(s) }

func (t *jobTools) listJobs(ctx context.Context, _ *mcp.CallToolRequest, in ListJobsInput) (*mcp.CallToolResult, PageResult[JobSummary], error) {
	limit, offset := effectiveLimit(in.Limit), effectiveOffset(in.Offset)

	var raw struct {
		Jobs []jenkinsJob `json:"jobs"`
	}
	path := jenkinsx.JobPath(in.Folder) + "/api/json"
	query := url.Values{"tree": {"jobs[" + jobFieldsTree + "]" + treeRange(offset, limit)}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, PageResult[JobSummary]{}, err
	}

	items := make([]JobSummary, 0, len(raw.Jobs))
	for _, j := range raw.Jobs {
		items = append(items, j.summary())
	}
	return nil, newPage(items, offset, limit), nil
}

// GetJobInput is the input to jenkins_get_job.
type GetJobInput struct {
	Job string `json:"job" jsonschema:"full job path, e.g. team-a/service-b (a fullName from jenkins_list_jobs)"`
}

// RefineSchema requires a non-blank job path.
func (GetJobInput) RefineSchema(s *jsonschema.Schema) { refineRequiredString(s, "job") }

// BuildRef points to a single build.
type BuildRef struct {
	Number int    `json:"number" jsonschema:"build number; pass this as jenkins_get_build's build argument"`
	URL    string `json:"url" jsonschema:"build URL"`
}

// ParamDef describes one of a job's declared build parameters.
type ParamDef struct {
	Name    string `json:"name" jsonschema:"parameter name"`
	Type    string `json:"type" jsonschema:"parameter type, e.g. StringParameterDefinition"`
	Default string `json:"default,omitempty" jsonschema:"default value, if any"`
}

// HealthReport is one of a job's health metrics. Jenkins reports several
// independent ones (build stability, test results, ...), each with its own
// score, so they are returned as a list rather than collapsed to one
// number.
type HealthReport struct {
	Score       int    `json:"score" jsonschema:"0-100, where 100 is healthiest"`
	Description string `json:"description,omitempty" jsonschema:"what this score measures, e.g. \"Build stability: No recent builds failed.\""`
}

// JobDetail is the output of jenkins_get_job.
type JobDetail struct {
	Name                string         `json:"name"`
	FullName            string         `json:"fullName"`
	URL                 string         `json:"url"`
	Description         string         `json:"description,omitempty"`
	Class               string         `json:"class,omitempty" jsonschema:"Jenkins internal class for this job"`
	IsFolder            bool           `json:"isFolder" jsonschema:"true when this is a folder; list its contents with jenkins_list_jobs"`
	Buildable           bool           `json:"buildable"`
	InQueue             bool           `json:"inQueue" jsonschema:"true when a build of this job is waiting in the queue"`
	NextBuildNumber     int            `json:"nextBuildNumber"`
	HealthScore         int            `json:"healthScore" jsonschema:"lowest of the health report scores (the worst signal), or -1 if unavailable"`
	Health              []HealthReport `json:"health,omitempty" jsonschema:"each health metric with its own score and description"`
	LastBuild           *BuildRef      `json:"lastBuild,omitempty"`
	LastSuccessfulBuild *BuildRef      `json:"lastSuccessfulBuild,omitempty"`
	LastFailedBuild     *BuildRef      `json:"lastFailedBuild,omitempty"`
	LastStableBuild     *BuildRef      `json:"lastStableBuild,omitempty"`
	LastCompletedBuild  *BuildRef      `json:"lastCompletedBuild,omitempty"`
	Parameters          []ParamDef     `json:"parameters,omitempty" jsonschema:"parameters this job declares; pass matching names to jenkins_trigger_build"`
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
	Class           string `json:"_class"`
	Buildable       bool   `json:"buildable"`
	InQueue         bool   `json:"inQueue"`
	NextBuildNumber int    `json:"nextBuildNumber"`
	HealthReport    []struct {
		Score       int    `json:"score"`
		Description string `json:"description"`
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
	if err := requireJobPath("job", in.Job); err != nil {
		return nil, JobDetail{}, err
	}

	var raw jenkinsJobDetail
	path := jenkinsx.JobPath(in.Job) + "/api/json"
	query := url.Values{"tree": {
		"name,fullName,url,description,buildable,inQueue,nextBuildNumber,_class," +
			"healthReport[score,description]," +
			"lastBuild[number,url],lastSuccessfulBuild[number,url],lastFailedBuild[number,url]," +
			"lastStableBuild[number,url],lastCompletedBuild[number,url]," +
			"property[parameterDefinitions[name,type,defaultParameterValue[value]]]",
	}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, JobDetail{}, err
	}

	out := JobDetail{
		Name: raw.Name, FullName: raw.FullName, URL: raw.URL, Description: raw.Description,
		Class:     raw.Class,
		IsFolder:  strings.HasSuffix(raw.Class, folderClassSuffix),
		Buildable: raw.Buildable, InQueue: raw.InQueue, NextBuildNumber: raw.NextBuildNumber,
		HealthScore:         -1,
		LastBuild:           toBuildRef(raw.LastBuild),
		LastSuccessfulBuild: toBuildRef(raw.LastSuccessfulBuild),
		LastFailedBuild:     toBuildRef(raw.LastFailedBuild),
		LastStableBuild:     toBuildRef(raw.LastStableBuild),
		LastCompletedBuild:  toBuildRef(raw.LastCompletedBuild),
	}
	// Jenkins returns one health report per metric; the worst score is the
	// one that actually characterizes the job, so HealthScore reports the
	// minimum rather than whichever happened to be listed first.
	for _, h := range raw.HealthReport {
		out.Health = append(out.Health, HealthReport{Score: h.Score, Description: h.Description})
		if out.HealthScore == -1 || h.Score < out.HealthScore {
			out.HealthScore = h.Score
		}
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

// GetJobConfigInput is the input to jenkins_get_job_config.
type GetJobConfigInput struct {
	Job string `json:"job" jsonschema:"full job path, e.g. team-a/service-b (a fullName from jenkins_list_jobs)"`
}

// RefineSchema requires a non-blank job path.
func (GetJobConfigInput) RefineSchema(s *jsonschema.Schema) { refineRequiredString(s, "job") }

// JobConfig is the output of jenkins_get_job_config.
type JobConfig struct {
	Job       string `json:"job" jsonschema:"the job whose config this is"`
	XML       string `json:"xml" jsonschema:"the job's config.xml"`
	Truncated bool   `json:"truncated" jsonschema:"true if the config was longer than the returned XML; it was cut short to bound response size"`
}

func (t *jobTools) getJobConfig(ctx context.Context, _ *mcp.CallToolRequest, in GetJobConfigInput) (*mcp.CallToolResult, JobConfig, error) {
	if err := requireJobPath("job", in.Job); err != nil {
		return nil, JobConfig{}, err
	}

	body, _, err := t.client.Text(ctx, jenkinsx.JobPath(in.Job)+"/config.xml", nil)
	if err != nil {
		return nil, JobConfig{}, err
	}

	xml, truncated := truncate(body, maxTextBytes)
	return nil, JobConfig{Job: in.Job, XML: xml, Truncated: truncated}, nil
}

// Job-search recursion depth. Each level of nesting multiplies the work
// Jenkins does to answer, so the depth is bounded and the default is
// shallow enough to stay fast on a large instance while still reaching
// jobs two folders deep.
const (
	defaultSearchDepth = 3
	maxSearchDepth     = 5
)

// SearchJobsInput is the input to jenkins_search_jobs.
type SearchJobsInput struct {
	Query string `json:"query" jsonschema:"case-insensitive substring to match against job names"`
	Depth int    `json:"depth,omitempty" jsonschema:"how many folder levels to search (default 3, maximum 5)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum matches to return (default 50, maximum 200)"`
}

// RefineSchema bounds the query, depth and limit.
func (SearchJobsInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "query")
	if p, ok := s.Properties["depth"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxSearchDepth))
	}
	if p, ok := s.Properties["limit"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxPageLimit))
	}
}

// SearchResults is the output of jenkins_search_jobs.
type SearchResults struct {
	Count      int          `json:"count"`
	Items      []JobSummary `json:"items"`
	Truncated  bool         `json:"truncated" jsonschema:"true when more jobs matched than limit allowed; narrow the query"`
	DepthLimit int          `json:"depthLimit" jsonschema:"folder depth actually searched; a job nested deeper than this was not considered"`
}

// nestedJob mirrors jenkinsJob but carries the recursive jobs[] that folder
// entries contain, so one request can walk a folder tree.
type nestedJob struct {
	jenkinsJob
	Jobs []nestedJob `json:"jobs"`
}

// searchTree builds the nested jobs[...] selector for the given depth, e.g.
// depth 2 -> "jobs[<fields>,jobs[<fields>]]". Verified against Jenkins
// 2.568.3: nesting resolves folder contents in a single request, and each
// level reports fullName as the "team-a/sub/job" path other tools accept.
func searchTree(depth int) string {
	inner := jobFieldsTree
	for i := 1; i < depth; i++ {
		inner = jobFieldsTree + ",jobs[" + inner + "]"
	}
	return "jobs[" + inner + "]"
}

func (t *jobTools) searchJobs(ctx context.Context, _ *mcp.CallToolRequest, in SearchJobsInput) (*mcp.CallToolResult, SearchResults, error) {
	if err := requireNonEmpty("query", in.Query); err != nil {
		return nil, SearchResults{}, err
	}
	depth := in.Depth
	switch {
	case depth <= 0:
		depth = defaultSearchDepth
	case depth > maxSearchDepth:
		depth = maxSearchDepth
	}
	limit := effectiveLimit(in.Limit)

	var raw struct {
		Jobs []nestedJob `json:"jobs"`
	}
	query := url.Values{"tree": {searchTree(depth)}}
	if err := t.client.Get(ctx, "/api/json", query, &raw); err != nil {
		return nil, SearchResults{}, err
	}

	needle := strings.ToLower(strings.TrimSpace(in.Query))
	out := SearchResults{Items: []JobSummary{}, DepthLimit: depth}

	var walk func(jobs []nestedJob)
	walk = func(jobs []nestedJob) {
		for _, j := range jobs {
			if out.Truncated {
				return
			}
			// Match on the short name, not the full path: searching for
			// "api" should not match every job inside a folder named
			// "api-team".
			if strings.Contains(strings.ToLower(j.Name), needle) {
				if len(out.Items) >= limit {
					out.Truncated = true
					return
				}
				out.Items = append(out.Items, j.summary())
			}
			walk(j.Jobs)
		}
	}
	walk(raw.Jobs)

	out.Count = len(out.Items)
	return nil, out, nil
}
