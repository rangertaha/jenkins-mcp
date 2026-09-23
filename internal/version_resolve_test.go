// SPDX-License-Identifier: GPL-3.0-or-later

package internal

import (
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(mainVersion string, settings ...debug.BuildSetting) *debug.BuildInfo {
	bi := &debug.BuildInfo{Settings: settings}
	bi.Main.Version = mainVersion
	return bi
}

// TestResolveVersion pins the documented precedence: an -ldflags value beats
// the module version, which beats a VCS-stamped "dev" string, which beats a
// bare "dev".
func TestResolveVersion(t *testing.T) {
	cases := []struct {
		name     string
		injected string
		bi       *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "ldflags value wins over everything",
			injected: "v1.2.3",
			bi:       buildInfo("v9.9.9", debug.BuildSetting{Key: "vcs.revision", Value: "abcdef123456"}),
			ok:       true,
			want:     "v1.2.3",
		},
		{
			name: "no build info at all",
			ok:   false,
			want: "dev",
		},
		{
			name: "nil build info even when ok",
			bi:   nil,
			ok:   true,
			want: "dev",
		},
		{
			name: "module version from go install",
			bi:   buildInfo("v0.4.0"),
			ok:   true,
			want: "v0.4.0",
		},
		{
			name: "(devel) module version is not a real version",
			bi:   buildInfo("(devel)"),
			ok:   true,
			want: "dev",
		},
		{
			name: "vcs revision, clean tree",
			bi:   buildInfo("", debug.BuildSetting{Key: "vcs.revision", Value: "abcdef1234567890"}),
			ok:   true,
			want: "dev-abcdef123456",
		},
		{
			name: "vcs revision, dirty tree",
			bi: buildInfo("",
				debug.BuildSetting{Key: "vcs.revision", Value: "abcdef1234567890"},
				debug.BuildSetting{Key: "vcs.modified", Value: "true"},
			),
			ok:   true,
			want: "dev-abcdef123456-dirty",
		},
		{
			name: "vcs.modified false is not dirty",
			bi: buildInfo("",
				debug.BuildSetting{Key: "vcs.revision", Value: "abcdef1234567890"},
				debug.BuildSetting{Key: "vcs.modified", Value: "false"},
			),
			ok:   true,
			want: "dev-abcdef123456",
		},
		{
			name: "short revision is not truncated",
			bi:   buildInfo("", debug.BuildSetting{Key: "vcs.revision", Value: "abc123"}),
			ok:   true,
			want: "dev-abc123",
		},
		{
			name: "unrelated build settings are ignored",
			bi:   buildInfo("", debug.BuildSetting{Key: "GOARCH", Value: "amd64"}),
			ok:   true,
			want: "dev",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveVersion(c.injected, c.bi, c.ok); got != c.want {
				t.Errorf("resolveVersion(%q, %+v, %v) = %q, want %q", c.injected, c.bi, c.ok, got, c.want)
			}
		})
	}
}

// TestVersionUsesRealBuildInfo confirms the exported wrapper is actually
// wired to resolveVersion: with no injected value it must still produce one
// of the documented shapes, never an empty string (which MCP clients would
// display as a missing version).
func TestVersionUsesRealBuildInfo(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	version = ""
	got := Version()
	if got == "" {
		t.Fatal("Version() = \"\", want a non-empty fallback")
	}
	if !strings.HasPrefix(got, "dev") && !strings.HasPrefix(got, "v") {
		t.Errorf("Version() = %q, want a \"dev...\" or \"v...\" value", got)
	}
}
