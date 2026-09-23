# CLI

`jenkins` is a small command tree (built on [`urfave/cli`](https://cli.urfave.org/)). A bare `jenkins` with no subcommand is equivalent to `jenkins mcp`.

## `jenkins mcp`

Run the MCP server over stdio. This is what MCP clients (Claude Desktop/Code, Cursor) invoke — see [Configuration](configuration.md) for client setup.

```sh
jenkins mcp
```

## `jenkins test`

Verify credentials against Jenkins: calls the `/whoAmI` endpoint and prints the resolved principal. Useful for confirming credentials are correct before wiring up an MCP client.

```sh
$ jenkins test
OK  authenticated with Jenkins (url=https://ci.example.com)
    name=ada authenticated=true
    read-only=false
```

## Next: browse the tools

See [Tools](tools.md) for the full list of registered tools, or [Architecture](architecture.md) for how each one calls Jenkins.
