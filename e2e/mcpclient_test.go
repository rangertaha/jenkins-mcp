// SPDX-License-Identifier: GPL-3.0-or-later

//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"sync"
	"testing"
	"time"
)

// syncBuffer guards a bytes.Buffer with a mutex. cmd.Stderr is written by a
// goroutine exec.Cmd spawns internally for as long as the subprocess lives,
// while the test goroutine reads it when building failure messages — an
// unsynchronized buffer there is a real data race on the failure path.
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

// mcpClient drives the compiled server the way a real MCP client does:
// line-delimited JSON-RPC over the subprocess's actual stdin/stdout.
type mcpClient struct {
	t       *testing.T
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	stderr  *syncBuffer
	nextID  int
}

// toolResult is one tools/call response, separating a protocol-level failure
// (which fails the test outright) from a tool-level error, which callers may
// legitimately want to assert on.
type toolResult struct {
	structured map[string]any
	isError    bool
	content    string
}

// startMCPServer launches the binary's `mcp` subcommand and completes the
// MCP initialize handshake.
func startMCPServer(t *testing.T, bin string, env []string, timeout time.Duration) *mcpClient {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bin, "mcp")
	cmd.Env = env

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
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	scanner := bufio.NewScanner(stdout)
	// Console logs can be large, so allow a generous line size.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	c := &mcpClient{t: t, stdin: stdin, scanner: scanner, stderr: &stderr}
	c.initialize()
	return c
}

func (c *mcpClient) send(v any) {
	c.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		c.t.Fatalf("marshaling request: %v", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		c.t.Fatalf("writing to stdin: %v (stderr so far: %s)", err, c.stderr.String())
	}
}

// recvID reads responses until it sees the one matching id, skipping any
// server-initiated notifications that arrive in between.
func (c *mcpClient) recvID(id int) map[string]any {
	c.t.Helper()
	for {
		if !c.scanner.Scan() {
			c.t.Fatalf("no response on stdout for id %d: %v (stderr: %s)",
				id, c.scanner.Err(), c.stderr.String())
		}
		var v map[string]any
		if err := json.Unmarshal(c.scanner.Bytes(), &v); err != nil {
			c.t.Fatalf("unmarshaling response %q: %v", c.scanner.Text(), err)
		}
		got, ok := v["id"].(float64)
		if !ok {
			continue // a notification, not our response
		}
		if int(got) == id {
			return v
		}
	}
}

// request issues one JSON-RPC call and fails the test on a protocol error.
func (c *mcpClient) request(method string, params any) map[string]any {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	resp := c.recvID(id)
	if resp["error"] != nil {
		c.t.Fatalf("%s returned a protocol error: %v (stderr: %s)", method, resp["error"], c.stderr.String())
	}
	return resp
}

func (c *mcpClient) initialize() {
	c.t.Helper()
	c.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e-test", "version": "0.0.0"},
	})
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// callTool invokes a tool and returns its result without asserting success,
// so callers can test error behavior too.
func (c *mcpClient) callTool(name string, args map[string]any) toolResult {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	resp := c.request("tools/call", map[string]any{"name": name, "arguments": args})

	result, ok := resp["result"].(map[string]any)
	if !ok {
		c.t.Fatalf("tools/call %s: result = %v, want an object", name, resp["result"])
	}
	out := toolResult{}
	out.isError, _ = result["isError"].(bool)
	out.structured, _ = result["structuredContent"].(map[string]any)
	if raw, err := json.Marshal(result["content"]); err == nil {
		out.content = string(raw)
	}
	return out
}

// mustCallTool invokes a tool and fails the test if it reports a tool error.
func (c *mcpClient) mustCallTool(name string, args map[string]any) map[string]any {
	c.t.Helper()
	res := c.callTool(name, args)
	if res.isError {
		c.t.Fatalf("%s returned a tool error: %s", name, res.content)
	}
	if res.structured == nil {
		c.t.Fatalf("%s returned no structuredContent: %s", name, res.content)
	}
	return res.structured
}

// items pulls the Items slice out of a ListResult-shaped structured result.
func items(t *testing.T, tool string, structured map[string]any) []map[string]any {
	t.Helper()
	if structured["items"] == nil && structured["count"] != nil {
		return nil // an empty list marshals items as null
	}
	return objects(t, tool, structured, "items")
}

// objects pulls an array of objects out of the named field of a structured
// result. Not every tool returns a ListResult — jenkins_get_view, for
// instance, returns a ViewDetail whose jobs live under "jobs".
func objects(t *testing.T, tool string, structured map[string]any, field string) []map[string]any {
	t.Helper()
	raw, ok := structured[field].([]any)
	if !ok {
		t.Fatalf("%s: structured result has no %s array: %v", tool, field, structured)
	}
	out := make([]map[string]any, 0, len(raw))
	for i, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("%s: %s[%d] = %v, want an object", tool, field, i, it)
		}
		out = append(out, m)
	}
	return out
}
