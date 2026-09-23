// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools registers the hand-written Jenkins MCP tools. Unlike the
// AWS predecessor's reflection-driven, dynamically-discovered operations,
// each tool here is a concrete Go type calling a concrete Jenkins REST
// endpoint, registered directly through server.Register.
package tools

// EmptyInput is used by tools that take no arguments.
type EmptyInput struct{}
