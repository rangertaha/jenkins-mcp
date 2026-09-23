// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestTruncateDoesNotCollapseOnInvalidLeadingByte guards the failure that
// made a paging caller loop forever: the old implementation stripped bytes
// off the END while the whole string was invalid UTF-8, so a single bad
// byte near the front reduced the result to "". The caller then got empty
// text with nextStart unchanged and hasMore=true, and re-requested the same
// offset indefinitely.
func TestTruncateDoesNotCollapseOnInvalidLeadingByte(t *testing.T) {
	// A continuation byte first, exactly what a byte-offset seek into the
	// middle of a multi-byte rune produces.
	s := "\x80" + strings.Repeat("a", 100)

	got, truncated := truncate(s, 10)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if got == "" {
		t.Fatal("truncate returned an empty string; a caller following nextStart would loop forever")
	}
	if len(got) > 10 {
		t.Errorf("len = %d, want <= 10", len(got))
	}
}

func TestTruncateTailKeepsTheEnd(t *testing.T) {
	s := strings.Repeat("old", 100) + "NEWEST"

	got, dropped, truncated := truncateTail(s, 10)
	if !truncated {
		t.Fatal("truncated = false, want true")
	}
	if !strings.HasSuffix(got, "NEWEST") {
		t.Errorf("text = %q, want it to end with the newest bytes", got)
	}
	if dropped != len(s)-len(got) {
		t.Errorf("dropped = %d, want %d so start can be adjusted correctly", dropped, len(s)-len(got))
	}
}

// TestTruncateTailDropsPartialLeadingRune pins that a tail cut landing
// mid-rune drops only that rune's remnant, not the whole slice.
func TestTruncateTailDropsPartialLeadingRune(t *testing.T) {
	s := "aaa" + "é" + "bbbb" // é is 2 bytes

	got, dropped, _ := truncateTail(s, 6)
	if got == "" {
		t.Fatal("truncateTail returned empty")
	}
	if strings.ContainsRune(got, '�') {
		t.Errorf("text = %q, should not begin mid-rune", got)
	}
	if dropped+len(got) != len(s) {
		t.Errorf("dropped(%d) + len(text)(%d) != len(s)(%d)", dropped, len(got), len(s))
	}
}

// TestGetBuildConsoleTailReportsThatItSkipped is the honesty check: a tail
// read of a finished multi-megabyte log previously reported truncated=false
// and hasMore=false, i.e. "you have the whole log", while holding its last
// slice.
func TestGetBuildConsoleTailReportsThatItSkipped(t *testing.T) {
	const size = 5_000_000
	const maxBytes = 1000

	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("X-Text-Size", strconv.Itoa(size))
			w.WriteHeader(http.StatusOK)
			return
		}
		// Finished build: no X-More-Data. Body is exactly maxBytes.
		w.Header().Set("X-Text-Size", strconv.Itoa(size))
		_, _ = w.Write([]byte(strings.Repeat("z", maxBytes)))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Tail: true, MaxBytes: maxBytes})
	if err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if out.Start != size-maxBytes {
		t.Errorf("Start = %d, want %d so the caller knows where this text begins", out.Start, size-maxBytes)
	}
	if !out.Truncated {
		t.Error("Truncated = false, but ~5MB before Start was never returned; " +
			"the caller would believe it holds the complete log")
	}
}

// TestGetBuildConsoleTailKeepsNewestWhenLogGrows covers a build that appends
// output between the size probe and the fetch: keeping the start of the
// oversized window would discard exactly the bytes tail was asked for.
func TestGetBuildConsoleTailKeepsNewestWhenLogGrows(t *testing.T) {
	const maxBytes = 100

	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("X-Text-Size", "1000")
			w.WriteHeader(http.StatusOK)
			return
		}
		// The build grew: far more than maxBytes comes back, and the newest
		// bytes (the failure) are at the end.
		w.Header().Set("X-Text-Size", "3000")
		_, _ = w.Write([]byte(strings.Repeat("o", 500) + "THE_FAILURE"))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Tail: true, MaxBytes: maxBytes})
	if err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if !strings.Contains(out.Text, "THE_FAILURE") {
		t.Errorf("tail dropped the newest bytes: %q", out.Text)
	}
}

// TestGetBuildConsoleTailFailsWithoutSize covers a proxy that strips X-*
// headers: silently reading from offset 0 would answer a request for the
// END of the log with its BEGINNING.
func TestGetBuildConsoleTailFailsWithoutSize(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK) // no X-Text-Size
			return
		}
		_, _ = w.Write([]byte("the beginning of a very long log"))
	})
	tls := &buildTools{client: c}

	_, _, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Tail: true})
	if err == nil {
		t.Fatal("expected an error when the log size is unknown, got success with head-of-log text")
	}
	if !strings.Contains(err.Error(), "tail") {
		t.Errorf("error should explain the tail read failed, got: %v", err)
	}
}

// TestGetStageLogUnknownStageOnPipelineIsAnError distinguishes "no such
// stage" from "not a Pipeline build". Reporting the former as
// isPipeline=false contradicts the jenkins_get_build_stages call the caller
// just made.
func TestGetStageLogUnknownStageOnPipelineIsAnError(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/execution/node/"):
			http.NotFound(w, r) // unknown stage
		case strings.HasSuffix(r.URL.Path, "/wfapi/describe"):
			// The build itself IS a Pipeline run.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"1","status":"FAILED","stages":[]}`))
		default:
			http.NotFound(w, r)
		}
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getStageLog(context.Background(), nil,
		GetStageLogInput{Job: "demo", Build: "1", StageID: "999"})
	if err == nil {
		t.Fatalf("expected an error for an unknown stage on a Pipeline build, got isPipeline=%v", out.IsPipeline)
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("error should name the bad stage ID, got: %v", err)
	}
}

// TestGetTestResultsHandlesMatrixShape covers a matrix/multi-config job,
// where Jenkins nests suites under childReports and reports totalCount
// instead of passCount. Decoding only the simple shape left the tool saying
// "3 tests failed, here are none of them, and none were withheld".
func TestGetTestResultsHandlesMatrixShape(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"_class":"hudson.matrix.MatrixTestResult",
			"failCount":2,"skipCount":1,"totalCount":103,
			"childReports":[{"result":{"suites":[{"cases":[
				{"className":"pkg.ATest","name":"alpha","status":"FAILED","errorDetails":"boom"},
				{"className":"pkg.BTest","name":"beta","status":"REGRESSION","errorDetails":"bang"},
				{"className":"pkg.CTest","name":"gamma","status":"PASSED"}
			]}]}}]
		}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo", Build: "1"})
	if err != nil {
		t.Fatalf("getTestResults: %v", err)
	}
	if !out.HasResults {
		t.Fatal("HasResults = false, want true")
	}
	if len(out.FailedTests) != 2 {
		t.Errorf("FailedTests = %d (%+v), want the 2 failures nested under childReports",
			len(out.FailedTests), out.FailedTests)
	}
	if out.Total != 103 {
		t.Errorf("Total = %d, want 103 from totalCount", out.Total)
	}
	if out.Passed != 100 {
		t.Errorf("Passed = %d, want 100 (total - failed - skipped)", out.Passed)
	}
}

// TestTriggerBuildRejectsUndeclaredParameter: Jenkins silently ignores a
// parameter a job does not declare, so a typo'd name would queue a build
// with the default value and be reported as a successful override.
func TestTriggerBuildRejectsUndeclaredParameter(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"property":[{"parameterDefinitions":[{"name":"BRANCH"}]}]}`))
			return
		}
		t.Errorf("build should not have been triggered; request reached %s", r.URL.Path)
		w.Header().Set("Location", "http://x/queue/item/1/")
		w.WriteHeader(http.StatusCreated)
	})
	tls := &buildTools{client: c}

	_, _, err := tls.triggerBuild(context.Background(), nil,
		TriggerBuildInput{Job: "demo", Parameters: map[string]string{"BRANCHE": "release-2"}})
	if err == nil {
		t.Fatal("expected an error for an undeclared parameter, got success")
	}
	if !strings.Contains(err.Error(), "BRANCHE") || !strings.Contains(err.Error(), "BRANCH") {
		t.Errorf("error should name the bad parameter and the declared ones, got: %v", err)
	}
}

// TestGetArtifactRefusesBinaryContent: JSON-encoding invalid UTF-8 expands
// each bad byte into a 3-byte U+FFFD, so a capped read could serialize to
// several times the cap and convey nothing.
func TestGetArtifactRefusesBinaryContent(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0xfe, 0x00, 0x01})
	})
	tls := &buildTools{client: c}

	_, _, err := tls.getArtifact(context.Background(), nil,
		GetArtifactInput{Job: "demo", Build: "1", Path: "out/archive.zip"})
	if err == nil {
		t.Fatal("expected an error for a binary artifact, got success")
	}
	if !strings.Contains(err.Error(), "not text") {
		t.Errorf("error should explain the artifact is not text, got: %v", err)
	}
}
