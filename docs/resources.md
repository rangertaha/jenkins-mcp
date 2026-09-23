# Resources

jenkins-mcp exposes three MCP **resources** alongside its [tools](tools.md).

Tools are actions the model chooses and pays a round trip for. Resources are
documents a client reads as context — it can attach them to a conversation, or
a user can pick them directly, without the model spending a tool call. So the
resources here are the bulky read-only documents that a question about a job or
a build usually needs in full, rather than the filtered, paginated views the
tools return.

Resources are always registered. Unlike tools they are not affected by
`JENKINS_TOOLSETS` (which names tool areas) or `JENKINS_READONLY` (they are
read-only already).

| URI | Type | Contents |
| --- | ---- | -------- |
| `jenkins://info` | `application/json` | Controller version, mode, URL, executor count, security status. |
| `jenkins://job/{path}/config.xml` | `text/xml` | A job's full `config.xml` definition. |
| `jenkins://build/{job}/{number}/console` | `text/plain` | A build's complete console log. |

## `jenkins://info`

```json
{
  "version": "2.568.3",
  "mode": "NORMAL",
  "nodeDescription": "the Jenkins controller's built-in node",
  "numExecutors": 2,
  "url": "https://ci.example.com/",
  "useSecurity": true,
  "quietingDown": false
}
```

`version` comes from Jenkins' `X-Jenkins` response header — no Jenkins endpoint
reports it in a JSON body.

## `jenkins://job/{path}/config.xml`

`path` is the job's full path, exactly as `fullName` reports it:

```
jenkins://job/demo/config.xml
jenkins://job/team-a/service-b/config.xml
```

Reading a job's `config.xml` is the way to see what a job actually *does* — its
pipeline script or build steps, triggers, and parameter definitions — which the
`jenkins_get_job` tool summarizes but does not include.

## `jenkins://build/{job}/{number}/console`

`number` is a build number or one of the permalinks
`lastBuild`, `lastSuccessfulBuild`, `lastFailedBuild`, `lastStableBuild`,
`lastCompletedBuild`:

```
jenkins://build/demo/42/console
jenkins://build/team-a/service-b/lastFailedBuild/console
```

This returns the whole log in a single read. Prefer the
[`jenkins_get_build_console`](tools.md) tool when the log is large enough to
need paging, or when the build is still running and you want to follow it as it
grows — the tool reads by byte offset and reports whether more output is
pending.

## Notes

Job paths containing `/` work in both templates: they are declared with RFC 6570
reserved expansion (`{+path}`), since a plain `{path}` would stop at the first
slash and nested jobs in folders would not resolve at all.

A URI naming a job or build that does not exist returns a protocol-level
*resource not found*, which clients surface as a missing resource rather than a
server error. A malformed URI — a blank job path, or a build reference that is
neither a number nor a known permalink — is rejected before any request reaches
Jenkins.
