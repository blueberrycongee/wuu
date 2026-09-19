import type { ToolActivityProcessSegment } from "./ToolActivityHelpers";
import type { WuuMascotActivity } from "./WuuMascot";
import { translateCurrent as translate } from "./i18n";

// Mixed activity becomes harder to scan than a sentence once a group reaches
// this size. Same-kind groups keep their more useful count summary.
export const CONDENSED_SUMMARY_MIN_TOOL_COUNT = 4;

// Same-kind aggregate counts ("正在搜索 3 次") can tick on every parallel
// tool admission. Hold those digits briefly so the compact row does not jump
// on every increment; kind, status, and copy changes still flush immediately.
export const PROCESS_SUMMARY_COUNT_DEBOUNCE_MS = 300;
export const PROCESS_SUMMARY_COUNT_MAX_WAIT_MS = 600;

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

export type ProcessSummaryPresentation = {
  segments: ToolActivityProcessSegment[];
  toolCount: number;
};

function processSummaryCondensed(
  segments: ToolActivityProcessSegment[],
  toolCount: number,
): boolean {
  return toolCount >= CONDENSED_SUMMARY_MIN_TOOL_COUNT && segments.length > 1;
}

/**
 * Identity of the compact process sentence, ignoring same-kind counts.
 * A change here is a phase shift (new kind, status, copy, condensed layout,
 * or reasoning) and should paint immediately.
 */
export function processSummaryIdentity(
  segments: ToolActivityProcessSegment[],
  toolCount: number,
  reasoningStreaming: boolean,
): string {
  return [
    processSummaryCondensed(segments, toolCount) ? "condensed" : "full",
    reasoningStreaming ? "thinking" : "idle",
    segments
      .map((segment) =>
        [
          segment.id,
          segment.kind,
          segment.status,
          segment.countPrefix ?? "",
          segment.countSuffix ?? "",
          segment.text ?? "",
        ].join(":"),
      )
      .join("|"),
  ].join(";");
}

/** Full compact-row snapshot, including counts that may be held while live. */
export function processSummarySignature(
  segments: ToolActivityProcessSegment[],
  toolCount: number,
  reasoningStreaming: boolean,
): string {
  return [
    processSummaryIdentity(segments, toolCount, reasoningStreaming),
    String(toolCount),
    segments.map((segment) => String(segment.count ?? "")).join(","),
  ].join(";");
}
