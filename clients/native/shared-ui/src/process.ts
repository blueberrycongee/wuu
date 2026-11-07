import { buildToolActivityProcessSegments } from "../../../../desktop/src/renderer/ToolActivityHelpers";
import { processSegmentText, mascotActivityForToolKind } from "../../../../desktop/src/renderer/ProcessSummary";
import { translateCurrent } from "../../../../desktop/src/renderer/i18n";
import type { ThreadItem } from "../../../../desktop/src/shared/protocol";

// The native text row and the embedded renderer use the same desktop rules.
// This entry point also runs in JavaScriptCore without a DOM or a WebView.
export function summarize(tools: ThreadItem[]) {
  const segments = buildToolActivityProcessSegments(tools);
  const current = [...segments].reverse().find(segment => segment.status === "running") ?? segments.at(-1);
  return {
    text: segments.map(processSegmentText).join(translateCurrent("process.actionSeparator")),
    activity: mascotActivityForToolKind(current?.kind),
    failed: segments.some(segment => segment.status === "failed"),
  };
}

export function summarizeJSON(json: string): string {
  return JSON.stringify(summarize(JSON.parse(json)));
}
