# AGENTS.md

Guidance for AI coding agents (and new humans) working on kdelta.

kdelta is a Kubernetes CLI + service that detects deployed resources, resolves
their versions, fetches changelogs between versions, and assesses upgrade
impact. One Go binary contains the cobra CLI, a ConnectRPC server, and an
embedded Next.js static-export UI.

Read these before non-trivial work:

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — target design, client/server
  model, domain model, AI boundaries.
- [docs/design/DATA_MODEL.md](docs/design/DATA_MODEL.md) — rationale for the
  protobuf contracts; read before changing anything in `proto/`.
- [docs/ROADMAP.md](docs/ROADMAP.md) — the living list of unimplemented work.
  Remove items you ship; add ideas you defer. Never turn it into a changelog.
- [docs/CODING_STANDARDS.md](docs/CODING_STANDARDS.md) — Go/TS/proto standards,
  comment policy (self-describing code; comments only for the non-obvious,
  except exported APIs which always get doc comments).
- [CONTRIBUTING.md](CONTRIBUTING.md) — prerequisites, dev environment, tests.

## Hard rules

1. **Never commit or push unless explicitly asked.** Leave the working tree for
   the human to review.
2. **Protobuf first.** All API and data-model changes start in `proto/`, then
   `task generate`. Never hand-edit `gen/` or `packages/api/src/gen/` —
   generated code is committed and CI fails on drift (`task generate:check`).
3. **The Taskfile is the only entry point** for build/test/lint/generate — the
   same targets run locally and in CI. Don't invent parallel invocations of
   `go build`, `next build`, `buf generate`, etc.; if a step is missing, add a
   task.
4. **Conventional commits** (`feat:`, `fix:`, `docs:`, …) — release-please
   derives releases from them.
5. **Check current docs for pinned versions.** Dependency versions are pinned in
   `go.mod` / `package.json`; consult documentation matching those versions
   (they move fast: Next.js, Tailwind v4, connect-es v2, buf v2 configs). When
   adding a dependency, use the latest stable release.
6. **Tests are behavioral.** Drive the real surface and assert on what a user
   observes — rendered output, exit status, cache effects — not on internal
   helpers. `internal/servertest` starts a real server over HTTP with a seeded
   cache, a fake upstream forge, and a scripted agent runner; `cmd/cli_test.go`
   runs actual CLI invocations against it. Extend that harness rather than
   mocking internals.
7. **Verify UI work in a browser.** Changes to `apps/app-ui` are checked by
   running `task dev` and driving the page (including the console and network),
   never by asking the human to look.

## Commands

| Command | What it does |
| --- | --- |
| `task dev` | Run the Go server and the Next.js dev server in parallel |
| `task build` | Build the UI, embed it, and build `bin/kdelta` |
| `task install` | `task build` then `go install` the binary |
| `task serve` | Build then run `kdelta serve` |
| `task generate` | buf codegen (Go + TypeScript) from `proto/` |
| `task lint` | golangci-lint, buf lint, UI lint |
| `task fmt` | Format Go and proto sources |
| `task test` | All tests with coverage output |
| `task scan:cve` | syft SBOM + grype CVE scan (fails on high/critical) |
| `task hooks:install` | Install the pre-commit hook (lint + test via Taskfile) |

## Repo map

| Path | Contents |
| --- | --- |
| `cmd/` | cobra commands (thin — logic lives behind the RPC client) |
| `internal/server/` | ConnectRPC handlers, HTTP mux, static UI serving |
| `internal/client/` | RPC client; in-process unix-socket default, `--server` remote |
| `internal/ui/` | go:embed of the built web UI |
| `internal/store/` | SQLite cache (scan snapshots, versions, changes, impact) |
| `internal/ref/` | canonical ResourceRef string form (parse/format) |
| `internal/detect/` | detector interface + registry; `detect/helm` reads release storage |
| `internal/kube/` | cluster connection resolution (kubeconfig or in-cluster) |
| `internal/upstream/` | deterministic fetchers (releases, chart indexes, Artifact Hub) behind the egress guard |
| `internal/resolve/` | post-detect upstream identity resolution (Artifact Hub + index verification) |
| `internal/vers/` | version-string ordering per scheme |
| `internal/agent/` | model-driven flows behind the swappable `Runner` seam |
| `gen/` | generated Go (do not edit) |
| `proto/` | protobuf sources — the source of truth for all contracts |
| `apps/app-ui/` | served UI (Next.js static export, embedded in the binary) |
| `apps/marketing-ui/` | docs/marketing site for kdelta.dev (GitHub Pages, stub) |
| `packages/api/` | generated TypeScript client (do not edit `src/gen/`) |
| `packages/theme/` | Tailwind v4 design tokens (shadcn-compatible CSS variables) |
| `packages/ui/` | shared shadcn/ui components (`shadcn add` targets this package) |
| `infra/` | kustomize base + overlays (skaffold.yaml sits at the repo root) |
| `hack/` | demo cluster script + cert-manager fixtures, sops public keys |
| `data/` | (planned) community dataset of upstream links/changelogs |

## Secrets

Secrets are sops-encrypted with PGP and rendered by ksops at build time. The
convention is a plaintext file that is git-ignored beside its committed
ciphertext sibling:

```text
infra/kustomize/overlays/<overlay>/secrets/<class>/name.env      # git-ignored plaintext
infra/kustomize/overlays/<overlay>/secrets/<class>/name.enc.env  # committed ciphertext
```

`<class>` selects the key that encrypts it (see `.sops.yaml`).
`task sops:encrypt` regenerates every `.enc.env` from its plaintext sibling;
`task sops:decrypt` reverses it. Never commit a plaintext `.env`, and never
paste secret values into code, docs, or commit messages.

## Documentation conventions

- **Diagram the boundaries.** Architecture and interface docs should carry
  mermaid diagrams where a picture beats prose — component/data flow, the
  seams new code plugs into (detector registry, `agent.Runner`), trust
  boundaries, and cache/invalidation flow. Keep labels free of angle brackets,
  and verify a diagram parses rather than assuming it renders.
- **Write technically.** Reviews, findings, and docs are technical writing:
  state the observed behavior, the mechanism, and the consequence. Prefer
  specifics (`file:line`, the actual command, the real error) over
  characterization, and drop adjectives that carry no information.
- **Keep docs true to the code.** A claim about what is implemented is a claim
  that can go stale — check it against the code before repeating it, and fix it
  when it drifts.

## MCP

kdelta already serves one MCP surface: the hidden `kdelta agent-tools`
command exposes the agent's restricted, no-secrets tool set
(`list_inventory`, `list_cluster_objects`) over MCP stdio, which is how the
`claude` CLI backend reaches cluster state.

The roadmap item is the *full* API over MCP — `Scan`, `ListVersions`,
`GetChanges`, `AssessImpact` — so agents can drive kdelta end to end during
development and use. Until that lands, exercise those RPCs through the CLI or
`buf curl`/HTTP against `/rpc/`.
