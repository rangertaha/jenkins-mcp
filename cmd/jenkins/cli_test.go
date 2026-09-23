// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateEnv clears every JENKINS_* variable inherited from the environment
// running the test, so these in-process tests can never depend on (or be
// broken by) a real Jenkins configured on the machine. t.Setenv restores the
// originals when the test ends.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(key, "JENKINS_") {
			t.Setenv(key, "")
		}
	}
}

// configureEnv points the server at mockURL with dummy credentials.
func configureEnv(t *testing.T, mockURL string) {
	t.Helper()
	isolateEnv(t)
	t.Setenv("JENKINS_URL", mockURL)
	t.Setenv("JENKINS_USER", "test-user")
	t.Setenv("JENKINS_TOKEN", "test-token")
}

// runCLI runs the real command tree in-process with the given args (minus the
// program name), returning whatever it wrote to stdout and the error.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newCommand()
	cmd.Writer = &out
	err := cmd.Run(context.Background(), append([]string{"jenkins"}, args...))
	return out.String(), err
}

func TestNewCommandShape(t *testing.T) {
	cmd := newCommand()

	if cmd.Name != "jenkins" {
		t.Errorf("Name = %q, want jenkins", cmd.Name)
	}
	if cmd.Version == "" {
		t.Error("Version is empty; MCP clients see this as the server version")
	}
	if cmd.Action == nil {
		t.Error("bare `jenkins` has no Action; it should run the MCP server")
	}

	want := map[string]bool{"mcp": false, "test": false}
	for _, sub := range cmd.Commands {
		if _, ok := want[sub.Name]; ok {
			want[sub.Name] = true
		}
		if sub.Action == nil {
			t.Errorf("subcommand %q has no Action", sub.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q is missing from the command tree", name)
		}
	}
}

func TestTestCommandReportsIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/whoAmI/api/json" {
			t.Errorf("path = %q, want /whoAmI/api/json", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"anonymous":false,"name":"test-user"}`))
	}))
	defer srv.Close()

	configureEnv(t, srv.URL)
	t.Setenv("JENKINS_READONLY", "true")

	out, err := runCLI(t, "test")
	if err != nil {
		t.Fatalf("jenkins test: %v", err)
	}
	for _, want := range []string{"OK", srv.URL, "test-user", "authenticated=true", "read-only=true"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestTestCommandReportsConfigError(t *testing.T) {
	isolateEnv(t)

	out, err := runCLI(t, "test")
	if err == nil {
		t.Fatal("jenkins test with no JENKINS_URL should fail")
	}
	if !strings.Contains(err.Error(), "configuration error") {
		t.Errorf("error = %v, want it to mention a configuration error", err)
	}
	if out != "" {
		t.Errorf("wrote %q to stdout on a config error, want nothing", out)
	}
}

func TestTestCommandReportsAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad credentials"))
	}))
	defer srv.Close()

	configureEnv(t, srv.URL)

	out, err := runCLI(t, "test")
	if err == nil {
		t.Fatal("jenkins test against a 401 should fail")
	}
	if !strings.Contains(err.Error(), "verifying Jenkins credentials") {
		t.Errorf("error = %v, want it to mention verifying credentials", err)
	}
	if out != "" {
		t.Errorf("wrote %q to stdout on an auth failure, want nothing", out)
	}
}

func TestTestCommandRejectsUnparseableURL(t *testing.T) {
	isolateEnv(t)
	t.Setenv("JENKINS_URL", "http://[::1]:namedport")
	t.Setenv("JENKINS_USER", "u")
	t.Setenv("JENKINS_TOKEN", "t")

	if _, err := runCLI(t, "test"); err == nil {
		t.Fatal("jenkins test with a malformed JENKINS_URL should fail")
	}
}

// TestMCPCommandReportsConfigError covers runMCP's configuration-error path
// for both the `mcp` subcommand and a bare `jenkins`, which share the same
// action. Neither reaches srv.Run, so neither touches stdio.
func TestMCPCommandReportsConfigError(t *testing.T) {
	for _, args := range [][]string{{"mcp"}, {}} {
		name := "bare"
		if len(args) > 0 {
			name = args[0]
		}
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)

			out, err := runCLI(t, args...)
			if err == nil {
				t.Fatal("expected a configuration error with no JENKINS_URL set")
			}
			if !strings.Contains(err.Error(), "configuration error") {
				t.Errorf("error = %v, want it to mention a configuration error", err)
			}
			if out != "" {
				t.Errorf("wrote %q to stdout, which is reserved for the MCP stream", out)
			}
		})
	}
}

// TestMCPCommandReportsAssembleError covers runMCP's app.Assemble error path:
// the configuration is complete, so it gets past config.Load, but names a
// toolset that doesn't exist.
func TestMCPCommandReportsAssembleError(t *testing.T) {
	configureEnv(t, "https://ci.example.com")
	t.Setenv("JENKINS_TOOLSETS", "jobs,buildss")

	out, err := runCLI(t, "mcp")
	if err == nil {
		t.Fatal("expected an error for an unknown toolset name")
	}
	if !strings.Contains(err.Error(), "buildss") {
		t.Errorf("error = %v, want it to name the unknown toolset", err)
	}
	if out != "" {
		t.Errorf("wrote %q to stdout, which is reserved for the MCP stream", out)
	}
}

// TestMCPCommandServesUntilContextCancelled covers runMCP's happy path —
// assembling the server and handing it to srv.Run over the stdio transport —
// without the test ever consuming real stdin: the context is cancelled before
// Run starts, and cancellation is checked ahead of any read, so Run returns
// immediately regardless of whether stdin is a terminal, a pipe, or
// /dev/null. The timeout guard turns a regression in that behavior into a
// clear failure instead of a hung test run.
func TestMCPCommandServesUntilContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	configureEnv(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := newCommand()
	cmd.Writer = &bytes.Buffer{}

	done := make(chan error, 1)
	go func() { done <- cmd.Run(ctx, []string{"jenkins", "mcp"}) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runMCP did not return on an already-cancelled context")
	}
}

// TestUnreadableEnvFileDoesNotStopStartup covers the .env-loading branch both
// commands share: a .env that exists but can't be read is logged to stderr
// and startup continues with whatever the real environment provides. The
// alternative — refusing to start — would take the server down over a file
// that is only ever a local-development convenience.
func TestUnreadableEnvFileDoesNotStopStartup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"name":"test-user"}`))
	}))
	defer srv.Close()

	// A directory named .env opens but fails to read, so LoadEnvFile errors
	// without depending on file permissions (which behave differently as root).
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".env"), 0o755); err != nil {
		t.Fatalf("creating .env directory: %v", err)
	}
	t.Chdir(dir)

	configureEnv(t, srv.URL)

	t.Run("test", func(t *testing.T) {
		out, err := runCLI(t, "test")
		if err != nil {
			t.Fatalf("jenkins test should still run with an unreadable .env: %v", err)
		}
		if !strings.Contains(out, "test-user") {
			t.Errorf("output = %q, want the identity reported despite the unreadable .env", out)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		cmd := newCommand()
		cmd.Writer = &bytes.Buffer{}

		done := make(chan error, 1)
		go func() { done <- cmd.Run(ctx, []string{"jenkins", "mcp"}) }()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run error = %v, want context.Canceled (startup should proceed past the bad .env)", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("runMCP did not return on an already-cancelled context")
		}
	})
}

// TestExitErrHandlerIsSilent confirms the root command keeps cli's own error
// printing disabled: main() prints errors to stderr itself precisely so the
// MCP stdio stream on stdout is never written to by the framework.
func TestExitErrHandlerIsSilent(t *testing.T) {
	cmd := newCommand()
	if cmd.ExitErrHandler == nil {
		t.Fatal("ExitErrHandler is nil; cli would print errors itself")
	}

	var out bytes.Buffer
	cmd.Writer = &out
	cmd.ExitErrHandler(context.Background(), cmd, context.DeadlineExceeded)
	if out.Len() != 0 {
		t.Errorf("ExitErrHandler wrote %q, want nothing", out.String())
	}
}
