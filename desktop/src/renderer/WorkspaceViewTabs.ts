import { useCallback, useState } from "react";
import { arrayMove } from "@dnd-kit/sortable";
import type { ExtensionIconDescriptor, RuntimeContext } from "../shared/protocol";
import {
  parseWorkspaceFileTarget,
  type WorkspaceFileLinkTarget,
  type WorkspaceFileSelection,
} from "./LinkTargets";
import type { WorkspacePanelView } from "./WorkspacePanels";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import type { ArtifactPreviewRequest } from "./ArtifactPreviewContext";
import { workspacePathToSlash } from "./WorkspacePaths";

/**
 * Content shown in the workspace right panel's tab strip. Built-in tools are
 * singleton tabs, while files and diffs have one tab per resource. A file
 * tab renders its editor beside the persistent file tree.
 */
export type WorkspaceToolViewTab = {
  kind: WorkspacePanelView;
  id: WorkspacePanelView;
};

export type WorkspaceDiffViewTab = {
  kind: "diff";
  id: string;
  threadID: string;
  path: string;
  title: string;
  selection: TurnFileDiffSelection;
};

export type WorkspaceFileViewTab = {
  kind: "file";
  id: string;
  context: RuntimeContext;
  path: string;
  title: string;
  selection?: WorkspaceFileSelection;
  anchor?: string;
};

export type WorkspacePluginViewTab = {
  kind: "plugin";
  id: string;
  pluginId: string;
  fingerprint: string;
  viewTypeId: string;
  title: string;
  icon?: ExtensionIconDescriptor;
};

export type WorkspaceArtifactViewTab = ArtifactPreviewRequest & {
  kind: "artifact";
  id: string;
  title: string;
};

export function workspaceArtifactViewTab(input: ArtifactPreviewRequest): WorkspaceArtifactViewTab {
  const { artifact, threadID, cwd } = input;
  // Snapshot identity is separate from the editable workspace file path.
  const identity = [threadID, cwd, artifact.sha256 ?? artifact.uri ?? artifact.id, artifact.name, artifact.mimeType];
  return { ...input, kind: "artifact", id: `artifact:${JSON.stringify(identity)}`, title: artifact.name };
}

export type WorkspaceViewTab = WorkspaceToolViewTab | WorkspaceDiffViewTab | WorkspaceFileViewTab | WorkspacePluginViewTab | WorkspaceArtifactViewTab;

export type WorkspaceViewTabsState = {
  tabs: WorkspaceViewTab[];
  activeTabID: string | undefined;
  // Mirrors activeTabID when a file is active. Kept separately so the shell
  // can derive the selected tree path without inspecting view rendering.
  activeFileTabID: string | undefined;
  // Most-recently-active tab ids, oldest first, excluding the current
  // activeTabID. Lets closing a tab restore whichever tab was active
  // immediately before it, instead of always falling back to the tool
  // picker.
  activationHistory: string[];
};

export const initialWorkspaceViewTabsState: WorkspaceViewTabsState = {
  tabs: [],
  activeTabID: undefined,
  activeFileTabID: undefined,
  activationHistory: [],
};

export function workspaceToolViewTab(kind: WorkspacePanelView): WorkspaceToolViewTab {
  return { kind, id: kind };
}

export function workspacePluginViewTab(entry: {
  id: string;
  pluginId: string;
  generation: string;
  view: string;
  title: string;
  icon?: ExtensionIconDescriptor;
}): WorkspacePluginViewTab {
  return {
    kind: "plugin",
    id: `plugin:${entry.pluginId}:${entry.id}`,
    pluginId: entry.pluginId,
    fingerprint: entry.generation,
    viewTypeId: entry.view,
    title: entry.title,
    ...(entry.icon ? { icon: entry.icon } : {}),
  };
}

export function workspaceDiffViewTabID(threadID: string, path: string, artifactID?: string): string {
  return `diff:${threadID}:${artifactID ? `${encodeURIComponent(artifactID)}:` : ""}${encodeURIComponent(path)}`;
}

export function workspaceDiffViewTab({
  threadID,
  path,
  selection,
}: {
  threadID: string;
  path: string;
  selection: TurnFileDiffSelection;
}): WorkspaceDiffViewTab {
  return {
    kind: "diff",
    id: workspaceDiffViewTabID(threadID, path, selection.artifactID),
    threadID,
    path,
    title: workspaceFileBasename(path),
    selection,
  };
}

export function workspaceFileViewTabID(context: RuntimeContext, path: string): string {
  const normalizedPath = normalizeWorkspaceFileTabPath(
    context,
    workspaceFileTarget(path).path,
  );
  const contextKey =
    context.kind === "project"
      ? `project:${context.project_id}:${context.cwd}`
      : `no_project:${context.cwd}`;
  return `file:${encodeURIComponent(contextKey)}:${encodeURIComponent(normalizedPath)}`;
}

export function workspaceFileViewTab({
  context,
  path,
}: {
  context: RuntimeContext;
  path: string;
}): WorkspaceFileViewTab {
  const target = workspaceFileTarget(path);
  const normalizedPath = normalizeWorkspaceFileTabPath(context, target.path);
  return {
    kind: "file",
    id: workspaceFileViewTabID(context, normalizedPath),
    context,
    path: normalizedPath,
    title: workspaceFileBasename(normalizedPath),
    ...(target.selection ? { selection: target.selection } : {}),
    ...(target.anchor ? { anchor: target.anchor } : {}),
  };
}

function workspaceFileTarget(path: string): WorkspaceFileLinkTarget {
  return parseWorkspaceFileTarget(path) ?? { kind: "workspace-file", path };
}

export function normalizeWorkspaceFileTabPath(context: RuntimeContext, path: string): string {
  const normalizedRoot = workspacePathToSlash(context.cwd, context.cwd).replace(/\/+$/, "");
  const normalizedPath = workspacePathToSlash(path, context.cwd).replace(/\/+$/, "");
  const relativePath = normalizedPath.startsWith(`${normalizedRoot}/`)
    ? normalizedPath.slice(normalizedRoot.length + 1)
    : normalizedPath.replace(/^\/+/, "");
  return relativePath
    .split("/")
    .filter((segment) => segment.length > 0 && segment !== ".")
    .join("/");
}

export function workspaceFileBasename(path: string): string {
  const trimmed = path.replace(/[/\\]+$/, "");
  const separatorIndex = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  const base = separatorIndex < 0 ? trimmed : trimmed.slice(separatorIndex + 1);
  return base || path;
}

/**
 * Opens `tab`, replacing an existing resource with the same id or appending
 * a new one. Opening the first file replaces the temporary Files browser
 * entry; closing the final file restores that entry.
 */
export function openViewTab(
  state: WorkspaceViewTabsState,
  tab: WorkspaceViewTab,
  options: { activate?: boolean } = {},
): WorkspaceViewTabsState {
  const activate = options.activate !== false;
  if (tab.kind === "file") {
    const existingFileIndex = state.tabs.findIndex((candidate) => candidate.id === tab.id);
    const tabsWithoutBrowser = state.tabs.filter((candidate) => candidate.kind !== "files");
    const adjustedFileIndex = tabsWithoutBrowser.findIndex((candidate) => candidate.id === tab.id);
    const tabs = existingFileIndex < 0
      ? [...tabsWithoutBrowser, tab]
      : tabsWithoutBrowser.map((candidate, index) => (index === adjustedFileIndex ? tab : candidate));
    return activate ? focusViewTab({ ...state, tabs }, tab.id) : { ...state, tabs };
  }
  const index = state.tabs.findIndex((candidate) => candidate.id === tab.id);
  const tabs =
    index < 0 ? [...state.tabs, tab] : state.tabs.map((candidate, i) => (i === index ? tab : candidate));
  return activate ? focusViewTab({ ...state, tabs }, tab.id) : { ...state, tabs };
}

/** Focuses the tab with the given id (or the tool picker, when `id` is undefined). */
export function focusViewTab(state: WorkspaceViewTabsState, id: string | undefined): WorkspaceViewTabsState {
  const fileTab = id ? state.tabs.find((tab) => tab.id === id && tab.kind === "file") : undefined;
  if (fileTab) {
    if (state.activeTabID === id && state.activeFileTabID === id) {
      return state;
    }
    const activationHistory = state.activeTabID
      ? [...state.activationHistory.filter((entry) => entry !== state.activeTabID), state.activeTabID]
      : state.activationHistory;
    return { ...state, activeTabID: id, activeFileTabID: id, activationHistory };
  }
  if (state.activeTabID === id) {
    return state;
  }
  const activationHistory = state.activeTabID
    ? [...state.activationHistory.filter((entry) => entry !== state.activeTabID), state.activeTabID]
    : state.activationHistory;
  return {
    ...state,
    activeTabID: id,
    activeFileTabID: id === "files" ? undefined : state.activeFileTabID,
    activationHistory,
  };
}

/**
 * Closes the tab with the given id. If it was the active tab, focuses
 * whichever tab was active immediately before it (walking back through the
 * activation history for one that still exists after the close), falling
 * back to the tool picker (`activeTabID: undefined`) if there is none.
 */
export function closeViewTab(state: WorkspaceViewTabsState, id: string): WorkspaceViewTabsState {
  const closingTab = state.tabs.find((tab) => tab.id === id);
  let tabs = state.tabs.filter((tab) => tab.id !== id);
  if (
    closingTab?.kind === "file" &&
    state.activeTabID === id &&
    !tabs.some((tab) => tab.kind === "file")
  ) {
    tabs = [...tabs, workspaceToolViewTab("files")];
    return {
      tabs,
      activeTabID: "files",
      activeFileTabID: undefined,
      activationHistory: state.activationHistory.filter(
        (entry) => entry !== id && tabs.some((tab) => tab.id === entry),
      ),
    };
  }
  const activeFileTabID = state.activeFileTabID === id
    ? tabs.findLast((tab) => tab.kind === "file")?.id
    : state.activeFileTabID;
  let activationHistory = state.activationHistory.filter((entry) => entry !== id);
  if (state.activeTabID !== id) {
    return { ...state, tabs, activeFileTabID, activationHistory };
  }
  let activeTabID: string | undefined;
  while (activationHistory.length > 0) {
    const candidate = activationHistory[activationHistory.length - 1];
    activationHistory = activationHistory.slice(0, -1);
    if (tabs.some((tab) => tab.id === candidate)) {
      activeTabID = candidate;
      break;
    }
  }
  return { tabs, activationHistory, activeTabID, activeFileTabID };
}

/** Closes every tab matching `predicate`, one at a time, preserving closeViewTab's fallback-focus behavior. */
export function closeViewTabsWhere(
  state: WorkspaceViewTabsState,
  predicate: (tab: WorkspaceViewTab) => boolean,
): WorkspaceViewTabsState {
  let next = state;
  for (const tab of state.tabs) {
    if (predicate(tab)) {
      next = closeViewTab(next, tab.id);
    }
  }
  return next;
}

export function reorderViewTabs(
  state: WorkspaceViewTabsState,
  activeID: string,
  overID: string,
): WorkspaceViewTabsState {
  if (activeID === overID) {
    return state;
  }
  const sourceIndex = state.tabs.findIndex((tab) => tab.id === activeID);
  const targetIndex = state.tabs.findIndex((tab) => tab.id === overID);
  if (sourceIndex < 0 || targetIndex < 0) {
    return state;
  }
  return { ...state, tabs: arrayMove(state.tabs, sourceIndex, targetIndex) };
}

/**
 * React binding over the pure WorkspaceViewTabs state machine above. Not
 * yet wired into the app shell — WorkspaceToolState.ts composes this hook
 * to back the unified right-panel tab strip (tool tabs + diff tabs).
 */
export function useWorkspaceViewTabs(): {
  tabs: WorkspaceViewTab[];
  activeTabID: string | undefined;
  activeFileTabID: string | undefined;
  openTab: (tab: WorkspaceViewTab, options?: { activate?: boolean }) => void;
  focusTab: (id: string | undefined) => void;
  closeTab: (id: string) => void;
  closeTabsWhere: (predicate: (tab: WorkspaceViewTab) => boolean) => void;
  reorderTabs: (activeID: string, overID: string) => void;
} {
  const [state, setState] = useState<WorkspaceViewTabsState>(initialWorkspaceViewTabsState);

  const openTab = useCallback((tab: WorkspaceViewTab, options?: { activate?: boolean }) => {
    setState((current) => openViewTab(current, tab, options));
  }, []);
  const focusTab = useCallback((id: string | undefined) => {
    setState((current) => focusViewTab(current, id));
  }, []);
  const closeTab = useCallback((id: string) => {
    setState((current) => closeViewTab(current, id));
  }, []);
  const closeTabsWhere = useCallback((predicate: (tab: WorkspaceViewTab) => boolean) => {
    setState((current) => closeViewTabsWhere(current, predicate));
  }, []);
  const reorderTabs = useCallback((activeID: string, overID: string) => {
    setState((current) => reorderViewTabs(current, activeID, overID));
  }, []);

  return {
    tabs: state.tabs,
    activeTabID: state.activeTabID,
    activeFileTabID: state.activeFileTabID,
    openTab,
    focusTab,
    closeTab,
    closeTabsWhere,
    reorderTabs,
  };
}
