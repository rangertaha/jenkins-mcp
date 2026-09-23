// SPDX-License-Identifier: GPL-3.0-or-later

// Package app assembles the fully-configured jenkins-mcp server from
// configuration. It is shared by the command entry point (cmd/jenkins) so the
// exact server the binary runs is the one under test.
package app

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/rangertaha/jenkins-mcp/internal/config"
	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx/tools"
	"github.com/rangertaha/jenkins-mcp/internal/prompts"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// toolsetNames lists every toolset Assemble knows how to register, used to
// validate JENKINS_TOOLSETS so a typo fails startup instead of silently
// registering fewer tools than intended.
var toolsetNames = []string{"jobs", "builds", "queue", "nodes", "views", "plugins"}

// Assemble builds the fully-configured server (every enabled toolset and the
// built-in prompts) and returns it with a cleanup function. version is
// reported to clients.
func Assemble(cfg *config.Config, version string) (*server.Server, func(), error) {
	if err := validateToolsets(cfg); err != nil {
		return nil, nil, err
	}

	client, err := jenkinsx.NewClient(cfg.URL, cfg.User, cfg.Token, nil)
	if err != nil {
		return nil, nil, err
	}

	srv := server.New("jenkins-mcp", version, cfg.ReadOnly)

	if cfg.ToolsetEnabled("jobs") {
		tools.RegisterJobs(srv, client)
	}
	if cfg.ToolsetEnabled("builds") {
		tools.RegisterBuilds(srv, client)
	}
	if cfg.ToolsetEnabled("queue") {
		tools.RegisterQueue(srv, client)
	}
	if cfg.ToolsetEnabled("nodes") {
		tools.RegisterNodes(srv, client)
	}
	if cfg.ToolsetEnabled("views") {
		tools.RegisterViews(srv, client)
	}
	if cfg.ToolsetEnabled("plugins") {
		tools.RegisterSystem(srv, client)
	}

	// Diagnostics go to stderr; stdout is reserved for the MCP protocol.
	log.SetOutput(os.Stderr)

	prompts.Register(srv)

	return srv, func() {}, nil
}

// validateToolsets fails on any JENKINS_TOOLSETS entry that doesn't match a
// known toolset name, so a typo (or every entry being a typo) fails startup
// with a clear error instead of silently registering fewer tools than
// expected.
func validateToolsets(cfg *config.Config) error {
	if cfg.AllToolsets() {
		return nil
	}

	known := make(map[string]bool, len(toolsetNames))
	for _, t := range toolsetNames {
		known[t] = true
	}

	var unknown []string
	for _, t := range cfg.Toolsets {
		if !known[t] {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown %s entries: %s (valid: %s, or \"all\")",
			config.EnvToolsets, strings.Join(unknown, ", "), strings.Join(toolsetNames, ", "))
	}
	return nil
}
