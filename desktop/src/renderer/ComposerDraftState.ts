import {
  startTransition,
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import {
  cloneComposerDraft,
  emptyComposerDraft,
  initialSplitComposerDrafts,
  type ComposerDraftState,
  type ConversationPaneID,
} from "./AppState";
import {
  composerFilePlaceholder,
  composerImagePlaceholder,
  isComposerImageFile,
  isComposerDocumentFile,
  revokeComposerImagePreview,
  type ComposerAttachmentPreparation,
  type ComposerFile,
  type ComposerImage,
} from "./ComposerMessages";
import { translateCurrent } from "./i18n";
import type { ResponseSelection } from "../shared/protocol";
import { showErrorToast } from "./Toast";

// The textarea owns the input-critical value. Publishing its draft to App
// after an idle window preserves tab/plugin state without making the entire
// desktop shell render for every character. A normal pause between words must
// not turn this back into a per-keystroke path.
export const COMPOSER_PROMPT_IDLE_COMMIT_MS = 400;

type PendingPromptCommit = {
  value: string;
};

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

type ComposerAttachmentTargets = {
  onImagePlaceholder: (placeholder: ComposerImage) => void;
  onImageEncoded: (id: string, encoded?: ComposerImage) => void;
  onFilePlaceholder: (placeholder: ComposerFile) => void;
  onFileEncoded: (id: string, encoded?: ComposerFile) => void;
};

async function prepareComposerAttachment<T extends { id: string }>(
  placeholder: T & { preparation: ComposerAttachmentPreparation<T> },
  settle: (id: string, encoded?: T) => void,
): Promise<void> {
  const preparation = placeholder.preparation;
  async function settleAttempt(): Promise<void> {
    try {
      settle(placeholder.id, await preparation.promise);
    } catch {
      // Keep the source and failed state available to drafts restored after a
      // send failure. A dismissed slot is still protected by settleAttachment.
      settle(placeholder.id, { ...placeholder, encodePromise: undefined, previewSrc: undefined });
    }
  }
  preparation.retry = async () => {
    if (!preparation.error) return;
    preparation.restart();
    settle(placeholder.id, { ...placeholder, encodePromise: preparation.promise, previewSrc: undefined });
    await settleAttempt();
  };
  await settleAttempt();
}

export async function buildComposerAttachments(
  files: File[],
  targets: ComposerAttachmentTargets,
): Promise<void> {
  await Promise.all([
    ...files.filter(isComposerImageFile).map(async (file) => {
      const placeholder = composerImagePlaceholder(file);
      targets.onImagePlaceholder(placeholder);
      await prepareComposerAttachment<ComposerImage>(placeholder, targets.onImageEncoded);
    }),
    ...files.filter(isComposerDocumentFile).map(async (file) => {
      const placeholder = composerFilePlaceholder(file);
      targets.onFilePlaceholder(placeholder);
      await prepareComposerAttachment<ComposerFile>(placeholder, targets.onFileEncoded);
    }),
  ]);
}

// Completion can replace or remove an existing slot, never recreate a draft
// that the user has already sent, removed, or closed.
function settleAttachment<T extends { id: string }>(items: T[], id: string, encoded?: T): T[] {
  if (!items.some((item) => item.id === id)) return items;
  return items.flatMap((item) => item.id === id ? encoded ? [encoded] : [] : [item]);
}

async function attachComposerAttachmentFilesToDraft(
  files: File[],
  targets: ComposerAttachmentTargets,
): Promise<void> {
  if (files.length === 0) {
    return;
  }
  const imageFiles = files.filter(isComposerImageFile);
  const documentFiles = files.filter(isComposerDocumentFile);
  if (imageFiles.length === 0 && documentFiles.length === 0) {
    showErrorToast(translateCurrent("composer.attachment.imagesAndPdfOnly"));
    return;
  }
  try {
    await buildComposerAttachments(files, targets);
  } catch (error) {
    showErrorToast(error, translateCurrent("composer.attachment.addFailed"));
  }
}

export function useComposerDraftState(
  updateStoredDrafts?: (update: (draft: ComposerDraftState) => ComposerDraftState) => void,
): ComposerDraftStateController {
  const [prompt, setPromptState] = useState("");
  const [promptRevision, setPromptRevision] = useState(0);
  const promptRef = useRef("");
  const promptCommitTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const pendingPromptCommitRef = useRef<PendingPromptCommit | null>(null);
  const cancelPromptCommit = useCallback(() => {
    pendingPromptCommitRef.current = null;
    if (promptCommitTimerRef.current !== undefined) {
      clearTimeout(promptCommitTimerRef.current);
      promptCommitTimerRef.current = undefined;
    }
  }, []);
  const setPrompt = useCallback<Dispatch<SetStateAction<string>>>((update) => {
    const next = typeof update === "function" ? update(promptRef.current) : update;
    promptRef.current = next;
    cancelPromptCommit();
    setPromptState((current) => (current === next ? current : next));
    // Programmatic clears/restores must reach Composer even when App's delayed
    // string snapshot already equals `next` (for example, send within 400ms).
    setPromptRevision((current) => current + 1);
  }, [cancelPromptCommit]);
  const setPromptFromInput = useCallback((value: string) => {
    promptRef.current = value;
    cancelPromptCommit();
    const pendingCommit = { value };
    pendingPromptCommitRef.current = pendingCommit;
    promptCommitTimerRef.current = setTimeout(() => {
      promptCommitTimerRef.current = undefined;
      if (pendingPromptCommitRef.current !== pendingCommit) {
        return;
      }
      pendingPromptCommitRef.current = null;
      startTransition(() => {
        setPromptState((current) =>
          promptRef.current === pendingCommit.value && current !== pendingCommit.value
            ? pendingCommit.value
            : current,
        );
      });
    }, COMPOSER_PROMPT_IDLE_COMMIT_MS);
  }, [cancelPromptCommit]);
  useEffect(() => cancelPromptCommit, [cancelPromptCommit]);
  const [composerImages, setComposerImages] = useState<ComposerImage[]>([]);
  const [composerFiles, setComposerFiles] = useState<ComposerFile[]>([]);
  const [composerSelections, setComposerSelections] = useState<ResponseSelection[]>([]);
  const [splitComposerDrafts, setSplitComposerDrafts] = useState<
    Record<ConversationPaneID, ComposerDraftState>
  >(initialSplitComposerDrafts);
  function updateSavedAttachmentDrafts(update: (draft: ComposerDraftState) => ComposerDraftState): void {
    setSplitComposerDrafts((current) => {
      const primary = update(current.primary);
      const secondary = update(current.secondary);
      return primary === current.primary && secondary === current.secondary ? current : { primary, secondary };
    });
    updateStoredDrafts?.(update);
  }

  function onImageEncoded(id: string, encoded?: ComposerImage): void {
    setComposerImages((current) => settleAttachment(current, id, encoded));
    updateSavedAttachmentDrafts((draft) => {
      const images = settleAttachment(draft.images, id, encoded);
      return images === draft.images ? draft : { ...draft, images };
    });
  }

  function onFileEncoded(id: string, encoded?: ComposerFile): void {
    setComposerFiles((current) => settleAttachment(current, id, encoded));
    updateSavedAttachmentDrafts((draft) => {
      const files = settleAttachment(draft.files, id, encoded);
      return files === draft.files ? draft : { ...draft, files };
    });
  }

  async function attachComposerAttachmentFiles(files: File[]): Promise<void> {
    await attachComposerAttachmentFilesToDraft(files, {
      onImagePlaceholder: (placeholder) =>
        setComposerImages((current) => [...current, placeholder]),
      onImageEncoded,
      onFilePlaceholder: (placeholder) => setComposerFiles((current) => [...current, placeholder]),
      onFileEncoded,
    });
  }

  function removeComposerImage(id: string): void {
    setComposerImages((current) => {
      const removed = current.find((image) => image.id === id);
      revokeComposerImagePreview(removed);
      return current.filter((image) => image.id !== id);
    });
  }

  function removeComposerFile(id: string): void {
    setComposerFiles((current) => current.filter((file) => file.id !== id));
  }

  function updateSplitComposerDraft(
    pane: ConversationPaneID,
    update: (draft: ComposerDraftState) => ComposerDraftState,
  ): void {
    setSplitComposerDrafts((current) => {
      const draft = current[pane] ?? emptyComposerDraft();
      return {
        ...current,
        [pane]: update(draft),
      };
    });
  }

  function setSplitComposerPrompt(
    pane: ConversationPaneID,
    value: string,
  ): void {
    updateSplitComposerDraft(pane, (draft) => ({ ...draft, prompt: value }));
  }

  async function attachSplitComposerAttachmentFiles(
    pane: ConversationPaneID,
    files: File[],
  ): Promise<void> {
    await attachComposerAttachmentFilesToDraft(files, {
      onImagePlaceholder: (placeholder) =>
        updateSplitComposerDraft(pane, (draft) => ({
          ...draft,
          images: [...draft.images, placeholder],
        })),
      onImageEncoded,
      onFilePlaceholder: (placeholder) =>
        updateSplitComposerDraft(pane, (draft) => ({
          ...draft,
          files: [...draft.files, placeholder],
        })),
      onFileEncoded,
    });
  }

  function removeSplitComposerImage(
    pane: ConversationPaneID,
    id: string,
  ): void {
    updateSplitComposerDraft(pane, (draft) => {
      const removed = draft.images.find((image) => image.id === id);
      revokeComposerImagePreview(removed);
      return {
        ...draft,
        images: draft.images.filter((image) => image.id !== id),
      };
    });
  }

  function removeSplitComposerFile(pane: ConversationPaneID, id: string): void {
    updateSplitComposerDraft(pane, (draft) => ({
      ...draft,
      files: draft.files.filter((file) => file.id !== id),
    }));
  }

  function moveSplitDraftToGlobalComposer(pane: ConversationPaneID): void {
    const draft = splitComposerDrafts[pane] ?? emptyComposerDraft();
    restorePrimaryComposerDraft(draft);
    setSplitComposerDrafts(initialSplitComposerDrafts());
  }

  function currentPrimaryComposerDraft(): ComposerDraftState {
    return cloneComposerDraft({
      prompt: promptRef.current,
      images: composerImages,
      files: composerFiles,
      selections: composerSelections,
    });
  }

  function restorePrimaryComposerDraft(draft: ComposerDraftState): void {
    const nextDraft = cloneComposerDraft(draft);
    setPrompt(nextDraft.prompt);
    setComposerImages(nextDraft.images);
    setComposerFiles(nextDraft.files);
    setComposerSelections(nextDraft.selections ?? []);
  }

  return {
    prompt,
    promptRevision,
    setPrompt,
    setPromptFromInput,
    composerImages,
    setComposerImages,
    composerFiles,
    setComposerFiles,
    composerSelections,
    setComposerSelections,
    splitComposerDrafts,
    setSplitComposerDrafts,
    attachComposerAttachmentFiles,
    removeComposerImage,
    removeComposerFile,
    setSplitComposerPrompt,
    attachSplitComposerAttachmentFiles,
    removeSplitComposerImage,
    removeSplitComposerFile,
    moveSplitDraftToGlobalComposer,
    currentPrimaryComposerDraft,
    restorePrimaryComposerDraft,
  };
}
