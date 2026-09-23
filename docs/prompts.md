# Prompts (workflows)

MCP has no dedicated "workflow" primitive, so multi-step flows are shipped as **prompts**: user-invoked, parameterized templates that guide the model through a sequence of tool calls. MCP clients surface these as slash commands automatically (e.g. in Claude Code and Claude Desktop).

| Prompt | Arguments | What it does |
| ------ | --------- | ------------ |
| `diagnose_failed_build` | `job`, `build` (default `lastFailedBuild`) | Pull a build's console log and change set, and summarize the likely cause of failure. |
| `survey_job` | `job` | Report a job's health, last build status, and configured parameters. |

Neither prompt calls Jenkins itself — each renders a short instruction telling the model which tools to call, in what order, and what to report back, the same way any other prompt-driven workflow would use the [tools](tools.md).

## Next: how the tools actually work

See [Architecture](architecture.md) for how each tool calls Jenkins.
