// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/server"
)

func TestGetBuild(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/42/api/json" {
			t.Errorf("path = %q, want /job/demo/42/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"number":42,"url":"http://x/job/demo/42/","result":"SUCCESS","building":false,
			"displayName":"#42","timestamp":1700000000000,"duration":1234,"estimatedDuration":1200,
			"builtOn":"agent-1",
			"actions":[{"parameters":[{"name":"BRANCH","value":"main"}]},{"parameters":[]}],
			"changeSet":{"items":[{"msg":"fix bug","author":{"fullName":"Alice"},"commitId":"abc123"}]}
		}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "42"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	if out.Result != "SUCCESS" || out.Number != 42 {
		t.Errorf("out = %+v", out)
	}
	if len(out.Parameters) != 1 || out.Parameters[0].Value != "main" {
		t.Errorf("Parameters = %+v", out.Parameters)
	}
	if len(out.Changes) != 1 || out.Changes[0].Author != "Alice" {
		t.Errorf("Changes = %+v", out.Changes)
	}
}

// TestGetBuildSkipsUnnamedParameters covers the guard against Jenkins action
// entries that carry a value with no parameter name. Jenkins' actions array
// is heterogeneous — plugins contribute their own entries — so a nameless
// "parameter" is real, and passing it through would hand the model a
// parameter it can't refer to when triggering a rebuild.
func TestGetBuildSkipsUnnamedParameters(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"number":7,
			"actions":[{"parameters":[
				{"name":"","value":"orphaned"},
				{"name":"BRANCH","value":"main"},
				{"value":"also-orphaned"}
			]}]
		}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "7"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	if len(out.Parameters) != 1 || out.Parameters[0].Name != "BRANCH" {
		t.Errorf("Parameters = %+v, want only the named BRANCH parameter", out.Parameters)
	}
}

// TestGetBuildNonStringParameterValues checks parameter values that aren't
// strings (Jenkins booleans and numbers are common: boolean parameters, build
// numbers) survive into the output rather than becoming empty strings.
//
// It also pins the current handling of a JSON null value, which fmt.Sprint
// renders as the literal string "<nil>". That is a wart rather than a
// decision: a model reading it back could pass "<nil>" to
// jenkins_trigger_build as a real parameter value. Changing it would change
// tool output, so this test records today's behavior rather than asserting
// it is correct.
func TestGetBuildNonStringParameterValues(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"number":8,
			"actions":[{"parameters":[
				{"name":"DEBUG","value":true},
				{"name":"RETRIES","value":3},
				{"name":"NOTHING","value":null}
			]}]
		}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "8"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	got := map[string]string{}
	for _, p := range out.Parameters {
		got[p.Name] = p.Value
	}
	for name, want := range map[string]string{"DEBUG": "true", "RETRIES": "3", "NOTHING": "<nil>"} {
		if got[name] != want {
			t.Errorf("parameter %s = %q, want %q", name, got[name], want)
		}
	}
}

func TestGetBuildPermalink(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/lastSuccessfulBuild/api/json" {
			t.Errorf("path = %q, want the lastSuccessfulBuild permalink", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":40,"result":"SUCCESS"}`))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuild(context.Background(), nil, GetBuildInput{Job: "demo", Build: "lastSuccessfulBuild"})
	if err != nil {
		t.Fatalf("getBuild: %v", err)
	}
	if out.Number != 40 {
		t.Errorf("Number = %d, want 40", out.Number)
	}
}

func TestTriggerBuildWithoutParameters(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/build" {
			t.Errorf("path = %q, want /job/demo/build", r.URL.Path)
		}
		w.Header().Set("Location", "http://x/queue/item/7/")
		w.WriteHeader(http.StatusCreated)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.triggerBuild(context.Background(), nil, TriggerBuildInput{Job: "demo"})
	if err != nil {
		t.Fatalf("triggerBuild: %v", err)
	}
	if out.QueueID != 7 {
		t.Errorf("QueueID = %d, want 7", out.QueueID)
	}
	if out.QueueURL != "http://x/queue/item/7/" {
		t.Errorf("QueueURL = %q", out.QueueURL)
	}
}

func TestTriggerBuildWithParameters(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/buildWithParameters" {
			t.Errorf("path = %q, want /job/demo/buildWithParameters", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("BRANCH") != "main" {
			t.Errorf("BRANCH form value = %q, want main", r.PostForm.Get("BRANCH"))
		}
		w.Header().Set("Location", "http://x/queue/item/8/")
		w.WriteHeader(http.StatusCreated)
	})
	tls := &buildTools{client: c}

	_, out, err := tls.triggerBuild(context.Background(), nil, TriggerBuildInput{
		Job:        "demo",
		Parameters: map[string]string{"BRANCH": "main"},
	})
	if err != nil {
		t.Fatalf("triggerBuild: %v", err)
	}
	if out.QueueID != 8 {
		t.Errorf("QueueID = %d, want 8", out.QueueID)
	}
}

func TestGetBuildConsole(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/demo/42/logText/progressiveText" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("start"); got != "10" {
			t.Errorf("start = %q, want 10", got)
		}
		w.Header().Set("X-Text-Size", "50")
		w.Header().Set("X-More-Data", "true")
		_, _ = w.Write([]byte("more build output"))
	})
	tls := &buildTools{client: c}

	_, out, err := tls.getBuildConsole(context.Background(), nil, GetBuildConsoleInput{Job: "demo", Build: "42", Start: 10})
	if err != nil {
		t.Fatalf("getBuildConsole: %v", err)
	}
	if out.Text != "more build output" || out.NextStart != 50 || !out.HasMore {
		t.Errorf("out = %+v", out)
	}
}

// TestRegisterBuildsSkipsWriteToolsWhenReadOnly confirms jenkins_trigger_build
// (the only Write tool in this toolset) is suppressed in read-only mode,
// while the two read tools remain registered.
func TestRegisterBuildsSkipsWriteToolsWhenReadOnly(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	s := server.New("test", "0.0.0", true)
	RegisterBuilds(s, c)

	if s.ToolCount() != 2 {
		t.Errorf("ToolCount() = %d, want 2 (trigger_build suppressed)", s.ToolCount())
	}
}

func TestRegisterBuildsRegistersAllToolsWhenNotReadOnly(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	s := server.New("test", "0.0.0", false)
	RegisterBuilds(s, c)

	if s.ToolCount() != 3 {
		t.Errorf("ToolCount() = %d, want 3", s.ToolCount())
	}
}
