import { Button } from "@kdelta/ui/components/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@kdelta/ui/components/card";

const repo = "https://github.com/shadiramadan/kdelta";

const dockerQuickstart = `docker run --rm -p 8080:8080 \\
  -v ~/.kube:/home/nonroot/.kube:ro \\
  -e CLAUDE_CODE_OAUTH_TOKEN \\
  ghcr.io/shadiramadan/kdelta:latest serve --bind :8080`;

const cliTour = `kdelta scan                    # detect deployed resources + versions + upstream links
kdelta resources               # list detected resources from the cached scan
kdelta versions <resource>     # versions since the deployed one (no AI)
kdelta changes  <resource>     # changelog for the version range
kdelta impact   <resource>     # AI-assessed blast radius of the upgrade
kdelta serve                   # ConnectRPC API + embedded web UI`;

const pipeline = [
  {
    stage: "scan",
    question: "What is deployed?",
    detail:
      "Detectors read the cluster — Helm release storage today, more on the roadmap — and emit resources with independently-updatable version streams.",
  },
  {
    stage: "versions",
    question: "What version is it?",
    detail:
      "Deterministic resolution, no AI: upstream identity is verified against public indexes and the deployed chart's own metadata, then versions are enumerated and ordered.",
  },
  {
    stage: "changes",
    question: "What changed upstream?",
    detail:
      "Release notes are fetched verbatim, then structured by a model into a normalized change set — every entry labeled with its provenance and streamed as it extracts.",
  },
  {
    stage: "impact",
    question: "What breaks if you upgrade?",
    detail:
      "An agent cross-references the change set against live cluster state through a confined, read-only tool surface, and reports findings, actions, and honest gaps.",
  },
] as const;

const features = [
  {
    title: "One binary",
    body: "The CLI, the ConnectRPC API server, and the embedded web UI ship together. Commands run against an in-process server by default, or a remote one with --server — same code path either way.",
  },
  {
    title: "Protobuf-first",
    body: "Every contract lives in proto/ with protovalidate rules; the Go server and the TypeScript UI consume generated clients from the same source of truth.",
  },
  {
    title: "Provenance on everything",
    body: "Observed from the cluster, fetched verbatim, or AI-extracted — how each fact was produced is a schema field, not a footnote, so results can be audited.",
  },
  {
    title: "A confined agent",
    body: "Release notes are third-party input. The impact agent runs with built-in tools disabled and reaches the cluster only through a no-secrets allowlisted view — Secrets are never listable and env values are redacted.",
  },
  {
    title: "A cached pipeline",
    body: "Each stage reuses the previous stage's cached output. Change sets are cluster-independent, so an assessment never re-scans, re-resolves, or re-extracts what is already known.",
  },
  {
    title: "Signed releases",
    body: "Container images on GHCR are cosign-signed with SBOMs and build-provenance attestations, published by CI from tagged releases.",
  },
] as const;

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen max-w-4xl flex-col gap-16 px-6 py-10">
      <output className="block rounded-xl border-2 border-dashed border-primary/60 bg-primary/10 px-5 py-4 text-center">
        <p className="text-lg font-semibold tracking-tight">
          🚧 Under construction
        </p>
        <p className="mt-1 text-sm text-muted-foreground">
          kdelta is in early development and this site is a work in progress —
          the{" "}
          <a
            href={`${repo}#readme`}
            className="underline underline-offset-4 hover:text-primary"
          >
            README
          </a>{" "}
          is the canonical reference for now.
        </p>
      </output>

      <header className="space-y-5 pt-6">
        <h1 className="text-6xl font-semibold tracking-tight">
          k<span className="text-primary">Δ</span>
        </h1>
        <p className="max-w-2xl text-xl text-muted-foreground">
          kdelta answers four questions about your Kubernetes cluster:{" "}
          <span className="text-foreground">
            what&apos;s deployed, what version is it, what changed upstream
            since then, and what happens to the rest of the system if you
            upgrade it.
          </span>
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Button asChild>
            <a href={repo}>GitHub</a>
          </Button>
          <Button asChild variant="outline">
            <a href={`${repo}/releases`}>Releases</a>
          </Button>
          <Button asChild variant="outline">
            <a href={`${repo}/discussions`}>Discussions</a>
          </Button>
          <a href={`${repo}/releases/latest`}>
            {/* eslint-disable-next-line @next/next/no-img-element -- static badge */}
            <img
              src="https://img.shields.io/github/v/release/shadiramadan/kdelta"
              alt="Latest kdelta release"
              className="h-5"
            />
          </a>
        </div>
      </header>

      <section className="space-y-4">
        <h2 className="text-2xl font-semibold tracking-tight">How it works</h2>
        <p className="text-sm text-muted-foreground">
          A four-stage pipeline; each stage caches its output for the next.
          Everything before <code className="font-mono">impact</code> is
          deterministic or provenance-labeled.
        </p>
        <div className="grid gap-4 sm:grid-cols-2">
          {pipeline.map((step, index) => (
            <Card key={step.stage}>
              <CardHeader>
                <CardTitle className="flex items-baseline gap-2 font-mono text-base">
                  <span className="text-muted-foreground/60">{index + 1}</span>
                  {step.stage}
                </CardTitle>
                <CardDescription>{step.question}</CardDescription>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground">{step.detail}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <h2 className="text-2xl font-semibold tracking-tight">
          Getting started
        </h2>
        <p className="text-sm text-muted-foreground">
          Run the container image — CLI, API, and web UI in one — then open{" "}
          <code className="font-mono">http://localhost:8080</code>. Drop the{" "}
          <code className="font-mono">-v</code>/
          <code className="font-mono">-e</code> flags for a UI-only tour
          without cluster or AI access.
        </p>
        <pre className="overflow-x-auto rounded-xl border border-border bg-card/60 p-4 font-mono text-sm text-primary">
          {dockerQuickstart}
        </pre>
        <p className="text-sm text-muted-foreground">
          The CLI runs the same pipeline against your current kubeconfig
          context. <code className="font-mono">scan</code> and{" "}
          <code className="font-mono">versions</code> need only cluster and
          network access; <code className="font-mono">impact</code> needs a
          Claude credential (a subscription via the{" "}
          <code className="font-mono">claude</code> CLI, or an API key).
        </p>
        <pre className="overflow-x-auto rounded-xl border border-border bg-card/60 p-4 font-mono text-sm">
          {cliTour}
        </pre>
        <p className="text-sm text-muted-foreground">
          Installing from source? <code className="font-mono">go install
          github.com/shadiramadan/kdelta@latest</code> builds the CLI and API
          (the web UI is embedded by container builds and{" "}
          <code className="font-mono">task install</code> from a clone). See{" "}
          <a
            href={`${repo}/blob/main/CONTRIBUTING.md`}
            className="underline underline-offset-4 hover:text-primary"
          >
            CONTRIBUTING.md
          </a>{" "}
          for the dev environment.
        </p>
      </section>

      <section className="space-y-4">
        <h2 className="text-2xl font-semibold tracking-tight">
          What&apos;s underneath
        </h2>
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((feature) => (
            <Card key={feature.title}>
              <CardHeader>
                <CardTitle className="text-base">{feature.title}</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground">{feature.body}</p>
              </CardContent>
            </Card>
          ))}
        </div>
        <p className="text-sm text-muted-foreground">
          The full picture — trust boundaries, the detector seam, cache
          invalidation — lives in{" "}
          <a
            href={`${repo}/blob/main/docs/ARCHITECTURE.md`}
            className="underline underline-offset-4 hover:text-primary"
          >
            ARCHITECTURE.md
          </a>
          ; what&apos;s deliberately not built yet lives in{" "}
          <a
            href={`${repo}/blob/main/docs/ROADMAP.md`}
            className="underline underline-offset-4 hover:text-primary"
          >
            ROADMAP.md
          </a>
          .
        </p>
      </section>

      <footer className="space-y-1 border-t border-border pt-6 text-xs text-muted-foreground/70">
        <p>
          Apache-2.0 · kdelta.dev is the documentation home for the kdelta
          project — documentation is under construction.
        </p>
        <p>
          Kubernetes is a registered trademark of The Linux Foundation; kdelta
          is not affiliated with or endorsed by the Kubernetes project or any
          project it detects.
        </p>
      </footer>
    </main>
  );
}
