// SPDX-License-Identifier: GPL-3.0-or-later

// Package prompts registers MCP prompts: user-invoked, parameterized templates
// that clients surface as slash commands. Each prompt encodes a multi-step
// workflow by guiding the model to call the right tools in order.
package prompts

import (
	"fmt"

	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// Register adds the built-in workflow prompts to the server.
func Register(s *server.Server) {
	s.AddPrompt(
		"diagnose_failed_build",
		"Diagnose a failed Jenkins build: pull its console log and change set, and summarize the likely cause.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
			{Name: "build", Description: "build number, or a permalink (default: lastFailedBuild)", Required: false},
		},
		func(a map[string]string) string {
			build := a["build"]
			if build == "" {
				build = "lastFailedBuild"
			}
			return fmt.Sprintf(`Diagnose the failed Jenkins build "%s" #%s.

Steps:
1. Call jenkins_get_build (job="%s", build="%s") to get its result, timing, parameters, and change set.
2. Call jenkins_get_build_console (job="%s", build="%s", start=0) to read the console log, following
   nextStart while hasMore is true until the whole log has been read.
3. Identify the point of failure in the console log and cross-reference it with the change set entries.
4. Summarize: what failed, the likely cause, and which change (if any) looks responsible.`,
				a["job"], build, a["job"], build, a["job"], build)
		},
	)

	s.AddPrompt(
		"survey_job",
		"Survey a Jenkins job: report its health, last build status, and configured parameters.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
		},
		func(a map[string]string) string {
			return fmt.Sprintf(`Survey the Jenkins job "%s".

Steps:
1. Call jenkins_get_job (job="%s") to get its description, health score, buildability, and parameter definitions.
2. Call jenkins_get_build (job="%s", build="lastBuild") to get the most recent build's status and duration.
3. Summarize the job's overall health, whether its last build succeeded, and what parameters it takes.`,
				a["job"], a["job"], a["job"])
		},
	)
}
