import type { FileSelectionSource, PdfSelectionReference, WorkspaceFileReadResult } from "../shared/protocol";

export type PdfPreviewSource = Pick<FileSelectionSource, "workspace" | "path" | "revision"> & {
  artifact?: Pick<PdfSelectionReference, "artifact_uri" | "artifact_sha256" | "artifact_thread_id">;
};

// Workspace revisions identify the observed file, without pretending the bounded
// text-preview digest is a digest of the complete PDF.
export function workspacePdfRevision(file: WorkspaceFileReadResult): string {
  return `workspace-pdf:${file.size_bytes}:${file.mtime_ms}:${file.sha256}`;
}

export function validPdfSelectionReference(value: unknown): value is PdfSelectionReference {
  if (!value || typeof value !== "object") return false;
  const pdf = value as Record<string, unknown>;
  if (!Number.isSafeInteger(pdf.start_page) || (pdf.start_page as number) < 1
    || !Number.isSafeInteger(pdf.end_page) || (pdf.end_page as number) < (pdf.start_page as number)) return false;
  if (pdf.artifact_uri === undefined && pdf.artifact_sha256 === undefined && pdf.artifact_thread_id === undefined) return true;
  if (typeof pdf.artifact_uri !== "string" || typeof pdf.artifact_sha256 !== "string"
    || !/^[a-f0-9]{64}$/i.test(pdf.artifact_sha256) || typeof pdf.artifact_thread_id !== "string" || !pdf.artifact_thread_id) return false;
  try {
    const uri = new URL(pdf.artifact_uri);
    const segments = uri.pathname.slice(1).split("/").map(decodeURIComponent);
    return uri.protocol === "wuu-artifact:" && segments.length === 3
      && segments[0] === pdf.artifact_thread_id && uri.searchParams.get("sha256") === pdf.artifact_sha256;
  } catch { return false; }
}

export function fileSelectionLocation(source: FileSelectionSource): string {
  if (source.pdf) return source.pdf.start_page === source.pdf.end_page
    ? `p. ${source.pdf.start_page}` : `pp. ${source.pdf.start_page}–${source.pdf.end_page}`;
  return `${source.start_line}:${source.start_column}–${source.end_line}:${source.end_column}`;
}
