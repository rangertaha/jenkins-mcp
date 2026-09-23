// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromptArg describes a single argument accepted by a prompt.
type PromptArg struct {
	Name        string
	Description string
	Required    bool
}

// PromptCount returns the number of prompts registered so far.
func (s *Server) PromptCount() int { return s.prompts }

// AddPrompt registers a prompt (a user-invoked, parameterized template that
// clients surface as a slash command). The render function builds the prompt
// text from the supplied argument values; the result is returned to the client
// as a single user message that guides the model through a multi-step flow.
func (s *Server) AddPrompt(name, description string, args []PromptArg, render func(args map[string]string) string) {
	margs := make([]*mcp.PromptArgument, 0, len(args))
	for _, a := range args {
		margs = append(margs, &mcp.PromptArgument{
			Name:        a.Name,
			Description: a.Description,
			Required:    a.Required,
		})
	}
	p := &mcp.Prompt{Name: name, Description: description, Arguments: margs}

	s.mcp.AddPrompt(p, func(_ context.Context, req *mcp.GetPromptRequest) (result *mcp.GetPromptResult, err error) {
		// Same reasoning as recoverPanics in server.go: a panic in render
		// would otherwise unwind uncaught through the MCP SDK's own
		// request-handling goroutine and crash the whole server, not just
		// fail this one prompt request. No render function panics today
		// (Go map indexing safely zero-values on a missing key), but
		// AddPrompt is a general API future prompts use too.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("jenkins-mcp: recovered panic in prompt %q: %v", name, r)
				result, err = nil, fmt.Errorf("panic: %v", r)
			}
		}()

		var values map[string]string
		if req != nil && req.Params != nil {
			values = req.Params.Arguments
		}
		return &mcp.GetPromptResult{
			Description: description,
			Messages: []*mcp.PromptMessage{
				{Role: "user", Content: &mcp.TextContent{Text: render(values)}},
			},
		}, nil
	})
	s.prompts++
}
