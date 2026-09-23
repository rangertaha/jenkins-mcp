// SPDX-License-Identifier: GPL-3.0-or-later

// Command jenkins runs the Jenkins Model Context Protocol server
// (`jenkins mcp`) and checks connectivity (`jenkins test`).
//
// Credentials are a Jenkins username and API token (Jenkins user -> Configure
// -> API Token -> Add new Token), supplied via JENKINS_URL, JENKINS_USER, and
// JENKINS_TOKEN. The `mcp` command communicates over stdio, the transport
// expected by MCP clients such as Claude Desktop/Code and Cursor.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/urfave/cli/v3"

	"github.com/rangertaha/jenkins-mcp/internal"
	"github.com/rangertaha/jenkins-mcp/internal/app"
	"github.com/rangertaha/jenkins-mcp/internal/config"
	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
)

func main() {
	cmd := &cli.Command{
		Name:    "jenkins",
		Usage:   "Jenkins as an MCP server",
		Version: internal.Version(),
		// A bare `jenkins` (no subcommand) runs the MCP server.
		Action: runMCP,
		Commands: []*cli.Command{
			mcpCommand(),
			testCommand(),
		},
		// Print errors ourselves so the MCP stdio stream is never touched.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "jenkins: %v\n", err)
		os.Exit(1)
	}
}

// mcpCommand runs the MCP server over stdio.
func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:   "mcp",
		Usage:  "Run the MCP server over stdio (for Claude Desktop/Code, Cursor, ...)",
		Action: runMCP,
	}
}

// runMCP assembles and serves the MCP server over stdio.
func runMCP(ctx context.Context, _ *cli.Command) error {
	if err := config.LoadEnvFile(config.EnvFile); err != nil {
		log.Printf("jenkins: reading %s: %v", config.EnvFile, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error:\n%w", err)
	}

	ver := internal.Version()
	srv, cleanup, err := app.Assemble(cfg, ver)
	if err != nil {
		return err
	}
	defer cleanup()

	log.Printf("jenkins-mcp %s starting: %d tools, %d prompts across toolsets %v (read-only=%v)",
		ver, srv.ToolCount(), srv.PromptCount(), srv.Toolsets(), cfg.ReadOnly)

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return srv.Run(ctx, &mcp.StdioTransport{})
}

// testCommand verifies credentials via Jenkins' /whoAmI endpoint.
func testCommand() *cli.Command {
	return &cli.Command{
		Name:  "test",
		Usage: "Test Jenkins credentials (whoAmI)",
		Action: func(ctx context.Context, _ *cli.Command) error {
			if err := config.LoadEnvFile(config.EnvFile); err != nil {
				log.Printf("jenkins: reading %s: %v", config.EnvFile, err)
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("configuration error:\n%w", err)
			}

			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			client, err := jenkinsx.NewClient(cfg.URL, cfg.User, cfg.Token, nil)
			if err != nil {
				return err
			}

			id, err := jenkinsx.Check(ctx, client)
			if err != nil {
				return fmt.Errorf("verifying Jenkins credentials: %w", err)
			}

			fmt.Printf("OK  authenticated with Jenkins (url=%s)\n", cfg.URL)
			fmt.Printf("    name=%s authenticated=%v\n", id.Name, id.Authenticated)
			fmt.Printf("    read-only=%v\n", cfg.ReadOnly)
			return nil
		},
	}
}
