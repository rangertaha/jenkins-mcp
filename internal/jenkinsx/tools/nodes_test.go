// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// realComputerListJSON mirrors what Jenkins 2.568.3 actually returns for
// /computer/api/json: the controller's displayName is "Built-In Node" (which
// 404s as a URL segment) and only its _class identifies it as the controller.
const realComputerListJSON = `{"computer":[
	{"_class":"hudson.model.Hudson$MasterComputer","displayName":"Built-In Node","offline":false,"idle":true,"numExecutors":2,"assignedLabels":[{"name":"built-in"}]},
	{"_class":"hudson.slaves.SlaveComputer","displayName":"agent-1","offline":true,"offlineCauseReason":"disconnected","numExecutors":1}
]}`

func TestListNodes(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/computer/api/json" {
			t.Errorf("path = %q, want /computer/api/json", r.URL.Path)
		}
		// _class is what distinguishes the controller from an agent, so the
		// request must ask for it or Name can't be derived.
		if tree := r.URL.Query().Get("tree"); !strings.Contains(tree, "_class") {
			t.Errorf("tree = %q, want it to request _class", tree)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realComputerListJSON))
	})
	tls := &nodeTools{client: c}

	_, out, err := tls.listNodes(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("listNodes: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("Count = %d, want 2", out.Count)
	}
	if out.Items[0].DisplayName != "Built-In Node" || len(out.Items[0].Labels) != 1 {
		t.Errorf("Items[0] = %+v", out.Items[0])
	}
	if !out.Items[1].Offline || out.Items[1].OfflineCauseReason != "disconnected" {
		t.Errorf("Items[1] = %+v", out.Items[1])
	}
}

// TestListNodesNameIsUsableAsGetNodeInput pins the contract that makes
// jenkins_list_nodes -> jenkins_get_node chainable: Name must be the URL
// segment, which for the controller is NOT its display name. Real Jenkins
// 404s on /computer/Built-In%20Node and 200s on /computer/(built-in).
func TestListNodesNameIsUsableAsGetNodeInput(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realComputerListJSON))
	})
	tls := &nodeTools{client: c}

	_, out, err := tls.listNodes(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("listNodes: %v", err)
	}
	if got := out.Items[0].Name; got != "(built-in)" {
		t.Errorf("controller Name = %q, want %q (the segment /computer/<name>/ resolves)", got, "(built-in)")
	}
	if got := out.Items[1].Name; got != "agent-1" {
		t.Errorf("agent Name = %q, want its display name %q", got, "agent-1")
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
			"_class":"hudson.model.Hudson$MasterComputer","displayName":"Built-In Node","offline":false,"idle":false,"numExecutors":2,
			"executors":[{"idle":false,"progress":42,"currentExecutable":{"url":"http://x/job/demo/1/"}},{"idle":true,"progress":-1}]
		}`))
	})
	tls := &nodeTools{client: c}

	_, out, err := tls.getNode(context.Background(), nil, GetNodeInput{Name: "(built-in)"})
	if err != nil {
		t.Fatalf("getNode: %v", err)
	}
	if out.Name != "(built-in)" || out.DisplayName != "Built-In Node" {
		t.Errorf("Name = %q, DisplayName = %q; want the controller's segment and display name to differ",
			out.Name, out.DisplayName)
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
