import type { RuntimeContext } from "../shared/protocol";
import type { WorkspaceFileSelection } from "./LinkTargets";
import { initialWorkspaceViewTabsState, workspaceFileViewTab, type WorkspaceFileViewTab, type WorkspaceViewTabsState } from "./WorkspaceViewTabs";

const TABS_KEY = "wuu.desktop.previewFiles.v1";
const SCROLL_PREFIX = "wuu.desktop.previewScroll.v1:";

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function contextFrom(value: unknown): RuntimeContext | undefined {
  const context = record(value);
  if (!context || typeof context.cwd !== "string" || !context.cwd || context.cwd.length > 4096) return;
  if (context.kind === "no_project") return { kind: "no_project", cwd: context.cwd };
  if (context.kind === "project" && typeof context.project_id === "string" && context.project_id.length > 0 && context.project_id.length <= 512) {
    return { kind: "project", project_id: context.project_id, cwd: context.cwd };
  }
}

function selectionFrom(value: unknown): WorkspaceFileSelection | undefined {
  const selection = record(value);
  if (!selection) return;
  const positive = (number: unknown): number is number => typeof number === "number" && Number.isSafeInteger(number) && number > 0;
  if (!positive(selection.startLineNumber) || !positive(selection.startColumn)
    || selection.endLineNumber !== undefined && !positive(selection.endLineNumber)
    || selection.endColumn !== undefined && !positive(selection.endColumn)) return;
  return { startLineNumber: selection.startLineNumber, startColumn: selection.startColumn,
    ...(positive(selection.endLineNumber) ? { endLineNumber: selection.endLineNumber } : {}),
    ...(positive(selection.endColumn) ? { endColumn: selection.endColumn } : {}) };
}

export function readWorkspacePreviewRecovery(): WorkspaceViewTabsState {
  try {
    const raw = window.sessionStorage.getItem(TABS_KEY);
    if (!raw || raw.length > 512 * 1024) return initialWorkspaceViewTabsState;
    const saved = record(JSON.parse(raw));
    if (saved?.version !== 1 || !Array.isArray(saved.files) || saved.files.length > 100) return initialWorkspaceViewTabsState;
    const tabs: WorkspaceFileViewTab[] = [];
    for (const value of saved.files) {
      const item = record(value);
      const context = contextFrom(item?.context);
      if (!context || typeof item?.path !== "string" || !item.path || item.path.length > 4096) continue;
      const tab = workspaceFileViewTab({ context, path: item.path });
      const selection = selectionFrom(item.selection);
      const anchor = typeof item.anchor === "string" && item.anchor.length <= 4096 ? item.anchor : undefined;
      if (!tabs.some(existing => existing.id === tab.id)) tabs.push({ ...tab, ...(selection ? { selection } : {}), ...(anchor ? { anchor } : {}) });
    }
    const active = typeof saved.activeFileTabId === "string" && tabs.some(tab => tab.id === saved.activeFileTabId)
      ? saved.activeFileTabId : tabs.at(-1)?.id;
    return { tabs, activeTabID: active, activeFileTabID: active, activationHistory: tabs.filter(tab => tab.id !== active).map(tab => tab.id) };
  } catch {
    return initialWorkspaceViewTabsState;
  }
}

export function writeWorkspacePreviewRecovery(state: WorkspaceViewTabsState): void {
  try {
    const files = state.tabs.filter((tab): tab is WorkspaceFileViewTab => tab.kind === "file").slice(0, 100);
    if (!files.length) { window.sessionStorage.removeItem(TABS_KEY); return; }
    window.sessionStorage.setItem(TABS_KEY, JSON.stringify({ version: 1, files, activeFileTabId: state.activeFileTabID }));
  } catch { /* Preview recovery is optional when session storage is unavailable. */ }
}

export function readWorkspacePreviewScroll(id: string): number | undefined {
  try {
    const value = window.sessionStorage.getItem(SCROLL_PREFIX + id);
    if (value === null) return;
    const scroll = Number(value);
    return Number.isFinite(scroll) && scroll >= 0 ? scroll : undefined;
  } catch { return; }
}

export function writeWorkspacePreviewScroll(id: string, scroll: number): void {
  try { window.sessionStorage.setItem(SCROLL_PREFIX + id, String(scroll)); } catch { /* Storage is optional. */ }
}

export function clearWorkspacePreviewScroll(id: string): void {
  try { window.sessionStorage.removeItem(SCROLL_PREFIX + id); } catch { /* Storage is optional. */ }
}
