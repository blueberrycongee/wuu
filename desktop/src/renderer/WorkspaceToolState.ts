import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useSyncExternalStore } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import { showErrorToast } from "./Toast";
import type { RuntimeContext } from "../shared/protocol";
import { restoreArtifactPreviewFocus, type ArtifactPreviewRequest } from "./ArtifactPreviewContext";
import type { WorkspacePanelView } from "./WorkspacePanels";
import type { TurnFileDiffSelection } from "./TurnFileDiffTypes";
import type { RegisteredPluginViewEntry } from "./plugins/PluginHost";
import { workspaceArtifactViewTab, type WorkspaceViewTab } from "./WorkspaceViewTabs";

export function useWorkspaceToolState({
  application,
  rightPanelOpen,
  rightPanelGlobalized,
  setRightPanelOpenWithMotion
}: {
  application: Pick<ApplicationServiceV1, "getSnapshot" | "subscribe" | "invoke">;
  rightPanelOpen: boolean;
  rightPanelGlobalized: boolean;
  setRightPanelOpenWithMotion: (open: boolean) => void;
}): {
  // Unified right-panel tab strip: the four singleton tools plus zero or
  // more per-file diff tabs. See WorkspaceViewTabs.ts.
  workspaceViewTabs: WorkspaceViewTab[];
  workspaceActiveViewTabID: string | undefined;
  workspaceActiveFileTabID: string | undefined;
  workspaceConversationOpen: boolean;
  workspaceConversationActive: boolean;
  focusWorkspaceConversation: () => void;
  ensureWorkspaceToolTab: (view: WorkspacePanelView) => void;
  activateWorkspaceTool: (view: WorkspacePanelView) => void;
  openWorkspaceTool: (view: WorkspacePanelView) => void;
  openWorkspaceSideThread: () => void;
  openWorkspacePluginTool: (entry: RegisteredPluginViewEntry) => void;
  openWorkspaceDiffTab: (input: { threadID: string; path: string; selection: TurnFileDiffSelection }) => void;
  openWorkspaceFileTab: (input: { context: Exclude<RuntimeContext, { kind: "user" }>; path: string }) => void;
  openWorkspaceArtifactTab: (input: ArtifactPreviewRequest) => void;
  openWorkspaceProjectTab: (projectID: string, title: string) => void;
  syncWorkspaceProjectTab: (project: { id: string; title: string } | undefined) => void;
  showWorkspaceToolPicker: () => void;
  focusWorkspaceViewTab: (id: string | undefined) => void;
  closeWorkspaceViewTab: (id: string) => void;
  closeWorkspaceViewTabsWhere: (predicate: (tab: WorkspaceViewTab) => boolean) => void;
  reorderWorkspaceViewTabs: (activeID: string, overID: string) => void;
  toggleRightPanel: () => void;
} {
  const snapshot = useSyncExternalStore(application.subscribe, () => application.getSnapshot().previewWorkspace);
  const origins = useRef(new Map<string, ArtifactPreviewRequest["origin"]>());
  const workspaceViewTabs = useMemo(() => structuredClone(snapshot.tabs) as WorkspaceViewTab[], [snapshot.tabs]);
  const workspaceActiveViewTabID = snapshot.activeTabId;
  const workspaceActiveFileTabID = snapshot.activeFileTabId;
  const conversationSelected = snapshot.conversationSelected;
  useLayoutEffect(() => {
    if (!rightPanelGlobalized && snapshot.conversationOpen === false) {
      void application.invoke("preview.open-conversation", {}).catch(showErrorToast);
    }
  }, [application, rightPanelGlobalized, snapshot.conversationOpen]);
  useLayoutEffect(() => {
    if (rightPanelOpen && !conversationSelected && !application.getSnapshot().previewWorkspace.activeTabId) {
      void application.invoke("preview.new-tab", {}).catch(showErrorToast);
    }
  }, [application, rightPanelOpen, conversationSelected, workspaceActiveViewTabID]);
  useEffect(() => {
    if (rightPanelOpen && !rightPanelGlobalized && application.getSnapshot().previewWorkspace.conversationSelected) {
      const preview = application.getSnapshot().previewWorkspace;
      if (preview.tabs.length === 0) setRightPanelOpenWithMotion(false);
      else void application.invoke("preview.focus-tab", { tabId: preview.activeTabId ?? preview.tabs.at(-1)?.id }).catch(showErrorToast);
    }
  }, [application, rightPanelOpen, rightPanelGlobalized, conversationSelected, setRightPanelOpenWithMotion]);
  const focusWorkspaceConversation = useCallback(() => {
    void application.invoke("preview.show-conversation", {}).catch(showErrorToast);
  }, [application]);
  const focusTab = useCallback((tabId: string | undefined) => {
    void application.invoke("preview.focus-tab", { tabId }).catch(showErrorToast);
  }, [application]);
  function ensureWorkspaceToolTab(view: WorkspacePanelView): void {
    if (!application.getSnapshot().previewWorkspace.tabs.some(tab => tab.id === view)) {
      void application.invoke("preview.open-tool", { toolId: view, activate: false }).catch(showErrorToast);
    }
  }
  function activateWorkspaceTool(view: WorkspacePanelView): void {
    void application.invoke("preview.open-tool", { toolId: view }).catch(showErrorToast);
  }
  function openWorkspaceTool(view: WorkspacePanelView): void {
    activateWorkspaceTool(view);
    setRightPanelOpenWithMotion(true);
  }
  function openWorkspaceSideThread(): void {
    void application.invoke("preview.open-side-thread", {}).catch(showErrorToast);
    setRightPanelOpenWithMotion(true);
  }
  function openWorkspacePluginTool(entry: RegisteredPluginViewEntry): void {
    void application.invoke("preview.open-plugin", { entryId: entry.id, pluginId: entry.pluginId,
      generation: entry.generation, viewTypeId: entry.view, title: entry.title, icon: entry.icon }).catch(showErrorToast);
    setRightPanelOpenWithMotion(true);
  }
  function openWorkspaceDiffTab(input: { threadID: string; path: string; selection: TurnFileDiffSelection }): void {
    void application.invoke("preview.open-diff", { threadId: input.threadID, path: input.path, selection: input.selection }).catch(showErrorToast);
  }
  function openWorkspaceFileTab(input: { context: Exclude<RuntimeContext, { kind: "user" }>; path: string }): void {
    void application.invoke("preview.open-file", input).catch(showErrorToast);
    setRightPanelOpenWithMotion(true);
  }
  function openWorkspaceArtifactTab(input: ArtifactPreviewRequest): void {
    const id = workspaceArtifactViewTab(input).id;
    origins.current.set(id, input.origin);
    void application.invoke("preview.open-artifact", { threadId: input.threadID, artifactId: input.artifact.id,
      sha256: input.artifact.sha256, page: input.page }).catch(error => { origins.current.delete(id); showErrorToast(error); });
    setRightPanelOpenWithMotion(true);
  }
  function openWorkspaceProjectTab(projectId: string, title: string): void {
    void application.invoke("preview.open-project", { projectId, title }).catch(showErrorToast);
    setRightPanelOpenWithMotion(true);
  }
  const syncProjectTab = useCallback((project: { id: string; title: string } | undefined) => {
    void application.invoke("preview.follow-project", { project }).catch(showErrorToast);
  }, [application]);
  function showWorkspaceToolPicker(): void {
    void application.invoke("preview.new-tab", {}).catch(showErrorToast);
    setRightPanelOpenWithMotion(true);
  }
  function reorderTabs(tabId: string, overTabId: string): void {
    void application.invoke("preview.reorder-tab", { tabId, overTabId }).catch(showErrorToast);
  }
  function toggleRightPanel(): void {
    if (rightPanelOpen) {
      setRightPanelOpenWithMotion(false);
      return;
    }
    if (workspaceViewTabs.length === 0) {
      void application.invoke("preview.new-tab", {}).catch(showErrorToast);
    } else if (workspaceActiveViewTabID === undefined) {
      focusTab(workspaceViewTabs[workspaceViewTabs.length - 1].id);
    }
    setRightPanelOpenWithMotion(true);
  }

  // The expanded strip retains its conversation when the last resource closes.
  function closeWorkspaceViewTab(id: string): void {
    const tab = workspaceViewTabs.find((candidate) => candidate.id === id);
    if (rightPanelOpen && workspaceActiveViewTabID === id && tab?.kind === "artifact") restoreArtifactPreviewFocus(origins.current.get(id));
    const willEmpty =
      workspaceViewTabs.length === 1 && workspaceViewTabs[0]?.id === id;
    origins.current.delete(id);
    void application.invoke("preview.close-tab", { tabId: id }).catch(showErrorToast);
    if (willEmpty && !rightPanelGlobalized) setRightPanelOpenWithMotion(false);
  }

  function closeWorkspaceViewTabsWhere(
    predicate: (tab: WorkspaceViewTab) => boolean,
  ): void {
    const willEmpty =
      workspaceViewTabs.length > 0 && workspaceViewTabs.every(predicate);
    const tabIds = workspaceViewTabs.filter(predicate).map(tab => tab.id);
    tabIds.forEach(id => origins.current.delete(id));
    void application.invoke("preview.close-tabs", { tabIds }).catch(showErrorToast);
    if (willEmpty && !rightPanelGlobalized) setRightPanelOpenWithMotion(false);
  }

  return {
    workspaceViewTabs,
    workspaceActiveViewTabID,
    workspaceActiveFileTabID,
    workspaceConversationOpen: snapshot.conversationOpen !== false,
    workspaceConversationActive: rightPanelGlobalized && conversationSelected,
    focusWorkspaceConversation,
    ensureWorkspaceToolTab,
    activateWorkspaceTool,
    openWorkspaceTool,
    openWorkspaceSideThread,
    openWorkspacePluginTool,
    openWorkspaceDiffTab,
    openWorkspaceFileTab,
    openWorkspaceArtifactTab,
    openWorkspaceProjectTab,
    syncWorkspaceProjectTab: syncProjectTab,
    showWorkspaceToolPicker,
    focusWorkspaceViewTab: focusTab,
    closeWorkspaceViewTab,
    closeWorkspaceViewTabsWhere,
    reorderWorkspaceViewTabs: reorderTabs,
    toggleRightPanel
  };
}
