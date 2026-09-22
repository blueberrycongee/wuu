import { FileText, X } from "./WuuIcons";
import {
  type ClipboardEvent as ReactClipboardEvent,
  type RefObject,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState
} from "react";
import { clipboardAttachmentFiles } from "./ComposerMessages";
import type { MessageContentPart } from "../shared/protocol";
import { translateCurrent as translate } from "./i18n";
import { TruncatedText } from "./TruncatedText";

export type CollapsedComposerPromptBlock = {
  id: string;
  text: string;
};

const COLLAPSIBLE_COMPOSER_PROMPT_LINE_THRESHOLD = 14;
const COLLAPSIBLE_COMPOSER_PROMPT_CHAR_THRESHOLD = 1200;
const COLLAPSIBLE_COMPOSER_PROMPT_SOFT_LINE_CHARS = 84;

// Fold layout survives composer unmounts and draft swaps (e.g. switching
// session tabs) so a folded long paste does not silently turn back into raw
// text when the composer returns. Entries are keyed by the draft owner and
// hold only the folded prefix plus its text chunks; the visible follow-up
// text always lives in the canonical prompt.
const FOLDED_PROMPT_REGISTRY_MAX_ENTRIES = 64;
const foldedPromptRegistry = new Map<string, { prefix: string; texts: string[] }>();

export function rememberCollapsedPromptParts(
  storageKey: string,
  prompt: string,
  contentParts: MessageContentPart[] | undefined,
): void {
  const texts = (contentParts ?? [])
    .filter((part) => part.type === "pasted_text")
    .map((part) => part.text);
  const prefix = texts.join("");
  if (!storageKey || texts.length === 0 || !prompt.startsWith(prefix)) return;
  foldedPromptRegistry.set(storageKey, { prefix, texts });
  if (foldedPromptRegistry.size > FOLDED_PROMPT_REGISTRY_MAX_ENTRIES) {
    const oldestKey = foldedPromptRegistry.keys().next().value;
    if (oldestKey !== undefined) foldedPromptRegistry.delete(oldestKey);
  }
}

export function isCollapsibleComposerPrompt(text: string): boolean {
  if (text.trim().length === 0) {
    return false;
  }
  if (text.length > COLLAPSIBLE_COMPOSER_PROMPT_CHAR_THRESHOLD) {
    return true;
  }
  let estimatedLines = 0;
  for (const line of text.split(/\r\n|\r|\n/)) {
    estimatedLines += Math.max(
      1,
      Math.ceil(line.length / COLLAPSIBLE_COMPOSER_PROMPT_SOFT_LINE_CHARS)
    );
    if (estimatedLines > COLLAPSIBLE_COMPOSER_PROMPT_LINE_THRESHOLD) {
      return true;
    }
  }
  return false;
}

export function collapsedComposerPromptTitle(text: string): string {
  const firstLine = text
    .split(/\r\n|\r|\n/)
    .map((line) => line.trim())
    .find(Boolean);
  return firstLine || translate("composer.longText");
}

/**
 * A compact, attachment-like chip for one folded long paste. The whole chip
 * body reveals the text back into the textarea; the circular remove button
 * overlays the top-right corner, mirroring the file attachment chip.
 */
export function CollapsedComposerPromptCard({
  text,
  onReveal,
  onRemove
}: {
  text: string;
  onReveal: () => void;
  onRemove: () => void;
}): JSX.Element {
  const title = collapsedComposerPromptTitle(text);
  return (
    <div className="composer-collapsed-prompt-card">
      <button
        className="composer-collapsed-prompt-main"
        type="button"
        aria-label={translate("composer.showCollapsedTextNamed", { title })}
        onClick={onReveal}
      >
        <span className="composer-collapsed-prompt-icon" aria-hidden="true">
          <FileText className="icon" />
        </span>
        <TruncatedText as="strong" className="composer-collapsed-prompt-title" text={title} />
      </button>
      <button
        className="composer-collapsed-prompt-remove"
        type="button"
        aria-label={translate("composer.removeCollapsedText")}
        onClick={onRemove}
      >
        <X aria-hidden="true" />
      </button>
    </div>
  );
}

export type CollapsedComposerPromptPasteOptions = {
  readOnly: boolean;
  fileAttachmentsEnabled: boolean;
  onPasteAttachmentFiles: (files: File[]) => void;
  /** Runs right after a paste is accepted as a folded block. */
  onFold?: () => void;
};

/**
 * Shared state machine for the long-paste fold used by every composer:
 * - long pastes are kept out of the textarea and shown as folded chips,
 * - the full text stays in the canonical `prompt` (chips are only a visual
 *   prefix), so sending always ships the original text plus the follow-up,
 * - chips can be revealed back into the textarea or removed individually.
 *
 * `prompt`/`setPrompt` are the composer's canonical draft value and setter.
 */
export function useCollapsedComposerPrompt({
  prompt,
  setPrompt,
  focusComposerSoon,
  storageKey
}: {
  prompt: string;
  setPrompt: (value: string) => void;
  focusComposerSoon: () => void;
  /** Stable draft-owner identity used to persist fold layout across
   *  unmounts and draft swaps. When omitted the fold state stays local. */
  storageKey?: string;
}): {
  blocks: CollapsedComposerPromptBlock[];
  hasBlocks: boolean;
  prefix: string;
  visiblePrompt: string;
  listRef: RefObject<HTMLDivElement | null>;
  handlePaste: (
    event: ReactClipboardEvent<HTMLTextAreaElement>,
    options: CollapsedComposerPromptPasteOptions
  ) => void;
  revealBlock: (index: number) => void;
  removeBlock: (index: number) => void;
  contentPartsForPrompt: (prompt: string) => MessageContentPart[] | undefined;
} {
  const [blocks, setBlocks] = useState<CollapsedComposerPromptBlock[]>([]);
  const blocksRef = useRef<CollapsedComposerPromptBlock[]>([]);
  const blockIDRef = useRef(0);
  const listRef = useRef<HTMLDivElement>(null);

  const prefix = useMemo(() => blocks.map((block) => block.text).join(""), [blocks]);
  const hasBlocks = blocks.length > 0 && prompt.startsWith(prefix);
  const activeBlocks = hasBlocks ? blocks : [];
  const visiblePrompt = hasBlocks ? prompt.slice(prefix.length) : prompt;

  function nextBlockID(): string {
    return `composer-prompt-block-${Date.now().toString(36)}-${blockIDRef.current++}`;
  }

  function replaceBlocks(nextBlocks: CollapsedComposerPromptBlock[]): void {
    blocksRef.current = nextBlocks;
    setBlocks(nextBlocks);
  }

  function contentPartsForPrompt(nextPrompt: string): MessageContentPart[] | undefined {
    const nextBlocks = blocksRef.current;
    const nextPrefix = nextBlocks.map((block) => block.text).join("");
    if (nextBlocks.length === 0 || !nextPrompt.startsWith(nextPrefix)) return undefined;
    const visibleText = nextPrompt.slice(nextPrefix.length);
    return [
      ...nextBlocks.map((block) => ({ type: "pasted_text" as const, text: block.text })),
      ...(visibleText ? [{ type: "text" as const, text: visibleText }] : []),
    ];
  }

  function persistFold(nextBlocks: CollapsedComposerPromptBlock[], nextPrefix: string): void {
    if (!storageKey) {
      return;
    }
    if (nextBlocks.length === 0) {
      foldedPromptRegistry.delete(storageKey);
      return;
    }
    foldedPromptRegistry.set(storageKey, {
      prefix: nextPrefix,
      texts: nextBlocks.map((block) => block.text)
    });
    if (foldedPromptRegistry.size > FOLDED_PROMPT_REGISTRY_MAX_ENTRIES) {
      const oldestKey = foldedPromptRegistry.keys().next().value;
      if (oldestKey !== undefined) {
        foldedPromptRegistry.delete(oldestKey);
      }
    }
  }

  // Bring the fold layout back when this draft owner's prompt returns after
  // a draft swap (tab switch) or a composer remount. The reset effect below
  // clears blocks during the swap; the registry entry is only written on
  // explicit fold/reveal/remove actions, so a temporary prompt replacement
  // does not destroy the persisted layout.
  useEffect(() => {
    const entry = storageKey ? foldedPromptRegistry.get(storageKey) : undefined;
    if (!entry || entry.texts.length === 0) {
      return;
    }
    if (blocks.length > 0) {
      return;
    }
    if (entry.prefix.length === 0 || !prompt.startsWith(entry.prefix)) {
      return;
    }
    replaceBlocks(entry.texts.map((text) => ({ id: nextBlockID(), text })));
  }, [blocks.length, prompt, storageKey]);

  useEffect(() => {
    if (blocks.length > 0 && !prompt.startsWith(prefix)) {
      replaceBlocks([]);
    }
  }, [blocks.length, prefix, prompt]);

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || activeBlocks.length === 0) {
      return;
    }
    list.scrollTop = list.scrollHeight;
  }, [activeBlocks.length]);

  function handlePaste(
    event: ReactClipboardEvent<HTMLTextAreaElement>,
    options: CollapsedComposerPromptPasteOptions
  ): void {
    if (options.readOnly) {
      return;
    }
    if (options.fileAttachmentsEnabled) {
      const pasted = clipboardAttachmentFiles(event);
      if (pasted.length > 0) {
        event.preventDefault();
        options.onPasteAttachmentFiles(pasted);
        return;
      }
    }

    const pastedText = event.clipboardData?.getData("text/plain") ?? "";
    if (!isCollapsibleComposerPrompt(pastedText)) {
      return;
    }

    const selectionStart = event.currentTarget.selectionStart ?? 0;
    const selectionEnd = event.currentTarget.selectionEnd ?? 0;
    const visibleValue = event.currentTarget.value;
    const replacingVisiblePrompt = selectionStart === 0 && selectionEnd === visibleValue.length;
    if (visibleValue.length > 0 && !replacingVisiblePrompt) {
      return;
    }

    event.preventDefault();
    options.onFold?.();
    const nextBlock = {
      id: nextBlockID(),
      text: pastedText
    };
    const nextBlocks = hasBlocks ? [...blocks, nextBlock] : [nextBlock];
    const nextPrefix = `${hasBlocks ? prefix : ""}${pastedText}`;
    replaceBlocks(nextBlocks);
    setPrompt(nextPrefix);
    persistFold(nextBlocks, nextPrefix);
    focusComposerSoon();
  }

  function revealBlock(index: number): void {
    if (!hasBlocks) {
      return;
    }
    const revealedBlock = activeBlocks[index];
    if (!revealedBlock) {
      return;
    }
    const nextBlocks = activeBlocks.filter((_, blockIndex) => blockIndex !== index);
    const nextPrefix = nextBlocks.map((block) => block.text).join("");
    const nextVisiblePrompt = `${visiblePrompt}${revealedBlock.text}`;
    replaceBlocks(nextBlocks);
    setPrompt(`${nextPrefix}${nextVisiblePrompt}`);
    persistFold(nextBlocks, nextPrefix);
    focusComposerSoon();
  }

  function removeBlock(index: number): void {
    if (!hasBlocks) {
      return;
    }
    const nextBlocks = activeBlocks.filter((_, blockIndex) => blockIndex !== index);
    const nextPrefix = nextBlocks.map((block) => block.text).join("");
    replaceBlocks(nextBlocks);
    setPrompt(`${nextPrefix}${visiblePrompt}`);
    persistFold(nextBlocks, nextPrefix);
    focusComposerSoon();
  }

  return {
    blocks: activeBlocks,
    hasBlocks,
    prefix,
    visiblePrompt,
    listRef,
    handlePaste,
    revealBlock,
    removeBlock,
    contentPartsForPrompt,
  };
}
