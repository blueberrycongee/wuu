import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationAttachmentV1, ApplicationComposerActionMapV1, ApplicationComposerDraftV1, ApplicationDraftsSnapshotV1 } from "../../../packages/plugin-sdk/src/ui-composer";
import { startTransition, useCallback, useLayoutEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { cloneComposerDraft, initialSplitComposerDrafts, type ComposerDraftState, type ConversationPaneID } from "./AppState";
import type { ComposerFile, ComposerImage } from "./ComposerMessages";
import type { ResponseSelection } from "../shared/protocol";
import { createApplicationComposerAttachments } from "./modules/ApplicationComposerAttachments";
import { translateCurrent } from "./i18n";
import { showErrorToast } from "./Toast";

export const COMPOSER_PROMPT_IDLE_COMMIT_MS = 400;
export type ComposerDraftStateController = {
  prompt: string;
  promptRevision: number;
  setPrompt: Dispatch<SetStateAction<string>>;
  setPromptFromInput: (value: string) => void;
  composerImages: ComposerImage[];
  setComposerImages: Dispatch<SetStateAction<ComposerImage[]>>;
  composerFiles: ComposerFile[];
  composerSelections: ResponseSelection[];
  setComposerSelections: Dispatch<SetStateAction<ResponseSelection[]>>;
  setComposerFiles: Dispatch<SetStateAction<ComposerFile[]>>;
  splitComposerDrafts: Record<ConversationPaneID, ComposerDraftState>;
  setSplitComposerDrafts: Dispatch<
    SetStateAction<Record<ConversationPaneID, ComposerDraftState>>
  >;
  attachComposerAttachmentFiles: (files: File[]) => Promise<void>;
  removeComposerImage: (id: string) => void;
  removeComposerFile: (id: string) => void;
  setSplitComposerPrompt: (pane: ConversationPaneID, value: string) => void;
  attachSplitComposerAttachmentFiles: (
    pane: ConversationPaneID,
    files: File[],
  ) => Promise<void>;
  removeSplitComposerImage: (pane: ConversationPaneID, id: string) => void;
  removeSplitComposerFile: (pane: ConversationPaneID, id: string) => void;
  moveSplitDraftToGlobalComposer: (pane: ConversationPaneID) => void;
  currentPrimaryComposerDraft: () => ComposerDraftState;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
};

type DraftView = { primary: ComposerDraftState; split: Record<ConversationPaneID, ComposerDraftState> };

function draftStructure(drafts: ApplicationDraftsSnapshotV1): string {
  return JSON.stringify([drafts.activeTabId, drafts.activePane, drafts.split,
    drafts.primary.imageIds, drafts.primary.fileIds, drafts.primary.selections,
    drafts.secondary.imageIds, drafts.secondary.fileIds, drafts.secondary.selections,
    drafts.attachments.map(item => [item.id, item.status, item.error, item.remoteRef, item.data?.length])]);
}

/** UI drafts mirror application-owned resources; only input-critical rendering is delayed. */
export function useComposerDraftState(application: ApplicationServiceV1): ComposerDraftStateController {
  const syncRef = useRef<(force?: boolean) => void>(() => {});
  const [attachments] = useState(() => createApplicationComposerAttachments(application, () => syncRef.current(true)));
  const read = (draft: ApplicationComposerDraftV1): ComposerDraftState => {
    const descriptors = new Map(application.getSnapshot().drafts.attachments.map(item => [item.id, item]));
    return { prompt: draft.text,
      images: draft.imageIds.map(id => attachments.read(descriptors.get(id)!) as ComposerImage),
      files: draft.fileIds.map(id => attachments.read(descriptors.get(id)!) as ComposerFile),
      ...(draft.selections.length ? { selections: draft.selections.map(item => ({ ...item, source: { ...item.source } })) } : {}),
    };
  };
  const [view, setView] = useState<DraftView>(() => {
    const drafts = application.getSnapshot().drafts;
    return { primary: read(drafts.primary), split: drafts.split
      ? { primary: read(drafts.primary), secondary: read(drafts.secondary) } : initialSplitComposerDrafts() };
  });
  const current = useRef(view);
  const [promptRevision, setPromptRevision] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const lastStructure = useRef<string | undefined>(undefined);
  if (lastStructure.current === undefined) lastStructure.current = draftStructure(application.getSnapshot().drafts);
  const cancelInputCommit = useCallback(() => {
    if (timer.current !== undefined) clearTimeout(timer.current);
    timer.current = undefined;
  }, []);
  function render(next: DraftView, revisePrompt = false): void {
    current.current = next;
    setView(next);
    if (revisePrompt) setPromptRevision(value => value + 1);
  }
  function sync(force = false): void {
    const drafts = application.getSnapshot().drafts;
    const structure = draftStructure(drafts);
    const primaryText = drafts.primary.text;
    const secondaryText = drafts.secondary.text;
    if (!force && structure === lastStructure.current && primaryText === current.current.primary.prompt
      && (!drafts.split || primaryText === current.current.split.primary.prompt && secondaryText === current.current.split.secondary.prompt)) return;
    lastStructure.current = structure;
    cancelInputCommit();
    const primary = read(drafts.primary);
    render({ primary, split: drafts.split ? { primary, secondary: read(drafts.secondary) } : current.current.split }, true);
    attachments.retainVisible(drafts.attachments.map(item => item.id));
  }
  syncRef.current = sync;
  useLayoutEffect(() => {
    attachments.resume();
    const unsubscribe = application.subscribe(() => syncRef.current());
    syncRef.current();
    return () => { unsubscribe(); cancelInputCommit(); attachments.dispose(); };
  }, [application, attachments, cancelInputCommit]);

  function checkpoint(next: DraftView): void {
    const snapshot = application.getSnapshot().drafts;
    if (!snapshot.activeTabId || !snapshot.primary.ownerTabId) return;
    const primary = snapshot.split ? next.split.primary : next.primary;
    const secondary = snapshot.split ? next.split.secondary : undefined;
    const ready: ApplicationAttachmentV1[] = [];
    const known = new Set(snapshot.attachments.map(item => item.id));
    function project(draft: ComposerDraftState): ApplicationComposerDraftV1 {
      for (const [kind, items] of [["image", draft.images], ["file", draft.files]] as const) {
        for (const item of items) {
          if (known.has(item.id)) continue;
          const value = item.preparation?.result ?? item;
          // Pending imports already belong to the service and retain their ID.
          if (value.preparation || value.encodePromise) continue;
          ready.push({ id: value.id, kind, name: attachments.nameFor(value.id) ?? ("filename" in value ? value.filename ?? "image" : "image"),
            mediaType: value.media_type, status: "ready", data: value.data, remoteRef: value.remote_ref,
            width: "width" in value ? value.width : undefined, height: "height" in value ? value.height : undefined });
        }
      }
      return { text: draft.prompt, imageIds: draft.images.map(item => item.id), fileIds: draft.files.map(item => item.id), selections: draft.selections ?? [] };
    }
    const input: ApplicationComposerActionMapV1["composer.checkpoint"]["input"] = { activeTabId: snapshot.activeTabId, primary: project(primary), secondary: secondary && project(secondary), attachments: ready };
    void application.invoke("composer.checkpoint", input).catch(showErrorToast);
  }
  function changePrimary(update: (draft: ComposerDraftState) => ComposerDraftState, revise = false): void {
    const next = { ...current.current, primary: update(current.current.primary) };
    render(next, revise);
    if (!application.getSnapshot().drafts.split) checkpoint(next);
  }
  const setPrompt: Dispatch<SetStateAction<string>> = update => {
    cancelInputCommit();
    changePrimary(draft => ({ ...draft, prompt: typeof update === "function" ? update(draft.prompt) : update }), true);
    if (!application.getSnapshot().drafts.primary.ownerTabId) {
      void application.invoke("composer.set-draft", { text: current.current.primary.prompt }).catch(showErrorToast);
    }
  };
  const setComposerImages: Dispatch<SetStateAction<ComposerImage[]>> = update => changePrimary(draft => ({ ...draft,
    images: typeof update === "function" ? update(draft.images) : update }));
  const setComposerFiles: Dispatch<SetStateAction<ComposerFile[]>> = update => changePrimary(draft => ({ ...draft,
    files: typeof update === "function" ? update(draft.files) : update }));
  const setComposerSelections: Dispatch<SetStateAction<ResponseSelection[]>> = update => changePrimary(draft => ({ ...draft,
    selections: typeof update === "function" ? update(draft.selections ?? []) : update }));
  const setSplitComposerDrafts: ComposerDraftStateController["setSplitComposerDrafts"] = update => {
    const next = { ...current.current, split: typeof update === "function" ? update(current.current.split) : update };
    render(next);
    if (application.getSnapshot().drafts.split) checkpoint(next);
  };
  function setPromptFromInput(value: string): void {
    cancelInputCommit();
    current.current = { ...current.current, primary: { ...current.current.primary, prompt: value } };
    void application.invoke("composer.set-draft", { text: value, ownerTabId: application.getSnapshot().drafts.primary.ownerTabId }).catch(showErrorToast);
    timer.current = setTimeout(() => { timer.current = undefined; startTransition(() => setView(current.current)); }, COMPOSER_PROMPT_IDLE_COMMIT_MS);
  }
  function updatePane(pane: ConversationPaneID, update: (draft: ComposerDraftState) => ComposerDraftState): void {
    setSplitComposerDrafts(previous => ({ ...previous, [pane]: update(previous[pane]) }));
  }
  async function attach(files: File[], pane: ConversationPaneID = "primary"): Promise<void> {
    if (!files.length) return;
    try {
      const ownerTabId = application.getSnapshot().drafts[pane].ownerTabId;
      if (!ownerTabId) throw new Error("Attachment draft owner is unavailable");
      await Promise.all(files.map(file => application.invoke("composer.attach", { files: [file], ownerTabId })
        .catch(error => { showErrorToast(error, translateCurrent("composer.attachment.addFailed")); })));
    } catch (error) { showErrorToast(error, translateCurrent("composer.attachment.addFailed")); }
  }
  function restorePrimaryComposerDraft(draft: ComposerDraftState): void {
    cancelInputCommit();
    const next = { ...current.current, primary: cloneComposerDraft(draft) };
    render(next, true);
    if (!application.getSnapshot().drafts.split) checkpoint(next);
  }
  // Actions read the current draft ref; stable identities preserve caller effects and focus.
  const actions = useMemo<Omit<ComposerDraftStateController, "prompt" | "promptRevision" | "composerImages" | "composerFiles" | "composerSelections" | "splitComposerDrafts">>(() => ({
    setPrompt, setPromptFromInput, setComposerImages, setComposerFiles, setComposerSelections, setSplitComposerDrafts,
    attachComposerAttachmentFiles: files => attach(files),
    attachSplitComposerAttachmentFiles: (pane, files) => attach(files, pane),
    removeComposerImage: id => setComposerImages(items => items.filter(item => item.id !== id)),
    removeComposerFile: id => setComposerFiles(items => items.filter(item => item.id !== id)),
    setSplitComposerPrompt: (pane, prompt) => updatePane(pane, draft => ({ ...draft, prompt })),
    removeSplitComposerImage: (pane, id) => updatePane(pane, draft => ({ ...draft, images: draft.images.filter(item => item.id !== id) })),
    removeSplitComposerFile: (pane, id) => updatePane(pane, draft => ({ ...draft, files: draft.files.filter(item => item.id !== id) })),
    moveSplitDraftToGlobalComposer: pane => {
      restorePrimaryComposerDraft(current.current.split[pane]);
      setSplitComposerDrafts(initialSplitComposerDrafts());
    },
    currentPrimaryComposerDraft: () => cloneComposerDraft(current.current.primary),
    restorePrimaryComposerDraft,
  }), [application, cancelInputCommit]);
  return { ...actions, prompt: view.primary.prompt, promptRevision,
    composerImages: view.primary.images, composerFiles: view.primary.files, composerSelections: view.primary.selections ?? [],
    splitComposerDrafts: view.split };
}
