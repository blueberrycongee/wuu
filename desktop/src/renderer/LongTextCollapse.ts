import { useCallback, useState } from "react";

/**
 * Shared "long text → fold with a click-to-expand toggle" helper.
 *
 * The desktop already had a copy of this pattern living inside
 * `ThreadItemView.UserMessageBubble` for the main conversation stream
 * (`user-message-long-card`, "显示更多 / 收起"). The chat-style thread view
 * needed the same affordance — a synthesized `user_message` carrying an
 * 8 KB agent-notification dump is unreadable otherwise — so the logic
 * moved here so both surfaces share the same thresholds, the same
 * preview estimator, and the same React keying pattern. The numbers
 * below were tuned against the main stream and intentionally copied
 * verbatim; treat them as a single source of truth.
 *
 * Behavioural notes worth carrying forward to a future reader:
 *
 *   * The state shape `{ text, expanded }` (instead of just `expanded`)
 *     is the reason the hook survives "same component instance, new
 *     message text" without flashing the previous message's expansion.
 *     A streaming turn that keeps mutating `text` would otherwise leak
 *     its prior collapse state into the new payload; the `text` key
 *     collapses that risk into one ternary on every render.
 *
 *   * `isCollapsibleLongText` is intentionally cheap: one length check
 *     plus a single line-by-line accumulator that bails out the moment
 *     it crosses the line threshold. Both surfaces call this on every
 *     render of every long-message candidate, so the loop is the hot
 *     path. Don't add DOM measurement or regex passes here.
 *
 *   * `collapsedLongTextPreview` produces a plain-text preview — no
 *     markdown parsing — because the user/participant typed the body
 *     once and we want byte-faithful display even when truncated. The
 *     chat surface adds its own RichContent re-render on expand (see
 *     `ChatBubbleText` in ChatThreadView) so that markdown surfaces
 *     once the reader opts in.
 */
export const COLLAPSIBLE_LINE_THRESHOLD = 14;
export const COLLAPSIBLE_CHAR_THRESHOLD = 1200;
/**
 * Soft line width used to estimate how many wrapped lines a line of
 * plain text will occupy at the bubble's reading measure. Tied to the
 * chat / conversation line-height + font-size pairing — see the
 * `.chat-bubble` and `.user-message` rules in styles/turns.css and
 * styles/chat.css. If you change those, re-tune this number so the
 * preview lands on the same number of visible rows on both surfaces.
 */
export const COLLAPSIBLE_SOFT_LINE_CHARS = 84;
export const COLLAPSIBLE_PREVIEW_LINES = 5;

export function isCollapsibleLongText(text: string): boolean {
  if (text.length > COLLAPSIBLE_CHAR_THRESHOLD) {
    return true;
  }
  let estimatedLines = 0;
  for (const line of text.split(/\r\n|\r|\n/)) {
    estimatedLines += Math.max(
      1,
      Math.ceil(line.length / COLLAPSIBLE_SOFT_LINE_CHARS),
    );
    if (estimatedLines > COLLAPSIBLE_LINE_THRESHOLD) {
      return true;
    }
  }
  return false;
}

/**
 * Plain-text preview used when the body is folded. Returns roughly the
 * first `COLLAPSIBLE_PREVIEW_LINES` worth of lines, trimmed on a soft
 * boundary so we never end a preview mid-glyph; appends `...` so the
 * reader can see the body was truncated without us having to render a
 * separate "已截断" chip (the toggle button is the explicit affordance).
 */
export function collapsedLongTextPreview(text: string): string {
  let estimatedLines = 0;
  const previewLines: string[] = [];

  for (const line of text.split(/\r\n|\r|\n/)) {
    const lineEstimate = Math.max(
      1,
      Math.ceil(line.length / COLLAPSIBLE_SOFT_LINE_CHARS),
    );
    if (estimatedLines + lineEstimate <= COLLAPSIBLE_PREVIEW_LINES) {
      previewLines.push(line);
      estimatedLines += lineEstimate;
      continue;
    }

    const remainingLines = COLLAPSIBLE_PREVIEW_LINES - estimatedLines;
    if (remainingLines > 0) {
      previewLines.push(
        line.slice(0, remainingLines * COLLAPSIBLE_SOFT_LINE_CHARS).trimEnd(),
      );
    }
    break;
  }

  const preview = previewLines.join("\n").trimEnd();
  return preview ? `${preview}...` : "...";
}

/**
 * React entry point for surfaces that want the long-text toggle.
 * Returns the same `{ collapsible, expanded, toggle }` shape that
 * `UserMessageBubble` and the chat bubble both consume — that lets
 * callers wire the toggle to a button without ever touching the
 * `useState` plumbing themselves.
 *
 * `text` is part of the dep array on `toggleExpanded` because the
 * returned setter must read the latest `text` even when React batches
 * the click event with a text mutation from the same parent tick.
 */
export function useLongTextCollapse(text: string): {
  collapsible: boolean;
  expanded: boolean;
  toggleExpanded: () => void;
} {
  const [state, setState] = useState<{ text: string; expanded: boolean }>({
    text,
    expanded: false,
  });
  const collapsible = isCollapsibleLongText(text);
  const expanded =
    collapsible && state.text === text ? state.expanded : false;
  const toggleExpanded = useCallback((): void => {
    setState((prev) => ({
      text,
      expanded: prev.text === text ? !prev.expanded : false,
    }));
  }, [text]);
  return { collapsible, expanded, toggleExpanded };
}