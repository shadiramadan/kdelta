import { Button } from "@kdelta/ui/components/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@kdelta/ui/components/card";

const quickstart = `go install github.com/shadiramadan/kdelta@latest
kdelta scan          # what's deployed, and how far behind is it?
kdelta serve         # web UI + API`;

const docSections = [
  {
    title: "Getting started",
    body: "Install the CLI, point it at a cluster, run your first scan.",
  },
  {
    title: "Detectors",
    body: "How kdelta finds ArgoCD apps, Helm releases, labels, and images.",
  },
  {
    title: "Changelogs & impact",
    body: "From version delta to changelog to blast-radius analysis.",
  },
] as const;

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col justify-center gap-12 px-6 py-16">
      <header className="space-y-4">
        <h1 className="text-5xl font-semibold tracking-tight">
          k<span className="text-primary">Δ</span>
        </h1>
        <p className="max-w-xl text-lg text-muted-foreground">
          What&apos;s deployed in your cluster, what version is it, what changed
          upstream since then — and what breaks if you upgrade it.
        </p>
        <div className="flex gap-3">
          <Button asChild>
            <a href="https://github.com/shadiramadan/kdelta">GitHub</a>
          </Button>
          <Button asChild variant="outline">
            <a href="https://github.com/shadiramadan/kdelta/discussions">
              Discussions
            </a>
          </Button>
        </div>
      </header>

      <pre className="overflow-x-auto rounded-xl border border-border bg-card/60 p-4 font-mono text-sm text-primary">
        {quickstart}
      </pre>

      <section className="grid gap-4 sm:grid-cols-3">
        {docSections.map((section) => (
          <Card key={section.title}>
            <CardHeader>
              <CardTitle>{section.title}</CardTitle>
              <CardDescription>{section.body}</CardDescription>
            </CardHeader>
            <CardContent>
              <p className="text-xs uppercase tracking-wide text-muted-foreground/70">
                Docs coming soon
              </p>
            </CardContent>
          </Card>
        ))}
      </section>

      <footer className="text-xs text-muted-foreground/70">
        Apache-2.0 · kdelta.dev is the documentation home for the kdelta
        project.
      </footer>
    </main>
  );
}
