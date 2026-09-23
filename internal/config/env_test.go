// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing test .env file: %v", err)
	}
	return path
}

func TestLoadEnvFileMissingIsNotAnError(t *testing.T) {
	if err := LoadEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env")); err != nil {
		t.Errorf("LoadEnvFile(missing) error = %v, want nil", err)
	}
}

func TestLoadEnvFileParsesKeyValuePairs(t *testing.T) {
	path := writeEnvFile(t, ""+
		"# a comment\n"+
		"\n"+
		"export FOO=bar\n"+
		"BAZ=\"quoted value\"\n"+
		"QUX='single quoted'\n"+
		"NOEQUALS\n"+
		"  \n")

	t.Setenv("FOO", "")
	t.Setenv("BAZ", "")
	t.Setenv("QUX", "")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}

	check := func(key, want string) {
		t.Helper()
		got := os.Getenv(key)
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	check("FOO", "bar")
	check("BAZ", "quoted value")
	check("QUX", "single quoted")
}

func TestLoadEnvFileExistingNonEmptyValueWins(t *testing.T) {
	path := writeEnvFile(t, "FOO=from-file\n")
	t.Setenv("FOO", "from-shell")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}
	if got := os.Getenv("FOO"); got != "from-shell" {
		t.Errorf("FOO = %q, want %q (real env should win over file)", got, "from-shell")
	}
}

func TestLoadEnvFileExistingEmptyValueIsOverridden(t *testing.T) {
	path := writeEnvFile(t, "FOO=from-file\n")
	t.Setenv("FOO", "")

	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile() error = %v", err)
	}
	if got := os.Getenv("FOO"); got != "from-file" {
		t.Errorf("FOO = %q, want %q (empty existing value should not block the file)", got, "from-file")
	}
}

// FuzzUnquote checks unquote never panics on arbitrary input (including
// multi-byte UTF-8 strings, where byte-indexing s[0]/s[len(s)-1] could in
// principle misbehave if a continuation byte ever collided with an ASCII
// quote byte: it cannot, since UTF-8 continuation and leading bytes are
// always at or above 0x80, well above the double- and single-quote byte
// values, but this is worth checking rather than assuming) and that its one
// documented invariant always holds: unquoting only ever removes at most
// the first and last byte, never more.
func FuzzUnquote(f *testing.F) {
	for _, seed := range []string{
		"", `"`, `'`, `""`, `''`, `"a"`, `'a'`, `"mismatched'`, `'mismatched"`,
		"😀", `"😀"`, "\x00", `"\x00"`, strings.Repeat(`"`, 1000),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := unquote(in)
		if len(got) < len(in)-2 {
			t.Fatalf("unquote(%q) = %q, removed more than the surrounding quote pair", in, got)
		}
	})
}

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"double"`:   "double",
		`'single'`:   "single",
		"unquoted":   "unquoted",
		`"`:          `"`,
		"":           "",
		`""`:         "",
		`"mixed'`:    `"mixed'`,
		`'mismatch"`: `'mismatch"`,
	}
	for in, want := range cases {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}
