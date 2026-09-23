# Prompts (workflows)

MCP has no dedicated "workflow" primitive, so multi-step flows are shipped as **prompts**: user-invoked, parameterized templates that guide the model through a sequence of tool calls. MCP clients surface these as slash commands automatically (e.g. in Claude Code and Claude Desktop).

| Prompt | Arguments | What it does |
| ------ | --------- | ------------ |
| `diagnose_failed_build` | `job`, `build` (default `lastFailedBuild`) | Find what broke, working from test results before console logs, and say which change likely caused it. |
| `survey_job` | `job` | Report a job's health, last build outcome, and how it is configured. |
| `triage_queue` | none | Explain why builds are queued and not starting, by correlating the queue's reasons with node availability. |
| `compare_builds` | `job`, `good` (default `lastSuccessfulBuild`), `bad` (default `lastFailedBuild`) | Find what changed between a passing build and a failing one. |
| `find_flaky_test` | `job`, `builds` (default `10`) | Distinguish genuine regressions from intermittent failures across recent builds. |
| `triage_pipeline_failure` | `job`, `build` (default `lastFailedBuild`) | Find which Pipeline stage failed and why, without reading the whole console log. |

No prompt calls Jenkins itself — each renders a short instruction telling the model which tools to call, in what order, and what to report back, the same way any other prompt-driven workflow would use the [tools](tools.md).

## Why the ordering in these prompts matters

The workflows deliberately consult narrow, structured sources before wide ones. A build that publishes tests names its own failure in a few hundred bytes via `jenkins_get_test_results`, whereas a console log can run to megabytes — so `diagnose_failed_build` asks for test results first and only falls back to the log when the tests don't explain the failure (a compile error, an infrastructure problem, or a job with no test report at all). When it does read the log, it reads the end first, because that is where a failure almost always is.

The same idea drives `triage_queue`: the queue's own `why` field is Jenkins' explanation of what an item is waiting for, and pairing it with node availability answers "why isn't this building?" without inspecting a single job.

`triage_pipeline_failure` goes further for Pipeline builds, where the console log interleaves every stage: `jenkins_get_build_stages` already knows which stage broke and why, so only that stage's log needs reading. It also encodes a Jenkins quirk worth not rediscovering — once a stage fails, **every later stage is reported `FAILED` with the same error even though none of them ran**, so the first failed stage is the real one and the rest are noise.

## Next: how the tools actually work

See [Architecture](architecture.md) for how each tool calls Jenkins.
