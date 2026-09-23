// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"bufio"
	"os"
	"strings"
)

// EnvFile is the default dotenv filename loaded for local development.
const EnvFile = ".env"

// LoadEnvFile loads KEY=VALUE pairs from a dotenv file into the process
// environment. It is intended for local development: run `cp .env.example .env`,
// fill it in, and the server picks it up on startup.
//
// Lines may be blank, comments (#...), or KEY=VALUE (an optional leading
// "export " is ignored, and surrounding single/double quotes are stripped from
// the value). A variable that already has a non-empty value in the environment
// is left untouched, so real shell variables take precedence over the file. A
// missing file is not an error.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if cur, ok := os.LookupEnv(key); ok && cur != "" {
			continue // existing non-empty value wins
		}
		if err := os.Setenv(key, unquote(strings.TrimSpace(val))); err != nil {
			return err
		}
	}
	return sc.Err()
}

// unquote strips a single matching pair of surrounding single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
