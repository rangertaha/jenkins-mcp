// SPDX-License-Identifier: GPL-3.0-or-later

package internal

import "testing"

func TestVersionPrefersLdflagsInjectedValue(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	version = "v1.2.3"
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want %q", got, "v1.2.3")
	}
}

// TestVersionFallsBackWithoutInjection can't pin an exact value: under `go
// test`, debug.ReadBuildInfo()'s module version and VCS stamping vary by
// environment. It only confirms Version() never returns an empty string
// once the ldflags override is absent, which would otherwise report to MCP
// clients as an empty/missing version.
func TestVersionFallsBackWithoutInjection(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	version = ""
	if got := Version(); got == "" {
		t.Error("Version() = \"\", want a non-empty fallback (build-info version or \"dev...\")")
	}
}
