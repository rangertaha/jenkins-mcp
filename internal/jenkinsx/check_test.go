// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"net/http"
	"testing"
)

func TestCheckReturnsIdentity(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/whoAmI/api/json" {
			t.Errorf("path = %q, want /whoAmI/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"anonymous":false,"name":"alice","authorities":["authenticated"]}`))
	})

	id, err := Check(context.Background(), c)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !id.Authenticated || id.Anonymous || id.Name != "alice" {
		t.Errorf("Check() = %+v, want authenticated alice", id)
	}
	if len(id.Authorities) != 1 || id.Authorities[0] != "authenticated" {
		t.Errorf("Authorities = %v", id.Authorities)
	}
}

func TestCheckPropagatesError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := Check(context.Background(), c); err == nil {
		t.Fatal("Check: expected an error")
	}
}
