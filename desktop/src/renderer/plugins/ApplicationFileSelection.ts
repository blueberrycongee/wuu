import type { ApplicationFileSelectionServiceV1, ApplicationFileSelectionTargetV1, ApplicationFileSelectionSourceV1, ApplicationFileSelectionSnapshotV1, FileSelectionCallOptionsV1 } from "../../../../packages/plugin-sdk/src/ui-file-selection";
import type { ApplicationSubmissionOperationV1 } from "../../../../packages/plugin-sdk/src/ui-submission";
import type { FileSelectionSource, MessageContentPart, WorkspaceFileReadResult } from "../../shared/protocol";
import { activeThreadForState, resolveThreadRuntimeContext, sameRuntimeContext, threadForTab, threadSessionTabID, type AppState, type ComposerDraftState } from "../AppState";
import { createComposerMessage, type QueuedComposerMessage } from "../ComposerMessages";
import { buildFileSelectionPart, type FileSelectionPart } from "../FileSelectionParts";
import { getCollapsedPromptRevision, readCollapsedPromptParts, rememberCollapsedPromptParts, subscribeCollapsedPromptParts } from "../ComposerCollapsedPrompt";
import { fileSelectionRevision } from "../FileSelectionMapping";
import { validPdfSelectionReference, workspacePdfRevision } from "../PdfSelection";
import type { ApplicationPreview } from "./ApplicationPreview";

export interface ApplicationFileSelectionOptions {
  getState(): AppState;
  subscribeState(listener: () => void): () => void;
  isNavigationPending(): boolean;
  getDraft(ownerTabId: string): ComposerDraftState;
  setDraft(ownerTabId: string, draft: ComposerDraftState): void;
  submission: { submitIndependent(ownerTabId: string, message: QueuedComposerMessage): Promise<boolean>;
    getSnapshot(): { readonly operations: readonly ApplicationSubmissionOperationV1[] } };
  preview: Pick<ApplicationPreview, "invoke">;
  previewContent: { readFile(target: { readonly root: string; readonly path: string }): Promise<WorkspaceFileReadResult> };
  side: { available(): boolean; open(parentThreadId: string, source: FileSelectionSource): Promise<boolean> };
}

/** Canonical file references are composer content, not selection-menu state. */
export class ApplicationFileSelection implements ApplicationFileSelectionServiceV1 {
  private readonly snapshots = new Map<string, { key: string; view: ApplicationFileSelectionSnapshotV1, FileSelectionCallOptionsV1 }>();
  private readonly interactionOwners = new Map<string, string>();
  constructor(private readonly options: ApplicationFileSelectionOptions) {}

  subscribe = (listener: () => void): (() => void) => {
    const state = this.options.subscribeState(listener);
    const parts = subscribeCollapsedPromptParts(listener);
    return () => { state(); parts(); };
  };
  private owner(target: ApplicationFileSelectionTargetV1, writable = true) {
    const state = this.options.getState();
    const tab = state.sessionTabs.find(tab => tab.id === target.ownerTabId);
    if (!tab || tab.kind === "skills" || !state.initialized || this.options.isNavigationPending()) return;
    const activeThread = activeThreadForState(state);
    const activeID = state.secondaryThread && activeThread ? threadSessionTabID(activeThread.id) : state.activeSessionTabID;
    if (target.ownerTabId !== activeID || !sameRuntimeContext(tab.context, state.activeContext) && tab.kind === "draft") return;
    const thread = tab.kind === "thread" ? threadForTab(state, tab.threadID) : undefined;
    if (tab.kind === "thread" && !thread || writable && thread?.read_only) return;
    return { tab, thread, context: thread ? resolveThreadRuntimeContext(thread, state.projects) : tab.context, storageKey: thread?.id ?? tab.id };
  }
  getSnapshot = (target: ApplicationFileSelectionTargetV1): ApplicationFileSelectionSnapshotV1 => {
    const state = this.options.getState();
    for (const id of this.snapshots.keys()) if (!state.sessionTabs.some(tab => tab.id === id)) { this.snapshots.delete(id); this.interactionOwners.delete(id); }
    const owner = this.owner(target);
    const draft = owner ? this.options.getDraft(target.ownerTabId) : undefined;
    const origin = owner?.thread && this.options.submission.getSnapshot().operations.find(operation => operation.threadId === owner.thread!.id && operation.ownerTabId.startsWith("draft:"))?.ownerTabId;
    if (origin) this.interactionOwners.set(target.ownerTabId, origin);
    const interactionOwnerKey = this.interactionOwners.get(target.ownerTabId) ?? target.ownerTabId;
    const canAskSide = Boolean(owner?.thread && this.options.side.available());
    const key = JSON.stringify([draft?.prompt, Boolean(owner), canAskSide, interactionOwnerKey, getCollapsedPromptRevision()]);
    const cached = this.snapshots.get(target.ownerTabId); if (cached?.key === key) return cached.view;
    const comments = owner && draft ? (readCollapsedPromptParts(owner.storageKey, draft.prompt) ?? [])
      .filter((part): part is FileSelectionPart => part.type === "file_selection" && part.intent === "comment") : [];
    const view = Object.freeze({ ownerTabId: target.ownerTabId, interactionOwnerKey, enabled: Boolean(owner), canAskSide,
      comments: Object.freeze(comments.map(part => Object.freeze({ ...part, source: Object.freeze({ ...part.source, ...(part.source.pdf ? { pdf: Object.freeze({ ...part.source.pdf }) } : {}) }) }))) });
    this.snapshots.set(target.ownerTabId, { key, view }); return view;
  };
  private validSource(target: ApplicationFileSelectionTargetV1, source: ApplicationFileSelectionSourceV1, writable = true) {
    const owner = this.owner(target, writable);
    if (!owner || !source.path || !source.revision || !source.workspace) return;
    if (source.pdf && (!validPdfSelectionReference(source.pdf) || source.pdf.artifact_uri &&
      (source.revision !== `sha256:${source.pdf.artifact_sha256}`))) return;
    return owner;
  }
  private update(target: ApplicationFileSelectionTargetV1, update: (parts: MessageContentPart[]) => MessageContentPart[]): boolean {
    const owner = this.owner(target); if (!owner) return false;
    const draft = this.options.getDraft(target.ownerTabId);
    const parts = readCollapsedPromptParts(owner.storageKey, draft.prompt) ?? [{ type: "text", text: draft.prompt }];
    const next = update(parts); const prompt = next.map(part => part.text).join("");
    rememberCollapsedPromptParts(owner.storageKey, prompt, next);
    this.options.setDraft(target.ownerTabId, { ...draft, prompt }); return true;
  }
  private attach(target: ApplicationFileSelectionTargetV1, part: FileSelectionPart): boolean {
    return this.update(target, current => {
      if (current.some(existing => existing.type === "file_selection" && existing.id === part.id)) return current.map(existing => existing.type === "file_selection" && existing.id === part.id ? part : existing);
      const text = current.findIndex(part => part.type === "text"); const at = text < 0 ? current.length : text;
      return [...current.slice(0, at), part, ...current.slice(at)];
    });
  }
  quote({ target, source }: Parameters<ApplicationFileSelectionServiceV1["quote"]>[0]): boolean {
    if (!this.validSource(target, source)) return false;
    return this.attach(target, buildFileSelectionPart(source, "quote"));
  }
  comment({ target, source, comment, id }: Parameters<ApplicationFileSelectionServiceV1["comment"]>[0]): boolean {
    if (!comment.trim() || !this.validSource(target, source)) return false;
    return this.attach(target, buildFileSelectionPart(source, "comment", comment.trim(), id));
  }
  removeComment({ target, id }: Parameters<ApplicationFileSelectionServiceV1["removeComment"]>[0]): boolean {
    return this.update(target, current => current.filter(part => part.type !== "file_selection" || part.id !== id));
  }
  async edit({ target, source, instruction }: Parameters<ApplicationFileSelectionServiceV1["edit"]>[0], options?: FileSelectionCallOptionsV1): Promise<boolean> {
    options?.signal?.throwIfAborted();
    if (!instruction.trim() || !this.validSource(target, source)) return false;
    if (!source.pdf?.artifact_uri) {
      const current = await this.options.previewContent.readFile({ root: source.workspace, path: source.path });
      options?.signal?.throwIfAborted();
      const revision = source.pdf ? workspacePdfRevision(current) : current.text === undefined ? undefined : fileSelectionRevision(current.text);
      if (revision !== source.revision || !this.validSource(target, source)) return false;
    }
    const part = buildFileSelectionPart(source, "edit", instruction.trim());
    const message = createComposerMessage(part.text, [], [], [part]);
    return message ? this.options.submission.submitIndependent(target.ownerTabId, { ...message, activeDocument: { path: source.path } }) : false;
  }
  async openPdf({ target, source }: Parameters<ApplicationFileSelectionServiceV1["openPdf"]>[0], options?: FileSelectionCallOptionsV1): Promise<boolean> {
    options?.signal?.throwIfAborted();
    const owner = this.validSource(target, source, false); const pdf = source.pdf;
    if (!owner?.thread || !pdf?.artifact_uri || !pdf.artifact_sha256 || pdf.artifact_thread_id !== owner.thread.id) return false;
    await this.options.preview.invoke("preview.open-artifact", { threadId: owner.thread.id, artifactId: pdf.artifact_uri, sha256: pdf.artifact_sha256, page: pdf.start_page }, options?.signal);
    return true;
  }
  async openFile({ target, path }: Parameters<ApplicationFileSelectionServiceV1["openFile"]>[0], options?: FileSelectionCallOptionsV1): Promise<void> {
    options?.signal?.throwIfAborted();
    const owner = this.owner(target, false); if (!owner) return;
    await this.options.preview.invoke("preview.open-file", { context: owner.context, path }, options?.signal);
  }
  async askSide({ target, source }: Parameters<ApplicationFileSelectionServiceV1["askSide"]>[0], options?: FileSelectionCallOptionsV1): Promise<boolean> {
    options?.signal?.throwIfAborted();
    const owner = this.validSource(target, source);
    if (!owner?.thread || !this.options.side.available()) return false;
    return this.options.side.open(owner.thread.id, structuredClone(source));
  }
}
