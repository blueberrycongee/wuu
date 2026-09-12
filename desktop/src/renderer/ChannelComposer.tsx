import { Plus } from "lucide-react";
import { forwardRef, type KeyboardEvent, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import type { ComposerFile, ComposerImage } from "./ComposerMessages";
import { Composer, type CodexModelLoadState } from "./ComposerView";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { focusComposerTextarea } from "./ComposerFocus";
import { useI18n } from "./i18n";

const EMPTY_MODEL_STATE: CodexModelLoadState = {
  loading: false,
  error: "",
  models: [],
};

const noop = () => {};

type MentionRange = { start: number; end: number; query: string };

export type ChannelComposerHandle = {
  focus: () => void;
  insertMention: (name: string) => void;
};

export function mentionRangeAtCursor(draft: string, cursor: number): MentionRange | null {
  const beforeCursor = draft.slice(0, cursor);
  const match = beforeCursor.match(/(?:^|\s)@([^\s@]*)$/u);
  if (!match) return null;
  return {
    start: cursor - match[1].length - 1,
    end: cursor,
    query: match[1],
  };
}

export function draftWithMention(draft: string, name: string, start: number, end: number): { value: string; cursor: number } {
  const before = draft.slice(0, start);
  const after = draft.slice(end);
  const leadingSpace = before && !/\s$/u.test(before) ? " " : "";
  const trailingSpace = !after || !/^\s/u.test(after) ? " " : "";
  const inserted = `${leadingSpace}@${name}${trailingSpace}`;
  return {
    value: `${before}${inserted}${after}`,
    cursor: before.length + inserted.length,
  };
}

export const ChannelComposer = forwardRef<ChannelComposerHandle, {
  draft: string;
  draftRevision?: number;
  placeholder: string;
  disabled: boolean;
  sending: boolean;
  files: ComposerFile[];
  images: ComposerImage[];
  hideExpandButton?: boolean;
  compact?: boolean;
  mentionAgents?: NamedAgent[];
  queryHistorySessionID?: string;
  onChangeDraft: (draft: string) => void;
  onPasteAttachmentFiles: (files: File[]) => void;
  onRemoveFile: (id: string) => void;
  onRemoveImage: (id: string) => void;
  onSend: (promptOverride?: string) => void;
}>(function ChannelComposer({
  draft,
  draftRevision = 0,
  placeholder,
  disabled,
  sending,
  files,
  images,
  hideExpandButton = false,
  compact = false,
  mentionAgents = [],
  queryHistorySessionID,
  onChangeDraft,
  onPasteAttachmentFiles,
  onRemoveFile,
  onRemoveImage,
  onSend,
}, ref): JSX.Element {
  const { t } = useI18n();
  const runtimeRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const accessMenuRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<HTMLDivElement>(null);
  const attachmentInputRef = useRef<HTMLInputElement>(null);
  // The mention picker is portaled to the protected layer host (fixed,
  // viewport coordinates), so it anchors to the composer dock's rendered
  // box instead of sitting inside the relatively-positioned composer root.
  const mentionAnchorRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    mentionAnchorRef.current =
      composerRef.current?.querySelector<HTMLElement>("textarea") ??
      composerRef.current?.querySelector<HTMLElement>(".composer-stack") ??
      composerRef.current;
  });
  const [mentionRange, setMentionRange] = useState<MentionRange | null>(null);
  const [selectedMentionIndex, setSelectedMentionIndex] = useState(0);
  const matchingAgents = useMemo(() => {
    if (!mentionRange) return [];
    const query = mentionRange.query.toLocaleLowerCase();
    return mentionAgents.filter((agent) => agent.name.toLocaleLowerCase().includes(query));
  }, [mentionAgents, mentionRange]);

  const textarea = useCallback(
    () => composerRef.current?.querySelector<HTMLTextAreaElement>("textarea") ?? null,
    [],
  );

  const resizeInput = useCallback(() => {
    const input = textarea();
    if (!input || !compact) return;
    input.style.height = "0px";
    input.style.height = `${Math.min(160, Math.max(32, input.scrollHeight))}px`;
  }, [compact, textarea]);
  useLayoutEffect(resizeInput, [draft, draftRevision, resizeInput]);
  useEffect(() => {
    const element = composerRef.current;
    if (!element || !compact || typeof ResizeObserver === "undefined") return;
    let width = 0;
    const observer = new ResizeObserver(([entry]) => {
      if (entry.contentRect.width === width) return;
      width = entry.contentRect.width;
      resizeInput();
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [compact, resizeInput]);

  const updateMentionRange = useCallback((value: string): void => {
    const input = textarea();
    const cursor = input?.selectionStart ?? value.length;
    const nextRange = mentionRangeAtCursor(value, cursor);
    setMentionRange(nextRange);
    setSelectedMentionIndex(0);
  }, [textarea]);

  const insertMention = useCallback((name: string, range?: MentionRange | null): void => {
    const input = textarea();
    const inputFocused = input === document.activeElement;
    const start = range?.start ?? (inputFocused ? (input?.selectionStart ?? draft.length) : draft.length);
    const end = range?.end ?? (inputFocused ? (input?.selectionEnd ?? start) : draft.length);
    const next = draftWithMention(draft, name, start, end);
    onChangeDraft(next.value);
    setMentionRange(null);
    focusComposerTextarea(textarea(), next.cursor);
  }, [draft, onChangeDraft, textarea]);

  useImperativeHandle(ref, () => ({
    focus: () => textarea()?.focus(),
    insertMention: (name) => insertMention(name),
  }), [insertMention, textarea]);

  function handleKeyDownCapture(event: KeyboardEvent<HTMLDivElement>): void {
    if (!mentionRange || event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setMentionRange(null);
      return;
    }
    if (matchingAgents.length === 0) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      event.stopPropagation();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      setSelectedMentionIndex((current) => (current + direction + matchingAgents.length) % matchingAgents.length);
    } else if (event.key === "Enter" || event.key === "Tab") {
      event.preventDefault();
      event.stopPropagation();
      insertMention(matchingAgents[selectedMentionIndex]!.name, mentionRange);
    }
  }

  return (
    <div
      ref={composerRef}
      className={`channel-composer${compact ? " channel-composer-inline" : ""}`}
      onInput={resizeInput}
      onClick={() => updateMentionRange(draft)}
      onKeyUp={(event) => {
        if (event.key === "ArrowLeft" || event.key === "ArrowRight" || event.key === "Home" || event.key === "End") {
          updateMentionRange(draft);
        }
      }}
      onKeyDownCapture={handleKeyDownCapture}
    >
      {mentionRange ? (
        <FloatingMenuPortal
          anchorRef={mentionAnchorRef}
          owner="channel-mention"
          placement="above"
          align="left"
          // The picker used to overlap the composer top edge by 4px; keep
          // that tuck instead of the shared 8px gap.
          offset={-4}
          width={320}
        >
          <div className="channel-mention-menu" role="listbox" aria-label={t("channels.mentionPicker")}>
            {matchingAgents.length > 0 ? matchingAgents.map((agent, index) => (
              <button
                className={index === selectedMentionIndex ? "selected" : ""}
                type="button"
                role="option"
                aria-label={agent.name}
                aria-selected={index === selectedMentionIndex}
                key={agent.id}
                onMouseEnter={() => setSelectedMentionIndex(index)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={(event) => {
                  event.stopPropagation();
                  insertMention(agent.name, mentionRange);
                }}
              >
                <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />
                <span className="channel-mention-name">{agent.name}</span>
                <span className="channel-mention-meta" aria-hidden="true">
                  {agent.model_override ? <span className="channel-mention-model">{agent.model_override}</span> : null}
                  <kbd className="channel-mention-key">↵</kbd>
                </span>
              </button>
            )) : <span className="channel-mention-menu-empty">{t("channels.noMatchingAgents")}</span>}
          </div>
        </FloatingMenuPortal>
      ) : null}
      <Composer
        variant="dock"
        hideRuntimeControls
        leadingActions={<>
          <input ref={attachmentInputRef} className="channel-attachment-input" type="file" accept="image/*,application/pdf" multiple onChange={(event) => {
            const selected = Array.from(event.currentTarget.files ?? []);
            event.currentTarget.value = "";
            if (selected.length > 0) onPasteAttachmentFiles(selected);
          }} />
          <button type="button" className="icon-button channel-attachment-button" disabled={disabled} aria-label={t("composer.addAttachment")} title={t("composer.addAttachment")} onClick={() => attachmentInputRef.current?.click()}><Plus aria-hidden="true" /></button>
        </>}
        hidePlusButton
        hidePermissionControl
        hideExpandButton={compact || hideExpandButton}
        slashCommandsEnabled={false}
        placeholder={placeholder}
        maxLength={4000}
        queryHistorySessionID={queryHistorySessionID}
        prompt={draft}
        promptRevision={draftRevision}
        setPrompt={(value) => {
          onChangeDraft(value);
          window.requestAnimationFrame(() => updateMentionRange(value));
        }}
        files={files}
        images={images}
        queuedMessages={[]}
        guideMessages={[]}
        running={false}
        sendDisabled={sending}
        runtimeControlsDisabled
        tokensPerSecond={0}
        status=""
        statusLiveProgress={false}
        readOnly={disabled}
        projects={[]}
        codexModels={EMPTY_MODEL_STATE}
        codexRuntimeMenu={null}
        codexRuntimeRef={runtimeRef}
        menuOpen={false}
        accessMenuOpen={false}
        branchMenuOpen={false}
        menuRef={menuRef}
        accessMenuRef={accessMenuRef}
        projectFilter=""
        setProjectFilter={noop}
        onToggleMenu={noop}
        onToggleAccessMenu={noop}
        onToggleBranchMenu={noop}
        onToggleCodexRuntimeMenu={noop}
        onSelectRuntimeModel={noop}
        onSelectRuntimeEffort={noop}
        onSelectPermissionMode={noop}
        onOpenSettings={noop}
        onOpenSkillsCatalog={noop}
        onSelectProject={noop}
        onSelectNoProject={noop}
        onSelectGitBranch={noop}
        onCreateProject={noop}
        onOpenProject={noop}
        onStartNewThread={noop}
        onOpenWorkspaceTool={noop}
        onOpenInstructions={noop}
        onPasteAttachmentFiles={onPasteAttachmentFiles}
        onRemoveFile={onRemoveFile}
        onRemoveImage={onRemoveImage}
        onRemoveQueuedMessage={noop}
        onRemoveGuideMessage={noop}
        onGuideQueuedMessage={noop}
        onEditQueuedMessage={noop}
        onEditGuideMessage={noop}
        onSend={onSend}
        onInterrupt={noop}
      />
    </div>
  );
});
