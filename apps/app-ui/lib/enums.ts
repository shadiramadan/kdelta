import { ChangeType, ImpactSeverity } from "@kdelta/api";

// Presentation for the protobuf enums: a shared enum→label formatter and the
// badge-variant maps, so a new ChangeType or ImpactSeverity is styled in one
// place instead of across the views.

type BadgeVariant = "default" | "secondary" | "destructive" | "outline";

// enumLabel turns a generated enum value into a lowercase display label
// ("DEFAULT_CHANGED" → "default changed"). The enum object maps its numeric
// value back to the name.
export function enumLabel(
  enumObj: Record<number, string>,
  value: number,
): string {
  return (enumObj[value] ?? "").toLowerCase().replaceAll("_", " ") || "unknown";
}

export function changeBadgeVariant(type: ChangeType): BadgeVariant {
  switch (type) {
    case ChangeType.BREAKING:
    case ChangeType.SECURITY:
    case ChangeType.REMOVED:
    case ChangeType.DEFAULT_CHANGED:
      return "destructive";
    case ChangeType.ADDED:
      return "default";
    default:
      return "secondary";
  }
}

export function severityVariant(severity: ImpactSeverity): BadgeVariant {
  switch (severity) {
    case ImpactSeverity.CRITICAL:
    case ImpactSeverity.HIGH:
      return "destructive";
    case ImpactSeverity.MEDIUM:
      return "default";
    default:
      return "secondary";
  }
}
