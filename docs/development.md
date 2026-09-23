# Development

```sh
make test        # go test -race ./...
make cover       # run tests and print a coverage summary
make vet         # go vet ./...
make fmt-check   # gofmt verification
make lint        # golangci-lint
make all         # fmt-check + vet + lint + test + build
```

`internal/jenkinsx` and `internal/jenkinsx/tools` test against `httptest.Server` fixtures rather than a real Jenkins instance, covering CSRF crumb caching/refresh, HTTP status error mapping, and each tool handler's request shape and response decoding — including a read-only-mode test per toolset confirming its `Write` tool(s) are suppressed. `cmd/jenkins` also has an integration test that builds and drives the real binary over its actual stdin/stdout (unlike every other test, which uses an in-memory transport) — it points `JENKINS_URL` at a local mock server and strips every inherited `JENKINS_*` environment variable first, so it never depends on or reaches a real Jenkins instance regardless of what's configured on the machine running it.

## Smoke-testing the protocol

List the tools over stdio without an MCP client:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"s","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
| JENKINS_URL=https://ci.example.com JENKINS_USER=alice JENKINS_TOKEN=tok JENKINS_READONLY=true ./bin/jenkins mcp
```

Or browse interactively with the [MCP Inspector](https://github.com/modelcontextprotocol/inspector):

```sh
npx @modelcontextprotocol/inspector ./bin/jenkins mcp
```

## Adding a tool

See [Tools](tools.md#adding-a-tool).

## Releasing

Releases are tag-triggered (GoReleaser via CI, `.goreleaser.yaml`); `make next`/`make bump` compute and tag the next version from conventional commits.
