// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestGetBuildConsoleTail covers reading the END of a log. Jenkins has no
// negative offset and ignores an out-of-range start (it returns the whole
// log from the beginning, verified against 2.568.3), so tail works by
// asking for the size with a HEAD and seeking back from it.
func TestGetBuildConsoleTail(t *testing.T) {
	const logSize = 10000
	var sawHead bool
	var gotStart string

	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Text-Size", strconv.Itoa(logSize))
		if r.Method == http.MethodHead {
			sawHead = true
			return
		}
		gotStart = r.URL.Query().Get("start")
		_, _ = w.Write([]byte(strings.Repeat("z", 100)))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Tail: true, MaxBytes: 512})
	if err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if !sawHead {
		t.Error("no HEAD request was made; tail must probe the log size first")
	}
	if want := strconv.Itoa(logSize - 512); gotStart != want {
		t.Errorf("start = %q, want %q (size minus maxBytes)", gotStart, want)
	}
	if out.Text == "" {
		t.Error("Text is empty")
	}
}

// TestGetBuildConsoleTailShorterThanMaxBytes checks a log smaller than the
// requested window starts at 0 rather than a negative offset.
func TestGetBuildConsoleTailShorterThanMaxBytes(t *testing.T) {
	var gotStart string
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Text-Size", "50")
		if r.Method == http.MethodHead {
			return
		}
		gotStart = r.URL.Query().Get("start")
		_, _ = w.Write([]byte("short log"))
	})
	tls := &buildTools{client: c}

	if _, _, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Tail: true, MaxBytes: 4096}); err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if gotStart != "0" {
		t.Errorf("start = %q, want 0 when the log is shorter than maxBytes", gotStart)
	}
}

// TestGetBuildConsoleTailIgnoresStart documents that tail overrides an
// explicitly supplied start, as its schema description promises.
func TestGetBuildConsoleTailIgnoresStart(t *testing.T) {
	var gotStart string
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Text-Size", "10000")
		if r.Method == http.MethodHead {
			return
		}
		gotStart = r.URL.Query().Get("start")
	})
	tls := &buildTools{client: c}

	if _, _, err := tls.getBuildConsole(context.Background(), nil,
		GetBuildConsoleInput{Job: "demo", Build: "1", Start: 42, Tail: true, MaxBytes: 1000}); err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if gotStart == "42" {
		t.Error("start = 42; tail must override an explicit start")
	}
}

// nestedSearchJSON mirrors the folder hierarchy verified against Jenkins
// 2.568.3: nested jobs[...] recursion resolves folder contents in one
// request and reports fullName as the "team-a/sub/job" path.
const nestedSearchJSON = `{"jobs":[
	{"name":"free-demo","fullName":"free-demo","url":"http://x/job/free-demo/","_class":"hudson.model.FreeStyleProject","buildable":true},
	{"name":"team-a","fullName":"team-a","url":"http://x/job/team-a/","_class":"com.cloudbees.hudson.plugins.folder.Folder","jobs":[
		{"name":"sub","fullName":"team-a/sub","url":"http://x/job/team-a/job/sub/","_class":"com.cloudbees.hudson.plugins.folder.Folder","jobs":[
			{"name":"deep-widget","fullName":"team-a/sub/deep-widget","url":"http://x/job/team-a/job/sub/job/deep-widget/","_class":"hudson.model.FreeStyleProject","buildable":true}
		]}
	]}
]}`

func TestSearchJobsFindsNestedJob(t *testing.T) {
	var gotTree string
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotTree = r.URL.Query().Get("tree")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nestedSearchJSON))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.searchJobs(context.Background(), nil, SearchJobsInput{Query: "widget"})
	if err != nil {
		t.Fatalf("searchJobs: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1: %+v", out.Count, out.Items)
	}
	// fullName is the whole point: it's what every other job tool takes.
	if got := out.Items[0].FullName; got != "team-a/sub/deep-widget" {
		t.Errorf("FullName = %q, want the full folder path", got)
	}
	if !strings.Contains(gotTree, "jobs[") || !strings.Contains(gotTree, "fullName") {
		t.Errorf("tree = %q, want a nested selector requesting fullName", gotTree)
	}
}

func TestSearchJobsIsCaseInsensitive(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nestedSearchJSON))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.searchJobs(context.Background(), nil, SearchJobsInput{Query: "WIDGET"})
	if err != nil {
		t.Fatalf("searchJobs: %v", err)
	}
	if out.Count != 1 {
		t.Errorf("Count = %d, want 1 for a differently-cased query", out.Count)
	}
}

// TestSearchJobsMatchesShortNameNotPath pins that the needle is matched
// against the job's own name: searching "team-a" must not drag in every
// job that merely lives inside a folder of that name.
func TestSearchJobsMatchesShortNameNotPath(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nestedSearchJSON))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.searchJobs(context.Background(), nil, SearchJobsInput{Query: "team-a"})
	if err != nil {
		t.Fatalf("searchJobs: %v", err)
	}
	if out.Count != 1 || out.Items[0].FullName != "team-a" {
		t.Errorf("out = %+v, want only the folder itself", out.Items)
	}
}

func TestSearchJobsRespectsLimit(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(nestedSearchJSON))
	})
	tls := &jobTools{client: c}

	// "e" matches several entries; a limit of 1 must cut and say so.
	_, out, err := tls.searchJobs(context.Background(), nil, SearchJobsInput{Query: "e", Limit: 1})
	if err != nil {
		t.Fatalf("searchJobs: %v", err)
	}
	if out.Count != 1 || !out.Truncated {
		t.Errorf("Count = %d Truncated = %v, want 1 and true", out.Count, out.Truncated)
	}
}

func TestSearchJobsDepthClamping(t *testing.T) {
	cases := []struct {
		name      string
		depth     int
		wantDepth int
		wantNest  int
	}{
		{"default", 0, defaultSearchDepth, defaultSearchDepth},
		{"explicit", 2, 2, 2},
		{"over maximum", 99, maxSearchDepth, maxSearchDepth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotTree string
			c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
				gotTree = r.URL.Query().Get("tree")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jobs":[]}`))
			})
			tls := &jobTools{client: c}

			_, out, err := tls.searchJobs(context.Background(), nil,
				SearchJobsInput{Query: "x", Depth: tc.depth})
			if err != nil {
				t.Fatalf("searchJobs: %v", err)
			}
			if out.DepthLimit != tc.wantDepth {
				t.Errorf("DepthLimit = %d, want %d", out.DepthLimit, tc.wantDepth)
			}
			if got := strings.Count(gotTree, "jobs["); got != tc.wantNest {
				t.Errorf("tree = %q has %d nesting levels, want %d", gotTree, got, tc.wantNest)
			}
		})
	}
}

func TestSearchJobsPropagatesError(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	tls := &jobTools{client: c}

	if _, _, err := tls.searchJobs(context.Background(), nil, SearchJobsInput{Query: "x"}); err == nil {
		t.Error("expected an error from a failing Jenkins")
	}
}
