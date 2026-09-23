# jenkins-mcp

A [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server, written
in Go, exposing Jenkins as tools an LLM client can call — jobs and builds,
the build queue, agents/nodes, views, and installed plugins.

Each tool is a concrete, hand-written call against the Jenkins REST API (see
[Architecture](architecture.md)) — no dynamic discovery, since Jenkins is one
API surface, not a family of SDK-generated clients. See [Tools](tools.md) for
the full list.

| Toolset   | Tools                                                                          |
| --------- | ------------------------------------------------------------------------------- |
| `jobs`    | `jenkins_list_jobs`, `jenkins_get_job`, `jenkins_get_job_config`, `jenkins_search_jobs` |
| `builds`  | `jenkins_get_build`, `jenkins_trigger_build`, `jenkins_get_build_console`, `jenkins_get_build_stages`, `jenkins_get_stage_log`, `jenkins_list_artifacts`, `jenkins_get_artifact`, `jenkins_get_test_results`, `jenkins_stop_build` |
| `queue`   | `jenkins_list_queue`, `jenkins_cancel_queue_item`                               |
| `nodes`   | `jenkins_list_nodes`, `jenkins_get_node`                                        |
| `views`   | `jenkins_list_views`, `jenkins_get_view`                                        |
| `plugins` | `jenkins_list_plugins`, `jenkins_system_info`, `jenkins_whoami`                 |

Beyond tools, the server exposes three MCP [resources](resources.md) (a job's
config.xml, a build's console log, and instance info) and six guided
[prompts](prompts.md).

## Next steps

- [Install](install.md) the server.
- Set up [Configuration](configuration.md), including your MCP client.
- Check the [CLI](cli.md) for `jenkins test`/`jenkins mcp`.
- Browse what's covered: [Tools](tools.md).
- Read documents directly as [Resources](resources.md).
- Try a built-in [Prompt](prompts.md).
- Read how it works: [Architecture](architecture.md).
- Contributing? See [Development](development.md).
