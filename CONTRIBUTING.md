# Contributing to kdelta

Thanks for contributing! This project follows the
[Contributor Covenant Code of Conduct](CODE_OF_CONDUCT.md) and uses
[Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`,
`docs:`, …) — release-please cuts releases from them. Unimplemented features and
ideas live in [docs/ROADMAP.md](docs/ROADMAP.md); check it before starting
something new, and update it in your PR when you ship or defer roadmap items.

## Contents

- [Prerequisites](#prerequisites)
- [Getting started](#getting-started)
- [Making changes](#making-changes)
- [Tests](#tests)
- [Linting & formatting](#linting--formatting)
- [Community dataset & AI contribution guidelines](#community-dataset--ai-contribution-guidelines)

## Prerequisites

| Tool | Used for | Install (macOS) |
| --- | --- | --- |
| [Go](https://go.dev/) 1.26+ | CLI + server | `brew install go` |
| [Node.js](https://nodejs.org/) 24 (LTS) | web UI | `nvm install` (reads [.nvmrc](.nvmrc)) |
| [pnpm](https://pnpm.io/) | JS workspace | `corepack enable` or `brew install pnpm` |
| [Task](https://taskfile.dev/) | all dev commands | `brew install go-task` |
| [buf](https://buf.build/) | protobuf codegen/lint | `brew install bufbuild/buf/buf` |
| [golangci-lint](https://golangci-lint.run/) v2 | Go linting | `brew install golangci-lint` |
| [Docker](https://www.docker.com/) | images, local cluster | Docker Desktop / OrbStack |
| [kind](https://kind.sigs.k8s.io/) | local Kubernetes | `brew install kind` |
| [skaffold](https://skaffold.dev/) | cluster dev loop | `brew install skaffold` |
| [sops](https://github.com/getsops/sops) + [GnuPG](https://gnupg.org/) | infra secrets (PGP keys) — only for `infra/` work | `brew install sops gnupg` |
| [kustomize](https://kustomize.io/) + [ksops](https://github.com/viaduct-ai/kustomize-sops) | rendering overlays with encrypted secrets — only for `infra/` work | `brew install kustomize` + ksops install script |

## Getting started

```bash
git clone https://github.com/shadiramadan/kdelta.git
cd kdelta
task setup        # pnpm install + git pre-commit hooks
task dev          # Go API server + Next.js dev server, in parallel
```

`task dev` starts the ConnectRPC server on `:8080` and the UI dev server on
`:3000`; the UI proxies `/rpc/*` to the Go server, so open
<http://localhost:3000>. The docs/landing site (`task dev:docs`) runs
separately on `:3001`. `task --list` shows every available target.

Default ports: `8080` kdelta server (API under `/rpc/`, embedded UI at `/`),
`3000` app-ui dev server, `3001` docs-ui dev server.

Other everyday targets:

```bash
task build        # build the UI, embed it, compile bin/kdelta
task install      # task build + go install
task serve        # build everything, then run `kdelta serve` (UI at :8080)
task generate     # regenerate Go + TypeScript from proto/
```

## Making changes

- **API or data-model changes start in `proto/`.** Run `task generate` and
  commit the generated code (`gen/`, `packages/api/src/gen/`) — CI fails if it
  drifts (`task generate:check`). Never edit generated files by hand.
- Follow [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) — self-describing
  code, comments only for the non-obvious, doc comments on exported APIs.
- Keep PRs small and focused, with a conventional-commit title.

## Tests

```bash
task test         # Go tests with coverage written to coverage/ (UI suite: see roadmap)
```

New behavior needs tests; bug fixes need a regression test.

Tests are **behavioral**: drive the real surface and assert on what a user
observes, rather than on internal helpers. `internal/servertest` starts a real
server over HTTP with a seeded cache, a fake upstream forge, and a scripted
agent runner; `cmd/cli_test.go` runs actual CLI invocations against it and
asserts on rendered stdout/stderr, exit errors, and cache behavior. Prefer
extending that harness over mocking internals.

## Linting & formatting

```bash
task lint         # golangci-lint + buf lint + UI lint
task fmt          # gofumpt/goimports + buf format
task scan:cve     # syft SBOM + grype CVE scan (fails on high/critical)
```

The pre-commit hook (installed by `task setup` or `task hooks:install`) runs the
same lint and test targets as CI — everything is reproducible locally via the
Taskfile.

## Community dataset & AI contribution guidelines

kdelta will keep cacheable upstream data — source repo links, changelog
locations, version lists — in this repo (planned `data/` directory) instead of a
centralized service, seeded with top CNCF projects. Until the layout and the
`kdelta export` command land (see the roadmap), the guidelines below govern any
dataset-shaped contribution:

- **Link to the authoritative source**: the project's own repo, release page, or
  changelog — not mirrors or blogs.
- **Prefer official changelogs.** Only fall back to summarizing source diffs
  when a project publishes no changelog.
- **AI-generated content must be labeled as such** (e.g. a changelog synthesized
  from diffs), and you must verify every referenced version/tag/URL actually
  exists before submitting.
- **Never fabricate entries.** An empty entry is better than a guessed one.

AI-assisted code contributions are welcome and follow the same bar as any other
change: read [AGENTS.md](AGENTS.md), meet the coding standards, include tests,
and review everything you submit as if you wrote it by hand — you own it.
