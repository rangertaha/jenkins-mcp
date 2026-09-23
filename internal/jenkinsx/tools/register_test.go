// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"net/http"
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// toolsetRegistration describes one toolset's Register function and what it
// is expected to register. Keeping all six in one table means a new toolset
// is covered — registration, toolset naming, and read-only suppression — by
// adding a single line, rather than by remembering to write three more tests
// (which is how four of these six ended up with no registration test at all).
var toolsetRegistrations = []struct {
	toolset    string
	register   func(*server.Server, *jenkinsx.Client)
	tools      int // tools registered when writes are allowed
	writeTools int // of those, how many are suppressed in read-only mode
}{
	{"jobs", RegisterJobs, 4, 0},
	{"builds", RegisterBuilds, 9, 2},
	{"queue", RegisterQueue, 2, 1},
	{"nodes", RegisterNodes, 2, 0},
	{"views", RegisterViews, 2, 0},
	{"plugins", RegisterSystem, 3, 0},
}

func registrationClient(t *testing.T) *jenkinsx.Client {
	t.Helper()
	return mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to %s; registration must not call Jenkins", r.URL.Path)
	})
}

func TestRegisterAllToolsets(t *testing.T) {
	c := registrationClient(t)

	for _, tc := range toolsetRegistrations {
		t.Run(tc.toolset, func(t *testing.T) {
			s := server.New("test", "0.0.0", false)
			tc.register(s, c)

			if s.ToolCount() != tc.tools {
				t.Errorf("ToolCount() = %d, want %d", s.ToolCount(), tc.tools)
			}
			got := s.Toolsets()
			if len(got) != 1 || got[0] != tc.toolset {
				t.Errorf("Toolsets() = %v, want [%s]", got, tc.toolset)
			}
		})
	}
}

func TestRegisterAllToolsetsReadOnly(t *testing.T) {
	c := registrationClient(t)

	for _, tc := range toolsetRegistrations {
		t.Run(tc.toolset, func(t *testing.T) {
			s := server.New("test", "0.0.0", true)
			tc.register(s, c)

			want := tc.tools - tc.writeTools
			if s.ToolCount() != want {
				t.Errorf("ToolCount() = %d, want %d (%d write tool(s) suppressed)", s.ToolCount(), want, tc.writeTools)
			}
		})
	}
}

// TestRegisterAllToolsetsTotals pins the documented headline numbers — 22
// tools across 6 toolsets — that README.md and docs/tools.md both state.
func TestRegisterAllToolsetsTotals(t *testing.T) {
	c := registrationClient(t)
	s := server.New("test", "0.0.0", false)

	for _, tc := range toolsetRegistrations {
		tc.register(s, c)
	}

	if got := s.ToolCount(); got != 22 {
		t.Errorf("ToolCount() = %d, want 22 (the documented tool count)", got)
	}
	if got := len(s.Toolsets()); got != 6 {
		t.Errorf("len(Toolsets()) = %d, want 6 (the documented toolset count)", got)
	}
}
