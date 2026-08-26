# Roadmap

This is a **living document** listing unimplemented features and ideas for kdelta.
Items are removed when they ship — this file does not track history or completed
work (git history and release notes do that). Discuss items or propose new ones in
[GitHub Discussions](https://github.com/shadiramadan/kdelta/discussions).

## Contents

- [Highlights](#highlights)
- [Detection & scan](#detection--scan)
- [Versions (no AI)](#versions-no-ai)
- [Changelog & impact (AI-assisted)](#changelog--impact-ai-assisted)
- [CLI & client](#cli--client)
- [Server, API & storage](#server-api--storage)
- [Continuous & scheduled deltas](#continuous--scheduled-deltas)
- [Reliability & operability](#reliability--operability)
- [Web UI](#web-ui)
- [Community dataset](#community-dataset)
- [Marketing / docs site](#marketing--docs-site)
- [Infra & deployment](#infra--deployment)
- [Developer experience](#developer-experience)

## Highlights

The near-term themes that shape everything below — the high-impact bets for the
project's next phase:

- **More detectors, one clean seam.** Broaden coverage beyond Helm — ArgoCD
  `Application`s, container images, and `app.kubernetes.io/*` labels — behind
  the existing `Detector`/`Registry` interface so each new source is additive.
  This is the single highest-leverage direction: every downstream feature
  (versions, changelogs, impact) inherits new detectors for free.
- **Delta as a service: scheduled & continuous.** Move from one-shot commands to
  a running service that re-scans on a schedule, tracks version drift over time,
  and surfaces *new* deltas (a freshly published breaking release affecting a
  deployed resource) — via notifications, a feed, and history. Turns kdelta from
  a tool you run into a system that watches.
- **Harden the agent for untrusted input at scale.** Evolve the `agent.Runner`
  seam from the in-process confined backend to the sandboxed Kubernetes
  agent-sandbox backend, so model-driven analysis of attacker-influenced
  changelogs runs with OS-level isolation, not just tool confinement.
- **Service reliability & operability.** Make the server production-grade:
  structured logging, metrics/tracing (OpenTelemetry), health/readiness beyond
  the gRPC probe, graceful degradation when upstreams or the model are
  unavailable, request rate-limiting and a model-spend ceiling, and retry/backoff
  on upstream fetches. Prerequisite for any hosted deployment.
- **Trustworthy results.** Provenance is modeled but under-surfaced — make
  AI-derived vs. verbatim vs. observed visible everywhere, add confidence
  signaling, and let the community dataset supply human-reviewed changelogs that
  supersede AI extraction. Impact assessments are only as useful as their
  credibility.
- **Multi-cluster & scale.** Named cluster connections, cluster identity in refs
  and scans, and a Postgres-backed store so a single kdelta can watch a fleet.

## Detection & scan

New detectors follow the path in
[design/DETECTORS.md](design/DETECTORS.md).

- Helm detector depth: image streams and hierarchy edges from rendered
  release manifests; structured changes from chart annotations; subchart
  dependency edges.
- Deeper upstream resolution (the Artifact Hub lookup with index/metadata
  verification shipped in `internal/resolve`): community-dataset profiles
  consulted ahead of the public lookup, `sources`/`home` heuristic fallbacks,
  non-helm systems (images/OCI) reusing the `upstream_resolutions` cache,
  bounded concurrency for fleet-scale scans, negative-cache backoff with a
  force-re-resolve escape hatch, and priming the chart version list from the
  verification's index fetch instead of refetching on first `versions` use.
- Surface resolution provenance in CLI/UI: `VersionStream.source_provenance`
  now records how a version source was determined (detector-observed vs
  resolved), but nothing renders it yet.
- ArgoCD detector: discover `Application` resources, their revisions
  (`spec.source.targetRevision` → `status.sync.revision`), sync/health state,
  and source repos.
- Kubernetes labels detector: `app.kubernetes.io/name|version|part-of|managed-by`
  and friends on workloads.
- Image detector: container image tags/digests and OCI
  `org.opencontainers.image.*` annotations (source, revision, url, documentation).
- Resource hierarchy: model parent/child and part-of relationships (e.g. ArgoCD
  depends on Redis, which can be updated independently; an Argo app owns charts
  which own images).
- Every detection must link back to source code or releases with a changelog.
- Label-selector filtering during scan.
- Source-resolver registry mirroring the detector seam: today `fetchVersions`
  and `fetchReleaseNotes` switch on the `VersionSource`/`ChangeSource` oneof
  inside `internal/server`. Move dispatch behind registered `VersionResolver` /
  `ChangeResolver` implementations so a new source type is additive, the way a
  new detector is.
- Shared cross-detector emission helpers in `internal/detect`: ref/object-ref
  construction, provenance stamping, and the safe-link gate are currently
  helm-local and will be copy-pasted by the next detector.

## Versions (no AI)

- More version source resolvers: git tags, OCI registries, container image
  registries (GitHub releases and Helm repository indexes exist).
- Version taxonomy: normalize and relate the different kinds of "version" — Helm
  chart version vs. app version, ArgoCD app revision, kustomize ref, image tag.
- Prerelease handling is inconsistent between counting and listing: the
  behind-count and `latest` deliberately consider only stable releases, but
  `kdelta versions` and the UI's from/to pickers list prereleases inline and
  unlabeled (`v1.21.0-alpha.0` sits between stable entries). Either mark them
  in both surfaces or add an opt-in `--prerelease` / UI toggle that defaults to
  stable-only, so "22 stable versions behind" and the picker agree.
- Upgrade-distance in terminal output: `kdelta scan`/`resources` listings
  showing versions-behind per resource (the web Resources view and
  `kdelta versions` report it today).

## Changelog & impact (AI-assisted)

- More change sources: `CHANGELOG.md` file parsing, chart annotations, URL
  templates, migration-guide extraction (release-notes fetching exists).
- Changelog generation from source diffs when a project publishes no
  changelog.
- Adopt a native Go Agent SDK for the `agent.Runner` CLI backend if/when
  Anthropic ships one, replacing the `claude -p` subprocess.
- Structured harness outputs: move extraction to `--output-format json` +
  `--json-schema` (schema-enforced `structured_output`) instead of prompt
  contracts.
- Run agentic flows in a Kubernetes agent sandbox
  (kubernetes-sigs/agent-sandbox): isolate model-driven gathering in
  sandboxed pods behind the same `agent.Runner` seam.
- Determinate progress: `Progress` carries only `stage` + a prose `message`,
  so counts live in the text ("6/22"). Add `completed`/`total` fields so the UI
  can render a real progress bar and the CLI a charm progress bar, instead of a
  scrolling log.
- Widen the impact agent's tool surface as detectors land (today: inventory +
  the restricted no-secrets cluster lister).

## CLI & client

- Charm-based terminal rendering (tables, spinners, styled output) for scan,
  versions, changes, and impact.
- `kdelta export`: package locally cached scan/versions/changelog data into a
  contribution to the community dataset.
- `--output yaml` machine-readable output (protobuf JSON via `--output json`
  exists on every command).

## Server, API & storage

- Postgres-swappable storage backend behind the store API: extract a
  `store.Cache` interface and make sqlite/postgres sibling implementations, so
  the swap does not thread through every call site.
- Declare the confined agent tool set once and make `ServeMCPTools`
  transport-agnostic: the allowlist and tool descriptions are currently stated
  in both `anthropic.go` (in-process runner) and `mcptools.go` (MCP stdio), and
  the two must not drift.
- `internal/cluster` package owning named cluster connections, as the seam the
  multi-cluster work below builds on.
- Retire the `Echo` placeholder RPC and CLI command.
- Expose the full API over MCP for an agent-friendly developer loop (the
  restricted agent tool surface is already served over MCP stdio by
  `kdelta agent-tools`; scan/versions/changes/impact RPCs remain).
- Cluster connections: multi-cluster support — named cluster configs on the
  server, cluster identity modeled in scan requests and resource refs, and a
  cluster picker in CLI/UI (scans are single-cluster, current-context today).
- Auth & abuse limits: the demo relies on Cloudflare Access at the edge and
  the server itself is unauthenticated. Add first-class API authentication
  (token/OIDC) and per-caller authorization — reads vs. scan triggers vs.
  agentic actions. Cloudflare Access is a coarse allow-gate, not a rate limit:
  add a rate-limiting/cost-ceiling interceptor and a global cap on concurrent
  agent runs so an authenticated caller cannot drain the operator's Claude
  subscription or GitHub quota by looping `AssessImpact`/`GetChanges` (each
  spawns a paid model run) or hammering `Scan`. Enforce the intended
  "internet-facing demo is read-only" by gating the state-changing/paid RPCs
  behind a capability the demo overlay disables. (Request bodies and prompt
  inputs are already bounded via `WithReadMaxBytes` + protovalidate.)
- Egress NetworkPolicy for the in-cluster deployment: default-deny egress with
  an allowlist for DNS, the kube-apiserver, and the required external HTTPS
  endpoints, explicitly denying the cloud metadata IP (169.254.169.254) and
  in-cluster ranges to contain a compromised/prompt-injected agent. Requires a
  NetworkPolicy-enforcing CNI (kind's default kindnet does not enforce it) and,
  for hostname-scoped egress to api.github.com/api.anthropic.com, an
  FQDN-policy CNI (e.g. Cilium); vanilla NetworkPolicy is CIDR-only. Note
  scan-time upstream resolution adds artifacthub.io and dynamically
  discovered chart-repository hosts to the egress set, so the FQDN allowlist
  needs a policy decision (any-https-chart-repo vs a curated list).

## Continuous & scheduled deltas

Today every command is one-shot and pull-based. The high-impact shift is a
running service that watches for deltas over time:

- Scheduled re-scans: a configurable interval (or cron) that refreshes the scan,
  version lists, and — for tracked resources — changelogs and impact, keeping
  the cache warm and the UI live without manual runs.
- Drift & new-delta detection: diff successive scans and version lists to surface
  what *changed* — a newly published upstream release that affects a deployed
  resource, a resource that fell further behind, a newly breaking upgrade path.
- Notifications: emit new high-severity deltas to Slack/webhook/email so an
  operator learns about a breaking cert-manager release without polling.
- History: retain scan/version/impact snapshots over time (the store already
  keys by scan generation) to answer "when did this drift appear" and to trend
  upgrade debt across a fleet.

## Reliability & operability

Make the server production-grade before any hosted, multi-tenant, or scheduled
deployment:

- Structured logging (slog) with request/scan correlation IDs across the RPC,
  detector, upstream, and agent layers — there is minimal logging today.
- Metrics and tracing (OpenTelemetry): RPC latencies, detector/upstream/model
  call durations and error rates, cache hit rates, agent tool-call counts.
- Health beyond liveness: a readiness signal that reflects store and
  cluster-connection health, not just process up.
- Graceful degradation: clear, typed partial results when an upstream, the
  cluster, or the model is unavailable, instead of a failed stream — `changes`
  already falls back to verbatim notes; extend the pattern.
- Resilient upstream fetches: retry with backoff and honoring
  GitHub/Artifact Hub/registry rate-limit headers in `upstream.Client`.

## Web UI

- Impact views: the per-resource rollup, plus an alternative grouping by
  impact cause with affected resources nested under each cause.
- Grow `packages/ui` as real views land: inputs, tables, dialogs, badges —
  added via `shadcn add` into the package.
- Dependency/hierarchy graph with XYFlow.
- Componentize `apps/app-ui/app/resource/page.tsx`: it holds several
  presentational components inline (`ChangeSetView`, `ImpactView`,
  `ProgressLog`, `InlineText`, `VersionChangesCard`). Move the reusable ones
  into `packages/ui` or a local `components/` directory and introduce a hooks
  layer, so new views stop growing the page file.
- Eliminate the Resources view's per-row `listVersions` fan-out: `BehindBadge`
  issues one request per row, which will not scale past a small inventory.
  Serve versions-behind from the scan/list response instead.
- Restructure `packages/theme` into the conventional two-layer shadcn shape
  (light tokens on `:root`, overrides under `.dark`).
- Render agent prose as markdown: model output contains lists, bold, and links,
  but `InlineText` handles only backtick code spans, so the rest shows as
  literal characters.
- Derive the streaming envelope type in `lib/streamed.ts` from the generated
  response message rather than the hand-written `StreamEvent` interface, which
  can silently drift from the proto.

## Community dataset

- A committable, contribution-friendly repo layout (`data/`) for cacheable
  scan/versions/changelog data, in lieu of a centralized API service.
- Seed with top CNCF projects: source repo links, release/changelog locations.
- Dataset profiles supplying chart repository URLs, consulted ahead of the
  Artifact Hub lookup in `internal/resolve`.
- AI contribution guidelines for dataset entries (documented in CONTRIBUTING.md).
- CI validation for dataset contributions.

## Marketing / docs site

- Real documentation content in `apps/marketing-ui`: getting started,
  detectors, changelog/impact guides.
- GitHub Pages deploy workflow, `kdelta.dev` custom domain (CNAME).
- Live internal demo at `demo.kdelta.dev` via the Cloudflare tunnel overlay.

## Infra & deployment

- Give release-please a PAT/GitHub App token so its releases trigger the
  publish workflow automatically (default `GITHUB_TOKEN` events are
  suppressed; until then `publish` is run manually from the release tag).
- Publish the container image to GitHub Packages so the demo cluster pulls
  released images instead of side-loaded dev builds, and pin the deployment to
  an image digest (`@sha256:…`) rather than the mutable `:latest` tag once a
  registry is in use.
- Multi-arch (amd64 + arm64) image builds — the demo node is amd64 while dev
  machines are arm64.
- Smoke-test the skaffold + kind loop in CI.
- Supply-chain pinning: pin Dockerfile base images by digest (they float on
  `:latest`/rolling tags today), pin or checksum the `claude` CLI installer
  (currently `curl | bash` of an unversioned script), and pin third-party
  GitHub Actions to commit SHAs instead of mutable major-version tags — with
  Dependabot to keep them current. Verify cosign signatures at deploy time.
- Production RBAC hardening: the demo grant includes cluster-wide secret reads
  because Helm stores releases as Secrets and RBAC cannot filter by type —
  offer a secret-free mode (reduced helm detection) and per-overlay
  least-privilege reviews.

## Developer experience

- Coverage thresholds and a coverage badge.
- `docs/TESTING.md`: document the behavioral-testing approach, the reusable
  `internal/servertest` harness, and the fixture conventions, so contributors
  extend the harness instead of mocking internals.
- kind-based end-to-end test harness with seeded fixtures (ArgoCD, sample Helm
  releases).
- Flagship demo scenario: helm detector + versions/changes/impact against the
  demo cluster's intentionally old cert-manager v1.17.2 — the fixtures in
  `hack/demo/` are built to trip real 1.18–1.21 changelog findings.
- Web UI test suite (component + a Playwright smoke test against the embedded
  build).
- Adopt oxfmt (OXC's Prettier-compatible formatter, currently beta) for
  TS/CSS formatting once stable — pairs with the oxlint setup.
- Consider `oxlint --type-check` replacing the separate `tsc --noEmit`
  typecheck step once we trust its TS7-semantics coverage.
- Migrate to pnpm 11 (Node 22+, settings move into pnpm-workspace.yaml, new
  supply-chain defaults like `minimumReleaseAge`).
