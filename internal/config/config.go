// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads and validates runtime configuration for the
// jenkins-mcp server from environment variables.
//
// Unlike an AWS-style credential chain, Jenkins has no ambient credential
// discovery: the server URL and API token must be supplied explicitly, so
// Load fails fast when a required variable is missing.
package config

import (
	"fmt"
	"os"
	"strings"
)

// Environment variable names recognised by the server.
const (
	EnvURL      = "JENKINS_URL"      // required: base URL of the Jenkins controller
	EnvUser     = "JENKINS_USER"     // required: username for API token auth
	EnvToken    = "JENKINS_TOKEN"    // required: Jenkins API token
	EnvToolsets = "JENKINS_TOOLSETS" // comma-separated toolset names, or "all"
	EnvReadOnly = "JENKINS_READONLY" // "true" disables all write tools
)

// Config holds validated server configuration.
type Config struct {
	// URL is the base URL of the Jenkins controller, e.g. "https://ci.example.com".
	URL string
	// User is the Jenkins username paired with Token for HTTP Basic auth.
	User string
	// Token is the Jenkins API token (Jenkins user -> Configure -> API Token).
	Token string
	// Toolsets is the set of enabled toolset names. A nil/empty set means "all".
	Toolsets []string
	// ReadOnly, when true, suppresses mutating tools at registration time.
	ReadOnly bool
}

// AllToolsets reports whether every toolset should be enabled.
func (c *Config) AllToolsets() bool {
	if len(c.Toolsets) == 0 {
		return true
	}
	for _, t := range c.Toolsets {
		if t == "all" {
			return true
		}
	}
	return false
}

// ToolsetEnabled reports whether the named toolset should be registered.
func (c *Config) ToolsetEnabled(name string) bool {
	if c.AllToolsets() {
		return true
	}
	for _, t := range c.Toolsets {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// Load reads configuration from the process environment. JENKINS_URL,
// JENKINS_USER, and JENKINS_TOKEN are required; Load returns an error naming
// every missing one if any are absent.
func Load() (*Config, error) {
	cfg := &Config{
		URL:      strings.TrimRight(strings.TrimSpace(os.Getenv(EnvURL)), "/"),
		User:     strings.TrimSpace(os.Getenv(EnvUser)),
		Token:    strings.TrimSpace(os.Getenv(EnvToken)),
		Toolsets: splitList(os.Getenv(EnvToolsets)),
		ReadOnly: isTruthy(os.Getenv(EnvReadOnly)),
	}

	var missing []string
	if cfg.URL == "" {
		missing = append(missing, EnvURL)
	}
	if cfg.User == "" {
		missing = append(missing, EnvUser)
	}
	if cfg.Token == "" {
		missing = append(missing, EnvToken)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variable(s): %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// splitList parses a comma-separated environment value into a trimmed,
// lower-cased slice, dropping empty entries.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isTruthy reports whether an environment value represents boolean true.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
