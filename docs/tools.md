# Tools

jenkins-mcp registers 14 tools, grouped into 6 toolsets. Each toolset can be
individually enabled/disabled with `JENKINS_TOOLSETS` (see [Configuration](configuration.md)); a `w` in the R/W column marks a mutating tool, suppressed
entirely when `JENKINS_READONLY=true`.

## `jobs`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_jobs` | r | List jobs at the top level or within a folder. |
| `jenkins_get_job` | r | Get a job's description, health, buildability, last-build pointers, and parameters. |

## `builds`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_get_build` | r | Get a build's status, timing, parameters, and change set. |
| `jenkins_trigger_build` | w | Enqueue a new build, optionally with parameters. |
| `jenkins_get_build_console` | r | Read a slice of a build's console log, paginated by byte offset. |

## `queue`

| Tool | R/W | Description |
| ---- | :-: | ----------- |
| `jenkins_list_queue` | r | List items waiting in the build queue. |
| `jenkins_cancel_queue_item` | w | Cancel a pending queue item. |

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

## Adding a tool

Each tool is a concrete Go `In`/`Out` struct pair and a handler registered via
`server.Register` — see [Architecture](architecture.md#calling-a-tool). To add
one: pick (or create) the right file under `internal/jenkinsx/tools/`, define
the input/output structs, write a handler that calls `t.client.Get`/`PostForm`/`Text`
against the Jenkins REST endpoint, and register it in that file's `Register*`
function. No codegen step is involved.
