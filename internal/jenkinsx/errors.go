// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// StatusError is returned when Jenkins responds with an HTTP error status.
// Jenkins has no structured error protocol comparable to AWS's smithy
// APIError, so this carries the status code plus a truncated response body.
type StatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *StatusError) Error() string {
	body := summarizeBody(e.Body)
	if body == "" {
		return fmt.Sprintf("jenkins: %s", e.Status)
	}
	return fmt.Sprintf("jenkins: %s: %s", e.Status, body)
}

// htmlTitle extracts the contents of a <title> element.
var htmlTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// summarizeBody reduces a Jenkins error body to something worth showing.
//
// Jenkins answers most errors with a full HTML page, and that body is
// returned to an LLM as the tool's error text. Passing it through verbatim
// is bad three ways: it spends kilobytes of the model's context on markup,
// it buries the one useful token ("Not Found") somewhere in the middle, and
// — because Jenkins embeds a CSRF crumb in the page head as
// data-crumb-value — it copies a live session token into the conversation
// on every failed call.
//
// Non-HTML bodies are left alone: Jenkins' genuinely informative errors
// ("Nothing is submitted", a stack trace from a plugin) arrive as plain
// text and are exactly what the caller needs.
func summarizeBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if !looksLikeHTML(trimmed) {
		return trimmed
	}
	if m := htmlTitle.FindStringSubmatch(trimmed); len(m) == 2 {
		if title := strings.TrimSpace(m[1]); title != "" {
			return title
		}
	}
	// No title does not mean nothing useful. Jenkins' 403 page has no
	// <title> at all, and its actual diagnostic — "You are authenticated
	// as: anonymous ... Permission you need to have (but didn't):
	// hudson.model.Item.Read" — sits in an HTML COMMENT in the body. That
	// is the single most actionable error this server can surface, so
	// dropping it would throw away the answer and leave a bare
	// "403 Forbidden".
	return stripMarkup(trimmed)
}

// htmlTagOrComment matches a tag, or a comment's delimiters (keeping the
// comment's text, which is where Jenkins hides its permission diagnostic).
var (
	htmlDropped     = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlCommentEnds = regexp.MustCompile(`(?s)<!--|-->`)
	htmlTag         = regexp.MustCompile(`(?s)<[^>]*>`)
	whitespaceRun   = regexp.MustCompile(`\s+`)
)

// maxSummaryBytes caps a markup-stripped body, which is a diagnostic rather
// than content: enough to carry a permission name, not enough to spend the
// model's context on boilerplate.
const maxSummaryBytes = 300

// stripMarkup reduces an HTML body to its readable text. Tags are removed
// (which is also what keeps the CSRF crumb out — it lives in a tag
// attribute), comment delimiters are removed while their text is kept, and
// runs of whitespace collapse.
func stripMarkup(body string) string {
	out := htmlDropped.ReplaceAllString(body, " ")
	out = htmlCommentEnds.ReplaceAllString(out, " ")
	out = htmlTag.ReplaceAllString(out, " ")
	out = strings.TrimSpace(whitespaceRun.ReplaceAllString(out, " "))
	if len(out) > maxSummaryBytes {
		out = strings.TrimSpace(out[:maxSummaryBytes]) + "..."
	}
	return out
}

// looksLikeHTML reports whether body is an HTML document rather than the
// plain text Jenkins uses for its more informative errors.
func looksLikeHTML(body string) bool {
	head := body
	if len(head) > 512 {
		head = head[:512]
	}
	head = strings.ToLower(head)
	return strings.HasPrefix(head, "<!doctype html") ||
		strings.HasPrefix(head, "<html") ||
		strings.Contains(head, "<head")
}

// IsNotFound reports whether err is a StatusError with status 404.
func IsNotFound(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.StatusCode == http.StatusNotFound
	}
	return false
}

// IsUnauthorized reports whether err is a StatusError with status 401 or 403.
func IsUnauthorized(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.StatusCode == http.StatusUnauthorized || se.StatusCode == http.StatusForbidden
	}
	return false
}
