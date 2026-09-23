// SPDX-License-Identifier: GPL-3.0-or-later

// Package prompts registers MCP prompts: user-invoked, parameterized templates
// that clients surface as slash commands. Each prompt encodes a multi-step
// workflow by guiding the model to call the right tools in order.
//
// The workflows here are written to reach an answer cheaply: they consult the
// narrow, structured sources first (a build's test summary, the queue's own
// "why" field) and fall back to reading console logs — the most expensive
// thing this server can return — only when the cheaper sources don't explain
// the failure.
package prompts

import (
	"fmt"

	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// Register adds the built-in workflow prompts to the server.
func Register(s *server.Server) {
	registerDiagnoseFailedBuild(s)
	registerSurveyJob(s)
	registerTriageQueue(s)
	registerCompareBuilds(s)
	registerFindFlakyTest(s)
	registerTriagePipelineFailure(s)
}

// registerTriagePipelineFailure exists because a Pipeline build's console
// log interleaves every stage, so finding the failure in it is needlessly
// expensive when wfapi already knows which stage broke and why.
//
// It also encodes a Jenkins quirk worth not rediscovering: once a stage
// fails, every later stage is reported FAILED with the same error, even
// though none of them ran. The FIRST failed stage is the real one.
func registerTriagePipelineFailure(s *server.Server) {
	s.AddPrompt(
		"triage_pipeline_failure",
		"Find which stage of a Pipeline build failed and why, without reading the whole console log.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
			{Name: "build", Description: "build number, or a permalink (default: lastFailedBuild)", Required: false},
		},
		func(a map[string]string) string {
			job, build := a["job"], a["build"]
			if build == "" {
				build = "lastFailedBuild"
			}
			return fmt.Sprintf(`Find out which stage of Pipeline build %q #%s failed, and why.

1. jenkins_get_build_stages (job=%q, build=%q). If isPipeline is false this
   is not a Pipeline build — switch to the diagnose_failed_build workflow
   instead.
2. Use the failedStage field, not the last stage reported FAILED. Jenkins
   marks every stage after a failure FAILED with the same error even though
   they never ran, so the first failure is the real one and the rest are
   noise.
3. jenkins_get_stage_log (job=%q, build=%q, stageId=<failedStage>) for that
   stage's output, broken down by step. The step that failed is normally
   the last one with output.
4. Only if the stage log is empty or inconclusive, fall back to
   jenkins_get_build_console (job=%q, build=%q, tail=true).
5. If the failure looks like a test rather than a command,
   jenkins_get_test_results (job=%q, build=%q) names the failing tests.

Report: which stage failed, the failing step and its error, and whether
the cause looks like code, configuration, or infrastructure. Say
explicitly which later stages were skipped rather than genuinely broken.`,
				job, build, job, build, job, build, job, build, job, build)
		},
	)
}

// registerDiagnoseFailedBuild guides a failure post-mortem. The ordering is
// the point: a build that published tests names its own failure in a few
// hundred bytes, so asking for test results before dumping a console log
// that can run to megabytes usually answers the question outright.
func registerDiagnoseFailedBuild(s *server.Server) {
	s.AddPrompt(
		"diagnose_failed_build",
		"Diagnose a failed Jenkins build: find what broke, from test results and the console log, and say which change likely caused it.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
			{Name: "build", Description: "build number, or a permalink (default: lastFailedBuild)", Required: false},
		},
		func(a map[string]string) string {
			job, build := a["job"], a["build"]
			if build == "" {
				build = "lastFailedBuild"
			}
			return fmt.Sprintf(`Diagnose the failed Jenkins build %q #%s.

Work from the cheapest evidence to the most expensive — stop as soon as the
failure is explained.

1. jenkins_get_build (job=%q, build=%q). Note its result, what triggered it
   (causes), its source changes, and whether it reports a tests summary.
2. If that summary shows failures, call jenkins_get_test_results
   (job=%q, build=%q) — it names the failing tests and their errors
   directly, and is far cheaper than reading the log.
3. Otherwise try jenkins_get_build_stages (job=%q, build=%q). For a
   Pipeline build this names the stage that broke and its error; read just
   that stage with jenkins_get_stage_log (stageId=<failedStage>) instead of
   the whole console. It returns isPipeline=false for a non-Pipeline job —
   that is an answer, not an error, so move on.
4. Only if none of the above explains it, read the console with
   jenkins_get_build_console (job=%q, build=%q, tail=true). Use tail: the
   error is almost always at the END, and paging forward from the start of
   a large log wastes the context window.
5. Cross-reference what broke against the build's change set, and against
   the last build that passed (build="lastSuccessfulBuild") if you need to
   narrow down when it started.

Report: what failed, the specific error, which change (if any) looks
responsible, and whether it looks like a code failure or an infrastructure
one.`,
				job, build, job, build, job, build, job, build, job, build)
		},
	)
}

func registerSurveyJob(s *server.Server) {
	s.AddPrompt(
		"survey_job",
		"Survey a Jenkins job: its health, recent build outcome, and how it is configured.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
		},
		func(a map[string]string) string {
			job := a["job"]
			return fmt.Sprintf(`Survey the Jenkins job %q.

1. jenkins_get_job (job=%q) for its description, health reports, whether it
   is buildable, its declared parameters, and pointers to recent builds.
   Note: health is a list — a low score on one report (say test results)
   next to a high score on another (build stability) tells you where the
   problem is.
2. jenkins_get_build (job=%q, build="lastBuild") for the most recent
   outcome and duration.
3. If the job is a folder (isFolder is true), it has no builds — list its
   contents with jenkins_list_jobs (folder=%q) instead.
4. If how the job actually works matters, jenkins_get_job_config (job=%q)
   returns its raw config.xml.

Report: is this job healthy, is it currently passing, what does it take to
run it, and anything that looks misconfigured.`,
				job, job, job, job, job)
		},
	)
}

// registerTriageQueue answers "why is nothing building?", which needs two
// facts correlated: what the queue says it is waiting for, and whether any
// executor is actually available to satisfy it.
func registerTriageQueue(s *server.Server) {
	s.AddPrompt(
		"triage_queue",
		"Work out why Jenkins builds are queued and not starting, by correlating the queue's reasons with node availability.",
		nil,
		func(map[string]string) string {
			return `Explain why builds are sitting in the Jenkins queue instead of running.

1. jenkins_list_queue. Each item carries a "why" field that is Jenkins' own
   explanation ("Waiting for next available executor", "is offline", a
   label expression, ...), plus blocked/buildable/stuck flags. "stuck"
   means Jenkins itself considers the item pathologically delayed.
2. jenkins_list_nodes. Check how many nodes are online versus offline, how
   many executors they have, whether they are idle, and their labels.
   offlineCauseReason explains any offline node.
3. Correlate the two. The usual causes are: every executor busy; the only
   node matching a required label is offline; a node offline for a reason
   worth fixing (disk space, lost agent connection); or a job blocked by
   its own concurrency settings.
4. For any item that should not be waiting, jenkins_get_job on its task
   name will show whether the job itself is unbuildable or already queued.

Report: which items are waiting and why, whether the cause is capacity,
labels, or an offline node, and what would unblock them. Mention any item
flagged stuck first — that is the strongest signal something is wrong
rather than merely busy.`
		},
	)
}

// registerCompareBuilds supports the "it worked yesterday" investigation,
// where the useful signal is the delta between a good build and a bad one.
func registerCompareBuilds(s *server.Server) {
	s.AddPrompt(
		"compare_builds",
		"Compare two builds of a job to find what changed between a passing build and a failing one.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
			{Name: "good", Description: "the build that worked (default: lastSuccessfulBuild)", Required: false},
			{Name: "bad", Description: "the build that broke (default: lastFailedBuild)", Required: false},
		},
		func(a map[string]string) string {
			job, good, bad := a["job"], a["good"], a["bad"]
			if good == "" {
				good = "lastSuccessfulBuild"
			}
			if bad == "" {
				bad = "lastFailedBuild"
			}
			return fmt.Sprintf(`Compare two builds of %q to find what changed: %s (worked) versus %s (broke).

1. jenkins_get_build for both (job=%q, build=%q and build=%q). Compare
   their parameters, what triggered them, the node each ran on (builtOn),
   their durations, and their change sets.
2. If both report test summaries, jenkins_get_test_results on each and
   compare which tests fail in the bad build but not the good one.
3. If the builds are more than one apart, walk the numbers between them —
   the first build that fails narrows the cause to that build's changes
   alone.

Pay attention to differences that are not code: a different builtOn node,
a changed parameter value, or a much longer duration before failing all
point away from the change set and toward the environment.

Report: the specific differences, which one most likely caused the
failure, and whether the evidence points at a code change or the
environment.`,
				job, good, bad, job, good, bad)
		},
	)
}

// registerFindFlakyTest distinguishes a genuinely broken test from an
// intermittent one, which needs history rather than a single build.
func registerFindFlakyTest(s *server.Server) {
	s.AddPrompt(
		"find_flaky_test",
		"Check whether a job's test failures are consistent or intermittent, by comparing test results across recent builds.",
		[]server.PromptArg{
			{Name: "job", Description: "full job path, e.g. team-a/service-b", Required: true},
			{Name: "builds", Description: "how many recent builds to examine (default: 10)", Required: false},
		},
		func(a map[string]string) string {
			job, builds := a["job"], a["builds"]
			if builds == "" {
				builds = "10"
			}
			return fmt.Sprintf(`Determine whether %q has flaky tests, looking at its last %s builds.

1. jenkins_get_job (job=%q) to find the most recent build number.
2. Walk backwards from it for %s builds, calling jenkins_get_test_results
   (job=%q, build=<number>) on each. A build with hasResults=false simply
   published no test report — skip it rather than treating it as a pass.
3. For each test that failed at least once, record which builds it failed
   in and which it passed in.

Classify each failing test:
- ALWAYS failing since a specific build: a real regression. Report the
  first build that failed — jenkins_get_build on it gives the change set
  that likely caused it.
- Failing intermittently with no code change between passes and failures:
  flaky. Note how often it fails.
- Failing only in the most recent build: too early to tell; say so rather
  than guessing.

Report: which tests are genuine regressions (with the build that
introduced them) and which are flaky (with their failure rate). Do not
describe a test as flaky on the strength of a single failure.`,
				job, builds, job, builds, job)
		},
	)
}
