// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
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
		Description: "Get one build's result, timing, parameters, what triggered it, its source changes, and " +
			"its test summary when the job publishes tests. Start here when diagnosing a build; follow up with " +
			"jenkins_get_build_console for the log or jenkins_get_test_results for individual test failures. " +
			"build takes a number or a permalink such as lastBuild or lastSuccessfulBuild.",
	}, t.getBuild)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_trigger_build",
		Title: "Trigger a Jenkins build",
		Description: "Enqueue a new build of a job. Pass parameters for a parameterized job (jenkins_get_job " +
			"lists the ones it declares); omitting them builds with each parameter's default. Returns the queue " +
			"item, not a build — the build number only exists once it leaves the queue, so poll jenkins_get_job's " +
			"lastBuild or use jenkins_list_queue to watch it.",
		Write: true,
	}, t.triggerBuild)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_build_console",
		Title: "Get Jenkins build console output",
		Description: "Read a build's console log from a byte offset. The log is returned in slices: when " +
			"hasMore is true, call again with start set to nextStart until it is false. Prefer this over " +
			"guessing at a failure from the build summary alone.",
	}, t.getBuildConsole)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_list_artifacts",
		Title: "List Jenkins build artifacts",
		Description: "List the files a build archived. Use the relativePath of an entry as jenkins_get_artifact's " +
			"path argument to read one.",
	}, t.listArtifacts)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_artifact",
		Title: "Get a Jenkins build artifact",
		Description: "Read one archived artifact's contents, by the relativePath jenkins_list_artifacts reports. " +
			"Intended for text artifacts (logs, reports, manifests); binary content will not survive being " +
			"returned as text, and large files are truncated.",
	}, t.getArtifact)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_get_test_results",
		Title: "Get Jenkins build test results",
		Description: "Get a build's test results, listing failed tests with their error details. Returns " +
			"hasResults=false (not an error) for a job that publishes no test report.",
	}, t.getTestResults)
	server.Register(s, server.ToolDef{
		Name:  "jenkins_stop_build",
		Title: "Stop a running Jenkins build",
		Description: "Abort a build that is currently running. The build ends with result ABORTED. Stopping a " +
			"build that has already finished succeeds and changes nothing.",
		Write:       true,
		Destructive: true,
		Idempotent:  true,
	}, t.stopBuild)
	s.NoteToolset(buildsToolset)
}

// GetBuildInput is the input to jenkins_get_build.
type GetBuildInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink: lastBuild, lastSuccessfulBuild, lastFailedBuild, lastStableBuild, lastCompletedBuild, lastUnsuccessfulBuild, lastUnstableBuild, firstBuild"`
}

// RefineSchema constrains build to a number or a real Jenkins permalink.
func (GetBuildInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
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

// BuildCause records what triggered a build — a user, a timer, an upstream
// job. This is usually the first question asked of a surprising build.
type BuildCause struct {
	Description string `json:"description" jsonschema:"Jenkins' own summary, e.g. \"Started by user alice\""`
	UserID      string `json:"userId,omitempty" jsonschema:"triggering user's ID, when a user triggered it"`
}

// TestSummary is a build's test totals, present only when the job
// publishes a test report.
type TestSummary struct {
	Total   int `json:"total"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// BuildDetail is the output of jenkins_get_build.
type BuildDetail struct {
	Number                  int             `json:"number"`
	URL                     string          `json:"url"`
	Result                  string          `json:"result,omitempty" jsonschema:"SUCCESS, FAILURE, UNSTABLE, ABORTED, NOT_BUILT, or empty while still building"`
	DisplayName             string          `json:"displayName"`
	Description             string          `json:"description,omitempty"`
	BuiltOn                 string          `json:"builtOn,omitempty" jsonschema:"node the build ran on"`
	Building                bool            `json:"building"`
	Timestamp               int64           `json:"timestamp" jsonschema:"start time, epoch milliseconds"`
	DurationMillis          int64           `json:"durationMillis"`
	EstimatedDurationMillis int64           `json:"estimatedDurationMillis"`
	Causes                  []BuildCause    `json:"causes,omitempty" jsonschema:"what triggered this build"`
	Tests                   *TestSummary    `json:"tests,omitempty" jsonschema:"test totals, when the job publishes a test report; use jenkins_get_test_results for individual failures"`
	Parameters              []ParamValue    `json:"parameters,omitempty"`
	Changes                 []ChangeSetItem `json:"changes,omitempty"`
}

// RefineSchema pins result to the closed set Jenkins reports.
func (BuildDetail) RefineSchema(s *jsonschema.Schema) {
	if p, ok := s.Properties["result"]; ok {
		p.Enum = buildResultEnum
	}
}

// jenkinsBuildDetail is the raw shape decoded from a build's api/json.
//
// Actions is a heterogeneous list: Jenkins puts parameters, causes and the
// test-result summary in separate entries distinguished by _class, and an
// entry that has none of the requested fields comes back as {}. Decoding
// them into one struct with every optional field is what the API shape
// forces — verified against Jenkins 2.568.3, where a build carries
// ParametersAction, CauseAction and (with the junit plugin)
// hudson.tasks.junit.TestResultAction.
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
		Class      string `json:"_class"`
		Parameters []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"parameters"`
		Causes []struct {
			ShortDescription string `json:"shortDescription"`
			UserID           string `json:"userId"`
		} `json:"causes"`
		TotalCount *int `json:"totalCount"`
		FailCount  *int `json:"failCount"`
		SkipCount  *int `json:"skipCount"`
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
	path := buildPath(in.Job, in.Build) + "/api/json"
	query := url.Values{"tree": {
		"number,url,result,building,timestamp,duration,estimatedDuration,description,displayName,builtOn," +
			"actions[parameters[name,value],causes[shortDescription,userId],totalCount,failCount,skipCount]," +
			"changeSet[items[msg,author[fullName],commitId]]",
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
			// A JSON null renders as "<nil>" via fmt.Sprint; report it as an
			// empty value instead, so the string can be handed straight back
			// to jenkins_trigger_build.
			value := ""
			if p.Value != nil {
				value = fmt.Sprint(p.Value)
			}
			out.Parameters = append(out.Parameters, ParamValue{Name: p.Name, Value: value})
		}
		for _, c := range a.Causes {
			out.Causes = append(out.Causes, BuildCause{Description: c.ShortDescription, UserID: c.UserID})
		}
		if a.TotalCount != nil {
			out.Tests = &TestSummary{Total: *a.TotalCount}
			if a.FailCount != nil {
				out.Tests.Failed = *a.FailCount
			}
			if a.SkipCount != nil {
				out.Tests.Skipped = *a.SkipCount
			}
		}
	}
	for _, item := range raw.ChangeSet.Items {
		out.Changes = append(out.Changes, ChangeSetItem{Message: item.Msg, Author: item.Author.FullName, CommitID: item.CommitID})
	}
	return nil, out, nil
}

// buildPath returns the URL path for one build of a job.
func buildPath(job, build string) string {
	return jenkinsx.JobPath(job) + "/" + url.PathEscape(build)
}

// TriggerBuildInput is the input to jenkins_trigger_build.
type TriggerBuildInput struct {
	Job        string            `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Parameters map[string]string `json:"parameters,omitempty" jsonschema:"build parameters as name/value pairs; omit to use each parameter's default"`
}

// RefineSchema requires a non-blank job path.
func (TriggerBuildInput) RefineSchema(s *jsonschema.Schema) { refineRequiredString(s, "job") }

// TriggerBuildOutput is the output of jenkins_trigger_build.
type TriggerBuildOutput struct {
	QueueURL string `json:"queueUrl" jsonschema:"URL of the created queue item"`
	QueueID  int    `json:"queueId,omitempty" jsonschema:"numeric ID of the created queue item; pass to jenkins_cancel_queue_item to undo this trigger"`
}

func (t *buildTools) triggerBuild(ctx context.Context, _ *mcp.CallToolRequest, in TriggerBuildInput) (*mcp.CallToolResult, TriggerBuildOutput, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, TriggerBuildOutput{}, err
	}

	// Jenkins rejects the wrong endpoint for the job in BOTH directions,
	// with an unhelpful 400 "Nothing is submitted" either way: a
	// parameterized job refuses POST /build, and a job with no parameters
	// refuses POST /buildWithParameters. (Both verified against Jenkins
	// 2.568.3.) So the endpoint cannot be chosen from whether the caller
	// happened to supply parameters — it depends on how the job is
	// defined, which costs one lookup to learn.
	parameterized, err := t.jobIsParameterized(ctx, in.Job)
	if err != nil {
		return nil, TriggerBuildOutput{}, err
	}
	if len(in.Parameters) > 0 && !parameterized {
		return nil, TriggerBuildOutput{}, fmt.Errorf("job %q declares no build parameters; omit parameters", in.Job)
	}

	endpoint := "/build"
	form := url.Values{}
	if parameterized {
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

// jobIsParameterized reports whether the job declares any build
// parameters, which decides the trigger endpoint.
func (t *buildTools) jobIsParameterized(ctx context.Context, job string) (bool, error) {
	var raw struct {
		Property []struct {
			ParameterDefinitions []struct {
				Name string `json:"name"`
			} `json:"parameterDefinitions"`
		} `json:"property"`
	}
	query := url.Values{"tree": {"property[parameterDefinitions[name]]"}}
	if err := t.client.Get(ctx, jenkinsx.JobPath(job)+"/api/json", query, &raw); err != nil {
		return false, err
	}
	for _, p := range raw.Property {
		if len(p.ParameterDefinitions) > 0 {
			return true, nil
		}
	}
	return false, nil
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
	Job      string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build    string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	Start    int64  `json:"start,omitempty" jsonschema:"byte offset to resume from; 0 for the beginning, or the previous response's nextStart"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"maximum bytes of log to return in this call (default and maximum 65536)"`
}

// RefineSchema bounds the offset and size arguments and constrains build.
func (GetBuildConsoleInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
	if p, ok := s.Properties["start"]; ok {
		p.Minimum = ptr(0.0)
	}
	if p, ok := s.Properties["maxBytes"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxTextBytes))
	}
}

// ConsoleOutput is the output of jenkins_get_build_console.
type ConsoleOutput struct {
	Text      string `json:"text"`
	NextStart int64  `json:"nextStart" jsonschema:"pass as start on the next call to continue reading"`
	HasMore   bool   `json:"hasMore" jsonschema:"true when more log remains — either the build is still running, or this slice hit maxBytes"`
	Truncated bool   `json:"truncated" jsonschema:"true when this slice was cut short at maxBytes rather than reaching the end of the available log"`
}

func (t *buildTools) getBuildConsole(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildConsoleInput) (*mcp.CallToolResult, ConsoleOutput, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, ConsoleOutput{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, ConsoleOutput{}, err
	}

	start := in.Start
	if start < 0 {
		start = 0
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 || maxBytes > maxTextBytes {
		maxBytes = maxTextBytes
	}

	path := buildPath(in.Job, in.Build) + "/logText/progressiveText"
	query := url.Values{"start": {strconv.FormatInt(start, 10)}}

	body, headers, err := t.client.Text(ctx, path, query)
	if err != nil {
		return nil, ConsoleOutput{}, err
	}

	out := ConsoleOutput{HasMore: headers.Get("X-More-Data") == "true"}
	if v := headers.Get("X-Text-Size"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			out.NextStart = n
		}
	}

	text, truncated := truncate(body, maxBytes)
	out.Text = text
	out.Truncated = truncated
	if truncated {
		// The rest of this slice hasn't been read, so resume from where the
		// returned text actually ends rather than from Jenkins' end-of-log
		// offset, which would silently skip the remainder.
		out.NextStart = start + int64(len(text))
		out.HasMore = true
	}
	return nil, out, nil
}

// ListArtifactsInput is the input to jenkins_list_artifacts.
type ListArtifactsInput struct {
	Job    string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build  string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum artifacts to return (default 50, maximum 200)"`
	Offset int    `json:"offset,omitempty" jsonschema:"number of artifacts to skip, for paging"`
}

// RefineSchema constrains the build identifier and paging arguments.
func (ListArtifactsInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
	refinePaging(s)
}

// Artifact describes one archived build artifact.
type Artifact struct {
	FileName     string `json:"fileName" jsonschema:"the artifact's base file name"`
	RelativePath string `json:"relativePath" jsonschema:"path within the build's archive; pass this as jenkins_get_artifact's path"`
}

func (t *buildTools) listArtifacts(ctx context.Context, _ *mcp.CallToolRequest, in ListArtifactsInput) (*mcp.CallToolResult, PageResult[Artifact], error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, PageResult[Artifact]{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, PageResult[Artifact]{}, err
	}
	limit, offset := effectiveLimit(in.Limit), effectiveOffset(in.Offset)

	var raw struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	path := buildPath(in.Job, in.Build) + "/api/json"
	query := url.Values{"tree": {"artifacts[fileName,relativePath]" + treeRange(offset, limit)}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		return nil, PageResult[Artifact]{}, err
	}
	return nil, newPage(raw.Artifacts, offset, limit), nil
}

// GetArtifactInput is the input to jenkins_get_artifact.
type GetArtifactInput struct {
	Job      string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build    string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	Path     string `json:"path" jsonschema:"the artifact's relativePath, as reported by jenkins_list_artifacts"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"maximum bytes to return (default and maximum 65536)"`
}

// RefineSchema constrains the build identifier and size argument.
func (GetArtifactInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job", "path")
	refineBuildID(s)
	if p, ok := s.Properties["maxBytes"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxTextBytes))
	}
}

// ArtifactContent is the output of jenkins_get_artifact.
type ArtifactContent struct {
	Path      string `json:"path" jsonschema:"the artifact's relativePath"`
	Content   string `json:"content" jsonschema:"the artifact's contents as text"`
	Truncated bool   `json:"truncated" jsonschema:"true when the artifact was larger than maxBytes and was cut short"`
}

func (t *buildTools) getArtifact(ctx context.Context, _ *mcp.CallToolRequest, in GetArtifactInput) (*mcp.CallToolResult, ArtifactContent, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, ArtifactContent{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, ArtifactContent{}, err
	}
	if err := requireNonEmpty("path", in.Path); err != nil {
		return nil, ArtifactContent{}, err
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 || maxBytes > maxTextBytes {
		maxBytes = maxTextBytes
	}

	// The artifact path is several segments deep ("out/result.txt"), so each
	// segment is escaped individually — escaping the whole thing would turn
	// its separators into %2F and miss the file.
	var b strings.Builder
	b.WriteString(buildPath(in.Job, in.Build))
	b.WriteString("/artifact")
	for _, seg := range strings.Split(strings.Trim(in.Path, "/"), "/") {
		if seg == "" {
			continue
		}
		b.WriteString("/")
		b.WriteString(url.PathEscape(seg))
	}

	body, _, err := t.client.Text(ctx, b.String(), nil)
	if err != nil {
		return nil, ArtifactContent{}, err
	}

	content, truncated := truncate(body, maxBytes)
	return nil, ArtifactContent{Path: in.Path, Content: content, Truncated: truncated}, nil
}

// GetTestResultsInput is the input to jenkins_get_test_results.
type GetTestResultsInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum failed tests to detail (default 50, maximum 200)"`
}

// RefineSchema constrains the build identifier and result limit.
func (GetTestResultsInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
	if p, ok := s.Properties["limit"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxPageLimit))
	}
}

// FailedTest is one failing test case.
type FailedTest struct {
	ClassName    string `json:"className"`
	Name         string `json:"name"`
	Status       string `json:"status" jsonschema:"FAILED or REGRESSION"`
	ErrorDetails string `json:"errorDetails,omitempty" jsonschema:"the failure message, when the report carries one"`
}

// TestResults is the output of jenkins_get_test_results.
type TestResults struct {
	HasResults   bool         `json:"hasResults" jsonschema:"false when the job publishes no test report; the remaining fields are then empty"`
	Total        int          `json:"total"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	Skipped      int          `json:"skipped"`
	FailedTests  []FailedTest `json:"failedTests,omitempty" jsonschema:"the failing tests, capped at limit"`
	MoreFailures bool         `json:"moreFailures" jsonschema:"true when more failures exist than were returned"`
}

func (t *buildTools) getTestResults(ctx context.Context, _ *mcp.CallToolRequest, in GetTestResultsInput) (*mcp.CallToolResult, TestResults, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, TestResults{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, TestResults{}, err
	}
	limit := effectiveLimit(in.Limit)

	var raw struct {
		FailCount int `json:"failCount"`
		PassCount int `json:"passCount"`
		SkipCount int `json:"skipCount"`
		Suites    []struct {
			Cases []struct {
				ClassName    string `json:"className"`
				Name         string `json:"name"`
				Status       string `json:"status"`
				ErrorDetails string `json:"errorDetails"`
			} `json:"cases"`
		} `json:"suites"`
	}
	path := buildPath(in.Job, in.Build) + "/testReport/api/json"
	query := url.Values{"tree": {"failCount,passCount,skipCount,suites[cases[className,name,status,errorDetails]]"}}
	if err := t.client.Get(ctx, path, query, &raw); err != nil {
		// A job with no test publisher has no testReport at all, which
		// Jenkins reports as 404. That is an ordinary answer to "did this
		// build have tests?", not a failure, so it is reported as
		// hasResults=false rather than surfaced as a tool error.
		if jenkinsx.IsNotFound(err) {
			return nil, TestResults{}, nil
		}
		return nil, TestResults{}, err
	}

	out := TestResults{
		HasResults: true,
		Total:      raw.PassCount + raw.FailCount + raw.SkipCount,
		Passed:     raw.PassCount, Failed: raw.FailCount, Skipped: raw.SkipCount,
	}
	for _, suite := range raw.Suites {
		for _, c := range suite.Cases {
			if c.Status != "FAILED" && c.Status != "REGRESSION" {
				continue
			}
			if len(out.FailedTests) >= limit {
				out.MoreFailures = true
				return nil, out, nil
			}
			out.FailedTests = append(out.FailedTests, FailedTest{
				ClassName: c.ClassName, Name: c.Name, Status: c.Status, ErrorDetails: c.ErrorDetails,
			})
		}
	}
	return nil, out, nil
}

// StopBuildInput is the input to jenkins_stop_build.
type StopBuildInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
}

// RefineSchema constrains the build identifier.
func (StopBuildInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
}

// StopBuildOutput is the output of jenkins_stop_build.
type StopBuildOutput struct {
	Stopped bool `json:"stopped" jsonschema:"true once the abort has been requested; the build ends with result ABORTED"`
}

func (t *buildTools) stopBuild(ctx context.Context, _ *mcp.CallToolRequest, in StopBuildInput) (*mcp.CallToolResult, StopBuildOutput, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, StopBuildOutput{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, StopBuildOutput{}, err
	}

	// Jenkins answers a successful stop with a 302 redirect back to the
	// build page, which the client treats as success.
	if _, err := t.client.PostForm(ctx, buildPath(in.Job, in.Build)+"/stop", nil, url.Values{}, nil); err != nil {
		return nil, StopBuildOutput{}, err
	}
	return nil, StopBuildOutput{Stopped: true}, nil
}
