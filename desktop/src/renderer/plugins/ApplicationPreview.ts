import type { ApplicationPreviewActionMapV1, ApplicationPreviewWorkspaceV1 } from "../../../../packages/plugin-sdk/src/ui-preview";
import type { Thread } from "../../shared/protocol";
import { validPdfSelectionReference } from "../PdfSelection";
import { clearWorkspacePreviewScroll, readWorkspacePreviewRecovery, writeWorkspacePreviewRecovery } from "../WorkspacePreviewRecovery";
import { collectTurnArtifacts } from "../ArtifactOutputs";
import type { TurnFileDiffSelection } from "../TurnFileDiffTypes";
import { closeViewTab, closeViewTabsWhere, focusViewTab, openViewTab, reorderViewTabs,
  workspaceArtifactViewTab, workspaceDiffViewTab, workspaceFileViewTab, workspacePluginViewTab, workspaceProjectViewTab,
  workspaceToolViewTab, WORKSPACE_CONVERSATION_TAB_ID, type WorkspaceViewTabsState } from "../WorkspaceViewTabs";

type PreviewTabs = Pick<ApplicationPreviewWorkspaceV1, "tabs" | "activeTabId" | "activeFileTabId" | "conversationOpen" | "conversationSelected">;

const actions = new Set<keyof ApplicationPreviewActionMapV1>([
  "preview.open-file", "preview.open-diff", "preview.open-artifact", "preview.open-tool", "preview.open-plugin", "preview.open-project",
  "preview.open-side-thread", "preview.new-tab", "preview.focus-tab", "preview.close-tab", "preview.close-tabs", "preview.reorder-tab",
  "preview.follow-project", "preview.open-conversation", "preview.close-conversation", "preview.show-conversation",
]);
export function isApplicationPreviewAction(action: string): action is keyof ApplicationPreviewActionMapV1 {
  return actions.has(action as keyof ApplicationPreviewActionMapV1);
}

/** Resource tabs survive selected UI replacement; geometry and DOM focus do not. */
export class ApplicationPreview {
  private state: WorkspaceViewTabsState = readWorkspacePreviewRecovery();
  private conversationOpen = true;
  private snapshot: PreviewTabs;
  constructor(private readonly host: { artifacts?: () => import("../../../../packages/plugin-sdk/src/ui-artifacts").ApplicationArtifactsV1; getThreads: () => readonly Thread[]; changed: () => void }) {
    this.snapshot = this.project(this.state, true);
  }

  getSnapshot(): PreviewTabs { return this.snapshot; }

  private update(state: WorkspaceViewTabsState, conversationOpen = this.conversationOpen): void {
    if (state === this.state && conversationOpen === this.conversationOpen) return;
    this.state = state;
    this.conversationOpen = conversationOpen;
    this.snapshot = this.project(state, conversationOpen);
    writeWorkspacePreviewRecovery(state);
    this.host.changed();
  }

  private project(state: WorkspaceViewTabsState, conversationOpen: boolean): PreviewTabs {
    const conversationSelected = state.activeTabID === WORKSPACE_CONVERSATION_TAB_ID;
    // Keep the last resource available to the dock while the expanded strip
    // uses the same selection history for conversations and resources.
    const activeTabId = conversationSelected
      ? state.activationHistory.findLast(id => state.tabs.some(tab => tab.id === id))
      : state.activeTabID;
    // Inputs are data-only; no origin WeakRef or render callbacks enter this owner.
    const snapshot: PreviewTabs = structuredClone({ tabs: state.tabs,
      activeTabId, activeFileTabId: state.activeFileTabID, conversationOpen, conversationSelected });
    const freeze = (value: unknown): void => {
      if (!value || typeof value !== "object") return;
      Object.values(value).forEach(freeze);
      Object.freeze(value);
    };
    freeze(snapshot);
    return snapshot;
  }

  private closeTabs(tabIds: readonly string[]): void {
    for (const tab of this.state.tabs) if (tab.kind === "file" && tabIds.includes(tab.id)) clearWorkspacePreviewScroll(tab.id);
    const state = closeViewTabsWhere(this.state, tab => tabIds.includes(tab.id));
    if (state.activeTabID !== undefined) {
      this.update(state);
    } else if (!this.conversationOpen && state.tabs.length > 0) {
      this.update(focusViewTab(state, state.tabs[state.tabs.length - 1].id));
    } else {
      this.update(focusViewTab(state, WORKSPACE_CONVERSATION_TAB_ID), true);
    }
  }

  async invoke<Action extends keyof ApplicationPreviewActionMapV1>(action: Action,
    input: ApplicationPreviewActionMapV1[Action]["input"], signal?: AbortSignal): Promise<ApplicationPreviewActionMapV1[Action]["result"]> {
    signal?.throwIfAborted();
    let result: unknown;
    switch (action) {
      case "preview.open-file": {
        const { context, path } = input as ApplicationPreviewActionMapV1["preview.open-file"]["input"];
        const tab = workspaceFileViewTab({ context: { ...context }, path });
        if (tab.selection || tab.anchor) clearWorkspacePreviewScroll(tab.id);
        this.update(openViewTab(this.state, tab)); break;
      }
      case "preview.open-diff": {
        const { threadId, path, selection } = input as ApplicationPreviewActionMapV1["preview.open-diff"]["input"];
        if (selection.path !== path) throw new Error("Diff selection does not match its file");
        this.update(openViewTab(this.state, workspaceDiffViewTab({ threadID: threadId, path,
          selection: structuredClone(selection) as TurnFileDiffSelection }))); break;
      }
      case "preview.open-artifact": {
        const { threadId, artifactId, sha256, page } = input as ApplicationPreviewActionMapV1["preview.open-artifact"]["input"];
        const thread = this.host.getThreads().find(thread => thread.id === threadId);
        let artifact = thread?.turns.flatMap(collectTurnArtifacts).find(artifact => artifact.id === artifactId
          || sha256 !== undefined && artifact.uri === artifactId && artifact.sha256 === sha256);
        if (page !== undefined && (!Number.isSafeInteger(page) || page < 1)) throw new Error("Artifact page must be a positive integer");
        if (!artifact && validPdfSelectionReference({ start_page: page ?? 1, end_page: page ?? 1,
          artifact_uri: artifactId, artifact_sha256: sha256, artifact_thread_id: threadId })) {
          const artifacts = this.host.artifacts?.();
          if (!artifacts) throw new Error("Selected artifact loading is unavailable");
          const candidate = { id: artifactId, itemId: "", index: 0, placement: "inline" as const, type: "file" as const,
            name: "Document.pdf", mimeType: "application/pdf", uri: artifactId, sha256, locator:thread?.session_locator, delivered: true };
          const source = await artifacts.acquireSource({...candidate, threadId}, {signal});
          try {
            const response = await fetch(source.url, { headers: { Range: "bytes=0-0" }, signal });
            await response.body?.cancel(); signal?.throwIfAborted();
            if (response.ok && response.headers.get("Content-Type")?.split(";")[0] === "application/pdf") artifact = candidate;
          } finally { if (source.leaseId) artifacts.releaseSource(source.leaseId); }
        }
        if (!artifact || sha256 !== undefined && artifact.sha256 !== sha256) throw new Error("Artifact is unavailable or its snapshot changed");
        this.update(openViewTab(this.state, workspaceArtifactViewTab({ threadID: threadId, cwd: thread?.cwd,
          artifact: structuredClone({...artifact,locator:thread?.session_locator}), page, pageRequest: page === undefined ? undefined : crypto.randomUUID() }))); break;
      }
      case "preview.open-tool": {
        const { toolId, activate = true } = input as ApplicationPreviewActionMapV1["preview.open-tool"]["input"];
        this.update(openViewTab(this.state, workspaceToolViewTab(toolId), { activate, replaceActiveNewTab: activate })); break;
      }
      case "preview.open-plugin": {
        const value = input as ApplicationPreviewActionMapV1["preview.open-plugin"]["input"];
        this.update(openViewTab(this.state, workspacePluginViewTab({ id: value.entryId, pluginId: value.pluginId,
          generation: value.generation, view: value.viewTypeId, title: value.title, icon: value.icon && { ...value.icon } }), { replaceActiveNewTab: true })); break;
      }
      case "preview.open-project": {
        const { projectId, title } = input as ApplicationPreviewActionMapV1["preview.open-project"]["input"];
        this.update(openViewTab(this.state, workspaceProjectViewTab(projectId, title))); break;
      }
      case "preview.follow-project": {
        const { project } = input as ApplicationPreviewActionMapV1["preview.follow-project"]["input"];
        const tab = this.state.tabs.find(tab => tab.kind === "project");
        if (tab && project) this.update(openViewTab(this.state, workspaceProjectViewTab(project.id, project.title), { activate: false }));
        else if (tab) this.closeTabs([tab.id]);
        break;
      }
      case "preview.open-side-thread": this.update(openViewTab(this.state, { kind: "side-thread", id: "side-thread" }, { replaceActiveNewTab: true })); break;
      case "preview.new-tab": {
        const tabId = `new:${crypto.randomUUID()}`;
        this.update(openViewTab(this.state, { kind: "new", id: tabId })); result = { tabId }; break;
      }
      case "preview.focus-tab": {
        const { tabId } = input as ApplicationPreviewActionMapV1["preview.focus-tab"]["input"];
        if (tabId !== undefined && !this.state.tabs.some(tab => tab.id === tabId)) throw new Error(`Unknown preview tab: ${tabId}`);
        this.update(focusViewTab(this.state, tabId)); break;
      }
      case "preview.close-tab": {
        const { tabId } = input as ApplicationPreviewActionMapV1["preview.close-tab"]["input"];
        this.closeTabs([tabId]); break;
      }
      case "preview.close-tabs": {
        const { tabIds } = input as ApplicationPreviewActionMapV1["preview.close-tabs"]["input"];
        this.closeTabs(tabIds); break;
      }
      case "preview.reorder-tab": {
        const { tabId, overTabId } = input as ApplicationPreviewActionMapV1["preview.reorder-tab"]["input"];
        this.update(reorderViewTabs(this.state, tabId, overTabId)); break;
      }
      case "preview.open-conversation": this.update(this.state, true); break;
      case "preview.close-conversation": {
        let state = closeViewTab(this.state, WORKSPACE_CONVERSATION_TAB_ID);
        if (state.activeTabID === undefined && state.tabs.length > 0) state = focusViewTab(state, state.tabs[state.tabs.length - 1].id);
        this.update(state, false); break;
      }
      case "preview.show-conversation": this.update(focusViewTab(this.state, WORKSPACE_CONVERSATION_TAB_ID), true); break;
    }
    return result as ApplicationPreviewActionMapV1[Action]["result"];
  }
}
