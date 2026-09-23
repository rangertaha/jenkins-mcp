// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ResourceDef describes a resource to register. For AddResource, URI is a
// concrete URI ("jenkins://info"); for AddResourceTemplate it is an RFC 6570
// URI template ("jenkins://job/{+path}/config.xml").
type ResourceDef struct {
	// URI is the resource's URI, or its URI template for a templated
	// resource. It must be absolute (have a scheme) — the MCP SDK panics on
	// a relative URI or an unparseable template.
	URI string
	// Name is the programmatic identifier clients list the resource under.
	Name string
	// Title is an optional human-readable display name.
	Title string
	// Description tells the model what the resource holds and when reading
	// it is useful.
	Description string
	// MIMEType is the content type of the resource body, e.g. "text/plain".
	MIMEType string
}

// ReadFunc returns the contents of the resource identified by uri. For a
// templated resource, uri is the concrete URI the client asked for (the MCP
// SDK routes by matching it against the template but does not extract the
// variables), so the read function parses out whatever it needs.
//
// A read that cannot find its target should return mcp.ResourceNotFoundError
// so the client sees a protocol-level "not found" rather than a generic
// failure.
type ReadFunc func(ctx context.Context, uri string) (string, error)

// ResourceCount returns the number of resources and resource templates
// registered so far.
func (s *Server) ResourceCount() int { return s.resources }

// AddResource registers a fixed-URI resource: a document the model can read
// as context, as opposed to a tool, which performs an action. The read
// function supplies the body; callers never touch the MCP content plumbing.
func (s *Server) AddResource(def ResourceDef, read ReadFunc) {
	s.mcp.AddResource(&mcp.Resource{
		URI:         def.URI,
		Name:        def.Name,
		Title:       def.Title,
		Description: def.Description,
		MIMEType:    def.MIMEType,
	}, s.resourceHandler(def, read))
	s.resources++
}

// AddResourceTemplate registers a templated resource, whose URI carries
// parameters (a job path, a build number). The SDK matches an incoming URI
// against the template to pick this handler; extracting the parameters from
// the concrete URI is the read function's job.
//
// Note that an RFC 6570 "{var}" does not match "/", so a template covering a
// Jenkins job path — which contains slashes once folders are involved — must
// use the reserved-expansion form "{+var}" instead.
func (s *Server) AddResourceTemplate(def ResourceDef, read ReadFunc) {
	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: def.URI,
		Name:        def.Name,
		Title:       def.Title,
		Description: def.Description,
		MIMEType:    def.MIMEType,
	}, s.resourceHandler(def, read))
	s.resources++
}

// resourceHandler adapts a ReadFunc to the SDK's handler signature and
// guards it the same way tool and prompt handlers are guarded: a panic in a
// read would otherwise unwind uncaught through the SDK's request-handling
// goroutine and take down the whole server, not just fail this one read.
// Resource reads parse client-supplied URIs and decode Jenkins-controlled
// responses, so this is the same class of risk Register already covers.
func (s *Server) resourceHandler(def ResourceDef, read ReadFunc) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (result *mcp.ReadResourceResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("jenkins-mcp: recovered panic in resource %q: %v", def.Name, r)
				result, err = nil, fmt.Errorf("panic: %v", r)
			}
		}()

		// Fall back to the definition's URI so a static resource still reads
		// correctly if a client omits params; a template has nothing useful
		// to fall back to, but its read function rejects the raw template
		// string as unparseable.
		uri := def.URI
		if req != nil && req.Params != nil && req.Params.URI != "" {
			uri = req.Params.URI
		}

		text, err := read(ctx, uri)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: def.MIMEType,
				Text:     text,
			}},
		}, nil
	}
}
