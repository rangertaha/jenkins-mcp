// SPDX-License-Identifier: GPL-3.0-or-later

// Package tools registers the hand-written Jenkins MCP tools. Unlike the
// AWS predecessor's reflection-driven, dynamically-discovered operations,
// each tool here is a concrete Go type calling a concrete Jenkins REST
// endpoint, registered directly through server.Register.
package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
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

// requireJobPath validates a job-path input. It is stricter than
// requireNonEmpty because a job path goes through JobPath, which drops
// empty segments: "/" and "//" are non-blank yet yield no segments at all,
// so the request loses its /job/<name> prefix and silently targets whatever
// sits at the base URL instead — jenkins_get_build{job:"/"} asked Jenkins
// for {base}/lastBuild/api/json.
func requireJobPath(field, value string) error {
	if err := requireNonEmpty(field, value); err != nil {
		return err
	}
	if !jenkinsx.HasJobSegments(value) {
		return fmt.Errorf("%s %q is not a valid job path: it contains no job name", field, value)
	}
	return nil
}

// Page size bounds. A Jenkins controller can host thousands of jobs and
// plugins, and an unbounded list would return all of them in a single tool
// result — enough to exhaust the model's context in one call. Every list
// tool therefore pages, defaulting to defaultPageLimit and refusing to go
// above maxPageLimit.
const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// PageResult is the output of every list tool. It replaces the plain
// server.ListResult so a truncated page is *discoverable*: HasMore tells
// the model another call is worthwhile, instead of leaving it to believe a
// capped list was the whole story.
type PageResult[T any] struct {
	Count   int  `json:"count" jsonschema:"number of items in this page"`
	Items   []T  `json:"items"`
	Offset  int  `json:"offset" jsonschema:"index of the first item in this page, echoing the requested offset"`
	HasMore bool `json:"hasMore" jsonschema:"true when more items exist past this page; call again with offset set to offset+count"`
}

// effectiveLimit clamps a requested page size into [1, maxPageLimit],
// treating the zero value (an omitted limit) as defaultPageLimit. The
// schema constrains this too, but a client is free to skip validation, so
// the handler must not rely on it.
func effectiveLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultPageLimit
	case limit > maxPageLimit:
		return maxPageLimit
	default:
		return limit
	}
}

// effectiveOffset floors a requested offset at 0.
func effectiveOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// treeRange returns the Jenkins `tree` range suffix selecting one page,
// e.g. "{0,51}" — Jenkins ranges are half-open, so this asks for limit+1
// elements. The extra element is a probe: if Jenkins returns it, there is
// at least one more item past this page, which is how HasMore is
// determined without a second round-trip or a separate count query (the
// API exposes no total). newPage trims it back off.
//
// Verified against Jenkins 2.568.3: `tree=jobs[name]{0,1}` returns exactly
// the first job, and the same suffix works on the queue, computer, views
// and plugin endpoints.
func treeRange(offset, limit int) string {
	return fmt.Sprintf("{%d,%d}", offset, offset+limit+1)
}

// newPage trims treeRange's probe element off items and reports whether it
// was present.
func newPage[T any](items []T, offset, limit int) PageResult[T] {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = []T{}
	}
	return PageResult[T]{Count: len(items), Items: items, Offset: offset, HasMore: hasMore}
}

// refinePaging applies the shared limit/offset constraints to a list tool's
// input schema. Inference can only say "integer"; these bounds are what let
// an MCP client reject limit=0 or offset=-1 before the call is made.
func refinePaging(s *jsonschema.Schema) {
	if p, ok := s.Properties["limit"]; ok {
		p.Minimum = ptr(1.0)
		p.Maximum = ptr(float64(maxPageLimit))
	}
	if p, ok := s.Properties["offset"]; ok {
		p.Minimum = ptr(0.0)
	}
}

// buildIDPattern matches the values Jenkins accepts as a build path
// segment: a build number, or one of its permalinks. Every permalink below
// was verified to resolve against Jenkins 2.568.3 — the pattern is
// deliberately no tighter than that, because an MCP client validates the
// call against this schema *before* it reaches the server, so an
// over-narrow pattern would reject legitimate builds at the client.
// buildIDPattern is derived from jenkinsx.BuildPermalinks rather than
// written out again, so the schema a client validates against cannot drift
// from the references the server actually accepts.
var buildIDPattern = `^([0-9]+|` + strings.Join(jenkinsx.BuildPermalinks, "|") + `)$`

// buildResultEnum is the closed set of values Jenkins reports for a
// finished build, plus "" which it reports while one is still running.
var buildResultEnum = []any{"SUCCESS", "FAILURE", "UNSTABLE", "ABORTED", "NOT_BUILT", ""}

// refineBuildID constrains a "build" property to buildIDPattern.
func refineBuildID(s *jsonschema.Schema) {
	if p, ok := s.Properties["build"]; ok {
		p.Pattern = buildIDPattern
	}
}

// refineRequiredString gives a required path-segment string a minLength of
// 1, so a client rejects a blank value before requireNonEmpty has to.
func refineRequiredString(s *jsonschema.Schema, names ...string) {
	for _, n := range names {
		if p, ok := s.Properties[n]; ok {
			p.MinLength = ptr(1)
		}
	}
}

// ptr returns a pointer to v, for the *float64/*int constraint fields on
// jsonschema.Schema.
func ptr[T any](v T) *T { return &v }

// maxTextBytes caps how much raw text (a config.xml, an artifact, a slice
// of console log) one tool result may carry. Jenkins will happily return a
// multi-megabyte console log or artifact, which would blow the model's
// context in a single call; tools that return text cut it here and say so
// via a truncated/hasMore flag rather than returning it silently shortened.
const maxTextBytes = 64 * 1024

// truncate cuts s to at most limit bytes, keeping the start, and reports
// whether it cut anything.
//
// The cut is on a byte boundary, which can split a multi-byte UTF-8 rune at
// the end; the trailing partial rune is dropped so the result doesn't end
// mid-character.
//
// It deliberately does NOT require the result to be valid UTF-8 overall. An
// earlier version looped `for !utf8.ValidString(cut) { cut = cut[:len-1] }`,
// which is a trap: validity is a property of the whole string, so a single
// invalid byte anywhere near the front makes every prefix invalid and the
// loop strips the string to empty. Console logs carry arbitrary bytes
// (binary output, latin-1) and a byte-offset seek can land mid-rune, so that
// case is routine, not exotic — and an empty result with a byte count of
// zero is what makes a paging caller loop forever on the same offset.
// Invalid bytes that survive here are replaced with U+FFFD by encoding/json
// on the way out, which is the right outcome.
func truncate(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	return dropPartialRuneSuffix(s[:limit]), true
}

// truncateTail cuts s to at most limit bytes, keeping the END rather than
// the start, and reports whether it cut anything.
//
// This is what a tail read needs: when a running build appends output
// between the size probe and the fetch, the fetched window overshoots
// limit, and keeping the start would discard the newest bytes — the exact
// ones the caller asked for. It also returns how many leading bytes it
// dropped, so the caller can report the offset its text actually begins at.
func truncateTail(s string, limit int) (text string, dropped int, truncated bool) {
	if len(s) <= limit {
		return dropLeadingRuneRemnant(s), leadingRemnantLen(s), false
	}
	cut := s[len(s)-limit:]
	n := leadingRemnantLen(cut)
	return cut[n:], len(s) - limit + n, true
}

// leadingRemnantLen returns the number of UTF-8 continuation bytes at the
// start of s — the tail of a rune whose leading byte fell before the cut.
// A byte-offset seek into a log has no idea where runes begin, so this is
// the normal case, not an error.
func leadingRemnantLen(s string) int {
	n := 0
	// A rune is at most 4 bytes, so at most 3 continuation bytes can precede
	// the first real boundary; stopping there avoids eating a legitimately
	// invalid byte run.
	for n < len(s) && n < 3 && s[n]&0xC0 == 0x80 {
		n++
	}
	return n
}

// dropLeadingRuneRemnant removes any partial rune at the start of s.
func dropLeadingRuneRemnant(s string) string { return s[leadingRemnantLen(s):] }

// dropPartialRuneSuffix removes an incomplete rune at the end of s, looking
// back at most the length of the longest UTF-8 sequence. It leaves other
// invalid bytes alone.
func dropPartialRuneSuffix(s string) string {
	for i := 0; i < utf8.UTFMax && i < len(s); i++ {
		end := len(s) - i
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if r != utf8.RuneError || size > 1 {
			return s[:end]
		}
		// A RuneError of size 1 at the end is either a genuinely invalid
		// byte or the start of a rune whose remaining bytes were cut. Only
		// the latter is worth dropping: check whether these trailing bytes
		// could begin a longer sequence.
		if !isRuneStart(s[end-1]) {
			continue
		}
		return s[:end-1]
	}
	return s
}

// isRuneStart reports whether b begins a multi-byte UTF-8 sequence.
func isRuneStart(b byte) bool { return b&0xC0 == 0xC0 }
