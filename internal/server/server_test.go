// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testIn struct {
	Name string          `json:"name"`
	Raw  json.RawMessage `json:"raw,omitempty"`
	Any  any             `json:"any,omitempty"`
}

type testOut struct {
	Result string `json:"result"`
}

func TestNewServer(t *testing.T) {
	s := New("test", "1.0.0", true)
	if !s.ReadOnly() {
		t.Error("ReadOnly() = false, want true")
	}
	if s.ToolCount() != 0 {
		t.Errorf("ToolCount() = %d, want 0", s.ToolCount())
	}
	if s.PromptCount() != 0 {
		t.Errorf("PromptCount() = %d, want 0", s.PromptCount())
	}
	if len(s.Toolsets()) != 0 {
		t.Errorf("Toolsets() = %v, want empty", s.Toolsets())
	}
}

func TestNoteToolset(t *testing.T) {
	s := New("test", "1.0.0", false)
	s.NoteToolset("s3")
	s.NoteToolset("ec2")
	if got := s.Toolsets(); len(got) != 2 || got[0] != "s3" || got[1] != "ec2" {
		t.Errorf("Toolsets() = %v, want [s3 ec2] in insertion order", got)
	}
}

func dummyHandler(context.Context, *mcp.CallToolRequest, testIn) (*mcp.CallToolResult, testOut, error) {
	return nil, testOut{Result: "ok"}, nil
}

func TestRegisterSkipsWriteToolsWhenReadOnly(t *testing.T) {
	s := New("test", "1.0.0", true)
	Register(s, ToolDef{Name: "write_thing", Write: true}, dummyHandler)
	if s.ToolCount() != 0 {
		t.Errorf("ToolCount() = %d, want 0 (write tool must be skipped in read-only mode)", s.ToolCount())
	}
}

func TestRegisterKeepsWriteToolsWhenNotReadOnly(t *testing.T) {
	s := New("test", "1.0.0", false)
	Register(s, ToolDef{Name: "write_thing", Write: true}, dummyHandler)
	if s.ToolCount() != 1 {
		t.Errorf("ToolCount() = %d, want 1", s.ToolCount())
	}
}

func TestRegisterCountsReadOnlyToolsRegardlessOfServerMode(t *testing.T) {
	for _, readOnly := range []bool{true, false} {
		s := New("test", "1.0.0", readOnly)
		Register(s, ToolDef{Name: "read_thing", Write: false}, dummyHandler)
		if s.ToolCount() != 1 {
			t.Errorf("readOnly=%v: ToolCount() = %d, want 1", readOnly, s.ToolCount())
		}
	}
}

func TestAddPromptCountsPrompts(t *testing.T) {
	s := New("test", "1.0.0", false)
	s.AddPrompt("greet", "says hello", nil, func(map[string]string) string { return "hi" })
	if s.PromptCount() != 1 {
		t.Errorf("PromptCount() = %d, want 1", s.PromptCount())
	}
}

// TestToolAndPromptEndToEnd drives Register and AddPrompt through a real,
// in-memory MCP client/server connection (rather than only checking internal
// counters), confirming the tool actually answers CallTool and the prompt's
// render function actually receives the caller-supplied arguments.
func TestToolAndPromptEndToEnd(t *testing.T) {
	s := New("test", "1.0.0", false)
	Register(s, ToolDef{Name: "echo", Description: "echoes name"},
		func(_ context.Context, _ *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
			return nil, testOut{Result: "echo:" + in.Name}, nil
		})

	var gotArgs map[string]string
	s.AddPrompt("greet", "greets someone", []PromptArg{{Name: "who", Required: true}},
		func(args map[string]string) string {
			gotArgs = args
			return "hello, " + args["who"]
		})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	toolRes, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("CallTool(echo): %v", err)
	}
	if toolRes.IsError {
		t.Fatalf("CallTool(echo) returned an error result: %+v", toolRes.Content)
	}
	raw, err := json.Marshal(toolRes.StructuredContent)
	if err != nil {
		t.Fatalf("marshaling structured content: %v", err)
	}
	var out testOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding structured content: %v", err)
	}
	if out.Result != "echo:world" {
		t.Errorf("tool result = %+v, want Result=echo:world", out)
	}

	promptRes, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "greet",
		Arguments: map[string]string{"who": "Ada"},
	})
	if err != nil {
		t.Fatalf("GetPrompt(greet): %v", err)
	}
	if gotArgs["who"] != "Ada" {
		t.Errorf("render func saw args = %v, want who=Ada", gotArgs)
	}
	if len(promptRes.Messages) != 1 {
		t.Fatalf("GetPrompt(greet) returned %d messages, want 1", len(promptRes.Messages))
	}
	text, ok := promptRes.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("prompt message content type = %T, want *mcp.TextContent", promptRes.Messages[0].Content)
	}
	if text.Text != "hello, Ada" {
		t.Errorf("prompt text = %q, want %q", text.Text, "hello, Ada")
	}

	cancel()
	<-serverErrCh
}

// TestRegisterRecoversPanickingHandler proves the server survives a
// handler panic instead of the whole process crashing: every tool handler
// decodes server-controlled JSON into its own struct, and neither
// jenkins-mcp nor the vendored MCP SDK recovers panics anywhere else in the
// call path, so Register's wrapping is the only backstop. Drives a real in-memory
// CallTool round trip (not a direct function call) so a regression that
// only protects the direct-call path, but not whatever the MCP SDK does
// around it, would still be caught.
func TestRegisterRecoversPanickingHandler(t *testing.T) {
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origOut) })

	s := New("test", "1.0.0", false)
	Register(s, ToolDef{Name: "boom", Description: "always panics"},
		func(context.Context, *mcp.CallToolRequest, testIn) (*mcp.CallToolResult, testOut, error) {
			panic("simulated handler panic")
		})
	// recover(x) may be any type, not just a string — a panic with an
	// error value (a common real pattern, e.g. panic(fmt.Errorf(...))) or
	// panic(nil) must be handled just as cleanly. Since Go 1.21, panic(nil)
	// is not special-cased into a no-op: recover() returns a non-nil
	// *runtime.PanicNilError, so the existing "r != nil" check in
	// recoverPanics already catches it correctly — this pins that down
	// rather than assuming it.
	Register(s, ToolDef{Name: "boom-error", Description: "panics with an error value"},
		func(context.Context, *mcp.CallToolRequest, testIn) (*mcp.CallToolResult, testOut, error) {
			panic(errors.New("simulated error-typed panic"))
		})
	Register(s, ToolDef{Name: "boom-nil", Description: "panics with nil"},
		func(context.Context, *mcp.CallToolRequest, testIn) (*mcp.CallToolResult, testOut, error) {
			panic(nil)
		})
	Register(s, ToolDef{Name: "echo", Description: "echoes name"},
		func(_ context.Context, _ *mcp.CallToolRequest, in testIn) (*mcp.CallToolResult, testOut, error) {
			return nil, testOut{Result: "echo:" + in.Name}, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	boomRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "boom", Arguments: map[string]any{"name": "x"}})
	if err != nil {
		t.Fatalf("CallTool(boom): transport/protocol error (server likely crashed): %v", err)
	}
	if !boomRes.IsError {
		t.Fatalf("CallTool(boom) = %+v, want an error result", boomRes)
	}
	if logged := logBuf.String(); !strings.Contains(logged, "boom") || !strings.Contains(logged, "simulated handler panic") {
		t.Errorf("recovered panic not logged for operator visibility; log output = %q", logged)
	}

	boomErrRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "boom-error", Arguments: map[string]any{"name": "x"}})
	if err != nil {
		t.Fatalf("CallTool(boom-error): transport/protocol error (server likely crashed): %v", err)
	}
	if !boomErrRes.IsError {
		t.Fatalf("CallTool(boom-error) = %+v, want an error result", boomErrRes)
	}
	if logged := logBuf.String(); !strings.Contains(logged, "simulated error-typed panic") {
		t.Errorf("recovered error-typed panic not logged sensibly; log output = %q", logged)
	}

	boomNilRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "boom-nil", Arguments: map[string]any{"name": "x"}})
	if err != nil {
		t.Fatalf("CallTool(boom-nil): transport/protocol error (server likely crashed): %v", err)
	}
	if !boomNilRes.IsError {
		t.Fatalf("CallTool(boom-nil) = %+v, want an error result (panic(nil) must still be recovered as an error, not silently swallowed)", boomNilRes)
	}

	// The server must still be alive and answer a normal call afterward —
	// a panic in one call must not have taken the whole session down.
	echoRes, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"name": "world"}})
	if err != nil {
		t.Fatalf("CallTool(echo) after a prior panic: %v", err)
	}
	if echoRes.IsError {
		t.Fatalf("CallTool(echo) after a prior panic returned an error result: %+v", echoRes.Content)
	}

	cancel()
	<-serverErrCh
}

// TestAddPromptRecoversPanickingRender is the prompt-side counterpart to
// TestRegisterRecoversPanickingHandler: a panicking render function must not
// crash the server either, since AddPrompt's request handler runs through
// the same unrecovered MCP SDK call path as tool handlers do.
func TestAddPromptRecoversPanickingRender(t *testing.T) {
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(origOut) })

	s := New("test", "1.0.0", false)
	s.AddPrompt("boom", "always panics", nil, func(map[string]string) string {
		panic("simulated render panic")
	})
	// Same non-string/nil coverage as TestRegisterRecoversPanickingHandler:
	// AddPrompt's recover has its own independent "r != nil" check (see
	// prompt.go), so it needs the same pinning rather than assuming the
	// tool-side test's coverage carries over.
	s.AddPrompt("boom-error", "panics with an error value", nil, func(map[string]string) string {
		panic(errors.New("simulated error-typed render panic"))
	})
	s.AddPrompt("boom-nil", "panics with nil", nil, func(map[string]string) string {
		panic(nil)
	})
	s.AddPrompt("greet", "greets someone", []PromptArg{{Name: "who"}}, func(args map[string]string) string {
		return "hello, " + args["who"]
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "boom"})
	if err == nil {
		t.Fatal("GetPrompt(boom): expected an error, not a successful response, from a panicking render")
	}
	if logged := logBuf.String(); !strings.Contains(logged, "boom") || !strings.Contains(logged, "simulated render panic") {
		t.Errorf("recovered panic not logged for operator visibility; log output = %q", logged)
	}

	if _, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "boom-error"}); err == nil {
		t.Fatal("GetPrompt(boom-error): expected an error from a render panicking with a non-string (error) value")
	}
	if logged := logBuf.String(); !strings.Contains(logged, "simulated error-typed render panic") {
		t.Errorf("recovered error-typed panic not logged sensibly; log output = %q", logged)
	}

	if _, err = session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "boom-nil"}); err == nil {
		t.Fatal("GetPrompt(boom-nil): expected an error from a render calling panic(nil), not a successful response")
	}

	// The server must still be alive and answer a normal prompt afterward.
	greetRes, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "greet", Arguments: map[string]string{"who": "Ada"}})
	if err != nil {
		t.Fatalf("GetPrompt(greet) after a prior panic: %v", err)
	}
	text, ok := greetRes.Messages[0].Content.(*mcp.TextContent)
	if !ok || text.Text != "hello, Ada" {
		t.Fatalf("GetPrompt(greet) after a prior panic = %+v, want text %q", greetRes, "hello, Ada")
	}

	cancel()
	<-serverErrCh
}

func TestNormalizedSchemaRawMessageIsUnconstrained(t *testing.T) {
	raw := normalizedSchema(reflect.TypeFor[testIn]())
	if raw == nil {
		t.Fatal("normalizedSchema(testIn) = nil")
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshaling schema: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %v", schema)
	}
	rawField, ok := props["raw"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no \"raw\" property: %v", props)
	}
	// Pin the exact expected schema (an empty object, "accept any JSON
	// value"), not just the absence of the old broken byte-array shape:
	// a check that only rules out "items" would pass just as well for any
	// other wrong-but-not-a-byte-array schema (e.g. {"type":"string"}),
	// without actually confirming the override engaged as intended.
	if len(rawField) != 0 {
		t.Errorf("raw (json.RawMessage) property schema = %v, want {} (accept any JSON value)", rawField)
	}
}

func TestNormalizeSchemaNodeBooleanRewrite(t *testing.T) {
	in := map[string]any{
		"a": true,
		"b": false,
		"c": []any{true, false},
	}
	out := normalizeSchemaNode(in).(map[string]any)
	if _, ok := out["a"].(map[string]any); !ok {
		t.Errorf("true not rewritten to {}: %v", out["a"])
	}
	bNode, ok := out["b"].(map[string]any)
	if !ok {
		t.Fatalf("false not rewritten to an object: %v", out["b"])
	}
	if _, ok := bNode["not"]; !ok {
		t.Errorf(`false should rewrite to {"not":{}}, got %v`, bNode)
	}
	cSlice, ok := out["c"].([]any)
	if !ok || len(cSlice) != 2 {
		t.Fatalf("slice not preserved: %v", out["c"])
	}
}
