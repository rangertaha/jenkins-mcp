// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer guards a bytes.Buffer with a mutex. cmd.Stderr is written to by
// a goroutine exec.Cmd spawns internally (since it's a plain io.Writer, not
// an *os.File) for as long as the subprocess is alive, while the test's main
// goroutine reads it in error messages before cmd.Wait() has joined that
// goroutine — a bare strings.Builder there would be an unsynchronized
// concurrent read/write, a real (if narrow — only triggered on a failure
// path) data race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// isolatedJenkinsEnv builds an environment for the subprocess that strips
// every inherited JENKINS_* variable and points JENKINS_URL at a local mock
// server instead, so this test never depends on or reaches a real Jenkins
// instance.
func isolatedJenkinsEnv(t *testing.T, mockURL string) []string {
	t.Helper()

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "JENKINS_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"JENKINS_URL="+mockURL,
		"JENKINS_USER=test-user",
		"JENKINS_TOKEN=test-token",
		"JENKINS_READONLY=true",
	)
}

// mockJenkinsServer serves just enough of the Jenkins REST API for this
// test's tool/prompt calls: jenkins_whoami.
func mockJenkinsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/whoAmI/api/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authenticated":true,"anonymous":false,"name":"test-user","authorities":["authenticated"]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestMCPStdioIntegration is the only test in this repo that exercises the
// real, compiled binary over its actual stdin/stdout — every other test
// (including internal/server's) drives the server through
// mcp.NewInMemoryTransports(), which bypasses cmd/jenkins/main.go entirely:
// its .env loading, config.Load(), the urfave/cli "mcp" subcommand
// dispatch, signal.NotifyContext wiring, and real line-delimited JSON-RPC
// framing over OS pipes. Every Jenkins tool needs a configured Jenkins URL
// and credentials (unlike AWS's ambient credential chain), so this test
// stands up a local mock Jenkins server rather than reaching the network.
func TestMCPStdioIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio integration test in -short mode (builds the full binary)")
	}

	bin := filepath.Join(t.TempDir(), "jenkins-mcp-test-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	mock := mockJenkinsServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Env = isolatedJenkinsEnv(t, mock.URL)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var stderr syncBuffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s mcp: %v", bin, err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	send := func(v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshaling request: %v", err)
		}
		if _, err := stdin.Write(append(raw, '\n')); err != nil {
			t.Fatalf("writing to stdin: %v (stderr so far: %s)", err, stderr.String())
		}
	}
	recv := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("no response on stdout: %v (stderr: %s)", scanner.Err(), stderr.String())
		}
		var v map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			t.Fatalf("unmarshaling response %q: %v", scanner.Text(), err)
		}
		return v
	}

	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "integration-test", "version": "0.0.0"},
		},
	})
	initResp := recv()
	if initResp["error"] != nil {
		t.Fatalf("initialize returned an error: %v", initResp["error"])
	}

	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": "jenkins_whoami", "arguments": map[string]any{}},
	})
	callResp := recv()
	if callResp["error"] != nil {
		t.Fatalf("tools/call jenkins_whoami returned a protocol error: %v", callResp["error"])
	}
	result, ok := callResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call result = %v, want an object", callResp["result"])
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("jenkins_whoami returned a tool error: %v", result["content"])
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("result has no structuredContent: %v", result)
	}
	if name, _ := structured["name"].(string); name != "test-user" {
		t.Errorf("jenkins_whoami name = %v, want test-user", structured["name"])
	}

	// Also exercise the prompts path (survey_job) over the same real stdio
	// connection, for broader coverage of the actual entrypoint.
	send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "survey_job", "arguments": map[string]any{"job": "team-a/service-b"}},
	})
	promptResp := recv()
	if promptResp["error"] != nil {
		t.Fatalf("prompts/get survey_job returned an error: %v", promptResp["error"])
	}
	promptRaw, err := json.Marshal(promptResp["result"])
	if err != nil {
		t.Fatalf("marshaling prompt result: %v", err)
	}
	if !strings.Contains(string(promptRaw), "team-a/service-b") {
		t.Errorf("survey_job prompt result over real stdio doesn't mention the job name: %s", promptRaw)
	}
}
