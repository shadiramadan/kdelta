import { create } from "@bufbuild/protobuf";
import { ResourceRefSchema, type ResourceRef } from "@kdelta/api";

// The canonical resource-ref string form is `detector:namespace/name` (or
// `detector:name` when cluster-scoped). refToString and parseRef are the
// single round-trip pair for it — every formatter and the URL builder route
// through here so the two directions can never drift (and so the form has one
// place to change when multi-cluster adds a cluster segment).

export function refToString(ref: ResourceRef | undefined): string {
  if (!ref) return "?";
  return ref.namespace
    ? `${ref.detector}:${ref.namespace}/${ref.name}`
    : `${ref.detector}:${ref.name}`;
}

export function parseRef(canonical: string): ResourceRef | undefined {
  const [detector, rest] = canonical.split(":", 2);
  if (!detector || !rest) return undefined;
  const slash = rest.indexOf("/");
  if (slash < 0) return create(ResourceRefSchema, { detector, name: rest });
  return create(ResourceRefSchema, {
    detector,
    namespace: rest.slice(0, slash),
    name: rest.slice(slash + 1),
  });
}
