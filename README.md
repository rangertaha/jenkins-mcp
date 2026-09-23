# jenkins-mcp

[![CI](https://github.com/rangertaha/jenkins-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/rangertaha/jenkins-mcp/actions/workflows/ci.yml)
[![Status: under construction](https://img.shields.io/badge/status-under%20construction-orange)](#-under-construction)
[![Go Reference](https://pkg.go.dev/badge/github.com/rangertaha/jenkins-mcp.svg)](https://pkg.go.dev/github.com/rangertaha/jenkins-mcp)
[![Go Version](https://img.shields.io/github/go-mod/go-version/rangertaha/jenkins-mcp)](go.mod)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

<div align="center">

## 🚧 &nbsp; UNDER CONSTRUCTION &nbsp; 🚧

**This server is a work in progress.**

APIs, configuration, and tool names may still change.

</div>

---

A [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server, written
in Go, exposing **Jenkins** as tools an LLM client (Claude Desktop/Code, Cursor,
and others) can call: list and inspect jobs, trigger and diagnose builds, watch
the queue, and check node/plugin status.

19 tools across 6 toolsets, each independently enable-able with `JENKINS_TOOLSETS`:

| Toolset   | Tools                                                                           |
| --------- | -------------------------------------------------------------------------------- |
| `jobs`    | `jenkins_list_jobs`, `jenkins_get_job`, `jenkins_get_job_config`                 |
| `builds`  | `jenkins_get_build`, `jenkins_trigger_build`, `jenkins_get_build_console`, `jenkins_list_artifacts`, `jenkins_get_artifact`, `jenkins_get_test_results`, `jenkins_stop_build` |
| `queue`   | `jenkins_list_queue`, `jenkins_cancel_queue_item`                                |
| `nodes`   | `jenkins_list_nodes`, `jenkins_get_node`                                         |
| `views`   | `jenkins_list_views`, `jenkins_get_view`                                         |
| `plugins` | `jenkins_list_plugins`, `jenkins_system_info`, `jenkins_whoami`                  |

List tools page (`limit`/`offset`, `hasMore`) and text-returning tools cap their
output, so one call can't exhaust the model's context.

**📖 Full documentation: [rangertaha.github.io/jenkins-mcp](https://rangertaha.github.io/jenkins-mcp/)** — install options, MCP client setup, the full tool reference, architecture, and development guide all live there. This README only covers the quickstart.

## Quickstart

```sh
go install github.com/rangertaha/jenkins-mcp/cmd/jenkins@latest
JENKINS_URL=https://ci.example.com JENKINS_USER=alice JENKINS_TOKEN=... jenkins test   # verify credentials
JENKINS_URL=https://ci.example.com JENKINS_USER=alice JENKINS_TOKEN=... jenkins mcp    # run the MCP server over stdio
```

See [Install](https://rangertaha.github.io/jenkins-mcp/install/) for prebuilt binaries and building from source.

Authentication is a Jenkins username + API token (Jenkins user → Configure → API Token → Add new Token) sent as HTTP Basic auth — nothing is stored by the server. Behavior is configured with:

| Variable            | Required | Description                                                          |
| -------------------- | :------: | ------------------------------------------------------------------- |
| `JENKINS_URL`        |   yes    | Base URL of the Jenkins controller.                                  |
| `JENKINS_USER`       |   yes    | Username paired with `JENKINS_TOKEN`.                                |
| `JENKINS_TOKEN`      |   yes    | Jenkins API token.                                                    |
| `JENKINS_TOOLSETS`   |    no    | Comma-separated toolset names to enable, or `all` (default).         |
| `JENKINS_READONLY`   |    no    | `true` to suppress the mutating tools (`jenkins_trigger_build`, `jenkins_stop_build`, `jenkins_cancel_queue_item`). |

See [Configuration](https://rangertaha.github.io/jenkins-mcp/configuration/) for MCP client setup (Claude Desktop/Code) and local development.

## Documentation

- [Install](https://rangertaha.github.io/jenkins-mcp/install/) — prebuilt binaries, `go install`, build from source.
- [Configuration](https://rangertaha.github.io/jenkins-mcp/configuration/) — environment variables, MCP client setup, local dev.
- [CLI](https://rangertaha.github.io/jenkins-mcp/cli/) — `jenkins mcp`, `jenkins test`.
- [Tools](https://rangertaha.github.io/jenkins-mcp/tools/) — the full tool reference and how to add one.
- [Prompts](https://rangertaha.github.io/jenkins-mcp/prompts/) — built-in guided workflows.
- [Architecture](https://rangertaha.github.io/jenkins-mcp/architecture/) — how the Jenkins client and tool dispatch work.
- [Development](https://rangertaha.github.io/jenkins-mcp/development/) — build, test, lint, smoke-test, release.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

GPLv3 — see [LICENSE](LICENSE).
