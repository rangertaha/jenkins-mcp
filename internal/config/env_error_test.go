// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadEnvFileUnreadableIsAnError checks an unreadable .env is reported
// rather than silently ignored. Only a *missing* file is benign: if the file
// is there but can't be read, the server would otherwise start with none of
// its configuration and blame the user for not setting it.
//
// The two cases fail at different points, so both are worth pinning: a
// directory opens successfully and fails when read, while an invalid path
// fails at open. Neither depends on file permissions, which would behave
// differently when the tests run as root.
func TestLoadEnvFileUnreadableIsAnError(t *testing.T) {
	t.Run("directory fails on read", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".env")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("creating directory: %v", err)
		}
		if err := LoadEnvFile(dir); err == nil {
			t.Error("LoadEnvFile(directory) error = nil, want an error")
		}
	})

	t.Run("invalid path fails on open", func(t *testing.T) {
		// A NUL byte makes the path invalid at the syscall layer: the error
		// is EINVAL, not "not exist", so it must not be swallowed the way a
		// missing file is.
		if err := LoadEnvFile("invalid\x00path/.env"); err == nil {
			t.Error("LoadEnvFile(invalid path) error = nil, want an error")
		}
	})
}

func TestLoadEnvFileSkipsEmptyKeys(t *testing.T) {
	path := writeEnvFile(t, "=orphaned-value\n   =also-orphaned\nGOOD=value\n")

	t.Setenv("GOOD", "")
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}
	if got := os.Getenv("GOOD"); got != "value" {
		t.Errorf("GOOD = %q, want %q (a nameless line must not stop parsing)", got, "value")
	}
}

// TestLoadEnvFileReportsOversizedLine covers the scanner error path: a line
// past bufio.Scanner's token limit is reported instead of silently
// truncating the file's remaining variables.
func TestLoadEnvFileReportsOversizedLine(t *testing.T) {
	path := writeEnvFile(t, "HUGE="+strings.Repeat("x", 128*1024)+"\nAFTER=value\n")

	t.Setenv("AFTER", "")
	if err := LoadEnvFile(path); err == nil {
		t.Error("LoadEnvFile() with an oversized line error = nil, want the scanner error reported")
	}
}

// TestLoadEnvFileReportsSetenvFailure covers the os.Setenv error path. A key
// containing a NUL byte is rejected by the OS, and passing that failure up
// is what stops a malformed .env from half-applying.
func TestLoadEnvFileReportsSetenvFailure(t *testing.T) {
	path := writeEnvFile(t, "BAD\x00KEY=value\n")

	if err := LoadEnvFile(path); err == nil {
		t.Error("LoadEnvFile() with a NUL in a key error = nil, want the Setenv failure reported")
	}
}
