// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"
)

func TestListNodes(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computer/api/json" {
			t.Errorf("path = %q, want /computer/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"computer":[
			{"displayName":"(built-in)","offline":false,"idle":true,"numExecutors":2,"assignedLabels":[{"name":"built-in"}]},
			{"displayName":"agent-1","offline":true,"offlineCauseReason":"disconnected","numExecutors":1}
		]}`))
	})
	tls := &nodeTools{client: c}

	_, out, err := tls.listNodes(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("listNodes: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2", out.Count)
	}
	if out.Items[0].DisplayName != "(built-in)" || len(out.Items[0].Labels) != 1 {
		t.Errorf("Items[0] = %+v", out.Items[0])
	}
	if !out.Items[1].Offline || out.Items[1].OfflineCauseReason != "disconnected" {
		t.Errorf("Items[1] = %+v", out.Items[1])
	}
}

func TestGetNode(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computer/(built-in)/api/json" {
			t.Errorf("path = %q, want the (built-in) node path", r.URL.Path)
		}
		if r.URL.EscapedPath() != "/computer/%28built-in%29/api/json" {
			t.Errorf("escaped path = %q, want the URL-escaped (built-in) node path", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"displayName":"(built-in)","offline":false,"idle":false,"numExecutors":2,
			"executors":[{"idle":false,"progress":42,"currentExecutable":{"url":"http://x/job/demo/1/"}},{"idle":true,"progress":-1}]
		}`))
	})
	tls := &nodeTools{client: c}

	_, out, err := tls.getNode(context.Background(), nil, GetNodeInput{Name: "(built-in)"})
	if err != nil {
		t.Fatalf("getNode: %v", err)
	}
	if len(out.Executors) != 2 {
		t.Fatalf("Executors = %+v, want 2", out.Executors)
	}
	if out.Executors[0].CurrentBuildURL != "http://x/job/demo/1/" {
		t.Errorf("Executors[0].CurrentBuildURL = %q", out.Executors[0].CurrentBuildURL)
	}
	if out.Executors[1].CurrentBuildURL != "" {
		t.Errorf("Executors[1].CurrentBuildURL = %q, want empty for an idle executor", out.Executors[1].CurrentBuildURL)
	}
}
