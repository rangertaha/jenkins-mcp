# Architecture

jenkins-mcp hand-writes a tool per Jenkins operation it exposes. Unlike a
generic AWS-SDK-style client family, Jenkins is a single REST API, so there's
no per-service type family to reflect over — each tool is a concrete Go
`In`/`Out` struct pair and a handler calling a concrete endpoint.

## Project layout

```
cmd/jenkins             entrypoint: a urfave/cli command tree (mcp, test)
internal/config          environment configuration (JENKINS_URL, JENKINS_USER, JENKINS_TOKEN, JENKINS_TOOLSETS, JENKINS_READONLY) + .env loading
internal/server          MCP server wrapper: typed tool registration, JSON Schema inference, read-only annotations, prompts
internal/jenkinsx        Jenkins REST client
  client.go                Client: Get/GetWithHeaders/PostForm/Text over a shared HTTP primitive
  crumb.go                  CSRF crumb acquisition, caching, and stale-crumb retry
  errors.go                 StatusError: HTTP status + body -> structured error
  check.go                  Check: /whoAmI connectivity check
  path.go                   JobPath: full job name -> Jenkins URL path
internal/jenkinsx/tools  the MCP tool surface, one file per toolset
  jobs.go, builds.go, queue.go, nodes.go, views.go, system.go
internal/prompts         built-in MCP prompts (diagnose_failed_build, survey_job)
internal/app              wires config + jenkinsx + tools + prompts into a *server.Server
```

## Calling a tool

Each tool handler follows the same shape:

1. Build the Jenkins REST path (via `jenkinsx.JobPath` for job-scoped endpoints) and a `tree=` query parameter selecting just the fields the tool needs.
2. Call `client.Get`/`GetWithHeaders` (JSON GET), `client.PostForm` (form-encoded POST, with a CSRF crumb attached automatically), or `client.Text` (raw-text GET, for the console log endpoint) on the shared `jenkinsx.Client`.
3. Decode into a package-private "raw" struct matching Jenkins' JSON shape (including its `_class` convention), then map it into the tool's public output struct — keeping the model-facing schema clean of Jenkins-internal field names.
4. Return the mapped struct; `server.Register`'s generic panic recovery and JSON Schema inference (from the struct's own type, via `jsonschema-go`) apply uniformly, the same as every other tool.

`client.PostForm` handles CSRF automatically: it fetches and caches a crumb from `/crumbIssuer/api/json` on first use (a 404 there means CSRF protection is disabled, cached so it isn't re-probed), attaches it as a header, and — if Jenkins rejects the crumb as stale — clears the cache and retries exactly once with a freshly fetched one.

## Read-only mode

`JENKINS_READONLY=true` suppresses every `Write` tool (`jenkins_trigger_build`, `jenkins_stop_build`, `jenkins_cancel_queue_item`) at registration time, via the same `server.Register` mechanism every tool uses — read-only enforcement lives entirely in `internal/server`, not per-tool.

## Toolsets

`app.Assemble` calls each toolset's `Register*` function conditionally on `JENKINS_TOOLSETS` (via `config.Config.ToolsetEnabled`), so an operator can expose only, say, `jobs` and `builds` to a given MCP client.

## Next: add a tool

See [Tools](tools.md#adding-a-tool) for the steps to expose a new Jenkins endpoint, or [Tools](tools.md) for what's currently registered.
