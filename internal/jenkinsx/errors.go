// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"errors"
	"fmt"
	"net/http"
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
	if e.Body == "" {
		return fmt.Sprintf("jenkins: %s", e.Status)
	}
	return fmt.Sprintf("jenkins: %s: %s", e.Status, e.Body)
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
