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
