// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

// realDescribeJSON is the payload Jenkins 2.568.3 (workflow-aggregator +
// pipeline-stage-view) returns for a three-stage pipeline whose middle
// stage ran `sh 'exit 3'`. Captured verbatim from a live instance, minus
// the _links blocks the code ignores.
const realDescribeJSON = `{
	"id":"1","name":"#1","status":"FAILED","durationMillis":1810,
	"stages":[
		{"id":"6","name":"Build","execNode":"","status":"SUCCESS","startTimeMillis":1790175141464,"durationMillis":72},
		{"id":"11","name":"Test","execNode":"","status":"FAILED","error":{"message":"script returned exit code 3","type":"hudson.AbortException"},"startTimeMillis":1790175141557,"durationMillis":414},
		{"id":"17","name":"Deploy","execNode":"","status":"FAILED","error":{"message":"script returned exit code 3","type":"hudson.AbortException"},"startTimeMillis":1790175141998,"durationMillis":65}
	]}`

func TestGetBuildStages(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/pipe-demo/1/wfapi/describe" {
			t.Errorf("path = %q, want the wfapi/describe path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realDescribeJSON))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildStages(context.Background(), nil, GetBuildStagesInput{Job: "pipe-demo", Build: "1"})
	if err != nil {
		t.Fatalf("getBuildStages: %v", err)
	}
	if !out.IsPipeline {
		t.Error("IsPipeline = false, want true")
	}
	if out.Status != "FAILED" || len(out.Stages) != 3 {
		t.Fatalf("out = %+v", out)
	}
	if out.Stages[1].Name != "Test" || out.Stages[1].ErrorMessage != "script returned exit code 3" {
		t.Errorf("Stages[1] = %+v", out.Stages[1])
	}
	if out.Stages[1].ErrorType != "hudson.AbortException" {
		t.Errorf("ErrorType = %q", out.Stages[1].ErrorType)
	}
}

// TestGetBuildStagesFailedStageIsTheFirstOne pins the reason FailedStage
// exists: Jenkins marks every stage after a failure FAILED too, carrying
// the same error, so the last failed stage is not the one that broke.
// Stage 17 ("Deploy") never ran, yet real Jenkins reports it FAILED.
func TestGetBuildStagesFailedStageIsTheFirstOne(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realDescribeJSON))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildStages(context.Background(), nil, GetBuildStagesInput{Job: "pipe-demo", Build: "1"})
	if err != nil {
		t.Fatalf("getBuildStages: %v", err)
	}
	if out.FailedStage != "11" {
		t.Errorf("FailedStage = %q, want %q (the FIRST failed stage, not the last)", out.FailedStage, "11")
	}
}

// TestGetBuildStagesNotAPipeline covers the graceful-degradation path: a
// freestyle build returns 404 from wfapi/describe on real Jenkins, which
// is an answer ("not a pipeline"), not a failure.
func TestGetBuildStagesNotAPipeline(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildStages(context.Background(), nil, GetBuildStagesInput{Job: "free-demo", Build: "1"})
	if err != nil {
		t.Fatalf("getBuildStages: %v, want a clean isPipeline=false answer", err)
	}
	if out.IsPipeline {
		t.Error("IsPipeline = true, want false for a non-pipeline build")
	}
	if len(out.Stages) != 0 {
		t.Errorf("Stages = %+v, want empty", out.Stages)
	}
}

func TestGetBuildStagesPropagatesRealErrors(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	tls := &buildTools{client: c}

	if _, _, err := tls.getBuildStages(context.Background(), nil, GetBuildStagesInput{Job: "x", Build: "1"}); err == nil {
		t.Error("expected a 500 to surface as an error, not isPipeline=false")
	}
}

// stageLogServer serves the two-request shape a stage log read needs: a
// describe listing the stage's child step nodes, then one log per node.
// Mirrors real Jenkins, where the stage node's own log is always empty and
// the output lives on its children.
func stageLogServer(t *testing.T) *jenkinsx.Client {
	t.Helper()
	return mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/job/pipe-demo/1/execution/node/11/wfapi/describe":
			_, _ = w.Write([]byte(`{"id":"11","name":"Test","status":"FAILED","stageFlowNodes":[
				{"id":"12","name":"Print Message","status":"SUCCESS"},
				{"id":"13","name":"Shell Script","status":"FAILED"}
			]}`))
		case "/job/pipe-demo/1/execution/node/12/wfapi/log":
			_, _ = w.Write([]byte(`{"nodeId":"12","nodeStatus":"SUCCESS","length":18,"hasMore":false,"text":"TEST_STAGE_MARKER\n"}`))
		case "/job/pipe-demo/1/execution/node/13/wfapi/log":
			_, _ = w.Write([]byte(`{"nodeId":"13","nodeStatus":"FAILED","length":9,"hasMore":false,"text":"+ exit 3\n"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	})
}

func TestGetStageLog(t *testing.T) {
	tls := &buildTools{client: stageLogServer(t)}

	_, out, err := tls.getStageLog(context.Background(), nil,
		GetStageLogInput{Job: "pipe-demo", Build: "1", StageID: "11"})
	if err != nil {
		t.Fatalf("getStageLog: %v", err)
	}
	if !out.IsPipeline || out.StageName != "Test" || out.Status != "FAILED" {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Steps) != 2 {
		t.Fatalf("Steps = %+v, want 2", out.Steps)
	}
	if out.Steps[0].Text != "TEST_STAGE_MARKER\n" {
		t.Errorf("Steps[0].Text = %q", out.Steps[0].Text)
	}
	if out.Steps[1].Name != "Shell Script" || out.Steps[1].Text != "+ exit 3\n" {
		t.Errorf("Steps[1] = %+v", out.Steps[1])
	}
	if out.Truncated {
		t.Error("Truncated = true, want false for a short log")
	}
}

func TestGetStageLogTruncatesAcrossSteps(t *testing.T) {
	tls := &buildTools{client: stageLogServer(t)}

	// A budget smaller than the first step's output must cut there and
	// still report the later steps, so the model knows they exist.
	_, out, err := tls.getStageLog(context.Background(), nil,
		GetStageLogInput{Job: "pipe-demo", Build: "1", StageID: "11", MaxBytes: 5})
	if err != nil {
		t.Fatalf("getStageLog: %v", err)
	}
	if !out.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(out.Steps) != 2 {
		t.Errorf("Steps = %+v, want both steps listed even when truncated", out.Steps)
	}
	if len(out.Steps[0].Text) > 5 {
		t.Errorf("Steps[0].Text = %q, want at most 5 bytes", out.Steps[0].Text)
	}
}

func TestGetStageLogNotAPipeline(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getStageLog(context.Background(), nil,
		GetStageLogInput{Job: "free-demo", Build: "1", StageID: "11"})
	if err != nil {
		t.Fatalf("getStageLog: %v, want a clean isPipeline=false answer", err)
	}
	if out.IsPipeline {
		t.Error("IsPipeline = true, want false")
	}
}

// TestGetStageLogSkipsUnavailableStepLog covers a step whose log is gone
// (404) while its siblings' output is still readable: the stage read must
// still return what it can.
func TestGetStageLogSkipsUnavailableStepLog(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/node/11/wfapi/describe"):
			_, _ = w.Write([]byte(`{"id":"11","name":"Test","status":"FAILED","stageFlowNodes":[
				{"id":"12","name":"Gone","status":"SUCCESS"},
				{"id":"13","name":"Shell Script","status":"FAILED"}
			]}`))
		case strings.HasSuffix(r.URL.Path, "/node/12/wfapi/log"):
			http.NotFound(w, r)
		default:
			_, _ = w.Write([]byte(`{"text":"+ exit 3\n"}`))
		}
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getStageLog(context.Background(), nil,
		GetStageLogInput{Job: "pipe-demo", Build: "1", StageID: "11"})
	if err != nil {
		t.Fatalf("getStageLog: %v", err)
	}
	if len(out.Steps) != 2 || out.Steps[0].Text != "" || out.Steps[1].Text != "+ exit 3\n" {
		t.Errorf("out.Steps = %+v, want the readable step's output preserved", out.Steps)
	}
}
