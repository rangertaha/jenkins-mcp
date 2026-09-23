// SPDX-License-Identifier: GPL-3.0-or-later

// Package resources registers MCP resources: Jenkins documents the model
// reads as context, as distinct from tools, which perform actions.
//
// The split matters for how a client spends its context window. A tool call
// is a deliberate step the model chooses and pays a round trip for; a
// resource is something the client can pull in (or a user can attach)
// without one. So the resources here are the bulky, read-only documents a
// question about a build usually needs in full — a job's config XML, a
// build's whole console log — rather than the paginated, filtered views the
// tools expose.
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// Resource URIs. The job and build templates use RFC 6570 reserved
// expansion ("{+var}") rather than a plain "{var}": a plain variable does
// not match "/", and a Jenkins job path contains slashes as soon as folders
// are involved ("team-a/service-b"), so "{path}" would silently fail to
// route for exactly the nested jobs most likely to be asked about.
const (
	InfoURI                 = "jenkins://info"
	JobConfigURITemplate    = "jenkins://job/{+path}/config.xml"
	BuildConsoleURITemplate = "jenkins://build/{+job}/{number}/console"
)

type jenkinsResources struct {
	client *jenkinsx.Client
}

// Register adds the Jenkins resources to the server.
func Register(s *server.Server, c *jenkinsx.Client) {
	r := &jenkinsResources{client: c}

	s.AddResource(server.ResourceDef{
		URI:         InfoURI,
		Name:        "jenkins_info",
		Title:       "Jenkins instance info",
		Description: "Version, mode, URL, executor count and security status of the Jenkins controller.",
		MIMEType:    "application/json",
	}, r.info)

	s.AddResourceTemplate(server.ResourceDef{
		URI:   JobConfigURITemplate,
		Name:  "jenkins_job_config",
		Title: "Jenkins job config.xml",
		Description: "A job's config.xml: its full definition, including the pipeline script or build steps, " +
			"triggers, and parameters. path is the job's full path, e.g. team-a/service-b.",
		MIMEType: "text/xml",
	}, r.jobConfig)

	s.AddResourceTemplate(server.ResourceDef{
		URI:   BuildConsoleURITemplate,
		Name:  "jenkins_build_console",
		Title: "Jenkins build console log",
		Description: "A build's complete console log in one read. job is the job's full path; number is a build " +
			"number or a permalink such as lastBuild or lastFailedBuild. Use the jenkins_get_build_console tool " +
			"instead when the log is large enough to need paging, or when the build is still running.",
		MIMEType: "text/plain",
	}, r.buildConsole)
}

// info reports the controller's own version and configuration.
type info struct {
	Version         string `json:"version"`
	Mode            string `json:"mode,omitempty"`
	NodeDescription string `json:"nodeDescription,omitempty"`
	NodeName        string `json:"nodeName,omitempty"`
	NumExecutors    int    `json:"numExecutors"`
	URL             string `json:"url,omitempty"`
	UseSecurity     bool   `json:"useSecurity"`
	QuietingDown    bool   `json:"quietingDown"`
}

func (r *jenkinsResources) info(ctx context.Context, uri string) (string, error) {
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

	headers, err := r.client.GetWithHeaders(ctx, "/api/json", query, &raw)
	if err != nil {
		return "", mapErr(uri, err)
	}

	// Jenkins reports its version only in this header — no endpoint carries
	// it in a JSON body.
	out := info{
		Version:      headers.Get("X-Jenkins"),
		Mode:         raw.Mode,
		NodeName:     raw.NodeName,
		NumExecutors: raw.NumExecutors,
		URL:          raw.URL,
		UseSecurity:  raw.UseSecurity,
		QuietingDown: raw.QuietingDown,

		NodeDescription: raw.NodeDescription,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding Jenkins info: %w", err)
	}
	return string(b), nil
}

func (r *jenkinsResources) jobConfig(ctx context.Context, uri string) (string, error) {
	job, err := parseJobConfigURI(uri)
	if err != nil {
		return "", err
	}

	body, _, err := r.client.Text(ctx, jenkinsx.JobPath(job)+"/config.xml", nil)
	if err != nil {
		return "", mapErr(uri, err)
	}
	return body, nil
}

func (r *jenkinsResources) buildConsole(ctx context.Context, uri string) (string, error) {
	job, build, err := parseBuildConsoleURI(uri)
	if err != nil {
		return "", err
	}

	// consoleText returns the whole log in one response, unlike
	// logText/progressiveText, which the paginated tool uses.
	path := jenkinsx.JobPath(job) + "/" + url.PathEscape(build) + "/consoleText"
	body, _, err := r.client.Text(ctx, path, nil)
	if err != nil {
		return "", mapErr(uri, err)
	}
	return body, nil
}

// parseJobConfigURI extracts the job path from a job-config URI. The value
// is percent-decoded here because jenkinsx.JobPath re-escapes each segment;
// passing the raw form through would double-escape a job name containing a
// space.
func parseJobConfigURI(uri string) (string, error) {
	const prefix = "jenkins://job/"
	const suffix = "/config.xml"

	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return "", fmt.Errorf("resource URI %q is not %s", uri, JobConfigURITemplate)
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)

	job, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("resource URI %q has an invalid job path: %w", uri, err)
	}
	// A blank check alone lets "/" and "//" through, and JobPath drops
	// empty segments, so those would produce a request with the /job/...
	// prefix gone entirely — pointed at whatever sits at the base URL.
	if !jenkinsx.HasJobSegments(job) {
		return "", fmt.Errorf("resource URI %q has an empty job path", uri)
	}
	return job, nil
}

// parseBuildConsoleURI extracts the job path and build reference from a
// build-console URI. The job path may contain slashes, so the build
// reference is taken as the final segment and everything before it is the
// job.
func parseBuildConsoleURI(uri string) (job, build string, err error) {
	const prefix = "jenkins://build/"
	const suffix = "/console"

	if !strings.HasPrefix(uri, prefix) || !strings.HasSuffix(uri, suffix) {
		return "", "", fmt.Errorf("resource URI %q is not %s", uri, BuildConsoleURITemplate)
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(uri, prefix), suffix)

	idx := strings.LastIndex(middle, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("resource URI %q is missing a job path or build number", uri)
	}
	rawJob, rawBuild := middle[:idx], middle[idx+1:]

	if job, err = url.PathUnescape(rawJob); err != nil {
		return "", "", fmt.Errorf("resource URI %q has an invalid job path: %w", uri, err)
	}
	if build, err = url.PathUnescape(rawBuild); err != nil {
		return "", "", fmt.Errorf("resource URI %q has an invalid build: %w", uri, err)
	}

	if !jenkinsx.HasJobSegments(job) {
		return "", "", fmt.Errorf("resource URI %q has an empty job path", uri)
	}
	if err := validateBuild(build); err != nil {
		return "", "", fmt.Errorf("resource URI %q: %w", uri, err)
	}
	return job, build, nil
}

// validateBuild rejects anything that is neither a build number nor a known
// permalink, so a malformed reference fails here with a clear message rather
// than as an opaque Jenkins 404 two layers down.
func validateBuild(build string) error {
	if jenkinsx.IsBuildPermalink(build) {
		return nil
	}
	n, err := strconv.Atoi(build)
	if err != nil || n <= 0 {
		return fmt.Errorf("build %q is neither a positive build number nor a permalink (lastBuild, lastSuccessfulBuild, lastFailedBuild, lastStableBuild, lastCompletedBuild)", build)
	}
	return nil
}

// mapErr turns a Jenkins 404 into the protocol-level "resource not found",
// which clients render as a missing resource rather than a server failure.
func mapErr(uri string, err error) error {
	if jenkinsx.IsNotFound(err) {
		return mcp.ResourceNotFoundError(uri)
	}
	return err
}
