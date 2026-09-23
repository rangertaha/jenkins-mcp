# Tools

jenkins-mcp registers 19 tools, grouped into 6 toolsets. Each toolset can be
individually enabled/disabled with `JENKINS_TOOLSETS` (see [Configuration](configuration.md)); a `w` in the R/W column marks a mutating tool, suppressed
entirely when `JENKINS_READONLY=true`.

## Paging and size limits

Every `list_*` tool takes `limit` (default 50, maximum 200) and `offset`, and
returns `count`, `offset` and `hasMore`. When `hasMore` is true, call again with
`offset` set to `offset + count`. Paging is done server-side with Jenkins' own
`tree` range syntax, so a controller with thousands of jobs never sends them all.

Tools that return raw text — `jenkins_get_build_console`, `jenkins_get_artifact`,
`jenkins_get_job_config` — cap their output at 64 KiB and set `truncated` when
they cut it, so a multi-megabyte console log can't exhaust the model's context in
a single call.

## `jobs`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_jobs` | r | List jobs at the top level or within a folder. |
| `jenkins_get_job` | r | Get a job's description, health, buildability, last-build pointers, and parameters. |
| `jenkins_get_job_config` | r | Get a job's raw `config.xml` — its full definition. |

Folders appear in `jenkins_list_jobs` as entries with `isFolder: true`; descend
by calling again with `folder` set to that entry's `fullName`.

## `builds`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_get_build` | r | Get a build's status, timing, parameters, causes, change set, and test summary. |
| `jenkins_trigger_build` | w | Enqueue a new build, optionally with parameters. |
| `jenkins_get_build_console` | r | Read a build's console log, paginated by byte offset. |
| `jenkins_list_artifacts` | r | List the files a build archived. |
| `jenkins_get_artifact` | r | Read one archived artifact's contents. |
| `jenkins_get_test_results` | r | Get a build's test results, listing failures with error details. |
| `jenkins_stop_build` | w | Abort a running build. |

`build` accepts a build number or any Jenkins permalink (`lastBuild`,
`lastSuccessfulBuild`, `lastFailedBuild`, `lastStableBuild`,
`lastCompletedBuild`, `lastUnsuccessfulBuild`, `lastUnstableBuild`,
`firstBuild`). `jenkins_get_test_results` returns `hasResults: false` rather than
an error for a job that publishes no test report.

## `queue`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_queue` | r | List items waiting in the build queue. |
| `jenkins_cancel_queue_item` | w | Cancel a pending queue item. |

Use `jenkins_cancel_queue_item` for a build that has not started yet, and
`jenkins_stop_build` for one that is already running.

## `nodes`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_nodes` | r | List build agents/nodes and their online/idle status. |
| `jenkins_get_node` | r | Get one node's status and per-executor activity. |

Pass `jenkins_get_node` the `name` field from `jenkins_list_nodes`, not
`displayName`. For the controller the two differ — Jenkins reports the display
name `Built-In Node`, but only the segment `(built-in)` resolves under
`/computer/`, and Jenkins exposes it nowhere in the API, so `name` is
reconstructed from the node's `_class`.

## `views`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_views` | r | List the views configured on the controller. |
| `jenkins_get_view` | r | Get a view's description and the jobs it contains. |

## `plugins`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_plugins` | r | List installed plugins with version, enabled/active, and update-available status. |
| `jenkins_system_info` | r | Get Jenkins' version and other system-level status. |
| `jenkins_whoami` | r | Report the identity the configured credentials resolve to. |

## Input validation

Tool inputs carry real JSON Schema constraints — a pattern on build
identifiers, minimums on `limit`/`offset`/`id`, minimum lengths on required
path segments — not just descriptions. MCP clients validate a call against the
input schema before it reaches the server, so a malformed argument is rejected
at the client naming the offending field, rather than surfacing as a confusing
Jenkins 404 several layers later.

## Adding a tool

Each tool is a concrete Go `In`/`Out` struct pair and a handler registered via
`server.Register` — see [Architecture](architecture.md#calling-a-tool). To add
one: pick (or create) the right file under `internal/jenkinsx/tools/`, define
the input/output structs, write a handler that calls `t.client.Get`/`PostForm`/`Text`
against the Jenkins REST endpoint, and register it in that file's `Register*`
function. No codegen step is involved.

Two rules the test suite enforces:

- A handler that builds a REST path from a required string must call
  `requireNonEmpty` and gain a case in `validation_test.go` — an empty segment
  silently collapses the URL onto a *different* Jenkins resource rather than
  404ing.
- A `tree=` string and its struct's JSON tags state the same truth twice. Add a
  field to one and you must add it to the other, and assert in the test that the
  outgoing `tree` request contains it — `omitempty` means a missing field
  decodes as a zero value with no error at all.

To express a constraint the Go type cannot, implement
`RefineSchema(*jsonschema.Schema)` on the input or output type.
