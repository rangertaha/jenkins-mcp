// SPDX-License-Identifier: GPL-3.0-or-later

package server

// ListResult wraps a collection of items. The MCP specification requires a
// tool's structured output to be a JSON object, so list-returning tools cannot
// return a bare array; they return a ListResult instead. The wrapper also gives
// the model an explicit item count.
type ListResult[T any] struct {
	Count int `json:"count" jsonschema:"number of items returned"`
	Items []T `json:"items" jsonschema:"the returned items"`
}

// List builds a ListResult from a slice, computing the count.
func List[T any](items []T) ListResult[T] {
	return ListResult[T]{Count: len(items), Items: items}
}
