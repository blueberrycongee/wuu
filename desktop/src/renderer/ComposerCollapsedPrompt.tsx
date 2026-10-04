import { FileText } from "./WuuIcons";
import {
  type ClipboardEvent as ReactClipboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore
} from "react";
import { clipboardAttachmentFiles } from "./ComposerMessages";
import { ComposerDocumentCard } from "./ComposerDocumentCard";
import type { MessageContentPart } from "../shared/protocol";
import { translateCurrent as translate, useI18n } from "./i18n";
import { TruncatedText } from "./TruncatedText";
import { buildFileSelectionPart } from "./FileSelectionContext";

export type CollapsedComposerPromptBlock = {
  id: string;
  text: string;
  part?: MessageContentPart;
};

const COLLAPSIBLE_COMPOSER_PROMPT_LINE_THRESHOLD = 14;
const COLLAPSIBLE_COMPOSER_PROMPT_CHAR_THRESHOLD = 1200;
const COLLAPSIBLE_COMPOSER_PROMPT_SOFT_LINE_CHARS = 84;

// Fold layout survives composer unmounts and draft swaps (e.g. switching
// session tabs) so a folded long paste does not silently turn back into raw
// text when the composer returns. Entries are keyed by the draft owner and
// hold the folded prefix and structured parts; the visible follow-up
// text always lives in the canonical prompt.
const FOLDED_PROMPT_REGISTRY_MAX_ENTRIES = 64;
const foldedPromptRegistry = new Map<string, { prefix: string; parts: MessageContentPart[] }>();
const foldedPromptListeners = new Set<() => void>();
let foldedPromptRevision = 0;

export function subscribeCollapsedPromptParts(listener: () => void): () => void {
  foldedPromptListeners.add(listener);
  return () => { foldedPromptListeners.delete(listener); };
}

export function getCollapsedPromptRevision(): number {
  return foldedPromptRevision;
}

function notifyCollapsedPromptParts(): void {
  foldedPromptRevision += 1;
  foldedPromptListeners.forEach((listener) => listener());
}

function consumedCollapsedPromptPrefix(
  entry: { prefix: string; parts: MessageContentPart[] },
  prompt: string,
): string | undefined {
  const canonicalPrefix = entry.parts.map((part) => part.text).join("");
  // Edits rebuild the canonical prefix. Prefer its full length so restored
  // separators never become part of the user's visible follow-up.
  if (prompt.startsWith(canonicalPrefix)) return canonicalPrefix;
  return prompt.startsWith(entry.prefix) ? entry.prefix : undefined;
}

export function readCollapsedPromptParts(
  storageKey: string,
  prompt: string,
): MessageContentPart[] | undefined {
  const entry = foldedPromptRegistry.get(storageKey);
  if (!entry) return undefined;
  const consumedPrefix = consumedCollapsedPromptPrefix(entry, prompt);
  if (consumedPrefix === undefined) return undefined;
  const visibleText = prompt.slice(consumedPrefix.length);
  return [...entry.parts, ...(visibleText ? [{ type: "text" as const, text: visibleText }] : [])];
}

export function rememberCollapsedPromptParts(
  storageKey: string,
  prompt: string,
  contentParts: MessageContentPart[] | undefined,
): void {
  if (!storageKey) return;
  const parts = (contentParts ?? [])
    .filter((part) => part.type === "pasted_text" || part.type === "file_selection");
  if (parts.length === 0) {
    if (foldedPromptRegistry.delete(storageKey)) notifyCollapsedPromptParts();
    return;
  }
  const originalPrefix = parts.map((part) => part.text).join("");
  // The server trims the outer prompt while retaining exact part text. Only
  // remove trailing prefix whitespace when it consumes the entire prompt;
  // before a visible question or another block that whitespace is internal.
  const prefix = [originalPrefix, originalPrefix.trimStart()]
    .find((candidate) => candidate.length > 0 && prompt.startsWith(candidate))
    ?? ([originalPrefix.trimEnd(), originalPrefix.trim()].includes(prompt) && prompt.length > 0
      ? prompt
      : undefined);
  if (prefix === undefined) return;
  const previous = foldedPromptRegistry.get(storageKey);
  if (previous?.prefix === prefix && JSON.stringify(previous.parts) === JSON.stringify(parts)) return;
  foldedPromptRegistry.set(storageKey, { prefix, parts });
  if (foldedPromptRegistry.size > FOLDED_PROMPT_REGISTRY_MAX_ENTRIES) {
    const oldestKey = foldedPromptRegistry.keys().next().value;
    if (oldestKey !== undefined) foldedPromptRegistry.delete(oldestKey);
  }
  notifyCollapsedPromptParts();
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
  const name = (firstLine || translate("composer.longText")).replace(/[\\/:*?"<>|]/g, "_").slice(0, 80);
  return name.endsWith(".txt") ? name : `${name}.txt`;
}

function lineCount(text: string): number {
  const trimmed = text.trimEnd();
  let lines = 1;
  for (let index = 0; index < trimmed.length; index += 1) {
    const code = trimmed.charCodeAt(index);
    if (code === 10 || (code === 13 && trimmed.charCodeAt(index + 1) !== 10)) lines += 1;
  }
  return lines;
}

/**
 * A pasted text attachment can be revealed directly into the input for editing.
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
  const { t, formatNumber } = useI18n();
  const title = collapsedComposerPromptTitle(text);
  const lines = useMemo(() => lineCount(text), [text]);
  return (
    <ComposerDocumentCard
      className="composer-collapsed-prompt-card"
      icon={<FileText className="icon" />}
      title={<TruncatedText as="strong" className="composer-document-card-title" text={title} />}
      meta={lines > 1
        ? t("composer.pastedTextLines", { count: formatNumber(lines) })
        : t("composer.pastedTextCharacters", { count: formatNumber(text.length) })}
      openLabel={t("composer.showCollapsedTextNamed", { title })}
      onOpen={onReveal}
      removeLabel={t("composer.removeCollapsedText")}
      onRemove={onRemove}
    />
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
 * - the full snapshot stays in the canonical draft and structured parts;
 *   the server materializes it as a file reference at submission,
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
  /** Exact part-text prefix to prepend when changing the visible draft. */
  prefix: string;
  visiblePrompt: string;
  handlePaste: (
    event: ReactClipboardEvent<HTMLTextAreaElement>,
    options: CollapsedComposerPromptPasteOptions
  ) => void;
  revealBlock: (index: number) => void;
  removeBlock: (index: number) => void;
  updateFileComment: (id: string, comment: string) => void;
  removeFileSelection: (id: string | string[]) => void;
  contentPartsForPrompt: (prompt: string) => MessageContentPart[] | undefined;
} {
  const [localBlocks, setLocalBlocks] = useState<CollapsedComposerPromptBlock[]>([]);
  const localBlocksRef = useRef(localBlocks);
  const blockIDRef = useRef(0);
  const listRef = useRef<HTMLDivElement>(null);
  useSyncExternalStore(subscribeCollapsedPromptParts, getCollapsedPromptRevision);
  const entry = storageKey ? foldedPromptRegistry.get(storageKey) : undefined;
  const blocks = useMemo(() => storageKey
    ? (entry?.parts ?? []).map((part, index) => ({
      id: part.type === "file_selection" ? `file-selection-${part.id}` : `composer-prompt-block-${index}`,
      text: part.text,
      part,
    }))
    : localBlocks, [entry, localBlocks, storageKey]);

  const prefix = useMemo(() => blocks.map((block) => block.text).join(""), [blocks]);
  const consumedPrefix = entry ? consumedCollapsedPromptPrefix(entry, prompt) : prefix;
  const hasBlocks = blocks.length > 0 && consumedPrefix !== undefined && prompt.startsWith(consumedPrefix);
  const activeBlocks = hasBlocks ? blocks : [];
  const visiblePrompt = hasBlocks ? prompt.slice(consumedPrefix.length) : prompt;

  function nextBlockID(): string {
    return `composer-prompt-block-${Date.now().toString(36)}-${blockIDRef.current++}`;
  }

  function contentPartsForPrompt(nextPrompt: string): MessageContentPart[] | undefined {
    if (storageKey) return readCollapsedPromptParts(storageKey, nextPrompt);
    const nextBlocks = localBlocksRef.current;
    const nextPrefix = nextBlocks.map((block) => block.text).join("");
    if (nextBlocks.length === 0 || !nextPrompt.startsWith(nextPrefix)) return undefined;
    const visibleText = nextPrompt.slice(nextPrefix.length);
    return [
      ...nextBlocks.map((block) => block.part ?? { type: "pasted_text" as const, text: block.text }),
      ...(visibleText ? [{ type: "text" as const, text: visibleText }] : []),
    ];
  }

  function applyBlocks(nextBlocks: CollapsedComposerPromptBlock[], nextVisiblePrompt: string, focusComposer = true): void {
    const nextPrompt = nextBlocks.map((block) => block.text).join("") + nextVisiblePrompt;
    if (storageKey) {
      rememberCollapsedPromptParts(storageKey, nextPrompt, nextBlocks.map((block) =>
        block.part ?? { type: "pasted_text" as const, text: block.text }));
    } else {
      localBlocksRef.current = nextBlocks;
      setLocalBlocks(nextBlocks);
    }
    setPrompt(nextPrompt);
    if (focusComposer) focusComposerSoon();
  }

  // Registry entries are changed only by explicit metadata operations. A
  // transient send-clear hides blocks without destroying queued restore data.
  // Deriving keyed blocks directly also prevents local effects from overwriting
  // an external same-owner update or leaking metadata across draft owners.

  useEffect(() => {
    if (!storageKey && localBlocks.length > 0 && !prompt.startsWith(prefix)) {
      localBlocksRef.current = [];
      setLocalBlocks([]);
    }
  }, [localBlocks.length, prefix, prompt, storageKey]);

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
    event.preventDefault();
    options.onFold?.();
    const nextBlock = {
      id: nextBlockID(),
      text: pastedText
    };
    const nextBlocks = hasBlocks ? [...blocks, nextBlock] : [nextBlock];
    applyBlocks(nextBlocks, visibleValue.slice(0, selectionStart) + visibleValue.slice(selectionEnd));
  }

  function revealBlock(index: number): void {
    if (!hasBlocks) {
      return;
    }
    const revealedBlock = activeBlocks[index];
    if (!revealedBlock || revealedBlock.part?.type === "file_selection") {
      return;
    }
    const nextBlocks = activeBlocks.filter((_, blockIndex) => blockIndex !== index);
    const nextVisiblePrompt = `${visiblePrompt}${revealedBlock.text}`;
    applyBlocks(nextBlocks, nextVisiblePrompt);
  }

  function removeBlock(index: number): void {
    if (!hasBlocks) {
      return;
    }
    const nextBlocks = activeBlocks.filter((_, blockIndex) => blockIndex !== index);
    applyBlocks(nextBlocks, visiblePrompt);
  }

  function updateFileComment(id: string, comment: string): void {
    const index = activeBlocks.findIndex((block) => block.part?.type === "file_selection" && block.part.id === id);
    const block = activeBlocks[index];
    if (block?.part?.type !== "file_selection") return;
    const part = buildFileSelectionPart(block.part.source, block.part.intent, comment, id);
    applyBlocks(activeBlocks.map((current, blockIndex) => blockIndex === index
      ? { ...block, text: part.text, part }
      : current), visiblePrompt, false);
  }

  function removeFileSelection(id: string | string[]): void {
    const ids = new Set(Array.isArray(id) ? id : [id]);
    const nextBlocks = activeBlocks.filter((block) => block.part?.type !== "file_selection" || !ids.has(block.part.id));
    if (nextBlocks.length !== activeBlocks.length) applyBlocks(nextBlocks, visiblePrompt);
  }

  return {
    blocks: activeBlocks,
    hasBlocks,
    prefix,
    visiblePrompt,
    handlePaste,
    revealBlock,
    removeBlock,
    updateFileComment,
    removeFileSelection,
    contentPartsForPrompt,
  };
}
