import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationComposerDraftV1, ApplicationResponseSelectionV1 } from "../../../packages/plugin-sdk/src/ui-composer";
import type { ApplicationComposerRuntimeChoiceV1, ComposerPermissionModeV1 } from "../../../packages/plugin-sdk/src/ui-composer-runtime";
import type { ApplicationSubmissionInputV1 } from "../../../packages/plugin-sdk/src/ui-submission";

export interface ComposerSource {
  readonly ownerTabId: string;
  readonly pane: "primary" | "secondary";
}

/** Source identity is captured by the mounted surface, never recovered from a later active tab. */
export function createComposerControls(application: ApplicationServiceV1, source: ComposerSource) {
  function current() {
    const snapshot = application.getSnapshot();
    if (snapshot.drafts[source.pane].ownerTabId !== source.ownerTabId) throw new Error("Composer source is no longer visible");
    return snapshot;
  }
  async function checkpoint(update: (draft: ApplicationComposerDraftV1) => ApplicationComposerDraftV1): Promise<void> {
    const snapshot = current();
    const draft = update(snapshot.drafts[source.pane]);
    await application.invoke("composer.checkpoint", {
      activeTabId: snapshot.drafts.activeTabId,
      primary: source.pane === "primary" ? draft : snapshot.drafts.primary,
      secondary: snapshot.drafts.split ? source.pane === "secondary" ? draft : snapshot.drafts.secondary : undefined,
    });
  }
  return {
    async setText(text: string): Promise<void> {
      current(); await application.invoke("composer.set-draft", { ownerTabId: source.ownerTabId, text });
    },
    async setSelections(selections: readonly ApplicationResponseSelectionV1[]): Promise<void> {
      await checkpoint(draft => ({ ...draft, selections }));
    },
    async removeAttachment(attachmentId: string): Promise<void> {
      await checkpoint(draft => ({ ...draft, imageIds: draft.imageIds.filter(id => id !== attachmentId), fileIds: draft.fileIds.filter(id => id !== attachmentId) }));
      // Retirement cannot transfer this discard to another owner; the host retains
      // any bytes still referenced by another draft, queue, or admitted send.
      await application.invoke("composer.release-attachments", { attachmentIds: [attachmentId], ifUnreferenced: true });
    },
    async attach(files: readonly File[]): Promise<PromiseSettledResult<unknown>[]> {
      current();
      return Promise.allSettled(files.map(file => application.invoke("composer.attach", { ownerTabId: source.ownerTabId, files: [file] })));
    },
    async submit(mode: "send" | "queue" | "steer", content: Omit<ApplicationSubmissionInputV1, "ownerTabId" | "mode"> = {}) {
      current(); return application.invoke("composer.submit", { ...content, ownerTabId: source.ownerTabId, mode });
    },
    async stop(): Promise<void> {
      const snapshot = current();
      const creation = snapshot.submission.pendingCreations.find(operation => operation.ownerTabId === source.ownerTabId);
      if (creation) { await application.invoke("composer.cancel-submission", { operationID: creation.operationID }); return; }
      const threadId = snapshot.tabs.items.find(tab => tab.id === source.ownerTabId)?.threadId;
      if (threadId) await application.invoke("conversation.interrupt", { threadId });
    },
    async chooseRuntime(selection: ApplicationComposerRuntimeChoiceV1) {
      current(); return application.invoke("composer.choose-runtime", { ownerTabId: source.ownerTabId, selection });
    },
    async choosePermission(mode: ComposerPermissionModeV1, approveForMe?: boolean) {
      current(); return application.invoke("composer.choose-permission", { ownerTabId: source.ownerTabId, mode, approveForMe });
    },
    async chooseWorktree(enabled: boolean, startBranch?: string) {
      current(); return application.invoke("composer.choose-worktree", { ownerTabId: source.ownerTabId, enabled, startBranch });
    },
    async refreshEngines(force = false): Promise<void> {
      current(); await application.invoke("composer.refresh-engine-models", { ownerTabId: source.ownerTabId, force });
    },
    async refreshProvider(provider?: string, force = false): Promise<void> {
      current(); await application.invoke("composer.refresh-provider-models", { ownerTabId: source.ownerTabId, provider, force });
    },
  };
}
