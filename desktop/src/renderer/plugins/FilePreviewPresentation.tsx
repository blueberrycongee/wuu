import { type ReactNode, useCallback, useMemo } from "react";

import type { WorkspaceFileReadResult } from "../../shared/protocol";
import {
  FILE_PREVIEW_ACTIONS,
  type FilePreviewSnapshotV1,
  type FileSelectionDescriptorV1,
} from "../../shared/workbench";
import type { WorkspaceFileSelection } from "../LinkTargets";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import type { PluginHost } from "./PluginHost";
import { PluginPresentation } from "./PluginPresentation";
import type { WorkbenchController } from "./Workbench";

interface FilePreviewCallbacks {
  open?: (path: string) => unknown | Promise<unknown>;
  reveal?: (path: string) => unknown | Promise<unknown>;
  select?: (path: string, selection: FileSelectionDescriptorV1) => unknown | Promise<unknown>;
  save?: (path: string, text: string) => unknown | Promise<unknown>;
  reload?: () => unknown | Promise<unknown>;
}

export interface FilePreviewPresentationProps extends FilePreviewCallbacks {
  workspaceRoot: string;
  workspaceRelativePath: string;
  file?: WorkspaceFileReadResult;
  text?: string;
  selection?: WorkspaceFileSelection;
  loading: boolean;
  error?: string;
  fallback: ReactNode;
  host?: PluginHost;
  controller?: WorkbenchController;
}

const EXTENSION_CONTENT_TYPES: Readonly<Record<string, string>> = Object.freeze({
  css: "text/css", csv: "text/csv", gif: "image/gif", html: "text/html",
  jpeg: "image/jpeg", jpg: "image/jpeg", js: "text/javascript", json: "application/json",
  jsx: "text/jsx", md: "text/markdown", mdx: "text/mdx", pdf: "application/pdf",
  png: "image/png", svg: "image/svg+xml", ts: "text/typescript", tsx: "text/tsx",
  txt: "text/plain", webp: "image/webp", xml: "application/xml", yaml: "application/yaml",
  yml: "application/yaml",
});

/**
 * Workspace reads do not currently carry MIME metadata. Until they do, use a
 * deterministic extension-derived content type, with binary/text defaults.
 */
export function filePreviewContentType(path: string, binary: boolean | undefined): string {
  const extension = path.split(".").at(-1)?.toLowerCase();
  return (extension && EXTENSION_CONTENT_TYPES[extension])
    ?? (binary ? "application/octet-stream" : "text/plain");
}

export function toFilePreviewSnapshot({
  workspaceRoot,
  workspaceRelativePath,
  file,
  text,
  selection,
  loading,
  error,
}: Pick<FilePreviewPresentationProps,
  "workspaceRoot" | "workspaceRelativePath" | "file" | "text" | "selection" | "loading" | "error"
>): FilePreviewSnapshotV1 {
  const publicSelection = selection === undefined ? undefined : Object.freeze({
    startLine: selection.startLineNumber,
    startColumn: selection.startColumn,
    endLine: selection.endLineNumber,
    endColumn: selection.endColumn,
  });
  const safeHostUrl = file?.renderable_url && /^wuu-file:\/\/local\/[A-Za-z0-9_-]+$/.test(file.renderable_url)
    ? file.renderable_url
    : undefined;
  return Object.freeze({
    contractVersion: 1,
    resourceId: opaqueResourceId(workspaceRoot, workspaceRelativePath),
    workspaceRelativePath,
    contentType: filePreviewContentType(workspaceRelativePath, file?.binary),
    text: file?.binary ? undefined : text,
    safeHostUrl,
    sizeBytes: file?.size_bytes,
    binary: file?.binary,
    truncated: file ? file.truncated && !file.binary : undefined,
    readOnly: true,
    dirty: false,
    loading,
    error,
    selection: publicSelection,
  });
}

export function FilePreviewPresentation({
  host = desktopPluginHost,
  controller = desktopWorkbenchController,
  fallback,
  open,
  reveal,
  select,
  save,
  reload,
  ...state
}: FilePreviewPresentationProps): JSX.Element {
  const snapshot = useMemo(() => toFilePreviewSnapshot(state), [
    state.error, state.file, state.loading, state.selection, state.text,
    state.workspaceRelativePath, state.workspaceRoot,
  ]);
  const callbacks = useMemo(() => ({ open, reveal, select, save, reload }), [open, reload, reveal, save, select]);
  const actions = useMemo(() => availableActions(callbacks), [callbacks]);
  const dispatchAction = useCallback(
    (action: string, input?: unknown) => dispatchFilePreviewAction(action, input, state.workspaceRelativePath, callbacks),
    [callbacks, state.workspaceRelativePath],
  );
  return (
    <PluginPresentation
      host={host}
      controller={controller}
      target="content.preview"
      presentationKey={snapshot.contentType}
      snapshot={snapshot}
      fallback={fallback}
      actions={actions}
      dispatchAction={dispatchAction}
    />
  );
}

function availableActions(callbacks: FilePreviewCallbacks): readonly string[] {
  return Object.freeze([
    ...(callbacks.open ? [FILE_PREVIEW_ACTIONS.open] : []),
    ...(callbacks.reveal ? [FILE_PREVIEW_ACTIONS.reveal] : []),
    ...(callbacks.select ? [FILE_PREVIEW_ACTIONS.select] : []),
    ...(callbacks.save ? [FILE_PREVIEW_ACTIONS.save] : []),
    ...(callbacks.reload ? [FILE_PREVIEW_ACTIONS.reload] : []),
  ]);
}

export async function dispatchFilePreviewAction(
  action: string,
  input: unknown,
  currentPath: string,
  callbacks: FilePreviewCallbacks,
): Promise<unknown> {
  const value = optionalRecord(input);
  const path = value?.path === undefined ? currentPath : requireCurrentPath(value.path, currentPath);
  switch (action) {
    case FILE_PREVIEW_ACTIONS.open:
      if (!callbacks.open) break;
      return callbacks.open(path);
    case FILE_PREVIEW_ACTIONS.reveal:
      if (!callbacks.reveal) break;
      return callbacks.reveal(path);
    case FILE_PREVIEW_ACTIONS.reload:
      if (!callbacks.reload || (input !== undefined && value === undefined)) break;
      return callbacks.reload();
    case FILE_PREVIEW_ACTIONS.select: {
      if (!callbacks.select || value === undefined) break;
      const selection = requireSelection(value.selection);
      return callbacks.select(path, selection);
    }
    case FILE_PREVIEW_ACTIONS.save:
      if (!callbacks.save || value === undefined || typeof value.text !== "string") break;
      return callbacks.save(path, value.text);
  }
  throw new TypeError("Unsupported or invalid file preview action");
}

function optionalRecord(value: unknown): Record<string, unknown> | undefined {
  if (value === undefined) return {};
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function requireCurrentPath(value: unknown, currentPath: string): string {
  if (typeof value !== "string" || value !== currentPath || !isSafeRelativePath(value)) {
    throw new TypeError("File preview action path must match the current workspace file");
  }
  return value;
}

function requireSelection(value: unknown): FileSelectionDescriptorV1 {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError("Invalid file preview selection");
  }
  const selection = value as Record<string, unknown>;
  const fields = ["startOffset", "endOffset", "startLine", "startColumn", "endLine", "endColumn"] as const;
  if (!fields.some((field) => selection[field] !== undefined)
    || fields.some((field) => selection[field] !== undefined
      && (!Number.isInteger(selection[field]) || (selection[field] as number) < 0))) {
    throw new TypeError("Invalid file preview selection");
  }
  return Object.freeze(Object.fromEntries(fields
    .filter((field) => selection[field] !== undefined)
    .map((field) => [field, selection[field]]))) as FileSelectionDescriptorV1;
}

function isSafeRelativePath(path: string): boolean {
  return path.length > 0 && !path.startsWith("/") && !path.includes("\\")
    && !path.includes("\0") && path.split("/").every((segment) => segment !== "" && segment !== ".." && segment !== ".");
}

function opaqueResourceId(root: string, path: string): string {
  let hash = 2166136261;
  for (const character of `${root}\n${path}`) {
    hash ^= character.charCodeAt(0);
    hash = Math.imul(hash, 16777619);
  }
  return `workspace-file-${(hash >>> 0).toString(36)}`;
}
