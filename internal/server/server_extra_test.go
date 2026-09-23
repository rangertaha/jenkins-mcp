// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestList(t *testing.T) {
	cases := []struct {
		name  string
		items []string
		want  int
	}{
		{"nil slice", nil, 0},
		{"empty slice", []string{}, 0},
		{"three items", []string{"a", "b", "c"}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := List(c.items)
			if got.Count != c.want {
				t.Errorf("Count = %d, want %d", got.Count, c.want)
			}
			if len(got.Items) != c.want {
				t.Errorf("len(Items) = %d, want %d", len(got.Items), c.want)
			}
		})
	}
}

// TestConnectServesSession covers Connect, the non-blocking counterpart to
// Run: it hands back the server session immediately rather than serving
// until the client disconnects.
func TestConnectServesSession(t *testing.T) {
	s := New("test", "1.0.0", false)
	Register(s, ToolDef{Name: "ping", Description: "pings"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, testOut, error) {
			return nil, testOut{Result: "pong"}, nil
		})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := s.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "ping" {
		t.Errorf("tools = %+v, want just \"ping\"", res.Tools)
	}
}

type cyclicIn struct {
	Name string    `json:"name"`
	Next *cyclicIn `json:"next,omitempty"`
}

// TestNormalizedSchemaFallsBackOnUnschematizableTypes covers the nil return
// that tells Register to fall back to the SDK's own schema generation. Both
// cases are real: jsonschema-go rejects types it can't express (channels,
// funcs) and self-referential types, which have no finite JSON Schema. A
// panic or a bogus schema here would break tool registration at startup
// rather than at call time.
func TestNormalizedSchemaFallsBackOnUnschematizableTypes(t *testing.T) {
	cases := map[string]reflect.Type{
		"channel":     reflect.TypeFor[chan int](),
		"func":        reflect.TypeFor[func()](),
		"cyclic type": reflect.TypeFor[cyclicIn](),
	}
	for name, typ := range cases {
		t.Run(name, func(t *testing.T) {
			if got := normalizedSchema(typ); got != nil {
				t.Errorf("normalizedSchema(%v) = %s, want nil so Register falls back", typ, got)
			}
		})
	}
}

// TestNormalizedSchemaDereferencesPointers confirms a pointer type schemas as
// the type it points at, since handlers are free to use either.
func TestNormalizedSchemaDereferencesPointers(t *testing.T) {
	direct := normalizedSchema(reflect.TypeFor[testOut]())
	viaPointer := normalizedSchema(reflect.TypeFor[*testOut]())

	if direct == nil || viaPointer == nil {
		t.Fatalf("normalizedSchema returned nil: direct=%s pointer=%s", direct, viaPointer)
	}
	if string(direct) != string(viaPointer) {
		t.Errorf("pointer schema = %s, want the same as the value schema %s", viaPointer, direct)
	}
}

func TestNormalizeSchemaNodePassesThroughScalars(t *testing.T) {
	for _, in := range []any{"a string", float64(3), nil} {
		if got := normalizeSchemaNode(in); got != in {
			t.Errorf("normalizeSchemaNode(%v) = %v, want it unchanged", in, got)
		}
	}
}
