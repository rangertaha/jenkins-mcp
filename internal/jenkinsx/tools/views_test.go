// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestListViews(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" {
			t.Errorf("path = %q, want /api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"views":[{"name":"All","url":"http://x/","_class":"hudson.model.AllView"}]}`))
	})
	tls := &viewTools{client: c}

	_, out, err := tls.listViews(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("listViews: %v", err)
	}
	if out.Count != 1 || out.Items[0].Name != "All" {
		t.Errorf("out = %+v", out)
	}
}

func TestGetView(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/view/My View/api/json" {
			t.Errorf("path = %q, want the My View path", r.URL.Path)
		}
		if r.URL.EscapedPath() != "/view/My%20View/api/json" {
			t.Errorf("escaped path = %q, want the URL-escaped view path", r.URL.EscapedPath())
		}
		// Regression check for the tree= string once omitting fullName/_class
		// for embedded jobs: JobSummary.FullName/Class are `omitempty`, so a
		// missing field decodes as "" with no error — the only way to catch
		// the regression is to confirm the request itself still asks for them.
		tree := r.URL.Query().Get("tree")
		if !strings.Contains(tree, "fullName") || !strings.Contains(tree, "_class") {
			t.Errorf("tree = %q, want it to request fullName and _class for embedded jobs", tree)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name":"My View","url":"http://x/view/My%20View/","description":"demo view",
			"jobs":[{"name":"demo","fullName":"team-a/demo","url":"http://x/job/demo/","color":"blue","buildable":true,"_class":"hudson.model.FreeStyleProject"}]
		}`))
	})
	tls := &viewTools{client: c}

	_, out, err := tls.getView(context.Background(), nil, GetViewInput{Name: "My View"})
	if err != nil {
		t.Fatalf("getView: %v", err)
	}
	if out.Description != "demo view" || len(out.Jobs) != 1 || out.Jobs[0].Name != "demo" {
		t.Errorf("out = %+v", out)
	}
	if out.Jobs[0].FullName != "team-a/demo" || out.Jobs[0].Class != "hudson.model.FreeStyleProject" {
		t.Errorf("Jobs[0] = %+v, want FullName and Class populated", out.Jobs[0])
	}
}
