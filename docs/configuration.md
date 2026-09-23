# Configuration

jenkins-mcp authenticates with a Jenkins username and API token (Jenkins user
-> Configure -> API Token -> "Add new Token"). It does not use your Jenkins
web session or SSO login. Server behavior is configured with:

| Variable           | Required | Description                                                        |
| ------------------- | :------: | ------------------------------------------------------------------- |
| `JENKINS_URL`       |   yes    | Base URL of the Jenkins controller, e.g. `https://ci.example.com`. |
| `JENKINS_USER`      |   yes    | Username paired with `JENKINS_TOKEN`.                               |
| `JENKINS_TOKEN`     |   yes    | Jenkins API token.                                                  |
| `JENKINS_TOOLSETS`  |    no    | Comma-separated toolset names to enable, or `all`. See [Tools](tools.md) for valid names. |
| `JENKINS_READONLY`  |    no    | `true` to disable all mutating tools (`jenkins_trigger_build`, `jenkins_stop_build`, `jenkins_cancel_queue_item`) at registration time. |

## Use with Claude Desktop / Claude Code

```json
{
  "mcpServers": {
    "jenkins": {
      "command": "jenkins",
      "args": ["mcp"],
      "env": {
        "JENKINS_URL": "https://ci.example.com",
        "JENKINS_USER": "your-username",
        "JENKINS_TOKEN": "your-api-token"
      }
    }
  }
}
```

For Claude Code: `claude mcp add jenkins -- jenkins mcp` (set the three `JENKINS_*` variables in your shell first, or add `--env JENKINS_URL=... --env JENKINS_USER=... --env JENKINS_TOKEN=...`).

## Local development

The repo ships a committed [`.mcp.json`](.mcp.json) that runs the server straight from source (`go run ./cmd/jenkins mcp`), so changes take effect on the next session without a build step. Run `cp .env.example .env` and fill in `JENKINS_URL`/`JENKINS_USER`/`JENKINS_TOKEN` before launching Claude Code in this directory.

## Next: the CLI

With credentials in place, see the [CLI](cli.md) reference for `jenkins test` (verify the connection).
