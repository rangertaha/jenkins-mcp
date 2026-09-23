// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import "testing"

func TestJobPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single", "my-job", "/job/my-job"},
		{"nested", "team-a/service-b", "/job/team-a/job/service-b"},
		{"deeply nested", "a/b/c", "/job/a/job/b/job/c"},
		{"leading and trailing slashes trimmed", "/team-a/service-b/", "/job/team-a/job/service-b"},
		{"empty interior segments skipped", "team-a//service-b", "/job/team-a/job/service-b"},
		{"whitespace only", "   ", ""},
		{"slashes only", "///", ""},
		{"space needs escaping", "my job", "/job/my%20job"},
		{"parens preserved via escaping", "(built-in)", "/job/%28built-in%29"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JobPath(c.in); got != c.want {
				t.Errorf("JobPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
