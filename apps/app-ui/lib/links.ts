import { LinkKind } from "@kdelta/api";

// safeHref returns the URL only if it is an http(s) link, else undefined so
// the anchor renders inert. Link URLs originate from attacker-controllable
// Helm chart metadata; this blocks javascript:/data: hrefs (React does not).
// The server-side detector also filters these — this is defense-in-depth.
export function safeHref(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:"
      ? url
      : undefined;
  } catch {
    return undefined;
  }
}

// linkLabel renders a LinkKind as a short lowercase label ("source",
// "migration guide") for inline link anchors.
export function linkLabel(kind: LinkKind): string {
  const name = LinkKind[kind] as string | undefined;
  return name === undefined || kind === LinkKind.UNSPECIFIED
    ? "link"
    : name.toLowerCase().replaceAll("_", " ");
}
