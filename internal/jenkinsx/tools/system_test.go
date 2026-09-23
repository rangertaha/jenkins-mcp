// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"net/http"
	"testing"
)

func TestListPlugins(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pluginManager/api/json" {
			t.Errorf("path = %q, want /pluginManager/api/json", r.URL.Path)
		}
		if got := r.URL.Query().Get("depth"); got != "1" {
			t.Errorf("depth = %q, want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plugins":[
			{"shortName":"git","longName":"Git plugin","version":"5.0.0","enabled":true,"active":true,"hasUpdate":false}
		]}`))
	})
	tls := &systemTools{client: c}

	_, out, err := tls.listPlugins(context.Background(), nil, ListPluginsInput{})
	if err != nil {
		t.Fatalf("listPlugins: %v", err)
	}
	if out.Count != 1 || out.Items[0].ShortName != "git" {
		t.Errorf("out = %+v", out)
	}
}

func TestSystemInfo(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" {
			t.Errorf("path = %q, want /api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Jenkins", "2.479.1")
		_, _ = w.Write([]byte(`{"mode":"NORMAL","nodeDescription":"the master Jenkins node","numExecutors":2,"useSecurity":true,"quietingDown":false}`))
	})
	tls := &systemTools{client: c}

	_, out, err := tls.systemInfo(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("systemInfo: %v", err)
	}
	if out.Version != "2.479.1" {
		t.Errorf("Version = %q, want 2.479.1 (from X-Jenkins header)", out.Version)
	}
	if out.Mode != "NORMAL" || !out.UseSecurity {
		t.Errorf("out = %+v", out)
	}
}

func TestWhoami(t *testing.T) {
	c := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/whoAmI/api/json" {
			t.Errorf("path = %q, want /whoAmI/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"anonymous":false,"name":"alice","authorities":["authenticated"]}`))
	})
	tls := &systemTools{client: c}

	_, out, err := tls.whoami(context.Background(), nil, EmptyInput{})
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !out.Authenticated || out.Name != "alice" {
		t.Errorf("out = %+v", out)
	}
}
