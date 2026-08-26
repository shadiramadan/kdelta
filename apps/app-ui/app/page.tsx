"use client";

import Link from "next/link";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { KdeltaService, type Resource, ConditionStatus } from "@kdelta/api";
import { linkLabel, safeHref } from "@/lib/links";
import { refToString } from "@/lib/ref";
import { Badge } from "@kdelta/ui/components/badge";
import { Button } from "@kdelta/ui/components/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@kdelta/ui/components/table";

// BehindBadge reports how far a resource trails its upstream, from the first
// stream with a resolved version source.
function BehindBadge({ resource }: { resource: Resource }) {
  const stream = resource.streams.find((s) => s.source?.source.case !== undefined);
  const versions = useQuery(
    KdeltaService.method.listVersions,
    { ref: resource.ref, streamId: stream?.id ?? "" },
    { enabled: stream !== undefined, retry: false },
  );
  if (stream === undefined) {
    return <span className="text-sm text-muted-foreground">—</span>;
  }
  if (versions.isPending) {
    return <span className="text-sm text-muted-foreground">…</span>;
  }
  if (versions.error !== null) {
    return (
      <Badge variant="outline" title={String(versions.error)}>
        unknown
      </Badge>
    );
  }
  const behind = versions.data.versionsBehind;
  return behind > 0 ? (
    <Badge variant="destructive" title={`latest ${versions.data.latest}`}>
      {behind} behind
    </Badge>
  ) : (
    <Badge variant="secondary">up to date</Badge>
  );
}

function isNoScanYet(error: unknown): boolean {
  return (
    error instanceof ConnectError && error.code === Code.FailedPrecondition
  );
}

export default function ResourcesPage() {
  const queryClient = useQueryClient();
  const resources = useQuery(KdeltaService.method.listResources, {});
  const scan = useMutation(KdeltaService.method.scan, {
    // refetchType "all" is required here: on a cold cache the resources query
    // has settled into an error state ("no scan cached yet"), and the default
    // invalidation does not refetch it — the first scan would appear to do
    // nothing until the user reloaded.
    onSuccess: () => queryClient.invalidateQueries({ refetchType: "all" }),
  });

  const scanButton = (
    <Button onClick={() => scan.mutate({})} disabled={scan.isPending}>
      {scan.isPending ? "Scanning…" : "Scan cluster"}
    </Button>
  );

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <header className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Resources</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Deployed resources detected in the cluster, with their version
            streams.
          </p>
        </div>
        {scanButton}
      </header>

      {scan.error !== null && (
        <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
          {String(scan.error)}
        </p>
      )}

      {resources.isPending && (
        <p className="text-sm text-muted-foreground">Loading…</p>
      )}

      {resources.error !== null && isNoScanYet(resources.error) && (
        <div className="rounded-xl border border-border bg-card/60 p-10 text-center">
          <p className="text-sm text-muted-foreground">
            No scan cached yet — run one to populate this view.
          </p>
        </div>
      )}
      {resources.error !== null && !isNoScanYet(resources.error) && (
        <p className="rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 font-mono text-sm text-destructive">
          {String(resources.error)}
        </p>
      )}

      {resources.data !== undefined && (
        <div className="rounded-xl border border-border bg-card/60">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Resource</TableHead>
                <TableHead>Streams</TableHead>
                <TableHead>Behind</TableHead>
                <TableHead>Upstream</TableHead>
                <TableHead>Links</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {resources.data.resources.map((resource) => {
                const deployed = resource.conditions.find(
                  (c) => c.type === "Deployed",
                );
                return (
                  <TableRow key={refToString(resource.ref)}>
                    <TableCell className="font-mono text-sm">
                      <Link
                        href={`/resource?ref=${encodeURIComponent(refToString(resource.ref))}`}
                        className="hover:text-primary hover:underline"
                      >
                        {refToString(resource.ref)}
                      </Link>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1.5">
                        {resource.streams.map((stream) => (
                          <Badge key={stream.id} variant="secondary">
                            {stream.id}@{stream.current?.value ?? "?"}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      <BehindBadge resource={resource} />
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {resource.upstream
                        ? `${resource.upstream.system}/${resource.upstream.name}`
                        : "—"}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-x-3 gap-y-1">
                        {(() => {
                          const safe = resource.links.filter(
                            (link) => safeHref(link.url) !== undefined,
                          );
                          if (safe.length === 0) {
                            return (
                              <span className="text-sm text-muted-foreground">—</span>
                            );
                          }
                          return safe.map((link) => (
                            <a
                              key={link.url}
                              href={safeHref(link.url)}
                              target="_blank"
                              rel="noreferrer"
                              className="text-sm text-muted-foreground underline-offset-4 hover:text-primary hover:underline"
                            >
                              {linkLabel(link.kind)}
                            </a>
                          ));
                        })()}
                      </div>
                    </TableCell>
                    <TableCell>
                      {deployed ? (
                        <Badge
                          variant={
                            deployed.status ===
                            ConditionStatus.TRUE
                              ? "default"
                              : "destructive"
                          }
                        >
                          {deployed.status === ConditionStatus.TRUE
                            ? "deployed"
                            : deployed.reason || "not deployed"}
                        </Badge>
                      ) : (
                        <Badge variant="outline">unknown</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
