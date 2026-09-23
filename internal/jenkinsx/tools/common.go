// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools registers the hand-written Jenkins MCP tools. Unlike the
// AWS predecessor's reflection-driven, dynamically-discovered operations,
// each tool here is a concrete Go type calling a concrete Jenkins REST
// endpoint, registered directly through server.Register.
package tools

import (
	"fmt"
	"strings"
)

// EmptyInput is used by tools that take no arguments.
type EmptyInput struct{}

// requireNonEmpty returns an error naming field unless value is non-blank.
// Every handler that builds a Jenkins REST path from a required string
// input (a job path, build identifier, or node/view name) must call this
// before constructing the request: url.JoinPath/PathEscape silently
// collapse an empty path segment into a shorter, still-valid path pointing
// at a different Jenkins resource instead of failing — e.g. an empty build
// collapses "/job/x//api/json" into "/job/x/api/json", the *job's* detail
// endpoint, not a 404 — so a missing check here surfaces as a confusing
// wrong-resource response instead of a clear input error. See
// validation_test.go for the class test covering every handler that needs
// this.
func requireNonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}
