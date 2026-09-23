// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"
)

func TestListJobsTopLevel(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" {
			t.Errorf("path = %q, want /api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[
			{"name":"demo","fullName":"demo","url":"http://x/job/demo/","color":"blue","buildable":true,"_class":"hudson.model.FreeStyleProject"},
			{"name":"team-a","fullName":"team-a","url":"http://x/job/team-a/","_class":"com.cloudbees.hudson.plugins.folder.Folder","buildable":false}
		]}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.listJobs(context.Background(), nil, ListJobsInput{})
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2", out.Count)
	}
	if out.Items[0].Name != "demo" || out.Items[0].Color != "blue" {
		t.Errorf("Items[0] = %+v", out.Items[0])
	}
	if out.Items[1].Class != "com.cloudbees.hudson.plugins.folder.Folder" {
		t.Errorf("Items[1].Class = %q, want a Folder class", out.Items[1].Class)
	}
}

func TestListJobsWithFolder(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/team-a/job/sub/api/json" {
			t.Errorf("path = %q, want /job/team-a/job/sub/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	})
	tls := &jobTools{client: c}

	if _, _, err := tls.listJobs(context.Background(), nil, ListJobsInput{Folder: "team-a/sub"}); err != nil {
		t.Fatalf("listJobs: %v", err)
	}
}

func TestGetJob(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/team-a/job/service-b/api/json" {
			t.Errorf("path = %q, want /job/team-a/job/service-b/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name":"service-b","fullName":"team-a/service-b","url":"http://x/job/team-a/job/service-b/",
			"description":"demo job","buildable":true,"inQueue":false,"nextBuildNumber":43,
			"healthReport":[{"score":80}],
			"lastBuild":{"number":42,"url":"http://x/.../42/"},
			"lastSuccessfulBuild":{"number":42,"url":"http://x/.../42/"},
			"lastFailedBuild":null,
			"property":[{"parameterDefinitions":[
				{"name":"BRANCH","type":"StringParameterDefinition","defaultParameterValue":{"value":"main"}}
			]}]
		}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.getJob(context.Background(), nil, GetJobInput{Job: "team-a/service-b"})
	if err != nil {
		t.Fatalf("getJob: %v", err)
	}
	if out.HealthScore != 80 {
		t.Errorf("HealthScore = %d, want 80", out.HealthScore)
	}
	if out.LastBuild == nil || out.LastBuild.Number != 42 {
		t.Errorf("LastBuild = %+v, want number 42", out.LastBuild)
	}
	if out.LastFailedBuild != nil {
		t.Errorf("LastFailedBuild = %+v, want nil", out.LastFailedBuild)
	}
	if len(out.Parameters) != 1 || out.Parameters[0].Name != "BRANCH" || out.Parameters[0].Default != "main" {
		t.Errorf("Parameters = %+v", out.Parameters)
	}
}

func TestGetJobMissingHealthReport(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"demo","fullName":"demo","healthReport":[]}`))
	})
	tls := &jobTools{client: c}

	_, out, err := tls.getJob(context.Background(), nil, GetJobInput{Job: "demo"})
	if err != nil {
		t.Fatalf("getJob: %v", err)
	}
	if out.HealthScore != -1 {
		t.Errorf("HealthScore = %d, want -1 when no health report is available", out.HealthScore)
	}
}

func TestGetJobRejectsEmptyJob(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; getJob should reject an empty job before calling Jenkins", r.URL.Path)
	})
	tls := &jobTools{client: c}

	for _, job := range []string{"", "   "} {
		if _, _, err := tls.getJob(context.Background(), nil, GetJobInput{Job: job}); err == nil {
			t.Errorf("getJob(Job:%q) succeeded, want an error", job)
		}
	}
}
