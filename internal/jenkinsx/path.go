// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"net/url"
	"strings"
)

// JobPath converts a "/"-separated full job name (e.g. "team-a/service-b",
// as Jenkins reports it in a job's fullName) into its URL path relative to
// the Jenkins base URL (e.g. "/job/team-a/job/service-b"), URL-escaping each
// segment. An empty name yields "" (the root).
func JobPath(fullName string) string {
	fullName = strings.Trim(strings.TrimSpace(fullName), "/")
	if fullName == "" {
		return ""
	}

	var b strings.Builder
	for _, part := range strings.Split(fullName, "/") {
		if part == "" {
			continue
		}
		b.WriteString("/job/")
		b.WriteString(url.PathEscape(part))
	}
	return b.String()
}

// BuildPermalinks are the symbolic references Jenkins resolves in place of a
// build number in a build URL.
//
// This list is the single source of truth for both the schema pattern the
// tools publish and the validation the resources perform. It lives here,
// next to JobPath, because the two previously kept their own copies and
// drifted: the resource rejected lastUnstableBuild, lastUnsuccessfulBuild
// and firstBuild while the equivalent tool accepted them, so the same build
// reference worked through one surface and not the other.
var BuildPermalinks = []string{
	"lastBuild",
	"lastSuccessfulBuild",
	"lastFailedBuild",
	"lastStableBuild",
	"lastCompletedBuild",
	"lastUnsuccessfulBuild",
	"lastUnstableBuild",
	"firstBuild",
}

// IsBuildPermalink reports whether ref is one of BuildPermalinks.
func IsBuildPermalink(ref string) bool {
	for _, p := range BuildPermalinks {
		if ref == p {
			return true
		}
	}
	return false
}

// HasJobSegments reports whether name yields at least one path segment —
// i.e. whether JobPath(name) produces a usable /job/... path.
//
// A blank check alone is not enough: JobPath drops empty segments, so "/",
// "//" and "   /  " all survive a non-blank test and then collapse to "",
// leaving the request without its /job/<name> prefix entirely and pointed
// at whatever sits at the base URL instead.
func HasJobSegments(name string) bool { return JobPath(name) != "" }
