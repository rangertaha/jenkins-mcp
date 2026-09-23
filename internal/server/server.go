// SPDX-License-Identifier: GPL-3.0-or-later

// Package server wires the Model Context Protocol server together: it owns the
// underlying mcp.Server, enforces the read-only policy, and exposes typed
// helpers that the per-area tool packages use to register tools.
//
// Tools are registered through the generic Register function, which derives the
// JSON input/output schema from Go types, attaches MCP annotation hints, and
// transparently skips mutating tools when the server runs in read-only mode.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is the jenkins-mcp application server.
type Server struct {
	mcp      *mcp.Server
	readOnly bool

	// registered counts the tools actually exposed (after read-only filtering).
	registered int
	// prompts counts the registered prompts.
	prompts int
	// toolsets records the names of registered toolsets, in order.
	toolsets []string
}

// New creates a Server with the given name/version and read-only policy.
func New(name, version string, readOnly bool) *Server {
	impl := &mcp.Implementation{Name: name, Version: version}
	return &Server{
		mcp:      mcp.NewServer(impl, nil),
		readOnly: readOnly,
	}
}

// ReadOnly reports whether mutating tools are suppressed.
func (s *Server) ReadOnly() bool { return s.readOnly }

// ToolCount returns the number of tools registered so far.
func (s *Server) ToolCount() int { return s.registered }

// Toolsets returns the names of registered toolsets.
func (s *Server) Toolsets() []string { return s.toolsets }

// NoteToolset records that a toolset is being registered. Call once per area.
func (s *Server) NoteToolset(name string) { s.toolsets = append(s.toolsets, name) }

// Run serves the MCP protocol over the given transport until the context is
// cancelled or the client disconnects.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	return s.mcp.Run(ctx, t)
}

// Connect serves the MCP protocol over the given transport, returning the
// session. Unlike Run it does not block; it is primarily used by tests that
// drive the server in-process via an in-memory transport.
func (s *Server) Connect(ctx context.Context, t mcp.Transport) (*mcp.ServerSession, error) {
	return s.mcp.Connect(ctx, t, nil)
}

// ToolDef describes a tool to register. The zero value is a read-only,
// non-destructive tool.
type ToolDef struct {
	// Name is the unique tool identifier, e.g. "wit_get_work_item".
	Name string
	// Title is an optional human-readable display name.
	Title string
	// Description tells the model what the tool does and when to use it.
	Description string
	// Write marks the tool as mutating. Write tools are skipped entirely when
	// the server is in read-only mode and are annotated readOnlyHint=false.
	Write bool
	// Destructive hints that the tool may delete or overwrite data (e.g. delete
	// a work item). Only meaningful for write tools.
	Destructive bool
	// Idempotent hints that repeating the call with identical arguments has no
	// further effect. Only meaningful for write tools.
	Idempotent bool
}

// Register adds a typed tool to the server. In and Out are arbitrary structs:
// their JSON schemas are inferred automatically, inputs are validated against
// the schema before the handler runs, and outputs are returned as structured
// content. Mutating tools (def.Write) are silently skipped in read-only mode.
//
// The handler should return business results as the Out value; transport- and
// API-level failures should be returned as the error, which Register surfaces
// to the client as a tool error rather than a protocol error.
func Register[In, Out any](s *Server, def ToolDef, h mcp.ToolHandlerFor[In, Out]) {
	if def.Write && s.readOnly {
		return
	}

	annotations := &mcp.ToolAnnotations{
		Title:          def.Title,
		ReadOnlyHint:   !def.Write,
		IdempotentHint: def.Idempotent,
	}
	if def.Write {
		destructive := def.Destructive
		annotations.DestructiveHint = &destructive
	}

	tool := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		Annotations: annotations,
	}
	// Pre-generate normalized schemas. The Go schema inference emits boolean
	// subschemas (`true`) for interface{} fields, which some MCP clients (e.g.
	// Claude Code) reject during tool-list validation. normalizedSchema rewrites
	// those to the equivalent object form so the tool list is universally
	// accepted. A nil result falls back to the SDK's own generation.
	if sc := normalizedSchema(reflect.TypeFor[In]()); sc != nil {
		tool.InputSchema = sc
	}
	if sc := normalizedSchema(reflect.TypeFor[Out]()); sc != nil {
		tool.OutputSchema = sc
	}
	mcp.AddTool(s.mcp, tool, recoverPanics(def.Name, h))
	s.registered++
}

// recoverPanics wraps a tool handler so a panic anywhere in its execution
// becomes a clean tool error instead of unwinding the goroutine — which,
// uncaught, would crash the whole MCP server and every other in-flight or
// future request along with it. Every tool call passes through this one
// choke point, so it's the right place for a single blanket guard rather
// than scattering recover() at each call site that happens to decode
// server-controlled JSON today — and it covers any handler added later too.
//
// Unlike ordinary tool errors (an expected Jenkins API failure, a bad input —
// returned to the client and otherwise left unlogged, matching this
// codebase's existing convention), a panic means a genuine bug in jenkins-mcp
// or the Jenkins client, not routine API behavior. It's logged to stderr (name is set
// up by app.Assemble) so an operator running the server long-term has any
// visibility into it at all — otherwise a recovered panic would be
// completely invisible server-side, surfacing only if a user happened to
// report the one-off error text they saw.
func recoverPanics[In, Out any](name string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (result *mcp.CallToolResult, out Out, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("jenkins-mcp: recovered panic in tool %q: %v", name, r)
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		return h(ctx, req, in)
	}
}

// rawMessageType is encoding/json.RawMessage, a []byte. Left to the default
// inference it schemas as an array of 0-255 integers (a byte array), when a
// RawMessage field actually holds arbitrary embedded JSON; schemaOptions
// overrides it to an unconstrained schema ("accept any JSON value") instead.
var rawMessageType = reflect.TypeFor[json.RawMessage]()

// schemaOptions is shared by every Register call so every json.RawMessage
// field anywhere in a tool's In/Out struct gets the same override.
var schemaOptions = &jsonschema.ForOptions{
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		rawMessageType: {},
	},
}

// normalizedSchema generates the JSON schema for t (dereferencing pointers) and
// rewrites any boolean subschemas into their object equivalent. It returns nil
// on any error, signaling the caller to fall back to the SDK's own generation.
func normalizedSchema(t reflect.Type) json.RawMessage {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	s, err := jsonschema.ForType(t, schemaOptions)
	if err != nil {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil
	}
	out, err := json.Marshal(normalizeSchemaNode(node))
	if err != nil {
		return nil
	}
	return out
}

// normalizeSchemaNode recursively replaces boolean schemas with object form:
// true -> {} (accept anything), false -> {"not": {}} (accept nothing).
func normalizeSchemaNode(v any) any {
	switch n := v.(type) {
	case bool:
		if n {
			return map[string]any{}
		}
		return map[string]any{"not": map[string]any{}}
	case map[string]any:
		for k, child := range n {
			n[k] = normalizeSchemaNode(child)
		}
		return n
	case []any:
		for i, child := range n {
			n[i] = normalizeSchemaNode(child)
		}
		return n
	default:
		return v
	}
}
