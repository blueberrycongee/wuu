import { useEffect, useMemo, useRef, useState } from "react";
import type { ThreadItem } from "../shared/protocol";
import { useI18n } from "./i18n";
import { TruncatedText } from "./TruncatedText";
import { UILayerPortal } from "./ui/layers/UILayerHost";

function useTriggerPosition() {
  const [rect, setRect] = useState<DOMRect | null>(null);
  const triggerRef = useRef<HTMLSpanElement | null>(null);

  const measure = () => {
    if (triggerRef.current) {
      setRect(triggerRef.current.getBoundingClientRect());
    }
  };

  const clear = () => setRect(null);

  return { triggerRef, rect, measure, clear };
}

function previewBoundsForTrigger(
  trigger: HTMLElement | null,
): DOMRect | undefined {
  const pane = trigger?.closest(".conversation-pane");
  return pane instanceof HTMLElement ? pane.getBoundingClientRect() : undefined;
}

type DiffLineOp = "equal" | "insert" | "delete";

type DiffLine = {
  op: DiffLineOp;
  content: string;
};

type DiffHunk = {
  oldStart: number;
  newStart: number;
  lines: DiffLine[];
};

type FileDiff = {
  path?: string;
  newFile?: boolean;
  hunks: DiffHunk[];
  truncated?: boolean;
  lines?: number;
  oldLines?: number;
  newLines?: number;
  summary?: string;
};

export type ToolDiffPreviewFileDiff = FileDiff;

function parseJSON(value: string | undefined): unknown {
  if (!value) return undefined;
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringValue(record: unknown, key: string): string | undefined {
  if (!isRecord(record)) return undefined;
  const value = record[key];
  return typeof value === "string" ? value : undefined;
}

function arrayValue(record: unknown, key: string): unknown[] {
  if (!isRecord(record)) return [];
  const value = record[key];
  return Array.isArray(value) ? value : [];
}

function numberValue(record: unknown, key: string): number | undefined {
  if (!isRecord(record)) return undefined;
  const value = record[key];
  return typeof value === "number" ? value : undefined;
}

function parseDiffLineOp(value: unknown): DiffLineOp {
  if (value === "insert" || value === "delete" || value === "equal") {
    return value;
  }
  return "equal";
}

export function extractToolDiffPreview(
  rawDiff: unknown,
  path?: string,
): ToolDiffPreviewFileDiff | undefined {
  if (!isRecord(rawDiff)) return undefined;

  const hunks: DiffHunk[] = [];
  for (const rawHunk of arrayValue(rawDiff, "hunks")) {
    if (!isRecord(rawHunk)) continue;
    const oldStart = numberValue(rawHunk, "old_start") ?? 1;
    const newStart = numberValue(rawHunk, "new_start") ?? 1;
    const hunkLines: DiffLine[] = [];
    for (const rawLine of arrayValue(rawHunk, "lines")) {
      if (!isRecord(rawLine)) continue;
      hunkLines.push({
        op: parseDiffLineOp(rawLine.op),
        content: stringValue(rawLine, "content") ?? "",
      });
    }
    if (hunkLines.length > 0) {
      hunks.push({ oldStart, newStart, lines: hunkLines });
    }
  }

  const newFile = rawDiff.new_file === true;
  const truncated = rawDiff.truncated === true;
  const summary = stringValue(rawDiff, "summary");
  const lines = numberValue(rawDiff, "lines");
  const oldLines = numberValue(rawDiff, "old_lines");
  const newLines = numberValue(rawDiff, "new_lines");
  if (
    hunks.length === 0 &&
    !newFile &&
    !truncated &&
    !summary &&
    lines === undefined &&
    oldLines === undefined &&
    newLines === undefined
  ) {
    return undefined;
  }

  return {
    path,
    newFile,
    hunks,
    truncated,
    lines,
    oldLines,
    newLines,
    summary,
  };
}

function extractFileDiff(item: ThreadItem): FileDiff | undefined {
  const name = (item.name ?? "").trim();
  const capability = item.display?.capability?.trim();
  const isEditTool =
    name === "edit_file" ||
    name === "write_file" ||
    name === "apply_patch" ||
    capability === "file.edit";
  if (!isEditTool) return undefined;

  const result = parseJSON(item.result);
  const diff = isRecord(result) ? (result.diff as unknown) : undefined;
  if (!isRecord(diff)) return undefined;

  const path =
    stringValue(result, "path") ??
    stringValue(result, "file") ??
    stringValue(diff, "path");

  const newFile = diff.new_file === true;
  const preview = extractToolDiffPreview(diff, path);
  return preview ? { ...preview, newFile } : undefined;
}

function useHoverPreview(openDelayMs: number, closeDelayMs: number) {
  const [active, setActive] = useState(false);
  const openTimerRef = useRef<number | null>(null);
  const closeTimerRef = useRef<number | null>(null);

  const clearOpenTimer = () => {
    if (openTimerRef.current) {
      window.clearTimeout(openTimerRef.current);
      openTimerRef.current = null;
    }
  };

  const clearCloseTimer = () => {
    if (closeTimerRef.current) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  };

  const enter = () => {
    clearCloseTimer();
    clearOpenTimer();
    openTimerRef.current = window.setTimeout(() => {
      openTimerRef.current = null;
      setActive(true);
    }, openDelayMs);
  };

  const keepOpen = () => {
    clearCloseTimer();
    clearOpenTimer();
    setActive(true);
  };

  const leave = (onClose?: () => void) => {
    clearOpenTimer();
    clearCloseTimer();
    closeTimerRef.current = window.setTimeout(() => {
      closeTimerRef.current = null;
      setActive(false);
      onClose?.();
    }, closeDelayMs);
  };

  useEffect(() => {
    return () => {
      clearOpenTimer();
      clearCloseTimer();
    };
  }, []);

  return { active, enter, keepOpen, leave };
}

function DiffHunkView({ hunk }: { hunk: DiffHunk }): JSX.Element {
  let oldLine = hunk.oldStart;
  let newLine = hunk.newStart;

  const rows = hunk.lines.map((line, index) => {
    const op = line.op;
    let oldNumber: number | null = null;
    let newNumber: number | null = null;

    if (op === "equal") {
      oldNumber = oldLine;
      newNumber = newLine;
      oldLine++;
      newLine++;
    } else if (op === "delete") {
      oldNumber = oldLine;
      oldLine++;
    } else {
      newNumber = newLine;
      newLine++;
    }

    return (
      <div
        className={`tool-diff-line tool-diff-line-${op}`}
        key={`${oldNumber ?? newNumber}-${index}`}
      >
        <span className="tool-diff-line-number tool-diff-line-number-old">
          {oldNumber ?? ""}
        </span>
        <span className="tool-diff-line-number tool-diff-line-number-new">
          {newNumber ?? ""}
        </span>
        <TruncatedText className="tool-diff-line-content" text={line.content || " "} />
      </div>
    );
  });

  return <div className="tool-diff-hunk">{rows}</div>;
}

function DiffSummaryView({ diff }: { diff: FileDiff }): JSX.Element {
  const { t, formatNumber } = useI18n();
  const parts: string[] = [];
  if (diff.newFile) {
    parts.push(
      diff.lines === undefined
        ? t("toolDiff.newFile")
        : t("toolDiff.newFileLines", { count: formatNumber(diff.lines) }),
    );
  } else if (diff.summary) {
    parts.push(diff.summary);
  } else if (diff.oldLines !== undefined || diff.newLines !== undefined) {
    parts.push(
      t("toolDiff.oldNewLines", {
        old: formatNumber(diff.oldLines ?? 0),
        new: formatNumber(diff.newLines ?? 0),
      }),
    );
  }
  if (diff.truncated && !parts.some((part) => /截断|truncat/i.test(part))) {
    parts.push(t("toolDiff.lineDiffTruncated"));
  }
  return (
    <span className="tool-diff-preview-summary">
      {parts.length > 0
        ? parts.join(t("toolDiff.summarySeparator"))
        : t("toolDiff.noLineDiff")}
    </span>
  );
}

export function ToolDiffContent({
  diff,
}: {
  diff: ToolDiffPreviewFileDiff;
}): JSX.Element {
  return (
    <>
      {diff.hunks.length > 0 ? (
        diff.hunks.map((hunk, index) => (
          <DiffHunkView hunk={hunk} key={index} />
        ))
      ) : (
        <DiffSummaryView diff={diff} />
      )}
    </>
  );
}

export function ToolDiffPreview({
  item,
  diff: explicitDiff,
  children,
}: {
  item: ThreadItem | undefined;
  diff?: ToolDiffPreviewFileDiff;
  children: React.ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const diff = useMemo(
    () => explicitDiff ?? (item ? extractFileDiff(item) : undefined),
    [explicitDiff, item],
  );
  const preview = useHoverPreview(300, 120);
  const { triggerRef, rect, measure, clear } = useTriggerPosition();

  if (!diff) {
    return <>{children}</>;
  }

  const handleEnter = () => {
    measure();
    preview.enter();
  };

  const handleLeave = () => {
    preview.leave(clear);
  };

  const cardStyle: React.CSSProperties = rect
    ? (() => {
        const horizontalGutter = 24;
        const verticalGutter = 24;
        const triggerGap = 8;
        const cardHeightLimit = 360;
        const bounds = previewBoundsForTrigger(triggerRef.current);
        const boundLeft = bounds ? Math.max(0, bounds.left) : 0;
        const boundRight = bounds
          ? Math.min(window.innerWidth, bounds.right)
          : window.innerWidth;
        const minLeft = boundLeft + horizontalGutter;
        const maxRight = boundRight - horizontalGutter;
        const availableWidth = Math.max(240, maxRight - minLeft);
        const cardWidth = Math.min(
          640,
          Math.max(240, window.innerWidth - horizontalGutter * 2),
          availableWidth,
        );
        const left = Math.max(
          minLeft,
          Math.min(rect.left, maxRight - cardWidth),
        );
        const spaceAbove = Math.max(
          0,
          rect.top - triggerGap - verticalGutter,
        );
        const spaceBelow = Math.max(
          0,
          window.innerHeight - verticalGutter - rect.bottom - triggerGap,
        );
        // Prefer above when the full preview fits there. Otherwise use the
        // roomier side so the card never extends through the window frame.
        const showAbove =
          spaceAbove >= cardHeightLimit || spaceAbove >= spaceBelow;
        const availableHeight = showAbove ? spaceAbove : spaceBelow;
        return {
          position: "fixed",
          left,
          ...(showAbove
            ? { bottom: window.innerHeight - rect.top + triggerGap }
            : { top: rect.bottom + triggerGap }),
          width: cardWidth,
          maxHeight: Math.min(cardHeightLimit, availableHeight),
        };
      })()
    : {};

  return (
    <span
      ref={triggerRef}
      className="tool-diff-preview-trigger"
      onMouseEnter={handleEnter}
      onMouseLeave={handleLeave}
      onFocus={handleEnter}
      onBlur={handleLeave}
    >
      {children}
      {preview.active &&
        (
          <UILayerPortal layer="popover">
            <span
              className="tool-diff-preview-card"
              data-wuu-component="popover"
              data-wuu-layer="popover"
              data-wuu-state="open"
              role="region"
              style={cardStyle}
              aria-label={t("toolDiff.previewNamed", {
                name: diff.path ? diff.path : t("toolDiff.file"),
              })}
              onMouseEnter={preview.keepOpen}
              onMouseLeave={handleLeave}
            >
              <span className="tool-diff-preview-header">
                <span className="tool-diff-preview-title">
                  {diff.path ? diff.path : t("toolDiff.preview")}
                </span>
                {diff.truncated ? (
                  <span className="tool-diff-preview-truncated">
                    {t("toolDiff.truncated")}
                  </span>
                ) : null}
              </span>
              <span className="tool-diff-preview-body">
                <ToolDiffContent diff={diff} />
              </span>
            </span>
          </UILayerPortal>
        )}
    </span>
  );
}
