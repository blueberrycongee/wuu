import type { ToolActivityProcessSegment } from "./ToolActivityHelpers";
import type { WuuMascotActivity } from "./WuuMascot";

export function processSegmentText(segment: ToolActivityProcessSegment): string {
  return typeof segment.count === "number"
    ? `${segment.countPrefix}${segment.count}${segment.countSuffix}`
    : (segment.text ?? "");
}

export function mascotActivityForToolKind(
  kind: ToolActivityProcessSegment["kind"] | undefined,
): WuuMascotActivity {
  switch (kind) {
    case "search":
    case "list":
    case "browser":
      return "search";
    case "edit":
    case "create":
      return "edit";
    case "command":
      return "command";
    case "read":
    case "context":
    case "skill":
      return "read";
    default:
      return "tool";
  }
}
