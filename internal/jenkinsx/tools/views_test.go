// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name":"My View","url":"http://x/view/My%20View/","description":"demo view",
			"jobs":[{"name":"demo","url":"http://x/job/demo/","color":"blue","buildable":true}]
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
}
