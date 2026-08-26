/* oxlint-disable no-unsafe-type-assertion --
   createConnectQueryKey and the streaming client are typed for unary methods;
   the maintainers' streaming wrapper uses the same deliberate casts. */
// Server-streaming RPCs composed with TanStack Query — the pattern the
// connect-query maintainers endorse until their own streaming API ships:
// connect-query cache keys + experimental_streamedQuery around the streaming
// client, with a reducer folding progress events into a fixed-shape value
// (never the default unbounded chunk array) and refetchMode "reset" so a
// refetch replays progress. See docs/CODING_STANDARDS.md.
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import {
  experimental_streamedQuery as streamedQuery,
  queryOptions,
} from "@tanstack/react-query";
import {
  KdeltaService,
  type AssessImpactRequest,
  type ChangeSet,
  type GetChangesRequest,
  type ImpactAssessment,
  type Progress,
  type VersionChanges,
} from "@kdelta/api";

export interface StreamState<Result> {
  progress: Progress[];
  // Per-version changes streamed ahead of the final result (GetChanges only),
  // for incremental rendering while extraction batches complete.
  partials: VersionChanges[];
  result?: Result;
}

interface StreamEvent<Result> {
  event:
    | { case: "progress"; value: Progress }
    | { case: "partial"; value: VersionChanges }
    | { case: "result"; value: Result }
    | { case: undefined; value?: undefined };
}

function streamStateOptions<Result>(
  method: "getChanges" | "assessImpact",
  transport: Transport,
  input: unknown,
  enabled: boolean,
) {
  const client = createClient(KdeltaService, transport);
  return queryOptions<StreamState<Result>>({
    // createConnectQueryKey is typed for unary methods only; the maintainers'
    // streaming wrapper uses the same cast so keys stay convention-shaped.
    queryKey: createConnectQueryKey({
      schema: KdeltaService.method[method] as never,
      transport,
      input: input as never,
      cardinality: "finite",
    }),
    queryFn: streamedQuery({
      streamFn: (context) =>
        client[method](input as never, {
          signal: context.signal,
        }) as AsyncIterable<StreamEvent<Result>>,
      refetchMode: "reset",
      reducer: (acc: StreamState<Result>, chunk: StreamEvent<Result>) => {
        if (chunk.event.case === "progress") {
          return { ...acc, progress: [...acc.progress, chunk.event.value] };
        }
        if (chunk.event.case === "partial") {
          return { ...acc, partials: [...acc.partials, chunk.event.value] };
        }
        if (chunk.event.case === "result") {
          return { ...acc, result: chunk.event.value };
        }
        return acc;
      },
      initialValue: { progress: [], partials: [] } satisfies StreamState<Result>,
    }),
    enabled,
    staleTime: Infinity,
    retry: false,
  });
}

export function changesStreamOptions(
  transport: Transport,
  input: Partial<GetChangesRequest>,
  enabled: boolean,
) {
  return streamStateOptions<ChangeSet>(
    "getChanges",
    transport,
    input,
    enabled,
  );
}

export function impactStreamOptions(
  transport: Transport,
  input: Partial<AssessImpactRequest>,
  enabled: boolean,
) {
  return streamStateOptions<ImpactAssessment>(
    "assessImpact",
    transport,
    input,
    enabled,
  );
}
