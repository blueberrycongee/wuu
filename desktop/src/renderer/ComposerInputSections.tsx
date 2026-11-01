import { useComposerServices } from "./ComposerServicesContext";
import { ComposerInlineCompletionOverlay, useComposerInlineCompletion } from "./ComposerInlineCompletion";
import type { PluginHost } from "./plugins/PluginHost";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import { useComposerAttachmentFocus } from "./useComposerAttachmentFocus";
import { useComposerHeight } from "./useComposerHeight";
import { VideoAttachment } from "./VideoAttachment";
import { COMPOSER_ATTACHMENT_ACCEPT } from "./ComposerMessages";
import * as React from "react";
import { createComposerDrawer } from "../shared/ComposerDrawer";
import { AttachmentImage } from "./AttachmentImage";
import {
  ArrowUp,
  CornerDownRight,
  CornerUpLeft,
  FileText,
  ListTodo,
  Paperclip,
  PencilLine,
  Play,
  Square,
  LoaderCircle,
  RotateCw,
  X
} from "./WuuIcons";
import { type DragEvent as ReactDragEvent, type KeyboardEvent as ReactKeyboardEvent, useEffect, useMemo, useRef, useState } from "react";
import { useOptionalImagePreview } from "./ImagePreview";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import {
  CollapsedComposerPromptCard,
  rememberCollapsedPromptParts,
  useCollapsedComposerPrompt
} from "./ComposerCollapsedPrompt";
import { useFileSelectionActions } from "./FileSelectionContext";
import { ComposerAttachmentTray } from "./ComposerAttachmentTray";
import {
  WORKSPACE_FILE_DRAG_MIME,
  appendWorkspacePathToPrompt,
  queuedMessageFullPreview,
  queuedMessagePreview,
  type ComposerFile,
  type ComposerImage,
  type QueuedComposerMessage
} from "./ComposerMessages";
import { useComposerQueryHistory } from "./ComposerQueryHistory";
import { handoffPromptFromIntent } from "./HandoffDraft";
import { useI18n } from "./i18n";
import { focusComposerTextarea, isTouchWebShell } from "./ComposerFocus";
import { ComposerAttachMenuButton, ComposerCameraPanel } from "./ComposerCamera";
import { ComposerPlusButton } from "./ComposerRuntimeMenus";
import { useWorkbenchConnected } from "./WorkbenchConnectionContext";
import { Tooltip } from "./Tooltip";
import { showErrorToast } from "./Toast";
import { type TurnContinuationTarget } from "./TurnContinuation";
import { MessageCopyButton } from "./MessageActions";
import type { MessageContentPart, ResponseSelection } from "../shared/protocol";

const ComposerDrawer = createComposerDrawer(React);

/** Read-only attachments of a sent message; drafts use ComposerAttachmentTray. */
export function ComposerAttachmentStrip({
  files,
  images
}: {
  files: ComposerFile[];
  images: ComposerImage[];
}): JSX.Element | null {
  const { t } = useI18n();
  const imagePreview = useOptionalImagePreview();
  if (images.length === 0 && files.length === 0) {
    return null;
  }
  return (
    <div className="composer-attachments">
      {images.map((image, index) => {
        const label = t("composer.imageNumber", { number: index + 1 });
        return (
          <div className="composer-image-attachment" key={image.id}>
            <AttachmentImage image={image} label={label}
              onOpen={src => imagePreview?.openPreview({ src, alt: label, title: label })} />
          </div>
        );
      })}
      {files.map((file, index) => (
        <div className="composer-file-attachment" key={file.id}>
          {file.media_type.startsWith("video/") ? <VideoAttachment file={file} /> : <>
            <FileText className="icon" aria-hidden="true" />
            <span>{file.filename?.trim() || t("composer.pdfNumber", { number: index + 1 })}</span>
          </>}
        </div>
      ))}
    </div>
  );
}

export function ComposerStopIcon({ state, submitting = false }: { state?: "pending" | "retry"; submitting?: boolean }): JSX.Element {
  if (state === "pending") return <LoaderCircle className="composer-stop-progress is-spinning" aria-hidden="true" />;
  if (state === "retry") return <RotateCw className="composer-stop-progress" aria-hidden="true" />;
  if (submitting) return <LoaderCircle className="composer-stop-progress control-busy-icon" aria-hidden="true" />;
  return <Square aria-hidden="true" />;
}

export function ComposerContinueButton({ continuation, disabled }: {
  continuation: TurnContinuationTarget;
  disabled: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);
  return (
    <Tooltip asChild content={t("composer.continue")}><button
      className="composer-action-button composer-send-button"
      type="button"
      data-wuu-component="composer-send"
      data-wuu-state={pending ? "continuing" : "continue"}
      aria-label={t("composer.continue")}
      aria-busy={pending || undefined}
      disabled={disabled || pending}
      onClick={() => {
        if (disabled || pendingRef.current) return;
        pendingRef.current = true;
        setPending(true);
        void continuation.continue().catch(showErrorToast).finally(() => {
          pendingRef.current = false;
          setPending(false);
        });
      }}
    >
      {pending ? <LoaderCircle className="composer-stop-progress is-spinning" aria-hidden="true" /> : <Play aria-hidden="true" />}
    </button></Tooltip>
  );
}

export function SplitPaneComposer({
  pluginHost,
  placeholder,
  framed = false,
  allowWorkspaceReferences = true,
  prompt,
  setPrompt,
  files,
  images,
  selections = [],
  onChangeSelection,
  onRemoveSelection,
  running,
  readOnly: sessionReadOnly,
  interactionDisabled = false,
  sendDisabled: requestedSendDisabled = false,
  stopState,
  submitting = false,
  draftPending = false,
  clearOnSubmit = true,
  continuation,
  queryHistorySessionID,
  workspaceId,
  workspaceRoot,
  queuedCount,
  pendingCount,
  turnId,
  queryHistory = [],
  requestedHandoffIntent,
  onPasteAttachmentFiles,
  onRemoveFile,
  onRemoveImage,
  onSend,
  onInterrupt,
}: {
  pluginHost?: PluginHost;
  placeholder?: string;
  framed?: boolean;
  allowWorkspaceReferences?: boolean;
  prompt: string;
  setPrompt: (value: string) => void;
  files: ComposerFile[];
  images: ComposerImage[];
  selections?: ResponseSelection[];
  onChangeSelection?: (selection: ResponseSelection) => void;
  onRemoveSelection?: (id: string) => void;
  running: boolean;
  readOnly: boolean;
  /** Temporary ownership transfer lock; does not change session semantics. */
  interactionDisabled?: boolean;
  /** Instant view switches keep `running` on for the spinner but must not
   * allow a send/stop against the previous pane's thread. */
  sendDisabled?: boolean;
  stopState?: "pending" | "retry";
  submitting?: boolean;
  draftPending?: boolean;
  clearOnSubmit?: boolean;
  continuation?: TurnContinuationTarget;
  queryHistorySessionID?: string;
  workspaceId?: string;
  workspaceRoot?: string;
  queuedCount?: number;
  pendingCount?: number;
  turnId?: string;
  queryHistory?: string[];
  requestedHandoffIntent?: string;
  onPasteAttachmentFiles: (files: File[]) => void;
  onRemoveFile: (id: string) => void;
  onRemoveImage: (id: string) => void;
  onSend: (promptOverride?: string, contentParts?: MessageContentPart[]) => boolean | void | Promise<boolean | void>;
  onInterrupt: () => void;
}): JSX.Element {
  const readOnly = sessionReadOnly || interactionDisabled;
  const { t } = useI18n();
  const connected = useWorkbenchConnected();
  const sendDisabled = requestedSendDisabled || draftPending || Boolean(stopState) || !connected;
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const composing = useRef(false);
  const [submissionRevision, setSubmissionRevision] = useState(0);
  const inputDraftUpdates = useRef<string[]>([]);
  const externalDraft = useRef({ prompt, revision: 0 });
  if (externalDraft.current.prompt !== prompt) {
    const echo = inputDraftUpdates.current.lastIndexOf(prompt);
    if (echo >= 0) inputDraftUpdates.current.splice(0, echo + 1);
    else {
      inputDraftUpdates.current.length = 0;
      externalDraft.current.revision += 1;
    }
    externalDraft.current.prompt = prompt;
  }
  const attachmentInputRef = useRef<HTMLInputElement>(null);
  const photosInputRef = useRef<HTMLInputElement>(null);
  const shellRef = useRef<HTMLDivElement>(null);
  const appliedHandoffIntentRef = useRef<string | undefined>(undefined);
  const [dropActive, setDropActive] = useState(false);
  const [cameraOpen, setCameraOpen] = useState(false);
  const mobileWeb = useMemo(() => isTouchWebShell(), []);
  useEffect(() => {
    setCameraOpen(false);
  }, [readOnly, queryHistorySessionID]);
  const hasAttachments = images.length > 0 || files.length > 0 || selections.length > 0;
  const hasDraft = prompt.trim().length > 0 || hasAttachments;
  const draftSignature = JSON.stringify([prompt, images.map(image => image.id), files.map(file => file.id), selections]);
  const submittedDraftRef = useRef<string | undefined>(undefined);
  useEffect(() => () => { submittedDraftRef.current = undefined; }, []);
  useEffect(() => { submittedDraftRef.current = undefined; }, [draftSignature, queryHistorySessionID]);
  // Match the dock composer: the button is a stop control only while running
  // with an empty input. Once there is a draft, it flips to send (queuing
  // mid-turn) so a typed follow-up is never blocked by the stop state.
  const showStop = Boolean(stopState) || ((running || submitting) && !sendDisabled && !hasDraft);
  const showContinue = continuation && !running && !submitting && !hasDraft && !readOnly;
  const sendLabel = running ? t("composer.queueSend") : t("composer.send");

  function focusComposerSoon(): void {
    focusComposerTextarea(textareaRef.current);
  }

  function focusComposerAtEndSoon(): void {
    focusComposerTextarea(textareaRef.current, "end");
  }

  function openCamera(): void {
    textareaRef.current?.blur();
    setCameraOpen(true);
  }

  function closeCamera(): void {
    setCameraOpen(false);
    focusComposerSoon();
  }

  function captureCamera(file: File): void {
    setCameraOpen(false);
    onPasteAttachmentFiles([file]);
    focusComposerSoon();
  }

  const {
    blocks: collapsedPromptBlocks,
    hasBlocks: hasCollapsedPromptBlocks,
    prefix: collapsedPromptPrefix,
    visiblePrompt: visiblePromptValue,
    handlePaste: handleCollapsedComposerPaste,
    revealBlock: revealCollapsedPromptBlock,
    removeBlock: removeCollapsedPromptBlock,
    updateFileComment,
    removeFileSelection,
    contentPartsForPrompt: collapsedContentPartsForPrompt,
  } = useCollapsedComposerPrompt({
    prompt,
    setPrompt,
    focusComposerSoon,
    storageKey: queryHistorySessionID
  });
  useComposerHeight(textareaRef, visiblePromptValue);
  const fileSelectionActions = useFileSelectionActions();
  const onAttachmentPickerClick = useComposerAttachmentFocus(
    textareaRef, queryHistorySessionID, fileSelectionActions?.ownerKey, readOnly,
  );
  const fileSelectionParts = collapsedPromptBlocks.flatMap((block) =>
    block.part?.type === "file_selection" ? [block.part] : []);

  const { resetQueryHistoryNavigation, handleQueryHistoryKeyDown } = useComposerQueryHistory({
    disabled: readOnly || hasAttachments || hasCollapsedPromptBlocks,
    prompt,
    queryHistory,
    queryHistorySessionID,
    setPrompt,
    textareaRef
  });

  function updateVisiblePrompt(value: string): void {
    resetQueryHistoryNavigation();
    const nextPrompt = hasCollapsedPromptBlocks ? `${collapsedPromptPrefix}${value}` : value;
    inputDraftUpdates.current.push(nextPrompt);
    setPrompt(nextPrompt);
  }
  const services = useComposerServices();
  const inline = useComposerInlineCompletion({
    host: pluginHost ?? services?.presentation ?? desktopPluginHost, inputRef: textareaRef, value: visiblePromptValue,
    kind: "main",
    enabled: !readOnly && connected && !hasCollapsedPromptBlocks && !cameraOpen,
    contextKey: JSON.stringify([workspaceId, workspaceRoot, queryHistorySessionID]), revisionKey: JSON.stringify([submissionRevision, turnId, requestedHandoffIntent]),
    resetKey: JSON.stringify([externalDraft.current.revision, requestedHandoffIntent]),
    threadId: queryHistorySessionID, workspaceId, workspaceRoot,
    attachmentCount: files.length + images.length + selections.length, queuedCount, pendingCount,
    running, compositionActive: composing.current, onInsert: updateVisiblePrompt,
  });

  useEffect(() => {
    if (requestedHandoffIntent === undefined || appliedHandoffIntentRef.current === requestedHandoffIntent) {
      return;
    }
    appliedHandoffIntentRef.current = requestedHandoffIntent;
    setPrompt(handoffPromptFromIntent(requestedHandoffIntent));
  }, [requestedHandoffIntent, setPrompt]);

  // Mirrors the dock composer: a drop carries either a workspace path
  // reference (inserted as plain text) or external files (forwarded to the
  // attachment pipeline). Anything else keeps its native behavior.
  function handleDragOver(event: ReactDragEvent<HTMLDivElement>): void {
    if (readOnly) {
      return;
    }
    const types = Array.from(event.dataTransfer.types ?? []);
    if (!types.includes(WORKSPACE_FILE_DRAG_MIME) && !types.includes("Files")) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setDropActive(true);
  }

  function handleDragLeave(event: ReactDragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setDropActive(false);
  }

  function handleDrop(event: ReactDragEvent<HTMLDivElement>): void {
    setDropActive(false);
    if (readOnly) {
      return;
    }
    const workspacePath = event.dataTransfer.getData(WORKSPACE_FILE_DRAG_MIME);
    if (workspacePath && allowWorkspaceReferences) {
      event.preventDefault();
      resetQueryHistoryNavigation();
      setPrompt(appendWorkspacePathToPrompt(prompt, workspacePath));
      focusComposerAtEndSoon();
      return;
    }
    const dropped = Array.from(event.dataTransfer.files ?? []);
    if (dropped.length === 0) {
      return;
    }
    event.preventDefault();
    onPasteAttachmentFiles(dropped);
  }

  function submitComposer(): void {
    const visiblePrompt = textareaRef.current?.value ?? visiblePromptValue;
    const currentPrompt = hasCollapsedPromptBlocks ? `${collapsedPromptPrefix}${visiblePrompt}` : visiblePrompt;
    const currentSignature = JSON.stringify([currentPrompt, images.map(image => image.id), files.map(file => file.id), selections]);
    if (readOnly || sendDisabled || submittedDraftRef.current === currentSignature) return;
    resetQueryHistoryNavigation();
    const contentParts = collapsedContentPartsForPrompt(currentPrompt);
    const canonicalPrompt = contentParts ? contentParts.map((part) => part.text).join("") : currentPrompt;
    if (contentParts && queryHistorySessionID) rememberCollapsedPromptParts(queryHistorySessionID, canonicalPrompt, contentParts);
    submittedDraftRef.current = currentSignature;
    const dispatched = inline.actions.prepareDispatch(canonicalPrompt, running ? "queue" : "send");
    const finish = (accepted: boolean | void): void => {
      if (accepted !== false) dispatched();
      if (submittedDraftRef.current !== currentSignature) return;
      if (accepted === false) {
        submittedDraftRef.current = undefined;
        return;
      }
      if (!clearOnSubmit) {
        submittedDraftRef.current = undefined;
        return;
      }
      composing.current = false;
      setSubmissionRevision((revision) => revision + 1);
    };
    try {
      const accepted = contentParts ? onSend(canonicalPrompt, contentParts) : onSend(currentPrompt);
      if (accepted instanceof Promise) void accepted.then(finish, () => finish(false));
      else finish(accepted);
    } catch (error) {
      finish(false);
      throw error;
    }
    // Keep focus restoration in the user action, without viewport scrolling.
    const textarea = textareaRef.current;
    if (textarea && document.activeElement !== textarea) {
      textarea.focus({ preventScroll: true });
    }
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>): void {
    if (readOnly) {
      return;
    }
    if (isComposerTextComposing(event)) {
      return;
    }
    if (event.key === "Tab") {
      if (!event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey && !event.repeat && inline.actions.accept()) {
        event.preventDefault();
      }
      return;
    }
    if (event.key === "Escape" && inline.actions.dismiss()) {
      event.preventDefault();
      return;
    }
    if (handleQueryHistoryKeyDown(event)) {
      return;
    }
    if (event.key === "Escape" && (running || draftPending) && !requestedSendDisabled && connected && !event.repeat) {
      event.preventDefault();
      onInterrupt();
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submitComposer();
    }
  }

  return (
    <footer className="composer-wrap dock-composer-wrap split-composer" data-pip-obstacle="composer">
      <div className="composer-stack">
        <div className="composer-shell" ref={shellRef}>
          <div className="composer-frame-shell">
            {cameraOpen && !readOnly ? (
              <ComposerCameraPanel onCapture={captureCamera} onClose={closeCamera} />
            ) : null}
            <ComposerAttachmentTray
              images={images}
              files={files}
              selections={selections}
              pastedTexts={collapsedPromptBlocks}
              fileSelections={fileSelectionParts}
              onRemoveFileSelection={readOnly ? undefined : removeFileSelection}
              onEditFileSelection={readOnly ? undefined : (part, comment) => updateFileComment(part.id, comment)}
              onOpenSelectedFile={fileSelectionActions ? (path) => fileSelectionActions.openFile(path) : undefined}
              resetKey={queryHistorySessionID}
              onRemoveImage={onRemoveImage}
              onRemoveFile={onRemoveFile}
              onChangeSelection={readOnly ? undefined : onChangeSelection}
              onRemoveSelection={readOnly ? undefined : onRemoveSelection}
              onRevealText={revealCollapsedPromptBlock}
              onRemoveText={removeCollapsedPromptBlock}
            />
            <div
              className={`composer-frame split-composer-shell${dropActive ? " composer-frame-drop-active split-composer-shell-drop-active" : ""}`}
              data-wuu-component="composer-frame"
              onDragOver={handleDragOver}
              onDragLeave={handleDragLeave}
              onDrop={handleDrop}
            >
              <div className="composer">
                <input
                  ref={attachmentInputRef}
                  className="composer-file-input"
                  type="file"
                  accept={COMPOSER_ATTACHMENT_ACCEPT}
                  multiple
                  tabIndex={-1}
                  onClick={onAttachmentPickerClick}
                  onChange={(event) => {
                    const selected = Array.from(event.currentTarget.files ?? []);
                    event.currentTarget.value = "";
                    if (selected.length > 0) {
                      onPasteAttachmentFiles(selected);
                    }
                  }}
                />
                <input
                  ref={photosInputRef}
                  className="composer-file-input"
                  type="file"
                  accept="image/*"
                  multiple
                  tabIndex={-1}
                  onClick={onAttachmentPickerClick}
                  onChange={(event) => {
                    const selected = Array.from(event.currentTarget.files ?? []);
                    event.currentTarget.value = "";
                    if (selected.length > 0) {
                      onPasteAttachmentFiles(selected);
                    }
                  }}
                />
                <div className={`${framed ? "composer-editor" : "composer-bar"} split-composer-bar`}>
                  <div className="composer-bar-left">
                    {framed ? (
                      <ComposerPlusButton variant="dock" disabled={readOnly} commands={[]} menuAnchorRef={shellRef}
                        onAddAttachment={() => attachmentInputRef.current?.click()}
                        mobileAttachments={mobileWeb ? { onTakePhoto: openCamera, onPickPhotos: () => photosInputRef.current?.click(), onPickFiles: () => attachmentInputRef.current?.click() } : undefined}
                        onSelectCommand={() => {}} />
                    ) : mobileWeb ? (
                      <ComposerAttachMenuButton
                        variant="dock"
                        disabled={readOnly}
                        menuAnchorRef={shellRef}
                        onTakePhoto={openCamera}
                        onPickPhotos={() => photosInputRef.current?.click()}
                        onPickFiles={() => attachmentInputRef.current?.click()}
                      />
                    ) : (
                      <Tooltip asChild content={t("composer.addAttachment")}><button
                        className="composer-tool-button composer-attach-button"
                        type="button"
                        aria-label={t("composer.addAttachment")}
                        disabled={readOnly}
                        onClick={() => attachmentInputRef.current?.click()}
                      >
                        <Paperclip aria-hidden="true" />
                      </button></Tooltip>
                    )}
                  </div>
                  <textarea
                    ref={textareaRef}
                    data-wuu-component="composer-input"
                    value={visiblePromptValue}
                    placeholder={inline.text ? "" : sessionReadOnly ? t("composer.readOnly") : hasCollapsedPromptBlocks ? t("composer.followupChanges") : hasAttachments ? t("composer.addDescription") : (placeholder ?? t("composer.continueBranch"))}
                    disabled={readOnly}
                    aria-readonly={readOnly}
                    aria-describedby={inline.text ? inline.descriptionId : undefined}
                    onFocus={() => inline.observe()}
                    onBlur={() => inline.observe()}
                    onSelect={() => inline.observe()}
                    onCompositionStart={() => { composing.current = true; inline.observe(true); }}
                    onCompositionEnd={() => { composing.current = false; inline.observe(false); }}
                    onChange={(event) => {
                      updateVisiblePrompt(event.target.value);
                      inline.observe();
                    }}
                    onPaste={(event) =>
                      handleCollapsedComposerPaste(event, {
                        readOnly,
                        fileAttachmentsEnabled: true,
                        onPasteAttachmentFiles,
                        onFold: resetQueryHistoryNavigation
                      })
                    }
                    onKeyDown={handleKeyDown}
                  />
                  {inline.text ? <ComposerInlineCompletionOverlay inputRef={textareaRef} value={visiblePromptValue} text={inline.text} /> : null}
                  {inline.text ? <span className="sr-only" id={inline.descriptionId} role="status">
                    {t("composer.inlineCompletionAvailable", { text: inline.text })}
                  </span> : null}
                  <div className="composer-bar-right">
                    {showStop ? (
                      <Tooltip asChild content={t(stopState === "pending" ? "composer.stopping" : stopState === "retry" ? "composer.retryStop" : "composer.pauseShortcut")}><button
                        className="composer-action-button composer-stop-button"
                        type="button"
                        onClick={onInterrupt}
                        disabled={stopState === "pending" || !connected}
                        data-wuu-state={stopState ?? (submitting ? "submitting" : "stop")}
                        aria-busy={stopState === "pending" || submitting || undefined}
                        aria-label={t(stopState === "pending" ? "composer.stopping" : stopState === "retry" ? "composer.retryStop" : "composer.pause")}
                      >
                        <ComposerStopIcon state={stopState} submitting={submitting} />
                      </button></Tooltip>
                    ) : showContinue ? (
                      <ComposerContinueButton
                        key={`${continuation.threadID}:${continuation.turnID}`}
                        continuation={continuation}
                        disabled={sendDisabled}
                      />
                    ) : (
                      <Tooltip asChild content={sendLabel}><button
                        className="composer-action-button composer-send-button"
                        data-wuu-component="composer-send"
                        data-wuu-state={draftPending ? "submitting" : "send"}
                        aria-busy={draftPending || undefined}
                        onPointerDown={(event) => {
                          if (event.button === 0 && document.activeElement === textareaRef.current) {
                            event.preventDefault();
                          }
                        }}
                        type="button"
                        onClick={submitComposer}
                        aria-label={sendLabel}
                        disabled={readOnly || sendDisabled || !hasDraft}
                      >
                        {draftPending ? <ComposerStopIcon submitting /> : <ArrowUp aria-hidden="true" />}
                      </button></Tooltip>
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </footer>
  );
}

type QueueRowKind = "guide" | "queue";

type QueueRowEntry = {
  key: string;
  message: QueuedComposerMessage;
  kind: QueueRowKind;
};

function buildQueueRows(
  guideMessages: QueuedComposerMessage[],
  queuedMessages: QueuedComposerMessage[]
): QueueRowEntry[] {
  return [
    ...guideMessages.map((message) => ({
      key: `guide-${message.id}`,
      message,
      kind: "guide" as const
    })),
    ...queuedMessages.map((message) => ({
      key: `queue-${message.id}`,
      message,
      kind: "queue" as const
    }))
  ].sort((left, right) => {
    const leftPosition = left.message.heldPosition;
    const rightPosition = right.message.heldPosition;
    if (leftPosition !== undefined && rightPosition !== undefined) {
      return leftPosition - rightPosition;
    }
    if (leftPosition !== undefined) return -1;
    if (rightPosition !== undefined) return 1;
    return 0;
  });
}

export function ComposerQueueStrip({
  guideMessages,
  dispatchDisabled = false,
  queuedMessages,
  expanded: controlledExpanded,
  onExpandedChange,
  onRemoveGuideMessage,
  onRemoveQueuedMessage,
  onGuideQueuedMessage,
  onEditGuideMessage,
  onEditQueuedMessage
}: {
  guideMessages: QueuedComposerMessage[];
  dispatchDisabled?: boolean;
  queuedMessages: QueuedComposerMessage[];
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
  onRemoveGuideMessage: (id: string) => void;
  onRemoveQueuedMessage: (id: string) => void;
  onGuideQueuedMessage: (id: string) => void;
  onEditGuideMessage: (id: string) => void;
  onEditQueuedMessage: (id: string) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const [internalExpanded, setInternalExpanded] = useState(false);
  const expanded = controlledExpanded ?? internalExpanded;
  const rows = buildQueueRows(guideMessages, queuedMessages);
  if (rows.length === 0) {
    return null;
  }

  const hasHeldMessages = rows.some((row) => row.message.held);
  const hasUnconfirmedMessages = rows.some((row) => row.message.deliveryUnconfirmed);
  const latestMessage = rows.at(-1)?.message;

  function setExpanded(next: boolean): void {
    if (controlledExpanded === undefined) {
      setInternalExpanded(next);
    }
    onExpandedChange?.(next);
  }

  return (
    <ComposerDrawer
      className={`composer-pending-drawer${hasHeldMessages ? " is-held" : ""}`}
      data-wuu-component="composer-pending"
      expanded={expanded}
      onExpandedChange={setExpanded}
      toggleLabel={t(expanded ? "composer.collapsePending" : "composer.expandPending")}
      tone={hasHeldMessages ? "warning" : "default"}
      icon={<ListTodo className="icon-sm" />}
      summary={
        <Tooltip
          content={hasUnconfirmedMessages ? t("composer.deliveryUnconfirmedDetails") : hasHeldMessages ? t("composer.heldDetails") : latestMessage ? queuedMessageFullPreview(latestMessage) : undefined}
          disabled={
            !hasUnconfirmedMessages && !hasHeldMessages && (expanded ||
            !latestMessage ||
            queuedMessageFullPreview(latestMessage) === queuedMessagePreview(latestMessage))
          }
        >
          <span
            className="composer-pending-preview"
            role="status"
            aria-live="polite"
            aria-description={hasUnconfirmedMessages ? t("composer.deliveryUnconfirmedDetails") : hasHeldMessages ? t("composer.heldDetails") : undefined}
          >
            {hasUnconfirmedMessages
              ? t("composer.deliveryUnconfirmed")
              : expanded
              ? hasHeldMessages
                ? t("composer.heldNotice")
                : t("composer.pendingMessages")
              : latestMessage
                ? <span key={latestMessage.id} className="composer-pending-arrival">{queuedMessagePreview(latestMessage)}</span>
                : ""}
          </span>
        </Tooltip>
      }
    >
      <div className="composer-pending-details">
        <ol className="composer-queue-list" aria-label={t("composer.pendingMessages")}>
          {rows.map((row, index) => (
            <ComposerQueueItem
              key={row.key}
              position={index + 1}
              message={row.message}
              kind={row.kind}
              dispatchDisabled={dispatchDisabled}
              onGuide={
                !row.message.operationState
                  ? () => onGuideQueuedMessage(row.message.id)
                  : undefined
              }
              onEdit={() =>
                row.kind === "queue"
                  ? onEditQueuedMessage(row.message.id)
                  : onEditGuideMessage(row.message.id)
              }
              onRemove={() =>
                row.kind === "queue"
                  ? onRemoveQueuedMessage(row.message.id)
                  : onRemoveGuideMessage(row.message.id)
              }
            />
          ))}
        </ol>
      </div>
    </ComposerDrawer>
  );
}

function ComposerQueueItem({
  position,
  message,
  kind,
  onGuide,
  dispatchDisabled,
  onEdit,
  onRemove
}: {
  position: number;
  message: QueuedComposerMessage;
  kind: QueueRowKind;
  onGuide?: () => void;
  dispatchDisabled: boolean;
  onEdit: () => void;
  onRemove: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const editContentLabel = kind === "guide"
    ? t("composer.editGuideContent", { position })
    : t("composer.editQueuedContent", { position });
  const editLabel = kind === "guide"
    ? t("composer.editGuide", { position })
    : t("composer.editQueuedMessage", { position });
  return (
    <li
      className={`composer-queue-row ${kind}`}
      data-position={position}
      aria-busy={Boolean(message.operationState)}
      aria-description={message.deliveryUnconfirmed ? t("composer.deliveryUnconfirmedDetails") : undefined}
    >
      <span className="composer-queue-index" aria-hidden="true">
        {position}
      </span>
      <Tooltip
        content={queuedMessageFullPreview(message)}
        disabled={queuedMessageFullPreview(message) === queuedMessagePreview(message)}
      >
        {message.deliveryUnconfirmed ? (
          <span className="composer-queue-preview" aria-label={queuedMessageFullPreview(message)}>
            {queuedMessagePreview(message)}
          </span>
        ) : (
          <button
            type="button"
            className="composer-queue-preview"
            aria-label={editContentLabel}
            onClick={onEdit}
            disabled={Boolean(message.operationState)}
          >
            {queuedMessagePreview(message)}
          </button>
        )}
      </Tooltip>
      <div className="composer-queue-actions composer-input-header-actions">
        {message.deliveryUnconfirmed ? (
          <MessageCopyButton
            className="composer-queue-action composer-input-header-action"
            getText={() => message.text || queuedMessageFullPreview(message)}
          />
        ) : (
          <button
            type="button"
            className="composer-queue-action composer-input-header-action edit"
            aria-label={editLabel}
            onClick={onEdit}
            disabled={Boolean(message.operationState)}
          >
            <PencilLine className="icon-sm" aria-hidden="true" />
          </button>
        )}
        {onGuide ? (
          <Tooltip asChild content={
              message.held
                ? t("composer.continueHeldTitle")
                : kind === "guide"
                  ? t("composer.convertToQueueTitle")
                  : t("composer.convertToGuideTitle")
            }><button
            type="button"
            className="composer-queue-action composer-input-header-action"
            aria-label={
              message.held
                ? t("composer.continueHeld", { position })
                : kind === "guide"
                  ? t("composer.convertToQueue", { position })
                  : t("composer.convertToGuide", { position })
            }
            onClick={onGuide}
            disabled={dispatchDisabled || message.deliveryUnconfirmed}
          >
            {kind === "guide" && !message.held ? (
              <CornerUpLeft className="icon-sm" aria-hidden="true" />
            ) : (
              <CornerDownRight className="icon-sm" aria-hidden="true" />
            )}
          </button></Tooltip>
        ) : null}
        <button
          type="button"
          className="composer-queue-action composer-input-header-action danger"
          aria-label={t("composer.removeQueuedMessage", { position })}
          onClick={onRemove}
          disabled={message.operationState === "switching" || message.deliveryUnconfirmed}
        >
          <X className="icon-sm" aria-hidden="true" />
        </button>
      </div>
    </li>
  );
}
