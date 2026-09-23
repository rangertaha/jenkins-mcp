# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The project was rewritten from aws-mcp (AWS services, via reflection-based
dispatch over aws-sdk-go-v2) into jenkins-mcp. Nothing described in this
section has ever been released — `v0.1.0` predates it — so this entry
replaces the prior, now-superseded AWS-era description rather than adding
to it.

### Added
- 14 hand-written MCP tools across 6 toolsets calling the Jenkins REST API
  directly (`internal/jenkinsx`), replacing generic reflection-based
  dispatch over an AWS SDK client (Jenkins is one REST API, not a family of
  typed SDK clients, so that mechanism has no equivalent here):
  - `jobs`: `jenkins_list_jobs`, `jenkins_get_job`
  - `builds`: `jenkins_get_build`, `jenkins_trigger_build`, `jenkins_get_build_console`
  - `queue`: `jenkins_list_queue`, `jenkins_cancel_queue_item`
  - `nodes`: `jenkins_list_nodes`, `jenkins_get_node`
  - `views`: `jenkins_list_views`, `jenkins_get_view`
  - `plugins`: `jenkins_list_plugins`, `jenkins_system_info`, `jenkins_whoami`
- HTTP Basic auth (`JENKINS_USER`/`JENKINS_TOKEN`) with automatic CSRF crumb
  handling (fetched from `/crumbIssuer/api/json`, cached, retried once on a
  stale/rejected crumb) for the two mutating tools.
- `diagnose_failed_build` and `survey_job` guided-workflow prompts, replacing
  `survey_bucket`.
- A `jenkins test` connectivity check (`/whoAmI/api/json`), replacing the STS
  `GetCallerIdentity` check.

### Removed
- The AWS SDK reflection/dispatch engine and its generic meta-tools
  (`aws_list_services`, `aws_list_operations`, `aws_describe_operation`,
  `aws_invoke`, `aws_list_profiles`, `aws_use_profile`, `aws_whoami`), the
  `services.json` -> generated-client-registry codegen step, and all
  426-AWS-service coverage.

### Changed
- Environment variables: `AWS_REGION`/`AWS_TOOLSETS`/`AWS_READONLY` ->
  `JENKINS_URL`/`JENKINS_USER`/`JENKINS_TOKEN`/`JENKINS_TOOLSETS`/`JENKINS_READONLY`
  (`URL`/`USER`/`TOKEN` are required; Jenkins has no ambient credential chain
  the way AWS does).
- Module path: `github.com/rangertaha/aws-mcp` -> `github.com/rangertaha/jenkins-mcp`;
  binary: `aws` -> `jenkins`.
- License: MIT -> GPL-3.0-or-later.
