// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectResourceClient starts s on an in-memory transport and returns a
// connected client session, so resource reads are exercised through the real
// MCP round trip (including the SDK's own URI-template routing) rather than
// by calling handlers directly.
func connectResourceClient(t *testing.T, s *Server) (*mcp.ClientSession, context.Context) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect: %v", err)
	}

	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-serverErrCh
	})
	return session, ctx
}

func TestResourceCountCountsBothKinds(t *testing.T) {
	s := New("test", "1.0.0", false)
	if got := s.ResourceCount(); got != 0 {
		t.Errorf("ResourceCount() = %d on a new server, want 0", got)
	}

	read := func(context.Context, string) (string, error) { return "", nil }
	s.AddResource(ResourceDef{URI: "test://static", Name: "static"}, read)
	s.AddResourceTemplate(ResourceDef{URI: "test://item/{id}", Name: "item"}, read)

	if got := s.ResourceCount(); got != 2 {
		t.Errorf("ResourceCount() = %d, want 2 (one resource + one template)", got)
	}
}

func TestAddResourceServesContents(t *testing.T) {
	s := New("test", "1.0.0", false)
	s.AddResource(ResourceDef{
		URI:      "test://static",
		Name:     "static",
		MIMEType: "text/plain",
	}, func(_ context.Context, uri string) (string, error) {
		return "body for " + uri, nil
	})

	session, ctx := connectResourceClient(t, s)
	res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://static"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("Contents = %d entries, want 1", len(res.Contents))
	}
	got := res.Contents[0]
	if got.Text != "body for test://static" {
		t.Errorf("Text = %q, want the read function's output", got.Text)
	}
	if got.URI != "test://static" {
		t.Errorf("URI = %q, want test://static", got.URI)
	}
	if got.MIMEType != "text/plain" {
		t.Errorf("MIMEType = %q, want text/plain", got.MIMEType)
	}
}

// TestAddResourceTemplateReservedExpansionMatchesSlashes pins the reason the
// Jenkins templates use "{+var}" rather than "{var}": a plain variable stops
// at a "/", so a nested job path would not route at all. Both forms are
// registered here so the difference is asserted, not assumed.
func TestAddResourceTemplateReservedExpansionMatchesSlashes(t *testing.T) {
	s := New("test", "1.0.0", false)

	echo := func(_ context.Context, uri string) (string, error) { return uri, nil }
	s.AddResourceTemplate(ResourceDef{URI: "test://plain/{path}/end", Name: "plain"}, echo)
	s.AddResourceTemplate(ResourceDef{URI: "test://reserved/{+path}/end", Name: "reserved"}, echo)

	session, ctx := connectResourceClient(t, s)

	cases := []struct {
		name      string
		uri       string
		wantMatch bool
	}{
		{"plain template, single segment", "test://plain/demo/end", true},
		{"plain template, nested path", "test://plain/team-a/service-b/end", false},
		{"reserved template, single segment", "test://reserved/demo/end", true},
		{"reserved template, nested path", "test://reserved/team-a/service-b/end", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: c.uri})
			switch {
			case c.wantMatch && err != nil:
				t.Errorf("ReadResource(%q) = %v, want it to route to the template", c.uri, err)
			case !c.wantMatch && err == nil:
				t.Errorf("ReadResource(%q) succeeded (%+v), want no template to match a slash-containing value",
					c.uri, res)
			}
		})
	}
}

// TestResourceHandlerPassesConcreteURI confirms a templated read receives the
// URI the client asked for, not the template string — the read function has
// to parse its own variables out of it.
func TestResourceHandlerPassesConcreteURI(t *testing.T) {
	s := New("test", "1.0.0", false)

	var seen string
	s.AddResourceTemplate(ResourceDef{URI: "test://item/{+id}", Name: "item"},
		func(_ context.Context, uri string) (string, error) {
			seen = uri
			return "ok", nil
		})

	session, ctx := connectResourceClient(t, s)
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://item/a/b"}); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if seen != "test://item/a/b" {
		t.Errorf("read func saw uri = %q, want the concrete URI test://item/a/b", seen)
	}
}

func TestResourceReadErrorIsReturned(t *testing.T) {
	s := New("test", "1.0.0", false)
	s.AddResource(ResourceDef{URI: "test://broken", Name: "broken"},
		func(context.Context, string) (string, error) {
			return "", errors.New("boom")
		})

	session, ctx := connectResourceClient(t, s)
	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://broken"}); err == nil {
		t.Error("ReadResource succeeded, want the read function's error")
	}
}

// TestResourceHandlerRecoversPanic proves a panicking read fails just that
// read instead of unwinding through the SDK's goroutine and killing the
// server: the session stays usable afterwards.
func TestResourceHandlerRecoversPanic(t *testing.T) {
	s := New("test", "1.0.0", false)
	s.AddResource(ResourceDef{URI: "test://panic", Name: "panicky"},
		func(context.Context, string) (string, error) {
			panic("kaboom")
		})
	s.AddResource(ResourceDef{URI: "test://fine", Name: "fine"},
		func(context.Context, string) (string, error) { return "still here", nil })

	session, ctx := connectResourceClient(t, s)

	if _, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://panic"}); err == nil {
		t.Error("ReadResource(panicking) succeeded, want a recovered-panic error")
	}

	res, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "test://fine"})
	if err != nil {
		t.Fatalf("server did not survive the panic: %v", err)
	}
	if res.Contents[0].Text != "still here" {
		t.Errorf("Text = %q, want the server still serving after a panic", res.Contents[0].Text)
	}
}

func TestListResourcesIncludesRegistered(t *testing.T) {
	s := New("test", "1.0.0", false)
	read := func(context.Context, string) (string, error) { return "", nil }
	s.AddResource(ResourceDef{
		URI:         "test://static",
		Name:        "static",
		Title:       "Static thing",
		Description: "a static resource",
		MIMEType:    "text/plain",
	}, read)

	session, ctx := connectResourceClient(t, s)
	res, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res.Resources) != 1 {
		t.Fatalf("ListResources returned %d resources, want 1", len(res.Resources))
	}
	got := res.Resources[0]
	if got.URI != "test://static" || got.Name != "static" || got.MIMEType != "text/plain" {
		t.Errorf("resource = %+v, want the registered definition", got)
	}
	if got.Description != "a static resource" || got.Title != "Static thing" {
		t.Errorf("resource description/title = %q/%q, want the registered values", got.Description, got.Title)
	}
}
