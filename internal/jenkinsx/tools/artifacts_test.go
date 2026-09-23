// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListArtifacts(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/42/api/json" {
			t.Errorf("path = %q, want /job/demo/42/api/json", r.URL.Path)
		}
		if tree := r.URL.Query().Get("tree"); !strings.Contains(tree, "relativePath") {
			t.Errorf("tree = %q, want it to request relativePath", tree)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifacts":[
			{"fileName":"junit.xml","relativePath":"out/junit.xml"},
			{"fileName":"result.txt","relativePath":"out/result.txt"}
		]}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.listArtifacts(context.Background(), nil, ListArtifactsInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("listArtifacts: %v", err)
	}
	if out.Count != 2 || out.Items[0].RelativePath != "out/junit.xml" {
		t.Errorf("out = %+v", out)
	}
	if out.HasMore {
		t.Error("HasMore = true, want false when everything fit in the page")
	}
}

// TestGetArtifactEscapesEachPathSegment checks a multi-segment relativePath
// reaches Jenkins as real path separators. Escaping the whole path at once
// would turn "out/result.txt" into "out%2Fresult.txt" and miss the file.
func TestGetArtifactEscapesEachPathSegment(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/42/artifact/out dir/result.txt" {
			t.Errorf("path = %q, want each segment escaped separately", r.URL.Path)
		}
		if r.URL.EscapedPath() != "/job/demo/42/artifact/out%20dir/result.txt" {
			t.Errorf("escaped path = %q, want the space escaped but the separator kept", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte("artifact-body"))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getArtifact(context.Background(), nil, GetArtifactInput{
		Job: "demo", Build: "42", Path: "out dir/result.txt",
	})
	if err != nil {
		t.Fatalf("getArtifact: %v", err)
	}
	if out.Content != "artifact-body" || out.Truncated {
		t.Errorf("out = %+v", out)
	}
}

func TestGetArtifactTruncatesLargeContent(t *testing.T) {
	body := strings.Repeat("x", 500)
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getArtifact(context.Background(), nil, GetArtifactInput{
		Job: "demo", Build: "42", Path: "big.txt", MaxBytes: 100,
	})
	if err != nil {
		t.Fatalf("getArtifact: %v", err)
	}
	if len(out.Content) != 100 {
		t.Errorf("len(Content) = %d, want 100", len(out.Content))
	}
	if !out.Truncated {
		t.Error("Truncated = false, want true for content cut at maxBytes")
	}
}

func TestGetTestResults(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/42/testReport/api/json" {
			t.Errorf("path = %q, want the testReport path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Shape verified against Jenkins 2.568.3 with the junit plugin.
		_, _ = w.Write([]byte(`{"failCount":1,"passCount":1,"skipCount":0,"suites":[{"cases":[
			{"className":"c","name":"passes","status":"PASSED","errorDetails":null},
			{"className":"c","name":"fails","status":"FAILED","errorDetails":"boom"}
		]}]}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("getTestResults: %v", err)
	}
	if !out.HasResults || out.Total != 2 || out.Failed != 1 || out.Passed != 1 {
		t.Errorf("out = %+v", out)
	}
	if len(out.FailedTests) != 1 {
		t.Fatalf("FailedTests = %+v, want only the failing case", out.FailedTests)
	}
	if out.FailedTests[0].Name != "fails" || out.FailedTests[0].ErrorDetails != "boom" {
		t.Errorf("FailedTests[0] = %+v", out.FailedTests[0])
	}
}

// TestGetTestResultsNoTestReport covers a job that publishes no tests.
// Jenkins has no testReport for it at all and answers 404; that is an
// ordinary answer to "did this build have tests?", so it must come back as
// hasResults=false rather than a tool error.
func TestGetTestResultsNoTestReport(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("getTestResults: %v, want a clean no-results answer", err)
	}
	if out.HasResults {
		t.Errorf("HasResults = true, want false for a job with no test report")
	}
}

func TestGetTestResultsCapsFailures(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		var cases []string
		for i := 0; i < 5; i++ {
			cases = append(cases, `{"className":"c","name":"f","status":"FAILED","errorDetails":"e"}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"failCount":5,"passCount":0,"skipCount":0,"suites":[{"cases":[` +
			strings.Join(cases, ",") + `]}]}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo", Build: "42", Limit: 2})
	if err != nil {
		t.Fatalf("getTestResults: %v", err)
	}
	if len(out.FailedTests) != 2 {
		t.Errorf("len(FailedTests) = %d, want 2 (capped by limit)", len(out.FailedTests))
	}
	if !out.MoreFailures {
		t.Error("MoreFailures = false, want true when failures were capped")
	}
}

func TestStopBuild(t *testing.T) {
	var posted bool
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Real Jenkins answers a successful stop with a 302 back to the
		// build page, and net/http follows it (turning the POST into a GET),
		// so the mock serves both hops.
		if r.URL.Path == "/job/demo/42/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/job/demo/42/stop" {
			t.Errorf("path = %q, want /job/demo/42/stop", r.URL.Path)
		}
		posted = true
		w.Header().Set("Location", "/job/demo/42/")
		w.WriteHeader(http.StatusFound)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.stopBuild(context.Background(), nil, StopBuildInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("stopBuild: %v", err)
	}
	if !posted {
		t.Error("no POST reached the stop endpoint")
	}
	if !out.Stopped {
		t.Error("Stopped = false, want true")
	}
}

func TestGetJobConfig(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/team-a/job/svc/config.xml" {
			t.Errorf("path = %q, want the folder-aware config.xml path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<project><description>demo</description></project>`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.getJobConfig(context.Background(), nil, GetJobConfigInput{Job: "team-a/svc"})
	if err != nil {
		t.Fatalf("getJobConfig: %v", err)
	}
	if !strings.Contains(out.XML, "<description>demo</description>") {
		t.Errorf("XML = %q", out.XML)
	}
	if out.Truncated {
		t.Error("Truncated = true, want false for a small config")
	}
}

// TestGetTestResultsPropagatesNon404Errors checks only a 404 is treated as
// "no test report" — a 500 is a real failure and must surface, not be
// silently reported as a build with no tests.
func TestGetTestResultsPropagatesNon404Errors(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	tls := &buildTools{client: c}

	_, _, err := tls.getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo", Build: "1"})
	if err == nil {
		t.Error("getTestResults returned nil error on a Jenkins 500, want the failure surfaced")
	}
}

// TestTriggerBuildPropagatesLookupFailure checks a failure of the job
// lookup that decides the trigger endpoint is reported, rather than
// falling through to a guessed endpoint.
func TestTriggerBuildPropagatesLookupFailure(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Errorf("unexpected POST to %s; the lookup failed, so no build should be triggered", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, _, err := (&buildTools{client: c}).triggerBuild(context.Background(), nil, TriggerBuildInput{Job: "demo"})
	if err == nil {
		t.Error("triggerBuild returned nil error when the job lookup failed")
	}
}
