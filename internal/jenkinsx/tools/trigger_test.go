// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestTriggerBuildChoosesEndpointByJobNotByArguments pins the fix for a real
// bug: the trigger endpoint depends on how the JOB is defined, not on
// whether the caller supplied parameters.
//
// Verified against Jenkins 2.568.3, which rejects the wrong endpoint in both
// directions with an unhelpful 400 "Nothing is submitted":
//
//	parameterized job   + POST /build               -> 400
//	parameterized job   + POST /buildWithParameters -> 201
//	unparameterized job + POST /buildWithParameters -> 400
//	unparameterized job + POST /build               -> 201
//
// The previous implementation picked the endpoint from len(Parameters), so
// triggering a parameterized job without explicitly passing parameters —
// the natural way to build one with its defaults — always failed.
func TestTriggerBuildChoosesEndpointByJobNotByArguments(t *testing.T) {
	cases := []struct {
		name         string
		paramDefs    string
		params       map[string]string
		wantEndpoint string
	}{
		{
			name:         "parameterized job with no arguments uses buildWithParameters",
			paramDefs:    `{"name":"BRANCH"}`,
			params:       nil,
			wantEndpoint: "/job/demo/buildWithParameters",
		},
		{
			name:         "parameterized job with arguments uses buildWithParameters",
			paramDefs:    `{"name":"BRANCH"}`,
			params:       map[string]string{"BRANCH": "main"},
			wantEndpoint: "/job/demo/buildWithParameters",
		},
		{
			name:         "unparameterized job uses build",
			paramDefs:    "",
			params:       nil,
			wantEndpoint: "/job/demo/build",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotEndpoint string
			cl := triggerMock(t, c.paramDefs, func(w http.ResponseWriter, r *http.Request) {
				gotEndpoint = r.URL.Path
				w.Header().Set("Location", "http://x/queue/item/3/")
				w.WriteHeader(http.StatusCreated)
			})

			_, out, err := (&buildTools{client: cl}).triggerBuild(context.Background(), nil,
				TriggerBuildInput{Job: "demo", Parameters: c.params})
			if err != nil {
				t.Fatalf("triggerBuild: %v", err)
			}
			if gotEndpoint != c.wantEndpoint {
				t.Errorf("posted to %q, want %q", gotEndpoint, c.wantEndpoint)
			}
			if out.QueueID != 3 {
				t.Errorf("QueueID = %d, want 3", out.QueueID)
			}
		})
	}
}

// TestTriggerBuildRejectsParametersForUnparameterizedJob checks the caller
// gets a clear error rather than Jenkins' opaque 400, and that the
// parameters are not silently dropped.
func TestTriggerBuildRejectsParametersForUnparameterizedJob(t *testing.T) {
	cl := triggerMock(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected POST to %s; the mismatch should be caught before calling Jenkins", r.URL.Path)
	})

	_, _, err := (&buildTools{client: cl}).triggerBuild(context.Background(), nil,
		TriggerBuildInput{Job: "demo", Parameters: map[string]string{"BRANCH": "main"}})
	if err == nil {
		t.Fatal("triggerBuild succeeded, want an error for parameters on an unparameterized job")
	}
	if !strings.Contains(err.Error(), "no build parameters") {
		t.Errorf("error = %q, want it to explain the job declares no parameters", err)
	}
}

// TestGetBuildReportsCausesAndTests covers the enriched build output. The
// action shapes below are what Jenkins 2.568.3 actually returns: causes
// live in a CauseAction entry, the test summary in a separate
// TestResultAction entry, and unrelated actions come back as {}.
func TestGetBuildReportsCausesAndTests(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		tree := r.URL.Query().Get("tree")
		for _, field := range []string{"causes", "totalCount", "failCount", "skipCount"} {
			if !strings.Contains(tree, field) {
				t.Errorf("tree = %q, want it to request %q", tree, field)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"number":42,"result":"UNSTABLE","building":false,
			"actions":[
				{"_class":"hudson.model.ParametersAction","parameters":[{"name":"BRANCH","value":"main"}]},
				{"_class":"hudson.model.CauseAction","causes":[{"shortDescription":"Started by user alice","userId":"alice"}]},
				{"_class":"hudson.tasks.junit.TestResultAction","totalCount":2,"failCount":1,"skipCount":0},
				{}
			]
		}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	if len(out.Causes) != 1 || out.Causes[0].UserID != "alice" {
		t.Errorf("Causes = %+v, want the user cause", out.Causes)
	}
	if out.Causes[0].Description != "Started by user alice" {
		t.Errorf("Causes[0].Description = %q", out.Causes[0].Description)
	}
	if out.Tests == nil {
		t.Fatal("Tests = nil, want the junit summary")
	}
	if out.Tests.Total != 2 || out.Tests.Failed != 1 {
		t.Errorf("Tests = %+v, want total 2 / failed 1", out.Tests)
	}
	if len(out.Parameters) != 1 || out.Parameters[0].Value != "main" {
		t.Errorf("Parameters = %+v", out.Parameters)
	}
}

// TestGetBuildWithoutTestAction checks Tests stays nil for a job that
// publishes no tests, rather than reporting a misleading all-zero summary.
func TestGetBuildWithoutTestAction(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":1,"result":"SUCCESS","actions":[{},{"_class":"hudson.model.CauseAction","causes":[]}]}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "1"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	if out.Tests != nil {
		t.Errorf("Tests = %+v, want nil when the build has no test action", out.Tests)
	}
}

// TestGetJobReportsAllHealthReportsAndFolder covers the enriched job
// output. Real Jenkins returns one health report per metric — verified
// against 2.568.3, which reported both a test-result score of 50 and a
// build-stability score of 100 for the same job — so HealthScore reports
// the worst rather than whichever happened to come first.
func TestGetJobReportsAllHealthReportsAndFolder(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if tree := r.URL.Query().Get("tree"); !strings.Contains(tree, "healthReport[score,description]") {
			t.Errorf("tree = %q, want it to request the health report description", tree)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name":"svc","fullName":"team-a/svc","_class":"hudson.model.FreeStyleProject",
			"healthReport":[
				{"score":100,"description":"Build stability: No recent builds failed."},
				{"score":50,"description":"Tests: 1 test failing out of a total of 2 tests."}
			]
		}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.getJob(context.Background(), nil, GetJobInput{Job: "team-a/svc"})
	if err != nil {
		t.Fatalf("getJob: %v", err)
	}
	if len(out.Health) != 2 {
		t.Fatalf("Health = %+v, want both reports", out.Health)
	}
	if out.HealthScore != 50 {
		t.Errorf("HealthScore = %d, want 50 (the worst of the reports)", out.HealthScore)
	}
	if out.IsFolder {
		t.Error("IsFolder = true, want false for a FreeStyleProject")
	}
}

func TestGetJobDetectsFolder(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"team-a","fullName":"team-a","_class":"com.cloudbees.hudson.plugins.folder.Folder"}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.getJob(context.Background(), nil, GetJobInput{Job: "team-a"})
	if err != nil {
		t.Fatalf("getJob: %v", err)
	}
	if !out.IsFolder {
		t.Error("IsFolder = false, want true for a Folder class")
	}
	if out.HealthScore != -1 {
		t.Errorf("HealthScore = %d, want -1 when no health report is available", out.HealthScore)
	}
}

func TestListJobsMarksFolders(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[
			{"name":"demo","fullName":"demo","url":"u","_class":"hudson.model.FreeStyleProject","buildable":true},
			{"name":"team-a","fullName":"team-a","url":"u","_class":"com.cloudbees.hudson.plugins.folder.Folder"}
		]}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.listJobs(context.Background(), nil, ListJobsInput{})
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	if out.Items[0].IsFolder {
		t.Error("Items[0].IsFolder = true, want false for a FreeStyleProject")
	}
	if !out.Items[1].IsFolder {
		t.Error("Items[1].IsFolder = false, want true for a Folder")
	}
}
