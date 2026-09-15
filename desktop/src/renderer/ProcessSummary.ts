import type { ToolActivityProcessSegment } from "./ToolActivityHelpers";
import type { WuuMascotActivity } from "./WuuMascot";
import { translateCurrent as translate } from "./i18n";

// Mixed activity becomes harder to scan than a sentence once a group reaches
// this size. Same-kind groups keep their more useful count summary.
export const CONDENSED_SUMMARY_MIN_TOOL_COUNT = 4;

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

export function condensedToolActivityText(
  segments: ToolActivityProcessSegment[],
  toolCount: number,
  reasoningStreaming: boolean,
): string {
  if (reasoningStreaming && !segments.some((segment) => segment.status === "running")) {
    return translate("process.thinkingAfterOperations", { count: toolCount });
  }
  const parts = segments.slice(0, 3).map(processSegmentText);
  if (segments.length > 3) parts.push("…");
  if (reasoningStreaming) parts.push(translate("process.thinking"));
  return parts.join(translate("process.actionSeparator"));
}
