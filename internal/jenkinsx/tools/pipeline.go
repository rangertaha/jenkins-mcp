// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

// Pipeline stage data comes from the Pipeline Stage View plugin's wfapi
// endpoints, not Jenkins core, so every call here can 404 on an instance
// that has no Pipeline plugins or on a build that isn't a Pipeline run.
// That 404 is an ordinary answer ("this isn't a Pipeline build"), not a
// failure, and is reported as isPipeline=false — the same shape
// jenkins_get_test_results uses for a job with no test report.
//
// Endpoint shapes below were captured from a live Jenkins 2.568.3 with
// workflow-aggregator and pipeline-stage-view installed:
//
//	GET <build>/wfapi/describe
//	  -> {id,name,status,durationMillis,stages:[{id,name,status,
//	      startTimeMillis,durationMillis,error:{message,type}}]}
//	GET <build>/execution/node/<stageId>/wfapi/describe
//	  -> {id,name,status,stageFlowNodes:[{id,name,status,...}]}
//	GET <build>/execution/node/<flowNodeId>/wfapi/log
//	  -> {nodeId,nodeStatus,length,hasMore,text}
//
// A freestyle build returns 404 from wfapi/describe.

// stageStatusEnum is the closed set of statuses wfapi reports for a run or
// a stage.
var stageStatusEnum = []any{
	"SUCCESS", "FAILED", "UNSTABLE", "ABORTED", "NOT_EXECUTED",
	"IN_PROGRESS", "PAUSED_PENDING_INPUT", "QUEUED",
}

// GetBuildStagesInput is the input to jenkins_get_build_stages.
type GetBuildStagesInput struct {
	Job   string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
}

// RefineSchema constrains the build identifier.
func (GetBuildStagesInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job")
	refineBuildID(s)
}

// Stage is one stage of a Pipeline run.
type Stage struct {
	ID             string `json:"id" jsonschema:"stage node ID; pass this as jenkins_get_stage_log's stageId"`
	Name           string `json:"name" jsonschema:"the stage's name as written in the Jenkinsfile"`
	Status         string `json:"status" jsonschema:"SUCCESS, FAILED, UNSTABLE, ABORTED, NOT_EXECUTED, IN_PROGRESS, PAUSED_PENDING_INPUT or QUEUED"`
	DurationMillis int64  `json:"durationMillis"`
	ErrorMessage   string `json:"errorMessage,omitempty" jsonschema:"why the stage failed, when it did"`
	ErrorType      string `json:"errorType,omitempty" jsonschema:"the Java exception type behind the failure, e.g. hudson.AbortException"`
}

// BuildStages is the output of jenkins_get_build_stages.
type BuildStages struct {
	IsPipeline bool    `json:"isPipeline" jsonschema:"false when this build is not a Pipeline run, or the Pipeline plugins are not installed; the other fields are then empty"`
	Status     string  `json:"status,omitempty" jsonschema:"the run's overall status"`
	Stages     []Stage `json:"stages,omitempty"`
	// FailedStage points at the first stage that failed, which is the one
	// worth reading the log of. Jenkins marks every stage after a failure
	// FAILED too, carrying the same error, so the LAST failed stage is
	// usually not the one that actually broke.
	FailedStage string `json:"failedStage,omitempty" jsonschema:"ID of the first failed stage; pass it to jenkins_get_stage_log to see why"`
}

// RefineSchema pins status to the closed set wfapi reports.
func (BuildStages) RefineSchema(s *jsonschema.Schema) {
	if p, ok := s.Properties["status"]; ok {
		p.Enum = stageStatusEnum
	}
}

// wfStage is the raw shape of one entry in wfapi/describe's stages array.
type wfStage struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	DurationMillis int64  `json:"durationMillis"`
	Error          *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (t *buildTools) getBuildStages(ctx context.Context, _ *mcp.CallToolRequest, in GetBuildStagesInput) (*mcp.CallToolResult, BuildStages, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, BuildStages{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, BuildStages{}, err
	}

	var raw struct {
		Status string    `json:"status"`
		Stages []wfStage `json:"stages"`
	}
	path := buildPath(in.Job, in.Build) + "/wfapi/describe"
	if err := t.client.Get(ctx, path, nil, &raw); err != nil {
		if jenkinsx.IsNotFound(err) {
			return nil, BuildStages{}, nil
		}
		return nil, BuildStages{}, err
	}

	out := BuildStages{IsPipeline: true, Status: raw.Status}
	for _, s := range raw.Stages {
		stage := Stage{ID: s.ID, Name: s.Name, Status: s.Status, DurationMillis: s.DurationMillis}
		if s.Error != nil {
			stage.ErrorMessage = s.Error.Message
			stage.ErrorType = s.Error.Type
		}
		if out.FailedStage == "" && s.Status == "FAILED" {
			out.FailedStage = s.ID
		}
		out.Stages = append(out.Stages, stage)
	}
	return nil, out, nil
}

// GetStageLogInput is the input to jenkins_get_stage_log.
type GetStageLogInput struct {
	Job      string `json:"job" jsonschema:"full job path, e.g. team-a/service-b"`
	Build    string `json:"build" jsonschema:"build number, or a permalink such as lastBuild"`
	StageID  string `json:"stageId" jsonschema:"stage node ID from jenkins_get_build_stages (its id or failedStage field)"`
	MaxBytes int    `json:"maxBytes,omitempty" jsonschema:"maximum bytes of log to return (default and maximum 65536)"`
}

// RefineSchema constrains the build identifier and size argument.
func (GetStageLogInput) RefineSchema(s *jsonschema.Schema) {
	refineRequiredString(s, "job", "stageId")
	refineBuildID(s)
	if p, ok := s.Properties["maxBytes"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxTextBytes))
	}
}

// StageStep is one step within a stage, with the output it produced.
type StageStep struct {
	ID     string `json:"id"`
	Name   string `json:"name" jsonschema:"the step's display name, e.g. \"Shell Script\""`
	Status string `json:"status"`
	Text   string `json:"text,omitempty" jsonschema:"the step's own console output; empty for steps that produced none"`
}

// StageLog is the output of jenkins_get_stage_log.
type StageLog struct {
	IsPipeline bool        `json:"isPipeline" jsonschema:"false when this build is not a Pipeline run, or the Pipeline plugins are not installed"`
	StageID    string      `json:"stageId"`
	StageName  string      `json:"stageName,omitempty"`
	Status     string      `json:"status,omitempty"`
	Steps      []StageStep `json:"steps,omitempty" jsonschema:"the stage's steps in order, each with its own output"`
	Truncated  bool        `json:"truncated" jsonschema:"true when the combined output was cut short at maxBytes"`
}

// RefineSchema pins status to the closed set wfapi reports.
func (StageLog) RefineSchema(s *jsonschema.Schema) {
	if p, ok := s.Properties["status"]; ok {
		p.Enum = stageStatusEnum
	}
}

func (t *buildTools) getStageLog(ctx context.Context, _ *mcp.CallToolRequest, in GetStageLogInput) (*mcp.CallToolResult, StageLog, error) {
	if err := requireNonEmpty("job", in.Job); err != nil {
		return nil, StageLog{}, err
	}
	if err := requireNonEmpty("build", in.Build); err != nil {
		return nil, StageLog{}, err
	}
	if err := requireNonEmpty("stageId", in.StageID); err != nil {
		return nil, StageLog{}, err
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 || maxBytes > maxTextBytes {
		maxBytes = maxTextBytes
	}

	// The stage node itself carries no log — its wfapi/log reports length 0.
	// The output lives on the stage's child step nodes, so the stage has to
	// be described first to learn their IDs.
	var desc struct {
		Name           string `json:"name"`
		Status         string `json:"status"`
		StageFlowNodes []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"stageFlowNodes"`
	}
	nodeBase := buildPath(in.Job, in.Build) + "/execution/node/" + url.PathEscape(in.StageID)
	if err := t.client.Get(ctx, nodeBase+"/wfapi/describe", nil, &desc); err != nil {
		if jenkinsx.IsNotFound(err) {
			// This 404 has two very different causes: the build isn't a
			// Pipeline run at all, or it is but this stage ID doesn't exist
			// (a stale ID, or one from a different build). Reporting both as
			// isPipeline=false told a caller that had just been handed
			// isPipeline=true by jenkins_get_build_stages that the build
			// wasn't a Pipeline after all. One extra call on the error path
			// tells them apart.
			if t.buildIsPipeline(ctx, in.Job, in.Build) {
				return nil, StageLog{}, fmt.Errorf(
					"build %s of job %q is a Pipeline run but has no stage with ID %q; "+
						"take stageId from jenkins_get_build_stages for THIS build",
					in.Build, in.Job, in.StageID)
			}
			return nil, StageLog{StageID: in.StageID}, nil
		}
		return nil, StageLog{}, err
	}

	out := StageLog{IsPipeline: true, StageID: in.StageID, StageName: desc.Name, Status: desc.Status}
	budget := maxBytes
	for _, n := range desc.StageFlowNodes {
		step := StageStep{ID: n.ID, Name: n.Name, Status: n.Status}
		if budget <= 0 {
			out.Truncated = true
			out.Steps = append(out.Steps, step)
			continue
		}

		var logResp struct {
			Text   string `json:"text"`
			Length int64  `json:"length"`
		}
		logPath := buildPath(in.Job, in.Build) + "/execution/node/" + url.PathEscape(n.ID) + "/wfapi/log"
		if err := t.client.Get(ctx, logPath, nil, &logResp); err != nil {
			// A step whose log has aged out or is otherwise unavailable
			// should not sink the whole stage read: the other steps'
			// output is still the answer the caller wants.
			if !jenkinsx.IsNotFound(err) {
				return nil, StageLog{}, err
			}
			out.Steps = append(out.Steps, step)
			continue
		}

		text, truncated := truncate(logResp.Text, budget)
		step.Text = text
		budget -= len(text)
		if truncated {
			out.Truncated = true
		}
		out.Steps = append(out.Steps, step)
	}
	return nil, out, nil
}

// buildIsPipeline reports whether the build has a Pipeline execution at all,
// used only to tell "not a Pipeline build" apart from "no such stage" when a
// stage lookup 404s. Any error other than a clean 404 is treated as "not a
// Pipeline", since this runs on a path that is already reporting a failure
// and must not mask the original one.
func (t *buildTools) buildIsPipeline(ctx context.Context, job, build string) bool {
	var desc struct {
		ID string `json:"id"`
	}
	err := t.client.Get(ctx, buildPath(job, build)+"/wfapi/describe", nil, &desc)
	return err == nil
}
