// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/rangertaha/jenkins-mcp/internal/config"
)

// toolCounts maps each toolset name to the exact number of tools it
// registers. Assemble makes no network calls (jenkinsx.NewClient only
// parses the base URL), so these hermetic tests need no real Jenkins.
var toolCounts = map[string]int{
	"jobs":    4,
	"builds":  9,
	"queue":   2,
	"nodes":   2,
	"views":   2,
	"plugins": 3,
}

func baseConfig() *config.Config {
	return &config.Config{URL: "https://ci.example.com", User: "alice", Token: "tok_123"}
}

func TestAssembleSingleToolset(t *testing.T) {
	for name, want := range toolCounts {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig()
			cfg.Toolsets = []string{name}

			srv, cleanup, err := Assemble(cfg, "test-version")
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if srv.ToolCount() != want {
				t.Errorf("ToolCount() = %d, want %d", srv.ToolCount(), want)
			}
			if got := srv.Toolsets(); len(got) != 1 || got[0] != name {
				t.Errorf("Toolsets() = %v, want [%s]", got, name)
			}
			if cleanup == nil {
				t.Fatal("Assemble returned a nil cleanup func")
			}
			cleanup() // must not panic
		})
	}
}

func TestAssembleAllToolsets(t *testing.T) {
	cfg := baseConfig() // empty Toolsets means "all"

	srv, _, err := Assemble(cfg, "test-version")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	want := 0
	for _, n := range toolCounts {
		want += n
	}
	if srv.ToolCount() != want {
		t.Errorf("ToolCount() = %d, want %d", srv.ToolCount(), want)
	}
	if srv.PromptCount() == 0 {
		t.Error("PromptCount() = 0, want prompts to be registered")
	}
	if srv.ReadOnly() {
		t.Error("ReadOnly() = true, want false for a zero-value ReadOnly")
	}
}

func TestAssembleReadOnlySuppressesWriteTools(t *testing.T) {
	cfg := baseConfig()
	cfg.Toolsets = []string{"builds"} // 5 read tools + 2 write tools
	cfg.ReadOnly = true

	srv, _, err := Assemble(cfg, "test-version")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !srv.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	// builds has two Write tools: jenkins_trigger_build and jenkins_stop_build.
	if want := toolCounts["builds"] - 2; srv.ToolCount() != want {
		t.Errorf("ToolCount() = %d, want %d (write tools suppressed)", srv.ToolCount(), want)
	}
}

func TestAssemblePropagatesClientError(t *testing.T) {
	cfg := baseConfig()
	cfg.URL = "" // jenkinsx.NewClient rejects an empty base URL

	srv, cleanup, err := Assemble(cfg, "test-version")
	if err == nil {
		t.Fatal("expected an error for an empty Jenkins URL")
	}
	if srv != nil {
		t.Errorf("expected a nil server on error, got %v", srv)
	}
	if cleanup != nil {
		t.Error("expected a nil cleanup func on error")
	}
}

func TestAssembleRejectsUnknownToolset(t *testing.T) {
	cfg := baseConfig()
	cfg.Toolsets = []string{"jobs", "buildss"} // typo

	srv, cleanup, err := Assemble(cfg, "test-version")
	if err == nil {
		t.Fatal("expected an error for an unknown toolset name")
	}
	if srv != nil {
		t.Errorf("expected a nil server on error, got %v", srv)
	}
	if cleanup != nil {
		t.Error("expected a nil cleanup func on error")
	}
}

func TestAssembleAllToolsetsBypassesValidation(t *testing.T) {
	// "all" (and the empty-Toolsets default) must never be treated as an
	// unknown toolset name.
	cfg := baseConfig()
	cfg.Toolsets = []string{"all"}

	if _, _, err := Assemble(cfg, "test-version"); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
}
