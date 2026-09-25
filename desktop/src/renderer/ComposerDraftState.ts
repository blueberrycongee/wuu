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
  composerFileFromFile,
  composerImagePlaceholder,
  isComposerImageFile,
  isComposerDocumentFile,
  revokeComposerImagePreview,
  type ComposerFile,
  type ComposerImage,
} from "./ComposerMessages";
import { translateCurrent } from "./i18n";
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
  onImageEncoded: (encoded: ComposerImage) => void;
  onFile: (file: ComposerFile) => void;
};

export async function buildComposerAttachments(
  files: File[],
  onImagePlaceholder: (placeholder: ComposerImage) => void,
  onImageEncoded: (encoded: ComposerImage) => void,
  onFile: (file: ComposerFile) => void,
): Promise<void> {
  const imageFiles = files.filter(isComposerImageFile);
  const documentFiles = files.filter(isComposerDocumentFile);
  await Promise.all([
    ...imageFiles.map(async (file) => {
      const placeholder = composerImagePlaceholder(file);
      onImagePlaceholder(placeholder);
      const encoded = await placeholder.encodePromise;
      if (encoded) {
        onImageEncoded(encoded);
      }
    }),
    ...documentFiles.map(async (file) => {
      const attachment = await composerFileFromFile(file);
      onFile(attachment);
    }),
  ]);
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
    await buildComposerAttachments(
      files,
      targets.onImagePlaceholder,
      targets.onImageEncoded,
      targets.onFile,
    );
  } catch (error) {
    showErrorToast(error, translateCurrent("composer.attachment.addFailed"));
  }
}

export function useComposerDraftState(): ComposerDraftStateController {
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
  const [splitComposerDrafts, setSplitComposerDrafts] = useState<
    Record<ConversationPaneID, ComposerDraftState>
  >(initialSplitComposerDrafts);
  async function attachComposerAttachmentFiles(files: File[]): Promise<void> {
    await attachComposerAttachmentFilesToDraft(files, {
      onImagePlaceholder: (placeholder) =>
        setComposerImages((current) => [...current, placeholder]),
      onImageEncoded: (encoded) =>
        setComposerImages((current) =>
          current.map((existing) =>
            existing.id === encoded.id ? encoded : existing,
          ),
        ),
      onFile: (file) => setComposerFiles((current) => [...current, file]),
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
      onImageEncoded: (encoded) =>
        updateSplitComposerDraft(pane, (draft) => ({
          ...draft,
          images: draft.images.map((existing) =>
            existing.id === encoded.id ? encoded : existing,
          ),
        })),
      onFile: (file) =>
        updateSplitComposerDraft(pane, (draft) => ({
          ...draft,
          files: [...draft.files, file],
        })),
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
    });
  }

  function restorePrimaryComposerDraft(draft: ComposerDraftState): void {
    const nextDraft = cloneComposerDraft(draft);
    setPrompt(nextDraft.prompt);
    setComposerImages(nextDraft.images);
    setComposerFiles(nextDraft.files);
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
