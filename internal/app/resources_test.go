// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/config"
)

// wantResourceCount is the number of resources Assemble registers: the
// static jenkins://info plus the job-config and build-console templates.
const wantResourceCount = 3

func TestAssembleRegistersResources(t *testing.T) {
	cfg := &config.Config{URL: "https://ci.example.com", User: "alice", Token: "tok_123"}

	srv, _, err := Assemble(cfg, "test-version")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if got := srv.ResourceCount(); got != wantResourceCount {
		t.Errorf("ResourceCount() = %d, want %d", got, wantResourceCount)
	}
}

// TestAssembleRegistersResourcesRegardlessOfToolsets documents that
// resources are not gated on JENKINS_TOOLSETS: that setting names tool
// areas, which the resources don't map onto one-for-one.
func TestAssembleRegistersResourcesRegardlessOfToolsets(t *testing.T) {
	cases := []struct {
		name     string
		toolsets []string
		readOnly bool
	}{
		{"all toolsets", nil, false},
		{"single toolset", []string{"jobs"}, false},
		{"read-only", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{
				URL: "https://ci.example.com", User: "alice", Token: "tok_123",
				Toolsets: c.toolsets, ReadOnly: c.readOnly,
			}

			srv, _, err := Assemble(cfg, "test-version")
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if got := srv.ResourceCount(); got != wantResourceCount {
				t.Errorf("ResourceCount() = %d, want %d (resources are ungated)", got, wantResourceCount)
			}
		})
	}
}
