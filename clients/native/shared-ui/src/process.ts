import { buildToolActivityProcessSegments } from "../../../../desktop/src/renderer/ToolActivityHelpers";
import { condensedToolActivityText, mascotActivityForToolKind } from "../../../../desktop/src/renderer/ProcessSummary";
import type { ThreadItem } from "../../../../desktop/src/shared/protocol";

// The native text row and the embedded renderer use the same desktop rules.
// This entry point also runs in JavaScriptCore without a DOM or a WebView.
export function summarize(tools: ThreadItem[]) {
  const segments = buildToolActivityProcessSegments(tools);
  const current = [...segments].reverse().find(segment => segment.status === "running") ?? segments.at(-1);
  return {
    text: condensedToolActivityText(segments, tools.length, false),
    activity: mascotActivityForToolKind(current?.kind),
    failed: segments.some(segment => segment.status === "failed"),
  };
}

export function summarizeJSON(json: string): string {
  return JSON.stringify(summarize(JSON.parse(json)));
}
