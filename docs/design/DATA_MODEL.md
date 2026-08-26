# Data model

Rationale for the protobuf contracts in [`proto/kdelta/v1/`](../../proto/kdelta/v1/).
The protos are the source of truth; this document records *why* they look the
way they do.

## The pipeline

```
scan     →  Resource (identity + version streams)     detectors emit these
versions →  Version[] per stream                      deterministic, no AI
changes  →  ChangeSet (normalized)                    fetch → extract → generate
impact   →  ImpactAssessment                          agent over inventory + changes
```

Every stage's output is a typed message — and every stage's *input recipe* is
one too (`VersionSource`, `ChangeSource`). Recipes are data, not code: a
detector, a community-dataset entry, or a user override can all supply them,
and the same resolver machinery executes them. That is the flexibility
mechanism for the whole system.

## Identity: two layers

- **`ResourceRef`** identifies a detected thing *in a cluster*:
  `detector:namespace/name` (`helm:cert-manager/cert-manager`); cluster-scoped
  resources omit the namespace. Structured message, canonical string form.
- **`PackageKey`** identifies the *upstream* project or artifact globally:
  `(system, name, registry_url)`. Versions and changelogs attach here — they
  are cluster-independent, which is what makes them cacheable and exportable
  to the community dataset.

`registry_url` exists because names are only unique per distribution point,
and it is allowed to be empty because a running cluster does not always record
where an artifact came from (an installed release keeps its chart metadata but
not its repository). Resolving an empty `registry_url` is a first-class step,
run by `Scan` after the detectors: today a public index lookup (Artifact Hub
candidates, each verified against its repository index and the deployed chart
metadata — see the upstream-resolution section of ARCHITECTURE.md); dataset
profile matching and source-link heuristics remain planned tiers.

## Version streams

A resource does not have *a* version; it has independently-updatable
**streams** (`VersionStream`): the packaging revision, the packaged
application, each container image, a tracked git revision. Each stream carries
its own current value, an optional desired-state `constraint` (a target
revision or version range the cluster tracks, distinct from what is resolved),
a `VersioningScheme`, one `VersionSource`, an ordered `ChangeSource`
fallback chain, and a `source_provenance` recording how the version source
was determined (detector-observed vs resolved from a public index) — distinct
from the deployed value's own provenance. The first stream on a resource is its primary one.

Ordering is deliberately separated from fetching: sources return unordered
strings; the scheme alone defines parsing, ordering, and stability. Git
revisions get a scheme with no total order rather than pretending.

## Kubernetes conventions, not Kubernetes imports

The SDKs used by detectors stop at the detector boundary — the contracts never
import Kubernetes generated protos. Instead the messages mirror the
conventions: `KubernetesObjectRef` (apiVersion/kind/namespace/name/uid),
hierarchy edges with ownership semantics (`OWNS`/`DEPENDS_ON`/`PART_OF`),
label maps, three-valued `Condition`s, and label-selector filters on scan.

## Changes: one normalized shape

Whatever a `ChangeSource` yields — published release notes, a changelog file,
structured chart annotations, an upgrade guide, a source diff — extraction
lands in `ChangeSet → VersionChanges → Change`. Two fields carry most of the
design weight:

- **`affected_paths`**: the concrete configuration surfaces a change touches
  (field paths, flags, value paths). This is the join key impact analysis uses
  to match changes against what a cluster actually sets.
- **`Provenance`** (shared with every other stage): `OBSERVED`, `FETCHED`,
  `AI_EXTRACTED`, `AI_GENERATED`, `DATASET`, or `USER_PROVIDED`, plus source
  URL and model. AI-derived content is labeled in the schema itself, and
  impact findings inherit the trust lineage of their evidence.

`ChangeType` extends the conventional changelog vocabulary
(added/changed/fixed/security/deprecated/removed) with three upgrade-specific
types: `BREAKING`, `DEFAULT_CHANGED` (a default or runtime behavior flips with
no spec change — the archetypal silent-impact case), and
`MIGRATION_REQUIRED`.

## Version ranges

A concrete upgrade is a `from`/`to` pair (`ChangeSet`). *Applicability* —
"this migration guide matters for any upgrade crossing X" — is a
`VersionRange`: boundary events (`INTRODUCED`, `FIXED` exclusive,
`LAST_AFFECTED` inclusive) interpreted under a declared scheme. Events express
what pairs cannot: open-ended ranges ("changed in 1.18, never reverted") and
multiple intervals, while staying scheme-agnostic. Membership is a walk over
events in scheme order.

## Impact

`ImpactAssessment` is findings plus honesty: each `ImpactFinding` names the
affected resource, severity and likelihood, the `ChangeEvidence` that drives
it (which change, which matched paths), and `RecommendedAction`s with
before/after-upgrade sequencing. `InformationGap` lists what the analysis
could not see — the hook for agentic escalation (gather what is named, then
re-assess) and the reason a confident-sounding report can be audited.

## Service surface

Fast lookups (`Scan`, `ListResources`, `GetResource`, `ListVersions`) are
unary. `GetChanges` and `AssessImpact` are server-streaming because they can
be slow and agentic, and both the CLI and the UI want live status. Their event
oneof carries `Progress` updates during the run and the terminal `result`;
`GetChanges` additionally emits a `partial` `VersionChanges` per extraction
batch, so clients render the changelog incrementally instead of waiting for
the whole set.

The full pipeline is implemented. `Echo` remains only as a liveness
placeholder. What is still unimplemented is narrower: version sources other
than GitHub releases and Helm repository indexes (git tags, OCI registries,
container tags) return `Unimplemented` from `ListVersions`; change sources
other than GitHub release notes (changelog files, chart annotations, URL
templates, migration guides, source-diff generation) make `GetChanges` report
`FailedPrecondition`; and a stream whose upstream could not be resolved
reports `FailedPrecondition` naming the streams that can be resolved instead.

## Validation philosophy

Validate the obvious at the boundary — ref grammar, URL shape, bounded
numbers, non-empty required strings, required oneofs — via protovalidate,
enforced by the server interceptor and checked by `buf lint`/CI. Semantic
rules that are still settling (version-string grammar per scheme, path
syntax in `affected_paths`) stay unconstrained until the model hardens.
