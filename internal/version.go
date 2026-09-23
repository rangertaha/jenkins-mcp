// SPDX-License-Identifier: GPL-3.0-or-later

// Package internal holds build-wide values shared across jenkins-mcp, such as
// the server version reported to MCP clients.
package internal

import "runtime/debug"

// version is injected for release builds via:
//
//	-ldflags "-X github.com/rangertaha/jenkins-mcp/internal.version=v1.2.3"
//
// When empty (the common case for `go install` and source builds), Version
// derives a value from the build info instead.
var version string

// Version returns the server version, resolved in order of precedence:
//
//  1. the value injected at build time with -ldflags (release builds);
//  2. the module version from the build info, e.g. when installed with
//     `go install github.com/rangertaha/jenkins-mcp/cmd/jenkins@v1.2.3`;
//  3. a "dev" value annotated with the VCS revision when building from source.
func Version() string {
	bi, ok := debug.ReadBuildInfo()
	return resolveVersion(version, bi, ok)
}

// resolveVersion holds Version's precedence logic, separated from the
// process-wide debug.ReadBuildInfo() call so every branch is reachable from a
// test with synthetic build info. injected is the -ldflags value.
func resolveVersion(injected string, bi *debug.BuildInfo, ok bool) string {
	if injected != "" {
		return injected
	}

	if !ok || bi == nil {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	// Building from a checkout: annotate "dev" with the VCS revision if the
	// toolchain stamped one (go build does so by default from a git tree).
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		return "dev-" + rev + dirty
	}
	return "dev"
}
