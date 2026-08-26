# kdelta

[![CI](https://github.com/shadiramadan/kdelta/actions/workflows/ci.yaml/badge.svg)](https://github.com/shadiramadan/kdelta/actions/workflows/ci.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/shadiramadan/kdelta.svg)](https://pkg.go.dev/github.com/shadiramadan/kdelta)
[![TypeScript](https://img.shields.io/badge/TypeScript-strict-3178C6?logo=typescript&logoColor=white)](apps/app-ui)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE.md)
[![GitHub Discussions](https://img.shields.io/github/discussions/shadiramadan/kdelta?logo=github)](https://github.com/shadiramadan/kdelta/discussions)

**kdelta** answers four questions about your Kubernetes cluster: *what's
deployed, what version is it, what changed upstream since then, and what happens
to the rest of the system if you upgrade it.*

```text
kdelta scan                                # detect deployed resources + versions + upstream links
kdelta resources                           # list detected resources from the cached scan (alias: ls)
kdelta get <resource-ref>                  # one resource: streams, links, conditions, objects
kdelta versions <resource-ref>             # list versions since the deployed one (no AI)
kdelta changes  <resource-ref> --from x --to y   # changelog for a range (alias: changelog)
kdelta impact   <resource-ref> --from x --to y   # AI-assessed blast radius of the upgrade
kdelta serve                               # ConnectRPC API + embedded web UI
```

One Go binary contains the CLI, the server, and an embedded web UI. Commands run
against an in-process server by default, or a remote one with `--server` — same
code path either way. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

> **Status: early development.** The `scan → versions → changes → impact`
> pipeline runs end to end (helm detector, Artifact Hub chart-repository
> resolution, live GitHub and Helm-repository version/changelog fetching, AI
> changelog extraction, and agentic cluster-impact assessment), with a
> matching web UI. [docs/ROADMAP.md](docs/ROADMAP.md) tracks what's next.

## Contents

- [Requirements](#requirements)
- [Quickstart](#quickstart)
- [Contributing](#contributing)
- [Security](#security)
- [Community](#community)
- [Legal](#legal)

## Requirements

kdelta degrades gracefully — `scan`/`versions` need only cluster and network
access; the AI flows need a model credential:

- **Cluster access** — for `scan` (and everything downstream): a kubeconfig
  context when run outside the cluster (your permissions apply), or a
  ServiceAccount when deployed in-cluster. Helm detection reads Helm's release
  Secrets, so the identity needs `get`/`list` on them (see the demo overlay's
  read-only role and the [cluster access model](docs/ARCHITECTURE.md)).
- **A Claude credential** — required for `impact`, and used by `changes` (which
  otherwise falls back to serving the release notes verbatim). Two backends,
  selected automatically (override with `KDELTA_AGENT=claude|api`):
  - **`claude` CLI on PATH** (default when present): bills a **Claude
    subscription**. Authenticate once interactively (`claude`, then `/login`),
    or for servers/CI mint a long-lived token with `claude setup-token` and set
    `CLAUDE_CODE_OAUTH_TOKEN`. The container image ships the CLI.
  - **Claude API** (fallback, or `KDELTA_AGENT=api`): set `ANTHROPIC_API_KEY`
    (it outranks the subscription when both are present). `ANTHROPIC_MODEL`
    overrides the default model on this backend.
- **`GITHUB_TOKEN`** (optional) — raises GitHub API rate limits for version and
  release-note fetches. Unauthenticated works but is rate-limited.

## Quickstart

Run the container image (CLI, API, and web UI in one):

```bash
docker run --rm -p 8080:8080 \
  -v ~/.kube:/home/nonroot/.kube:ro \
  -e CLAUDE_CODE_OAUTH_TOKEN \
  ghcr.io/shadiramadan/kdelta:latest serve --bind :8080
```

Then open <http://localhost:8080> for the web UI. (Drop the `-v`/`-e` flags for
a UI-only tour without cluster or AI access.)

Or install the CLI from source (Go 1.26+ — note this build serves a placeholder
page instead of the web UI; the UI is embedded by `task install` from a clone
or by the container image):

```bash
go install github.com/shadiramadan/kdelta@latest
```

Then, against your current kubeconfig context:

```bash
kdelta scan                                  # what's deployed
kdelta impact helm:cert-manager/cert-manager --to v1.21.1   # blast radius of an upgrade
```

Working from a clone? `task dev` runs the Go server and the UI dev server
together — see [CONTRIBUTING.md](CONTRIBUTING.md) for prerequisites.

## Contributing

Contributions are welcome — code, detectors, and community dataset entries
alike. Start with [CONTRIBUTING.md](CONTRIBUTING.md) for prerequisites, the dev
environment, tests, and linting. This project follows the
[Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md) and
[Conventional Commits](https://www.conventionalcommits.org/).

## Security

Report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/shadiramadan/kdelta/security/advisories/new)
— see [SECURITY.md](SECURITY.md). Please don't open public issues for security
reports.

## Community

Questions, ideas, and show-and-tell live in
[GitHub Discussions](https://github.com/shadiramadan/kdelta/discussions).

## Legal

Licensed under the [Apache License 2.0](LICENSE.md). See [NOTICE](NOTICE) for
attribution. Kubernetes is a registered trademark of The Linux Foundation;
kdelta is not affiliated with or endorsed by the Kubernetes project or any
project it detects.
