// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"errors"
	"testing"
)

func TestStatusErrorMessage(t *testing.T) {
	err := &StatusError{StatusCode: 404, Status: "404 Not Found", Body: "job not found"}
	want := "jenkins: 404 Not Found: job not found"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	empty := &StatusError{StatusCode: 500, Status: "500 Internal Server Error"}
	if got := empty.Error(); got != "jenkins: 500 Internal Server Error" {
		t.Errorf("Error() with empty body = %q", got)
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&StatusError{StatusCode: 404}) {
		t.Error("IsNotFound(404) = false, want true")
	}
	if IsNotFound(&StatusError{StatusCode: 403}) {
		t.Error("IsNotFound(403) = true, want false")
	}
	if IsNotFound(errors.New("some other error")) {
		t.Error("IsNotFound(non-StatusError) = true, want false")
	}
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true, want false")
	}
}

func TestIsUnauthorized(t *testing.T) {
	if !IsUnauthorized(&StatusError{StatusCode: 401}) {
		t.Error("IsUnauthorized(401) = false, want true")
	}
	if !IsUnauthorized(&StatusError{StatusCode: 403}) {
		t.Error("IsUnauthorized(403) = false, want true")
	}
	if IsUnauthorized(&StatusError{StatusCode: 404}) {
		t.Error("IsUnauthorized(404) = true, want false")
	}
	if IsUnauthorized(errors.New("some other error")) {
		t.Error("IsUnauthorized(non-StatusError) = true, want false")
	}
}
