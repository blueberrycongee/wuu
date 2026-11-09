import { useState } from "react";
import { ChevronRight, FileDiff } from "./WuuIcons";
import type { ThreadItem, Turn } from "../shared/protocol";
import {
  extractToolDiffPreview,
  ToolDiffPreview,
  type ToolDiffPreviewFileDiff,
} from "./ToolDiffPreview";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import { turnIsAnswerReady } from "./AppState";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import {
  TURN_OUTPUT_SUMMARY_BATCH_SIZE,
  TurnOutputSummaryCard,
  TurnOutputSummaryChevron,
  TurnOutputSummaryMore,
} from "./TurnOutputSummaryCard";

type FileEdit = {
  path: string;
  item: ThreadItem;
  diff?: ToolDiffPreviewFileDiff;
  additions: number;
  deletions: number;
  newFile: boolean;
  action: "create" | "update" | "delete" | "rename";
  snapshotText?: string;
  afterSha?: string;
};

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

function toolResultRecord(item: ThreadItem): Record<string, unknown> | undefined {
  const structured = item.result_detail?.structured_content;
  if (isRecord(structured)) return structured;

  const direct = parseJSON(item.result);
  if (isRecord(direct)) return direct;

  for (const part of item.result_detail?.content ?? []) {
    if (part.type !== "text") continue;
    const parsed = parseJSON(part.text);
    if (isRecord(parsed)) return parsed;
  }
  return undefined;
}

function stringValue(record: unknown, key: string): string | undefined {
  if (!isRecord(record)) return undefined;
  const value = record[key];
  return typeof value === "string" ? value : undefined;
}

function numberValue(record: unknown, key: string): number | undefined {
  if (!isRecord(record)) return undefined;
  const value = record[key];
  return typeof value === "number" ? value : undefined;
}

function arrayValue(record: unknown, key: string): unknown[] {
  if (!isRecord(record)) return [];
  const value = record[key];
  return Array.isArray(value) ? value : [];
}

function summarizeDiff(diff: Record<string, unknown>): {
  additions: number;
  deletions: number;
} {
  if (diff.new_file === true) {
    return {
      additions: numberValue(diff, "lines") ?? numberValue(diff, "new_lines") ?? 0,
      deletions: 0,
    };
  }
  const oldLines = numberValue(diff, "old_lines");
  const newLines = numberValue(diff, "new_lines");
  if (oldLines !== undefined || newLines !== undefined) {
    return {
      additions: newLines ?? 0,
      deletions: oldLines ?? 0,
    };
  }
  let additions = 0;
  let deletions = 0;
  for (const hunk of arrayValue(diff, "hunks")) {
    if (!isRecord(hunk)) continue;
    for (const line of arrayValue(hunk, "lines")) {
      if (!isRecord(line)) continue;
      if (line.op === "insert") additions++;
      else if (line.op === "delete") deletions++;
    }
  }
  return { additions, deletions };
}

function summarizeRisk(result: Record<string, unknown>): {
  additions: number;
  deletions: number;
} {
  const riskSummary = isRecord(result.risk_summary)
    ? result.risk_summary
    : undefined;
  if (!riskSummary) {
    return { additions: 0, deletions: 0 };
  }
  return {
    additions: numberValue(riskSummary, "added_lines") ?? 0,
    deletions: numberValue(riskSummary, "deleted_lines") ?? 0,
  };
}

function firstChangedFilePath(result: Record<string, unknown>): string | undefined {
  const changedFiles = arrayValue(result, "changed_files")
    .filter((value): value is string => typeof value === "string" && value.trim().length > 0);
  if (changedFiles.length > 0) {
    return changedFiles[0];
  }
  for (const file of arrayValue(result, "files")) {
    if (!isRecord(file)) continue;
    const path = stringValue(file, "path") ?? stringValue(file, "move_path");
    if (path) return path;
  }
  return undefined;
}

function filePathFromPatchFile(file: Record<string, unknown>): string | undefined {
  return stringValue(file, "move_path") ?? stringValue(file, "path");
}

function artifactAction(
  value: string | undefined,
  newFile: boolean,
): FileEdit["action"] {
  if (newFile || value === "add" || value === "create") return "create";
  if (value === "delete") return "delete";
  if (value === "move" || value === "rename") return "rename";
  return "update";
}

function normalizedArtifactPath(path: string): string {
  return path.replace(/\\/g, "/").replace(/^\.\//, "");
}

function addedFileTextFromPatch(patchText: string, path: string): string | undefined {
  const lines = patchText.replace(/\r\n?/g, "\n").split("\n");
  const wanted = normalizedArtifactPath(path);
  for (let index = 0; index < lines.length; index++) {
    const match = /^\*\*\* Add File: (.+)$/.exec(lines[index]);
    if (!match || normalizedArtifactPath(match[1].trim()) !== wanted) continue;
    const content: string[] = [];
    for (index += 1; index < lines.length; index++) {
      if (lines[index].startsWith("*** ")) break;
      if (!lines[index].startsWith("+")) return undefined;
      content.push(lines[index].slice(1));
    }
    return content.length > 0 ? `${content.join("\n")}\n` : "";
  }
  return undefined;
}

function artifactSnapshotText(item: ThreadItem, path: string): string | undefined {
  const args = parseJSON(item.arguments);
  if (!isRecord(args)) return undefined;
  if (item.name === "write_file") {
    return stringValue(args, "content");
  }
  if (item.name === "apply_patch") {
    const patchText =
      stringValue(args, "patchText") ??
      stringValue(args, "patch_text") ??
      stringValue(args, "patch");
    return patchText ? addedFileTextFromPatch(patchText, path) : undefined;
  }
  return undefined;
}

function addedFileDiff(
  diff: ToolDiffPreviewFileDiff | undefined,
  path: string,
  snapshotText: string | undefined,
): ToolDiffPreviewFileDiff | undefined {
  if (snapshotText === undefined || (diff?.hunks.length ?? 0) > 0) return diff;

  const contentLines = snapshotText.replace(/\r\n?/g, "\n").split("\n");
  if (contentLines.at(-1) === "") contentLines.pop();
  return {
    ...diff,
    path,
    newFile: true,
    lines: diff?.lines ?? contentLines.length,
    hunks: contentLines.length > 0
      ? [{
          oldStart: 1,
          newStart: 1,
          lines: contentLines.map((content) => ({ op: "insert" as const, content })),
        }]
      : [],
  };
}

function extractPatchFileEdits(
  item: ThreadItem,
  result: Record<string, unknown>,
): FileEdit[] {
  const edits: FileEdit[] = [];
  for (const file of arrayValue(result, "files")) {
    if (!isRecord(file)) continue;
    const path = filePathFromPatchFile(file);
    if (!path) continue;
    const diffRecord = isRecord(file.diff) ? file.diff : undefined;
    const diff = diffRecord
      ? extractToolDiffPreview(diffRecord, path)
      : undefined;
    const stats = diffRecord ? summarizeDiff(diffRecord) : { additions: 0, deletions: 0 };
    const newFile = diffRecord?.new_file === true || file.action === "add";
    const snapshotText = newFile ? artifactSnapshotText(item, path) : undefined;
    edits.push({
      path,
      item,
      diff: newFile ? addedFileDiff(diff, path, snapshotText) : diff,
      additions: stats.additions,
      deletions: stats.deletions,
      newFile,
      action: artifactAction(stringValue(file, "action"), newFile),
      snapshotText,
      afterSha: stringValue(file, "new_file_sha"),
    });
  }
  return edits;
}

function extractFileEdits(item: ThreadItem): FileEdit[] {
  const name = (item.name ?? "").trim();
  const capability = item.display?.capability?.trim();
  const isEditTool =
    name === "edit_file" ||
    name === "write_file" ||
    name === "apply_patch" ||
    capability === "file.edit";
  if (!isEditTool) return [];

  const result = toolResultRecord(item);
  if (!result) return [];

  const patchFileEdits = extractPatchFileEdits(item, result);
  if (patchFileEdits.length > 0) {
    return patchFileEdits;
  }

  const path =
    stringValue(result, "path") ??
    stringValue(result, "file") ??
    firstChangedFilePath(result) ??
    stringValue(isRecord(result.diff) ? result.diff : undefined, "path");
  if (!path) return [];

  const diffRecord = isRecord(result.diff) ? result.diff : undefined;
  const newFile = diffRecord?.new_file === true || result.new_file === true;
  const newFileLines = numberValue(diffRecord, "lines") ?? 0;

  // For newly-created files the backend returns { new_file: true, lines: N }
  // instead of hunks. Treat those as additions so the card still surfaces them.
  if (newFile && newFileLines > 0) {
    const snapshotText = artifactSnapshotText(item, path);
    const diff = diffRecord ? extractToolDiffPreview(diffRecord, path) : undefined;
    return [{
      path,
      item,
      diff: addedFileDiff(diff, path, snapshotText),
      additions: newFileLines,
      deletions: 0,
      newFile,
      action: "create",
      snapshotText,
      afterSha: stringValue(result, "new_file_sha"),
    }];
  }

  const diffStats = diffRecord ? summarizeDiff(diffRecord) : summarizeRisk(result);

  return [{
    path,
    item,
    diff: diffRecord ? extractToolDiffPreview(diffRecord, path) : undefined,
    additions: diffStats.additions,
    deletions: diffStats.deletions,
    newFile,
    action: artifactAction(stringValue(result, "action"), newFile),
    snapshotText: artifactSnapshotText(item, path),
    afterSha: stringValue(result, "new_file_sha"),
  }];
}

function fileDisplayName(path: string): string {
  return path.replaceAll("\\", "/");
}

function collectTurnFileEdits(turn: Turn): FileEdit[] {
  const edits: FileEdit[] = [];
  for (const item of turn.items) {
    if (item.type !== "tool_call") {
      continue;
    }
    edits.push(...extractFileEdits(item));
  }
  return edits;
}

function aggregateFileEdits(edits: FileEdit[]): FileEdit[] {
  const byPath = new Map<string, FileEdit>();
  for (const edit of edits) {
    const existing = byPath.get(edit.path);
    if (existing) {
      existing.additions += edit.additions;
      existing.deletions += edit.deletions;
      // Keep the latest operation as the source of the stable artifact view.
      existing.item = edit.item;
      existing.diff = edit.diff;
      existing.snapshotText = edit.snapshotText ?? existing.snapshotText;
      existing.afterSha = edit.afterSha ?? existing.afterSha;
      if (existing.action !== "create") existing.action = edit.action;
    } else {
      byPath.set(edit.path, { ...edit });
    }
  }
  return Array.from(byPath.values());
}

export function turnHasFileEdits(turn: Turn): boolean {
  return collectTurnFileEdits(turn).length > 0;
}

function EditStats({ additions, deletions }: { additions: number; deletions: number }): JSX.Element | null {
  if (additions === 0 && deletions === 0) return null;
  return (
    <span className="turn-edit-summary-stats">
      {additions > 0 ? <span className="turn-edit-summary-add">+{additions}</span> : null}
      {deletions > 0 ? <span className="turn-edit-summary-delete">-{deletions}</span> : null}
    </span>
  );
}

export function TurnEditSummaryCard({
  turn,
  cwd,
  onOpenFile,
  onOpenFileDiff,
  compact = false,
}: {
  turn: Turn;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenFileDiff?: (selection: TurnFileDiffSelection) => void;
  compact?: boolean;
}): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const [visibleCount, setVisibleCount] = useState(TURN_OUTPUT_SUMMARY_BATCH_SIZE);
  const [expanded, setExpanded] = useState(false);

  if (turn.status === "in_progress" && !turnIsAnswerReady(turn)) return null;

  const rawEdits = collectTurnFileEdits(turn);
  const edits = aggregateFileEdits(rawEdits);

  if (edits.length === 0) return null;

  const visibleEdits = edits.slice(0, visibleCount);
  const hiddenCount = Math.max(0, edits.length - visibleCount);
  const nextCount = Math.min(TURN_OUTPUT_SUMMARY_BATCH_SIZE, hiddenCount);
  const additions = edits.reduce((total, edit) => total + edit.additions, 0);
  const deletions = edits.reduce((total, edit) => total + edit.deletions, 0);
  const title = t(
    edits.length === 1 ? "turnEdits.countOne" : "turnEdits.count",
    { count: formatNumber(edits.length) },
  );

  const openEdit = (edit: FileEdit): void => {
    if (onOpenFile) {
      onOpenFile(edit.path);
      return;
    }
    onOpenFileDiff?.({
      artifactID: edit.item.id,
      path: edit.path,
      cwd,
      action: edit.action,
      diff: edit.diff,
      snapshotText: edit.snapshotText,
      afterSha: edit.afterSha,
      additions: edit.additions,
      deletions: edit.deletions,
      newFile: edit.newFile,
    });
  };

  if (compact) {
    return (
      <div className="turn-edit-summary-compact">
        <button
          type="button"
          className="turn-edit-summary-toggle"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          <span>{t(turn.status === "interrupted" ? "turnEdits.retainedStopped" : "turnEdits.retained", { count: formatNumber(edits.length) })}</span>
          <EditStats additions={additions} deletions={deletions} />
          <ChevronRight className="icon-xs" aria-hidden="true" />
        </button>
        {expanded ? (
          <TurnEditSummaryCard
            turn={turn}
            cwd={cwd}
            onOpenFile={onOpenFile}
            onOpenFileDiff={onOpenFileDiff}
          />
        ) : null}
      </div>
    );
  }

  const singleEdit = edits.length === 1 ? edits[0] : undefined;
  const canOpen = Boolean(onOpenFile || onOpenFileDiff);
  const icon = <FileDiff className="icon" />;

  if (singleEdit) {
    return (
      <TurnOutputSummaryCard
        icon={icon}
        title={title}
        subtitle={
          <Tooltip content={singleEdit.path}>
            <span className="turn-edit-summary-overview-path">
              {fileDisplayName(singleEdit.path)}
            </span>
          </Tooltip>
        }
        trailing={
          <>
            <EditStats additions={singleEdit.additions} deletions={singleEdit.deletions} />
            {canOpen ? <TurnOutputSummaryChevron /> : null}
          </>
        }
        onOpen={canOpen ? () => openEdit(singleEdit) : undefined}
        openLabel={t("turnEdits.openFile", { path: singleEdit.path })}
        wrapOverview={(overview) => (
          <ToolDiffPreview diff={singleEdit.diff} item={singleEdit.item}>
            {overview}
          </ToolDiffPreview>
        )}
      />
    );
  }

  return (
    <TurnOutputSummaryCard
      icon={icon}
      title={title}
      subtitle={
        <span className="turn-edit-summary-overview-meta">
          <EditStats additions={additions} deletions={deletions} />
        </span>
      }
      rows={visibleEdits.map((edit) => ({
        key: edit.path,
        name: fileDisplayName(edit.path),
        tooltip: edit.path,
        trailing: <EditStats additions={edit.additions} deletions={edit.deletions} />,
        onOpen: canOpen ? () => openEdit(edit) : undefined,
        openLabel: t("turnEdits.openFile", { path: edit.path }),
        wrap: (row) => (
          <ToolDiffPreview diff={edit.diff} item={edit.item}>
            {row}
          </ToolDiffPreview>
        ),
      }))}
      footer={hiddenCount > 0 ? (
        <TurnOutputSummaryMore
          hiddenCount={hiddenCount}
          nextCount={nextCount}
          onShowMore={() =>
            setVisibleCount((current) => Math.min(current + TURN_OUTPUT_SUMMARY_BATCH_SIZE, edits.length))
          }
        />
      ) : null}
    />
  );
}
