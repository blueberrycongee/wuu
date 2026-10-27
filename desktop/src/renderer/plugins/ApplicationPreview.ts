import type { ApplicationPreviewActionMapV1, ApplicationPreviewWorkspaceV1 } from "../../../../packages/plugin-sdk/src/ui-preview";
import type { Thread } from "../../shared/protocol";
import { validPdfSelectionReference } from "../PdfSelection";
import { collectTurnArtifacts } from "../ArtifactOutputs";
import type { TurnFileDiffSelection } from "../TurnFileDiffTypes";
import { closeViewTabsWhere, focusViewTab, initialWorkspaceViewTabsState, openViewTab, reorderViewTabs,
  workspaceArtifactViewTab, workspaceDiffViewTab, workspaceFileViewTab, workspacePluginViewTab, workspaceProjectViewTab,
  workspaceToolViewTab, WORKSPACE_CONVERSATION_TAB_ID, type WorkspaceViewTabsState } from "../WorkspaceViewTabs";

type PreviewTabs = Pick<ApplicationPreviewWorkspaceV1, "tabs" | "activeTabId" | "activeFileTabId" | "conversationSelected">;

const actions = new Set<keyof ApplicationPreviewActionMapV1>([
  "preview.open-file", "preview.open-diff", "preview.open-artifact", "preview.open-tool", "preview.open-plugin", "preview.open-project",
  "preview.open-side-thread", "preview.new-tab", "preview.focus-tab", "preview.close-tab", "preview.close-tabs", "preview.reorder-tab",
  "preview.follow-project", "preview.show-conversation",
]);
export function isApplicationPreviewAction(action: string): action is keyof ApplicationPreviewActionMapV1 {
  return actions.has(action as keyof ApplicationPreviewActionMapV1);
}

/** Resource tabs survive selected UI replacement; geometry and DOM focus do not. */
export class ApplicationPreview {
  private state: WorkspaceViewTabsState = initialWorkspaceViewTabsState;
  private snapshot: PreviewTabs = Object.freeze({ tabs: Object.freeze([]), conversationSelected: false });
  constructor(private readonly host: { artifacts?: () => import("../../../../packages/plugin-sdk/src/ui-artifacts").ApplicationArtifactsV1; getThreads: () => readonly Thread[]; changed: () => void }) {}

  getSnapshot(): PreviewTabs { return this.snapshot; }

  private update(state: WorkspaceViewTabsState): void {
    if (state === this.state) return;
    this.state = state;
    const conversationSelected = state.activeTabID === WORKSPACE_CONVERSATION_TAB_ID;
    // Keep the last resource available to the dock while the expanded strip
    // uses the same selection history for conversations and resources.
    const activeTabId = conversationSelected
      ? state.activationHistory.findLast(id => state.tabs.some(tab => tab.id === id))
      : state.activeTabID;
    // Inputs are data-only; no origin WeakRef or render callbacks enter this owner.
    const snapshot: PreviewTabs = structuredClone({ tabs: state.tabs,
      activeTabId, activeFileTabId: state.activeFileTabID, conversationSelected });
    const freeze = (value: unknown): void => {
      if (!value || typeof value !== "object") return;
      Object.values(value).forEach(freeze);
      Object.freeze(value);
    };
    freeze(snapshot);
    this.snapshot = snapshot;
    this.host.changed();
  }

  private closeTabs(tabIds: readonly string[]): void {
    const state = closeViewTabsWhere(this.state, tab => tabIds.includes(tab.id));
    this.update(state.activeTabID === undefined ? focusViewTab(state, WORKSPACE_CONVERSATION_TAB_ID) : state);
  }

  async invoke<Action extends keyof ApplicationPreviewActionMapV1>(action: Action,
    input: ApplicationPreviewActionMapV1[Action]["input"], signal?: AbortSignal): Promise<ApplicationPreviewActionMapV1[Action]["result"]> {
    signal?.throwIfAborted();
    let result: unknown;
    switch (action) {
      case "preview.open-file": {
        const { context, path } = input as ApplicationPreviewActionMapV1["preview.open-file"]["input"];
        this.update(openViewTab(this.state, workspaceFileViewTab({ context: { ...context }, path }))); break;
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
      case "preview.show-conversation": this.update(focusViewTab(this.state, WORKSPACE_CONVERSATION_TAB_ID)); break;
    }
    return result as ApplicationPreviewActionMapV1[Action]["result"];
  }
}
