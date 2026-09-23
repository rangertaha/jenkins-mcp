// SPDX-License-Identifier: GPL-3.0-or-later

//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// pipelineJobName is the Pipeline job this suite creates.
const pipelineJobName = "e2e-pipeline-job"

// stageMarker is echoed inside the failing stage, so the stage-log test can
// prove it read that stage's output rather than the whole console.
const stageMarker = "E2E_TEST_STAGE_MARKER"

// pipelineConfig defines a three-stage pipeline whose middle stage fails.
// The third stage never runs, which is what makes this a good fixture:
// Jenkins still reports it FAILED, so it exercises the "first failed stage
// is the real one" rule.
const pipelineConfig = `<?xml version='1.1' encoding='UTF-8'?>
<flow-definition plugin="workflow-job">
  <description>jenkins-mcp e2e pipeline</description>
  <keepDependencies>false</keepDependencies>
  <properties/>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition" plugin="workflow-cps">
    <script>
pipeline {
  agent any
  stages {
    stage('Build') { steps { echo 'building' } }
    stage('Test')  { steps { echo '` + stageMarker + `'; sh 'exit 3' } }
    stage('Deploy') { steps { echo 'never reached' } }
  }
}
    </script>
    <sandbox>true</sandbox>
  </definition>
  <triggers/>
  <disabled>false</disabled>
</flow-definition>`

// TestPipelineEndToEnd drives the Pipeline tools against a real Jenkins
// carrying the workflow plugins. It runs as its own top-level test with its
// own container because it needs a different image from the main suite.
func TestPipelineEndToEnd(t *testing.T) {
	jenkins := startJenkinsImage(t, pipelineImage(t))
	bin := buildBinary(t)
	client := startMCPServer(t, bin, jenkins.env(), 10*time.Minute)

	createJob(t, jenkins, pipelineJobName, pipelineConfig)

	// Run it to completion. The pipeline is designed to fail.
	client.mustCallTool("jenkins_trigger_build", map[string]any{"job": pipelineJobName})
	build := waitForBuild(t, client, pipelineJobName)
	if result, _ := build["result"].(string); result != "FAILURE" {
		t.Fatalf("build result = %v, want FAILURE (the Test stage runs `exit 3`)", build["result"])
	}

	var failedStageID string

	t.Run("get_build_stages", func(t *testing.T) {
		got := client.mustCallTool("jenkins_get_build_stages",
			map[string]any{"job": pipelineJobName, "build": "1"})

		if isPipeline, _ := got["isPipeline"].(bool); !isPipeline {
			t.Fatalf("isPipeline = false against a real Pipeline build: %v", got)
		}
		stages, _ := got["stages"].([]any)
		if len(stages) != 3 {
			t.Fatalf("got %d stages, want 3: %v", len(stages), got)
		}

		names := make([]string, 0, len(stages))
		for _, s := range stages {
			stage, _ := s.(map[string]any)
			name, _ := stage["name"].(string)
			names = append(names, name)
		}
		if want := []string{"Build", "Test", "Deploy"}; strings.Join(names, ",") != strings.Join(want, ",") {
			t.Errorf("stage names = %v, want %v", names, want)
		}

		// failedStage must name the stage that actually broke (Test), not
		// Deploy — which real Jenkins also marks FAILED despite never
		// running.
		failedStageID, _ = got["failedStage"].(string)
		if failedStageID == "" {
			t.Fatalf("failedStage is empty: %v", got)
		}
		for _, s := range stages {
			stage, _ := s.(map[string]any)
			if id, _ := stage["id"].(string); id == failedStageID {
				if name, _ := stage["name"].(string); name != "Test" {
					t.Errorf("failedStage points at %q, want the Test stage", name)
				}
				if msg, _ := stage["errorMessage"].(string); msg == "" {
					t.Errorf("failed stage carries no errorMessage: %v", stage)
				}
			}
		}
	})

	t.Run("get_stage_log", func(t *testing.T) {
		if failedStageID == "" {
			t.Skip("no failed stage id from the previous subtest")
		}
		got := client.mustCallTool("jenkins_get_stage_log", map[string]any{
			"job": pipelineJobName, "build": "1", "stageId": failedStageID,
		})

		if isPipeline, _ := got["isPipeline"].(bool); !isPipeline {
			t.Fatalf("isPipeline = false: %v", got)
		}
		if name, _ := got["stageName"].(string); name != "Test" {
			t.Errorf("stageName = %v, want Test", got["stageName"])
		}

		steps, _ := got["steps"].([]any)
		if len(steps) == 0 {
			t.Fatalf("no steps returned: %v", got)
		}
		var combined strings.Builder
		for _, s := range steps {
			step, _ := s.(map[string]any)
			text, _ := step["text"].(string)
			combined.WriteString(text)
		}
		// The marker is echoed only inside the failing stage, so finding it
		// proves the tool read that stage's own output.
		if !strings.Contains(combined.String(), stageMarker) {
			t.Errorf("stage log does not contain %q; got:\n%s", stageMarker, combined.String())
		}
		if !strings.Contains(combined.String(), "exit 3") {
			t.Errorf("stage log does not show the failing command; got:\n%s", combined.String())
		}
	})

	// The graceful-degradation path, against real Jenkins rather than a
	// mock: a freestyle build on a Pipeline-capable instance still has no
	// wfapi, and must answer isPipeline=false instead of erroring.
	t.Run("stages_on_freestyle_build", func(t *testing.T) {
		createJob(t, jenkins, "e2e-freestyle-for-stages", freestyleConfig)
		client.mustCallTool("jenkins_trigger_build", map[string]any{"job": "e2e-freestyle-for-stages"})
		waitForBuild(t, client, "e2e-freestyle-for-stages")

		got := client.mustCallTool("jenkins_get_build_stages",
			map[string]any{"job": "e2e-freestyle-for-stages", "build": "1"})
		if isPipeline, _ := got["isPipeline"].(bool); isPipeline {
			t.Errorf("isPipeline = true for a freestyle build: %v", got)
		}
	})

	t.Run("console_tail", func(t *testing.T) {
		got := client.mustCallTool("jenkins_get_build_console", map[string]any{
			"job": pipelineJobName, "build": "1", "tail": true, "maxBytes": 512,
		})
		text, _ := got["text"].(string)
		if text == "" {
			t.Fatalf("tail returned no text: %v", got)
		}
		if len(text) > 512 {
			t.Errorf("tail returned %d bytes, want at most 512", len(text))
		}
		// A finished build's log ends with its result line, which is the
		// whole point of reading the tail when diagnosing a failure.
		if !strings.Contains(text, "Finished:") {
			t.Errorf("tail does not contain the end of the log; got:\n%s", text)
		}
	})

	t.Run("search_jobs", func(t *testing.T) {
		got := client.mustCallTool("jenkins_search_jobs", map[string]any{"query": "pipeline"})
		items, _ := got["items"].([]any)
		if len(items) == 0 {
			t.Fatalf("search found nothing for %q: %v", "pipeline", got)
		}
		var found bool
		for _, it := range items {
			job, _ := it.(map[string]any)
			if name, _ := job["fullName"].(string); name == pipelineJobName {
				found = true
			}
		}
		if !found {
			t.Errorf("search results do not include %q: %v", pipelineJobName, items)
		}
	})
}
