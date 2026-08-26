"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useQuery as useConnectQuery, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import {
  ChangeType,
  ImpactSeverity,
  KdeltaService,
  type ChangeSet,
  type ImpactAssessment,
  type Progress,
  type VersionChanges,
} from "@kdelta/api";
import { linkLabel, safeHref } from "@/lib/links";
import { parseRef, refToString } from "@/lib/ref";
import { changeBadgeVariant, enumLabel, severityVariant } from "@/lib/enums";
import { Badge } from "@kdelta/ui/components/badge";
import { Button } from "@kdelta/ui/components/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@kdelta/ui/components/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@kdelta/ui/components/select";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@kdelta/ui/components/tabs";
import { changesStreamOptions, impactStreamOptions } from "@/lib/streamed";

// ProgressLog streams the server's progress events as a running log while a
// flow is active, so the user sees what the agent is doing (fetching notes,
// extracting batches, querying cluster objects) instead of a silent wait.
function ProgressLog({ progress, active }: { progress: Progress[]; active: boolean }) {
  if (!active) return null;
  return (
    <div className="space-y-1 rounded-lg border border-border bg-card/60 px-3 py-2 font-mono text-xs text-muted-foreground">
      {progress.map((p, index) => (
        <p
          key={index}
          className={index === progress.length - 1 ? "animate-pulse" : "opacity-60"}
        >
          » {p.stage}: {p.message}
        </p>
      ))}
      {progress.length === 0 && <p className="animate-pulse">» starting…</p>}
    </div>
  );
}

// InlineText renders agent-written prose, turning `backtick` spans into
// inline code. The model output is markdown-flavored plain text, not MDX;
// code spans are the one construct it uses heavily enough to matter.
function InlineText({ text }: { text: string }) {
  const parts = text.split("`");
  if (parts.length % 2 === 0) return <>{text}</>;
  return (
    <>
      {parts.map((part, index) =>
        index % 2 === 1 ? (
          <code
            key={index}
            className="rounded bg-secondary px-1 py-0.5 font-mono text-[0.85em]"
          >
            {part}
          </code>
        ) : (
          <span key={index}>{part}</span>
        ),
      )}
    </>
  );
}

function VersionChangesCard({ version }: { version: VersionChanges }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="font-mono">{version.version}</CardTitle>
        {version.provenance && (
          <CardDescription>
            {version.provenance.model
              ? `extracted by ${version.provenance.model}`
              : "verbatim release notes"}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {version.changes.map((change, index) => (
          <div key={index} className="space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={changeBadgeVariant(change.type)}>
                {enumLabel(ChangeType, change.type)}
              </Badge>
              <span className="text-sm">
                <InlineText text={change.summary} />
              </span>
            </div>
            {change.affectedPaths.length > 0 && (
              <p className="font-mono text-xs text-muted-foreground">
                affects {change.affectedPaths.join(", ")}
              </p>
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function ChangeSetView({ set }: { set: ChangeSet }) {
  return (
    <div className="space-y-4">
      {set.versions.map((version) => (
        <VersionChangesCard key={version.version} version={version} />
      ))}
    </div>
  );
}

function ImpactView({ assessment }: { assessment: ImpactAssessment }) {
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Badge variant={severityVariant(assessment.overallSeverity)}>
              {enumLabel(ImpactSeverity, assessment.overallSeverity)}
            </Badge>
            Impact assessment
          </CardTitle>
          <CardDescription>
            <InlineText text={assessment.summary} />
          </CardDescription>
        </CardHeader>
      </Card>

      {assessment.resources.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Affected resources</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {assessment.resources.map((resource, index) => (
              <div key={index} className="space-y-1 border-b border-border pb-3 last:border-0 last:pb-0">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={severityVariant(resource.severity)}>
                    {enumLabel(ImpactSeverity, resource.severity)}
                  </Badge>
                  <span className="font-mono text-sm">
                    {refToString(resource.ref)}
                  </span>
                </div>
                <p className="text-sm text-muted-foreground">
                  <InlineText text={resource.explanation} />
                </p>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {assessment.findings.map((finding, index) => (
        <Card key={index}>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Badge variant={severityVariant(finding.severity)}>
                {enumLabel(ImpactSeverity, finding.severity)}
              </Badge>
              <InlineText text={finding.title} />
            </CardTitle>
            <CardDescription>
              <InlineText text={finding.rationale} />
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {finding.evidence.map((evidence, i) => (
              <p key={i} className="text-xs text-muted-foreground">
                {evidence.version}: {evidence.summary}
                {evidence.affectedPaths.length > 0 && (
                  <span className="font-mono"> ({evidence.affectedPaths.join(", ")})</span>
                )}
              </p>
            ))}
            {finding.actions.map((action, i) => (
              <p key={i} className="text-sm">
                <Badge variant="outline">{action.beforeUpgrade ? "before upgrade" : "after upgrade"}</Badge>{" "}
                <InlineText text={action.description} />
                {action.command && (
                  <code className="ml-1 rounded bg-secondary px-1 py-0.5 font-mono text-xs">{action.command}</code>
                )}
              </p>
            ))}
          </CardContent>
        </Card>
      ))}

      {assessment.gaps.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Unknowns</CardTitle>
          </CardHeader>
          <CardContent className="space-y-1">
            {assessment.gaps.map((gap, index) => (
              <p key={index} className="text-sm text-muted-foreground">
                ? <InlineText text={gap.description} />
                {gap.suggestion && (
                  <span className="text-xs">
                    {" — "}
                    <InlineText text={gap.suggestion} />
                  </span>
                )}
              </p>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function ResourceDetail() {
  const canonical = useSearchParams().get("ref") ?? "";
  const ref = parseRef(canonical);
  const transport = useTransport();

  const detail = useConnectQuery(
    KdeltaService.method.getResource,
    { ref },
    { enabled: ref !== undefined },
  );
  const streams = detail.data?.resource?.streams ?? [];
  const [selectedStream, setSelectedStream] = useState<string>();
  // Default to the first stream that can actually list versions; a stream
  // without a resolved source would leave the range pickers dead on arrival.
  const streamId =
    selectedStream ??
    streams.find((s) => s.source?.source.case !== undefined)?.id ??
    streams[0]?.id ??
    "";

  const versions = useConnectQuery(
    KdeltaService.method.listVersions,
    { ref, streamId },
    { enabled: ref !== undefined && streamId !== "" },
  );
  const [selectedFrom, setSelectedFrom] = useState<string>();
  const [selectedTo, setSelectedTo] = useState<string>();
  const fromVersion = selectedFrom ?? versions.data?.current ?? "";
  const toVersion = selectedTo ?? versions.data?.latest ?? "";

  const rangeReady = fromVersion !== "" && toVersion !== "";
  const rangeInput = { ref, streamId, fromVersion, toVersion };
  const [changesRequested, setChangesRequested] = useState(false);
  const [impactRequested, setImpactRequested] = useState(false);
  const [activeTab, setActiveTab] = useState("changelog");
  const changes = useQuery(
    changesStreamOptions(transport, rangeInput, changesRequested && rangeReady),
  );
  const impact = useQuery(
    impactStreamOptions(transport, rangeInput, impactRequested && rangeReady),
  );

  if (ref === undefined) {
    return <p className="text-sm text-destructive">Missing or invalid ?ref= parameter.</p>;
  }
  if (detail.isPending) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  if (detail.error !== null) {
    return (
      <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
        {String(detail.error)}
      </p>
    );
  }

  const versionValues = versions.data?.versions.map((v) => v.value) ?? [];

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <header>
        <h1 className="font-mono text-2xl font-semibold tracking-tight">{canonical}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Pick a stream and a version range, then extract the changelog or
          assess the upgrade's impact on this cluster.
        </p>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
          {detail.data?.resource?.upstream && (
            <span>
              {detail.data.resource.upstream.system}/{detail.data.resource.upstream.name}
            </span>
          )}
          {detail.data?.resource?.links
            .filter((link) => safeHref(link.url) !== undefined)
            .map((link) => (
              <a
                key={link.url}
                href={safeHref(link.url)}
                target="_blank"
                rel="noreferrer"
                className="underline-offset-4 hover:text-primary hover:underline"
              >
                {linkLabel(link.kind)}
              </a>
            ))}
        </div>
      </header>

      <Card>
        <CardContent className="flex flex-wrap items-end gap-3">
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">Stream</p>
            <Select value={streamId} onValueChange={(value) => {
              setSelectedStream(value);
              setSelectedFrom(undefined);
              setSelectedTo(undefined);
            }}>
              <SelectTrigger className="w-40"><SelectValue /></SelectTrigger>
              <SelectContent>
                {streams.map((s) => (
                  <SelectItem key={s.id} value={s.id}>
                    {s.id}@{s.current?.value}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">From</p>
            <Select value={fromVersion} onValueChange={setSelectedFrom} disabled={versionValues.length === 0}>
              <SelectTrigger className="w-44"><SelectValue /></SelectTrigger>
              <SelectContent>
                {versionValues.map((value) => (
                  <SelectItem key={value} value={value}>{value}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <p className="text-xs uppercase tracking-wide text-muted-foreground">To</p>
            <Select value={toVersion} onValueChange={setSelectedTo} disabled={versionValues.length === 0}>
              <SelectTrigger className="w-44"><SelectValue /></SelectTrigger>
              <SelectContent>
                {versionValues.map((value) => (
                  <SelectItem key={value} value={value}>{value}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="ml-auto flex gap-2">
            <Button
              variant="secondary"
              disabled={!rangeReady || (changesRequested && changes.isFetching)}
              onClick={() => {
                setActiveTab("changelog");
                if (changesRequested) {
                  void changes.refetch();
                } else {
                  setChangesRequested(true);
                }
              }}
            >
              {changesRequested && changes.isFetching ? "Extracting…" : "Extract changelog"}
            </Button>
            <Button
              disabled={!rangeReady || (impactRequested && impact.isFetching)}
              onClick={() => {
                setActiveTab("impact");
                if (impactRequested) {
                  void impact.refetch();
                } else {
                  setImpactRequested(true);
                }
              }}
            >
              {impactRequested && impact.isFetching ? "Assessing…" : "Assess impact"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {versions.error !== null && (
        <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
          {String(versions.error)}
        </p>
      )}
      {versions.data && (
        <p className="text-sm text-muted-foreground">
          {versions.data.versionsBehind} stable version
          {versions.data.versionsBehind === 1 ? "" : "s"} behind latest ({versions.data.latest}).
        </p>
      )}

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList>
          <TabsTrigger value="changelog">Changelog</TabsTrigger>
          <TabsTrigger value="impact">Impact</TabsTrigger>
        </TabsList>
        <TabsContent value="changelog" className="space-y-3">
          {!changesRequested && (
            <p className="text-sm text-muted-foreground">
              Extract the changelog to see what changed across this range.
            </p>
          )}
          {changesRequested && (
            <>
              <ProgressLog
                progress={changes.data?.progress ?? []}
                active={changes.isFetching}
              />
              {changes.error !== null && (
                <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
                  {String(changes.error)}
                </p>
              )}
              {changes.data?.result ? (
                <ChangeSetView set={changes.data.result} />
              ) : (
                (changes.data?.partials.length ?? 0) > 0 && (
                  <div className="space-y-4">
                    {changes.data?.partials.map((version) => (
                      <VersionChangesCard key={version.version} version={version} />
                    ))}
                  </div>
                )
              )}
            </>
          )}
        </TabsContent>
        <TabsContent value="impact" className="space-y-3">
          {!impactRequested && (
            <p className="text-sm text-muted-foreground">
              Assess the impact to see what this upgrade would do to the rest of
              the cluster.
            </p>
          )}
          {impactRequested && (
            <>
              <ProgressLog
                progress={impact.data?.progress ?? []}
                active={impact.isFetching}
              />
              {impact.error !== null && (
                <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
                  {String(impact.error)}
                </p>
              )}
              {impact.data?.result && <ImpactView assessment={impact.data.result} />}
            </>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}

export default function ResourcePage() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
      <ResourceDetail />
    </Suspense>
  );
}
