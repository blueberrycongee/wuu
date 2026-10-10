import { workspacePdfRevision } from "./PdfSelection";
import type { FileSelectionSource } from "../shared/protocol";
import { fileSelectionRevision, fileSelectionSource } from "./FileSelectionMapping";
import { formatWorkspaceFileTarget } from "./LinkTargets";

/** Resolve navigation against the current file without changing the saved excerpt. */
export function fileSelectionNavigation(source: FileSelectionSource, currentText?: string): {
  path: string;
  locationChanged: boolean;
} {
  const absolute = /^(?:[\\/]|[a-z]:[\\/])/i.test(source.path);
  const path = absolute ? source.path : `${source.workspace.replace(/[\\/]$/, "")}/${source.path}`;
  if (source.pdf) return { path, locationChanged: true };
  let currentSource: FileSelectionSource | null = null;
  if (currentText !== undefined) {
    if (fileSelectionRevision(currentText) === source.revision) {
      currentSource = source;
    } else if (source.quote) {
      const start = currentText.indexOf(source.quote);
      // Count overlapping matches too: any second occurrence is ambiguous.
      if (start >= 0 && currentText.indexOf(source.quote, start + 1) < 0) {
        currentSource = fileSelectionSource(source.workspace, source.path, currentText, {
          start, end: start + source.quote.length, blockFallback: false,
        });
      }
    }
  }
  // Monaco removes the leading BOM from its model, while saved excerpts retain
  // original-file coordinates. Only navigation uses the model's columns.
  const modelColumn = (line: number, column: number) =>
    currentText?.startsWith("\uFEFF") && line === 1 ? Math.max(1, column - 1) : column;
  return {
    path: formatWorkspaceFileTarget({
      kind: "workspace-file", path,
      ...(currentSource ? { selection: {
        startLineNumber: currentSource.start_line,
        startColumn: modelColumn(currentSource.start_line, currentSource.start_column),
        endLineNumber: currentSource.end_line,
        endColumn: modelColumn(currentSource.end_line, currentSource.end_column),
      } } : {}),
    }),
    locationChanged: currentSource === null,
  };
}

/** Inspect current workspace bytes before navigating a saved selection. */
export async function readFileSelectionNavigation(source: FileSelectionSource): Promise<{
  path: string; locationChanged: boolean; available: boolean;
}> {
  let file;
  try { file = await window.wuu.readWorkspaceFile(source.path, source.workspace); } catch { /* The saved quote remains readable. */ }
  if (source.pdf) {
    const current = file?.renderable_kind === "pdf" && workspacePdfRevision(file) === source.revision;
    const target = fileSelectionNavigation(source);
    return { path: current ? `${target.path}#pdf-page=${source.pdf.start_page}&selection=${crypto.randomUUID()}` : target.path,
      locationChanged: !current, available: Boolean(file) };
  }
  const currentText = file && !file.binary && !file.truncated ? file.text : undefined;
  return { ...fileSelectionNavigation(source, currentText), available: currentText !== undefined };
}
