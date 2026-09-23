// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

// TestRequiredStringInputsRejected is the class test for requireNonEmpty:
// every tool handler that builds a Jenkins REST path from a required
// string input (a job path, build identifier, or node/view name) must
// reject a blank value before calling Jenkins — see common.go's
// requireNonEmpty doc comment for why. A new handler that forgets the
// check fails this test instead of silently reaching Jenkins with a
// malformed, wrong-resource path. Add a case here for every such handler.
func TestRequiredStringInputsRejected(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; a blank required field should be rejected before calling Jenkins", r.URL.Path)
		// A valid, generic response: proves each case fails because of the
		// validation check itself, not an incidental decode/header error
		// from this handler's deliberately wrong (but well-formed) reply.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "http://example.com/queue/item/1/")
		w.Header().Set("X-Text-Size", "0")
		_, _ = w.Write([]byte(`{}`))
	})

	cases := []struct {
		name string
		call func() error
	}{
		{"getJob/job", func() error {
			_, _, err := (&jobTools{client: c}).getJob(context.Background(), nil, GetJobInput{})
			return err
		}},
		{"getBuild/job", func() error {
			_, _, err := (&buildTools{client: c}).getBuild(context.Background(), nil, GetBuildInput{Build: "42"})
			return err
		}},
		{"getBuild/build", func() error {
			_, _, err := (&buildTools{client: c}).getBuild(context.Background(), nil, GetBuildInput{Job: "demo"})
			return err
		}},
		{"triggerBuild/job", func() error {
			_, _, err := (&buildTools{client: c}).triggerBuild(context.Background(), nil, TriggerBuildInput{})
			return err
		}},
		{"getBuildConsole/job", func() error {
			_, _, err := (&buildTools{client: c}).getBuildConsole(context.Background(), nil, GetBuildConsoleInput{Build: "42"})
			return err
		}},
		{"getBuildConsole/build", func() error {
			_, _, err := (&buildTools{client: c}).getBuildConsole(context.Background(), nil, GetBuildConsoleInput{Job: "demo"})
			return err
		}},
		{"getNode/name", func() error {
			_, _, err := (&nodeTools{client: c}).getNode(context.Background(), nil, GetNodeInput{})
			return err
		}},
		{"getView/name", func() error {
			_, _, err := (&viewTools{client: c}).getView(context.Background(), nil, GetViewInput{})
			return err
		}},
		{"getJobConfig/job", func() error {
			_, _, err := (&jobTools{client: c}).getJobConfig(context.Background(), nil, GetJobConfigInput{})
			return err
		}},
		{"listArtifacts/job", func() error {
			_, _, err := (&buildTools{client: c}).listArtifacts(context.Background(), nil, ListArtifactsInput{Build: "42"})
			return err
		}},
		{"listArtifacts/build", func() error {
			_, _, err := (&buildTools{client: c}).listArtifacts(context.Background(), nil, ListArtifactsInput{Job: "demo"})
			return err
		}},
		{"getArtifact/job", func() error {
			_, _, err := (&buildTools{client: c}).getArtifact(context.Background(), nil, GetArtifactInput{Build: "42", Path: "a.txt"})
			return err
		}},
		{"getArtifact/build", func() error {
			_, _, err := (&buildTools{client: c}).getArtifact(context.Background(), nil, GetArtifactInput{Job: "demo", Path: "a.txt"})
			return err
		}},
		{"getArtifact/path", func() error {
			_, _, err := (&buildTools{client: c}).getArtifact(context.Background(), nil, GetArtifactInput{Job: "demo", Build: "42"})
			return err
		}},
		{"getTestResults/job", func() error {
			_, _, err := (&buildTools{client: c}).getTestResults(context.Background(), nil, GetTestResultsInput{Build: "42"})
			return err
		}},
		{"getTestResults/build", func() error {
			_, _, err := (&buildTools{client: c}).getTestResults(context.Background(), nil, GetTestResultsInput{Job: "demo"})
			return err
		}},
		{"stopBuild/job", func() error {
			_, _, err := (&buildTools{client: c}).stopBuild(context.Background(), nil, StopBuildInput{Build: "42"})
			return err
		}},
		{"stopBuild/build", func() error {
			_, _, err := (&buildTools{client: c}).stopBuild(context.Background(), nil, StopBuildInput{Job: "demo"})
			return err
		}},
		{"getBuildStages/job", func() error {
			_, _, err := (&buildTools{client: c}).getBuildStages(context.Background(), nil, GetBuildStagesInput{Build: "42"})
			return err
		}},
		{"getBuildStages/build", func() error {
			_, _, err := (&buildTools{client: c}).getBuildStages(context.Background(), nil, GetBuildStagesInput{Job: "demo"})
			return err
		}},
		{"getStageLog/job", func() error {
			_, _, err := (&buildTools{client: c}).getStageLog(context.Background(), nil, GetStageLogInput{Build: "42", StageID: "11"})
			return err
		}},
		{"getStageLog/build", func() error {
			_, _, err := (&buildTools{client: c}).getStageLog(context.Background(), nil, GetStageLogInput{Job: "demo", StageID: "11"})
			return err
		}},
		{"getStageLog/stageId", func() error {
			_, _, err := (&buildTools{client: c}).getStageLog(context.Background(), nil, GetStageLogInput{Job: "demo", Build: "42"})
			return err
		}},
		{"searchJobs/query", func() error {
			_, _, err := (&jobTools{client: c}).searchJobs(context.Background(), nil, SearchJobsInput{})
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Errorf("%s: succeeded with a blank required field, want an error", tc.name)
			}
		})
	}
}

// TestJobPathsMadeOnlyOfSlashesAreRejected extends the class above to a
// value that is non-blank yet yields no job name. JobPath drops empty
// segments, so "/" and "//" collapse to "", stripping the /job/<name>
// prefix and pointing the request at whatever sits at the base URL —
// jenkins_get_build{job:"/"} asked Jenkins for {base}/lastBuild/api/json.
func TestJobPathsMadeOnlyOfSlashesAreRejected(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; a job path with no segments should be rejected first", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})

	for _, job := range []string{"/", "//", " / "} {
		t.Run(job, func(t *testing.T) {
			if _, _, err := (&jobTools{client: c}).getJob(context.Background(), nil, GetJobInput{Job: job}); err == nil {
				t.Errorf("getJob(%q) succeeded, want an error", job)
			}
			if _, _, err := (&buildTools{client: c}).getBuild(context.Background(), nil,
				GetBuildInput{Job: job, Build: "lastBuild"}); err == nil {
				t.Errorf("getBuild(%q) succeeded, want an error", job)
			}
		})
	}
}

// TestBuildIDPatternMatchesAcceptedPermalinks pins the schema pattern to the
// canonical permalink list. These were two separate lists and drifted: the
// console resource rejected permalinks the equivalent tool accepted.
func TestBuildIDPatternMatchesAcceptedPermalinks(t *testing.T) {
	re := regexp.MustCompile(buildIDPattern)

	for _, p := range jenkinsx.BuildPermalinks {
		if !re.MatchString(p) {
			t.Errorf("buildIDPattern rejects %q, which the server accepts; a client would refuse the call", p)
		}
		if !jenkinsx.IsBuildPermalink(p) {
			t.Errorf("IsBuildPermalink(%q) = false for a canonical permalink", p)
		}
	}
	for _, ok := range []string{"1", "42", "999999"} {
		if !re.MatchString(ok) {
			t.Errorf("buildIDPattern rejects build number %q", ok)
		}
	}
	for _, bad := range []string{"", "-1", "lastBuild ", "latestBuild", "1;2"} {
		if re.MatchString(bad) {
			t.Errorf("buildIDPattern accepts %q, which is not a valid build reference", bad)
		}
	}
}
