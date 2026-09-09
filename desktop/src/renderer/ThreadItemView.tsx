import {
  memo,
  type ChangeEvent as ReactChangeEvent,
  type ClipboardEvent as ReactClipboardEvent,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  useEffect,
  useLayoutEffect,
  useRef,
  useState
} from "react";
import { ChevronDown, ChevronUp, FileText, Info, Plus, Send } from "lucide-react";
import type { InputFile, InputImage, MessageContentPart, ThreadItem, Turn } from "../shared/protocol";
import { CollapsedComposerPromptCard, collapsedComposerPromptTitle } from "./ComposerCollapsedPrompt";
import {
  clipboardAttachmentFiles,
  composerFileFromFile,
  composerImageFromFile,
  isSupportedComposerAttachment
} from "./ComposerMessages";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import {
  isInternalUserNotificationItem,
  isProcessNotificationItem,
} from "./InternalUserNotification";
import {
  collapsedLongTextPreview,
  useLongTextCollapse,
} from "./LongTextCollapse";
import { RichContent } from "./RichContent";
import {
  AgentMessageActions,
  MessageCopyButton,
  MessageEditButton,
  MessageFileList,
  MessageImageGrid,
} from "./MessageActions";
import { StreamingMarkdown } from "./StreamingMarkdown";
import { streamTextKey, streamTextStore } from "./StreamText";
import { streamFieldValue } from "./ThreadItemText";
import { ToolActivityRow } from "./ToolActivity";
import { RemoteItemContent } from "./RemoteItemContent";
import {
  ContextCompactionNotice,
  StreamReconnectNotice,
  TurnNotice,
} from "./TurnNotice";
import { userMessageAnchorID } from "./TurnViewHelpers";
import { requestOpenThreadInSplit } from "./ConversationSplitBridge";
import {
  userFacingErrorForMessage,
} from "./UserFacingErrors";
import { useI18n } from "./i18n";
import { ConversationItemPresentation } from "./plugins/ConversationItemPresentation";
import {
  ConversationMessageSurface,
  type ConversationMessageSurfaceContext,
} from "./plugins/ConversationMessageSurface";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import { PluginSlot } from "./plugins/PluginSlot";

interface ThreadItemViewProps {
  turnID: string;
  turnStatus: Turn["status"];
  turnStartedAt?: string | null;
  item: ThreadItem;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  streaming: boolean;
  pendingCompanionReasoning?: boolean;
  actionableAgentMessageID?: string;
  latestAgentMessageID?: string;
  /**
   * The owning turn was submitted live in this mounted conversation. This
   * lets an already-completed fast answer still play its one-shot action
   * entrance without replaying that entrance for historical messages.
   */
  animateCompletionActions?: boolean;
  onStreamFrame: () => void;
  onForkMessage?: (turnID: string, itemID: string) => void;
  onEditMessage?: (turnID: string, item: ThreadItem) => void;
  editing?: boolean;
  editSubmitting?: boolean;
  onCancelEditMessage?: () => void;
  onSubmitEditMessage?: (
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void;
  onOpenAgent?: (agentID: string) => void;
  editSummaryCard?: JSX.Element;
  pluginHost?: PluginHost;
}

export const ThreadItemView = memo(function ThreadItemView(props: ThreadItemViewProps): JSX.Element | null {
  const { item, onEditMessage, turnID, editing } = props;
  const { t } = useI18n();
  if (item.remote_content_ref && item.type !== "tool_call") {
    return <RemoteItemContent key={item.remote_content_ref} item={item} render={complete => <ThreadItemView {...props} item={complete} />} />;
  }
  if (item.type === "user_message" && isInternalUserNotificationItem(item)) {
    return null;
  }
  const pluginSlotContext = Object.freeze({
    kind: item.type,
    turnStatus: props.turnStatus,
    streaming: props.streaming,
    editing: Boolean(editing),
  });
  if (item.type === "tool_call") {
    return (
      <PluginMessageSlots host={props.pluginHost} context={pluginSlotContext}>
        <BuiltInThreadItemView {...props} />
      </PluginMessageSlots>
    );
  }
  const text = item.type === "error" ? undefined : item.text ?? item.reason;
  const editable = item.type === "user_message"
    && !editing
    && !item.read_only
    && onEditMessage !== undefined
    && ((item.text?.trim().length ?? 0) > 0 || (item.images?.length ?? 0) > 0 || (item.files?.length ?? 0) > 0);
  const fallback = (
    <ConversationMessageSurface
      context={conversationMessageSurfaceContext(props, editable)}
      fallback={<BuiltInThreadItemView {...props} />}
    />
  );
  return (
    <PluginMessageSlots host={props.pluginHost} context={pluginSlotContext}>
      <ConversationItemPresentation
        item={item}
        text={text}
        fallback={fallback}
        onEdit={editable ? () => onEditMessage(turnID, item) : undefined}
      />
    </PluginMessageSlots>
  );
});

function conversationMessageSurfaceContext(
  { item, turnID, streaming, onEditMessage, onForkMessage }: ThreadItemViewProps,
  editable: boolean,
): ConversationMessageSurfaceContext {
  const kind = item.type === "user_message"
    ? "user-message"
    : item.type === "agent_message"
      ? "assistant-message"
      : item.type === "reasoning"
        ? "reasoning"
        : "notice";
  const status = item.status === "in_progress" ? "streaming" : item.status;
  const threadId = desktopPluginHost.getActiveConversationThreadId();
  return Object.freeze({
    version: 1,
    messageId: item.id,
    turnId: turnID,
    ...(threadId === undefined ? {} : { threadId }),
    kind,
    status,
    phase:
      item.type === "agent_message"
        ? item.terminal
          ? "final_answer"
          : "commentary"
        : undefined,
    streaming,
    attachmentCount: (item.images?.length ?? 0) + (item.files?.length ?? 0),
    actions: Object.freeze({
      edit: editable && onEditMessage ? () => onEditMessage(turnID, item) : undefined,
      fork: onForkMessage ? () => onForkMessage(turnID, item.id) : undefined,
    }),
  });
}

function PluginMessageSlots({
  host = desktopPluginHost,
  context,
  children,
}: {
  host?: PluginHost;
  context: Readonly<Record<string, unknown>>;
  children: JSX.Element;
}): JSX.Element {
  return (
    <>
      <PluginSlot host={host} id="conversation.message.before" context={context} />
      {children}
      <PluginSlot host={host} id="conversation.message.after" context={context} />
    </>
  );
}

function BuiltInThreadItemView({
  turnID,
  turnStatus,
  turnStartedAt,
  item,
  cwd,
  onOpenFile,
  streaming,
  pendingCompanionReasoning,
  actionableAgentMessageID,
  latestAgentMessageID,
  animateCompletionActions,
  onStreamFrame,
  onForkMessage,
  onEditMessage,
  editing,
  editSubmitting,
  onCancelEditMessage,
  onSubmitEditMessage,
  onOpenAgent,
  editSummaryCard,
}: ThreadItemViewProps): JSX.Element | null {
  const { t, formatDate } = useI18n();
  // Only a live item/turn completion handoff should animate. Historical
  // completed messages mount without this marker, so virtualized content does
  // not replay the entrance while the user scrolls.
  const [settleEntered, setSettleEntered] = useState(
    () =>
      Boolean(animateCompletionActions) &&
      item.type === "agent_message" &&
      item.status === "completed" &&
      item.terminal === true,
  );
  const previousTurnStatusRef = useRef(turnStatus);
  const previousItemStatusRef = useRef(item.status);
  useLayoutEffect(() => {
    const previousTurnStatus = previousTurnStatusRef.current;
    const previousItemStatus = previousItemStatusRef.current;
    previousTurnStatusRef.current = turnStatus;
    previousItemStatusRef.current = item.status;
    if (
      (previousTurnStatus === "in_progress" && turnStatus === "completed") ||
      (previousItemStatus === "in_progress" && item.status === "completed")
    ) {
      setSettleEntered(true);
    }
  }, [item.status, turnStatus]);
  switch (item.type) {
    case "user_message": {
      const text = item.text ?? "";
      if (isProcessNotificationItem(item)) {
        return null;
      }
      const displayText = text;
      if (isInternalUserNotificationItem(item)) {
        return null;
      }
      const copyable = displayText.trim() !== "";
      const editable = Boolean(
        !item.read_only &&
          onEditMessage &&
          (copyable || (item.images?.length ?? 0) > 0 || (item.files?.length ?? 0) > 0),
      );
      const editActionVisible = editable;
      // Some plugin messages point to a durable related session. Keep that
      // navigation on the message itself rather than coupling it to a
      // separate inspector plugin.
      const deliveryText = item.input_text?.trim() ?? "";
      const relatedSessionID = item.related_session_id?.trim() || undefined;
      // input_text equals the bubble for ordinary messages (or would, if a
      // stale server projection ever leaks it); only hidden messages with a
      // related session get a navigation action.
      const relatedSessionAvailable = deliveryText !== ""
        && deliveryText !== displayText.trim()
        && relatedSessionID !== undefined;
      const openRelatedSession = (): void => {
        if (relatedSessionID !== undefined && relatedSessionAvailable) {
          requestOpenThreadInSplit(relatedSessionID);
        }
      };
      return (
        <div
          className={`user-message-block${copyable || editActionVisible ? " user-message-block-with-actions" : ""}`}
          data-wuu-component="message"
          data-wuu-variant="user"
          id={userMessageAnchorID(turnID, item.id)}
          data-user-message-id={item.id}
          data-turn-id={turnID}
        >
          {editing ? (
            <UserMessageInlineEditor
              item={item}
              initialText={text}
              submitting={Boolean(editSubmitting)}
              onCancel={onCancelEditMessage}
              onSubmit={(nextText, nextImages, nextFiles, contentParts) =>
                onSubmitEditMessage?.(turnID, item, nextText, nextImages, nextFiles, contentParts)
              }
            />
          ) : (
            <UserMessageContent
              text={displayText}
              contentParts={item.content_parts}
              images={item.images ?? []}
              files={item.files ?? []}
              cwd={cwd}
              onOpenFile={onOpenFile}
            />
          )}
          {!editing && (copyable || editActionVisible || relatedSessionAvailable) ? (
            <div
              className="message-actions user-message-actions"
              data-wuu-component="message-actions"
              data-wuu-placement="overlay"
              aria-label={t("message.userActions")}
            >
              {turnStartedAt ? (
                <time className="user-message-time" dateTime={turnStartedAt}>
                  {formatDate(turnStartedAt, {
                    hour: "2-digit",
                    minute: "2-digit",
                    hourCycle: "h23",
                  })}
                </time>
              ) : null}
              {copyable ? (
                <MessageCopyButton
                  getText={() => displayText}
                  className="message-action-button"
                  iconSize={15}
                />
              ) : null}
              {relatedSessionAvailable ? (
                <button
                  type="button"
                  className="message-action-button"
                  aria-label={t("message.openRelatedSession")}
                  title={t("message.openRelatedSession")}
                  onClick={openRelatedSession}
                >
                  <Info size={15} />
                </button>
              ) : null}
              {editActionVisible && onEditMessage ? (
                <MessageEditButton
                  onEdit={() => onEditMessage(turnID, item)}
                  className="message-action-button"
                  iconSize={15}
                />
              ) : null}
            </div>
          ) : null}
        </div>
      );
    }
    case "agent_message": {
      const streamKeyValue = streamTextKey(turnID, item.id, "text");
      const agentText = streamTextStore.has(streamKeyValue)
        ? streamTextStore.get(streamKeyValue)
        : (item.text ?? "");
      const copyable = agentText.trim() !== "";
      const isProcessText = !item.terminal;
      const finalItemCompletedBeforeTurn =
        turnStatus === "in_progress" &&
        item.status === "completed" &&
        item.id === latestAgentMessageID &&
        copyable &&
        !isProcessText;
      const forkVisible =
        finalItemCompletedBeforeTurn ||
        (turnStatus === "completed" &&
          item.id === actionableAgentMessageID &&
          copyable &&
          !isProcessText);
      // A completed final item is the user-visible completion boundary. The
      // backend can materialize a fork from the live turn snapshot while
      // provider cleanup and durable turn settlement continue independently.
      const actionsVisible = forkVisible;
      const actionsPersistent =
        actionsVisible &&
        (item.id === latestAgentMessageID || finalItemCompletedBeforeTurn);
      // Copy/fork always paint into the existing turn-boundary band.
      // Latest answers stay visible; older ones appear on hover. Neither
      // reserves in-flow height, so completion cannot shift auto-follow.
      return (
        <article
          data-wuu-component="message"
          data-wuu-variant="agent"
          className={`agent-block${
            actionsVisible
              ? ` agent-block-with-action-slot agent-actions-available${settleEntered ? " agent-actions-enter" : ""}${actionsPersistent ? " agent-actions-persistent" : " agent-actions-overlay"}`
              : ""
          }`}
        >
          <div className="agent-text">
            <AgentMessageContent
              turnID={turnID}
              item={item}
              cwd={cwd}
              onOpenFile={onOpenFile}
              pendingCompanionReasoning={pendingCompanionReasoning}
              onStreamFrame={onStreamFrame}
            />
          </div>
          {editSummaryCard}
          {actionsVisible ? (
            <AgentMessageActions
              getText={() => streamFieldValue(turnID, item, "text")}
              placement={actionsPersistent ? "persistent" : "overlay"}
              showFork
              onFork={
                forkVisible && onForkMessage
                  ? () => onForkMessage(turnID, item.id)
                  : undefined
              }
            />
          ) : null}
        </article>
      );
    }
    case "reasoning":
      return (
        <article className="reasoning-block">
          <ReasoningContent
            turnID={turnID}
            item={item}
            cwd={cwd}
            onOpenFile={onOpenFile}
            onStreamFrame={onStreamFrame}
          />
        </article>
      );
    case "tool_call":
      return <ToolActivityRow items={[item]} />;
    case "context_compaction":
      return (
        <ContextCompactionNotice
          text={item.text}
          reason={item.reason}
          status={item.status}
          summary={item.summary}
        />
      );
    case "stream_reconnect":
      return <StreamReconnectNotice item={item} />;
    case "error":
      return (
        <TurnNotice display={userFacingErrorForMessage(item.error, "turn")} />
      );
    default:
      return null;
  }
}

function UserMessageContent({
  text,
  contentParts,
  images,
  files,
  cwd,
  onOpenFile,
}: {
  text: string;
  contentParts?: MessageContentPart[];
  images: InputImage[];
  files: InputFile[];
  cwd?: string;
  onOpenFile?: (path: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const structured = Boolean(contentParts?.length);
  const pastedParts = (contentParts ?? []).filter(
    (part): part is Extract<MessageContentPart, { type: "pasted_text" }> =>
      part.type === "pasted_text",
  );
  const textParts = (contentParts ?? []).filter(
    (part): part is Extract<MessageContentPart, { type: "text" }> => part.type === "text",
  );
  const hasAttachments = images.length > 0 || files.length > 0 || pastedParts.length > 0;
  const hasTextBubble = structured
    ? textParts.some((part) => part.text.length > 0)
    : text.length > 0;
  // Threshold + preview logic + state keying all live in
  // `./LongTextCollapse` so the chat bubble can reuse the exact same
  // numbers. The hook's `{text, expanded}` state shape is what makes
  // the toggle survive a parent re-render with a new message body
  // without flashing the previous expansion — see the module doc.
  const { collapsible, expanded, toggleExpanded } = useLongTextCollapse(structured ? "" : text);
  const collapsed = collapsible && !expanded;
  const displayedText = collapsed ? collapsedLongTextPreview(text) : text;

  return (
    <>
      {hasAttachments ? (
        <div className="user-message-attachments" data-wuu-component="message-attachments">
          {images.length ? <MessageImageGrid images={images} collapsedLimit={4} /> : null}
          {files.length ? <MessageFileList files={files} collapsedLimit={3} /> : null}
          {pastedParts.map((part, index) => (
            <MessagePastedTextPart key={`${part.type}-${index}`} part={part} />
          ))}
        </div>
      ) : null}
      {hasTextBubble ? (
        <div
          className={`message user-message${
            collapsible
              ? ` user-message-long-card ${expanded ? "expanded" : "collapsed"}`
              : ""
          }`}
          data-wuu-component="message-bubble"
          data-wuu-variant="user"
        >
          {structured ? (
            <div className="user-message-content-parts">
              {textParts.map((part, index) =>
                part.text ? (
                  <div className="user-message-text-part" key={`${part.type}-${index}`}>
                    <RichContent text={part.text} cwd={cwd} onOpenFile={onOpenFile} />
                  </div>
                ) : null,
              )}
            </div>
          ) : collapsible ? (
            <div className="user-message-raw-query">{displayedText}</div>
          ) : (
            <RichContent text={text} cwd={cwd} onOpenFile={onOpenFile} />
          )}
          {collapsible ? (
            <button
              type="button"
              className="user-message-expand-toggle"
              aria-expanded={expanded}
              onClick={toggleExpanded}
            >
              <span>{expanded ? t("common.collapse") : t("common.showMore")}</span>
              {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
            </button>
          ) : null}
        </div>
      ) : null}
    </>
  );
}

function MessagePastedTextPart({
  part,
}: {
  part: Extract<MessageContentPart, { type: "pasted_text" }>;
}): JSX.Element {
  const { locale, t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const title = part.title || collapsedComposerPromptTitle(part.text);
  const characterCount = part.text.length.toLocaleString(locale);
  return (
    <section className={`user-message-pasted-text${expanded ? " expanded" : ""}`}>
      <button
        type="button"
        className="user-message-pasted-text-toggle"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        <span className="user-message-pasted-text-icon" aria-hidden="true">
          <FileText />
        </span>
        <span className="user-message-pasted-text-labels">
          <strong>{title}</strong>
          <span>{t("message.pastedTextMeta", { count: characterCount })}</span>
        </span>
        {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
      </button>
      {expanded ? <pre className="user-message-pasted-text-content">{part.text}</pre> : null}
    </section>
  );
}

// Both helpers (collapsedUserMessagePreview + isCollapsibleUserMessage) and
// the four COLLAPSIBLE_USER_MESSAGE_* constants moved to `./LongTextCollapse`
// so the chat bubble can reuse the same thresholds and preview estimator.
function UserMessageInlineEditor({
  item,
  initialText,
  submitting,
  onCancel,
  onSubmit,
}: {
  item: ThreadItem;
  initialText: string;
  submitting: boolean;
  onCancel?: () => void;
  onSubmit?: (
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
  ) => void;
}): JSX.Element {
  const { t } = useI18n();
  const initialPastedParts = (item.content_parts ?? []).filter(
    (part): part is Extract<MessageContentPart, { type: "pasted_text" }> =>
      part.type === "pasted_text",
  );
  const initialTextParts = (item.content_parts ?? []).filter(
    (part): part is Extract<MessageContentPart, { type: "text" }> => part.type === "text",
  );
  const [text, setText] = useState(
    item.content_parts?.length ? initialTextParts.map((part) => part.text).join("") : initialText,
  );
  const [pastedParts, setPastedParts] = useState(initialPastedParts);
  const [images, setImages] = useState<InputImage[]>(item.images ?? []);
  const [files, setFiles] = useState<InputFile[]>(item.files ?? []);
  const [dragOver, setDragOver] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const hasAttachments = images.length > 0 || files.length > 0 || pastedParts.length > 0;
  const canSubmit = text.trim().length > 0 || hasAttachments;

  // Re-seed local state when the editor is reopened on a different user
  // message, or when the upstream item swaps its attachment arrays (e.g.
  // after a stream update). Without this, editing message B and then
  // cancelling back to message A would show B's draft in A.
  useEffect(() => {
    const nextParts = item.content_parts ?? [];
    const nextPastedParts = nextParts.filter(
      (part): part is Extract<MessageContentPart, { type: "pasted_text" }> =>
        part.type === "pasted_text",
    );
    const nextTextParts = nextParts.filter(
      (part): part is Extract<MessageContentPart, { type: "text" }> => part.type === "text",
    );
    setText(nextParts.length ? nextTextParts.map((part) => part.text).join("") : initialText);
    setPastedParts(nextPastedParts);
    setImages(item.images ?? []);
    setFiles(item.files ?? []);
  }, [initialText, item.id, item.images, item.files, item.content_parts]);

  useEffect(() => {
    window.requestAnimationFrame(() => {
      const textarea = textareaRef.current;
      if (!textarea) {
        return;
      }
      // preventScroll: the conversation pane owns its scroll position, so
      // the browser's default focus-scroll must not yank the viewport to
      // bring the textarea into view — that scroll would disarm auto-follow
      // and surface the "跳到最新" pill on what is otherwise a deliberate
      // edit action. We scroll the editor into view ourselves on edit start
      // (see startEditingThreadMessageFromHistory) so this stays consistent
      // with the rest of the conversation scroll contract.
      textarea.focus({ preventScroll: true });
      textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    });
  }, []);

  function submit(): void {
    if (!canSubmit || submitting) {
      return;
    }
    const contentParts: MessageContentPart[] = [
      ...pastedParts,
      ...(text.length > 0 ? [{ type: "text" as const, text }] : []),
    ];
    const fullText = contentParts.length
      ? contentParts.map((part) => part.text).join("")
      : text;
    onSubmit?.(fullText, images, files, contentParts.length ? contentParts : undefined);
  }

  function revealPastedPart(index: number): void {
    const part = pastedParts[index];
    if (!part) return;
    setPastedParts((current) => current.filter((_, currentIndex) => currentIndex !== index));
    setText((current) => `${current}${part.text}`);
  }

  function removePastedPart(index: number): void {
    setPastedParts((current) => current.filter((_, currentIndex) => currentIndex !== index));
  }

  async function addAttachmentFiles(filesToAdd: File[]): Promise<void> {
    const supported = filesToAdd.filter(isSupportedComposerAttachment);
    if (supported.length === 0) {
      return;
    }
    const imageAdditions: InputImage[] = [];
    const fileAdditions: InputFile[] = [];
    for (const file of supported) {
      if (file.type.toLowerCase().startsWith("image/")) {
        try {
          const composed = await composerImageFromFile(file);
          imageAdditions.push({ media_type: composed.media_type, data: composed.data });
        } catch {
          // Skip the individual failed image; the rest still land.
        }
      } else {
        try {
          const composed = await composerFileFromFile(file);
          fileAdditions.push({
            media_type: composed.media_type,
            data: composed.data,
            filename: composed.filename
          });
        } catch {
          // Same per-file resilience — bad PDFs shouldn't kill the batch.
        }
      }
    }
    if (imageAdditions.length > 0) {
      setImages((prev) => [...prev, ...imageAdditions]);
    }
    if (fileAdditions.length > 0) {
      setFiles((prev) => [...prev, ...fileAdditions]);
    }
  }

  function handleKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      onCancel?.();
      return;
    }
    if (isComposerTextComposing(event)) {
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submit();
    }
  }

  function handlePaste(event: ReactClipboardEvent<HTMLTextAreaElement>): void {
    if (submitting) {
      return;
    }
    const pasted = clipboardAttachmentFiles(event);
    if (pasted.length === 0) {
      return;
    }
    event.preventDefault();
    void addAttachmentFiles(pasted);
  }

  function handleFileInputChange(event: ReactChangeEvent<HTMLInputElement>): void {
    const selected = Array.from(event.currentTarget.files ?? []);
    event.currentTarget.value = "";
    if (selected.length > 0) {
      void addAttachmentFiles(selected);
    }
  }

  function handleDragOver(event: ReactDragEvent<HTMLDivElement>): void {
    if (submitting) {
      return;
    }
    if (!event.dataTransfer.types.includes("Files")) {
      return;
    }
    event.preventDefault();
    setDragOver(true);
  }

  function handleDragLeave(event: ReactDragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setDragOver(false);
  }

  function handleDrop(event: ReactDragEvent<HTMLDivElement>): void {
    if (submitting) {
      return;
    }
    const dropped = Array.from(event.dataTransfer?.files ?? []);
    if (dropped.length === 0) {
      return;
    }
    event.preventDefault();
    setDragOver(false);
    void addAttachmentFiles(dropped);
  }

  function removeImage(index: number): void {
    setImages((prev) => prev.filter((_, currentIndex) => currentIndex !== index));
  }

  function removeFile(index: number): void {
    setFiles((prev) => prev.filter((_, currentIndex) => currentIndex !== index));
  }

  return (
    <div
      className={`user-message-edit${dragOver ? " user-message-edit-drop-active" : ""}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <input
        ref={fileInputRef}
        className="user-message-edit-file-input"
        type="file"
        accept="image/*,application/pdf"
        multiple
        tabIndex={-1}
        onChange={handleFileInputChange}
      />
      {images.length > 0 ? (
        <MessageImageGrid images={images} onRemove={removeImage} />
      ) : null}
      {files.length > 0 ? (
        <MessageFileList files={files} onRemove={removeFile} />
      ) : null}
      {pastedParts.length > 0 ? (
        <div className="user-message-edit-pasted-texts">
          {pastedParts.map((part, index) => (
            <CollapsedComposerPromptCard
              key={`${part.type}-${index}`}
              text={part.text}
              onReveal={() => revealPastedPart(index)}
              onRemove={() => removePastedPart(index)}
            />
          ))}
        </div>
      ) : null}
      <textarea
        ref={textareaRef}
        className="user-message-edit-input"
        value={text}
        disabled={submitting}
        onChange={(event) => setText(event.target.value)}
        onKeyDown={handleKeyDown}
        onPaste={handlePaste}
        rows={Math.max(1, Math.min(8, text.split("\n").length))}
      />
      <div className="user-message-edit-toolbar">
        <button
          type="button"
          className="composer-tool-button user-message-edit-attach-button"
          aria-label={t("composer.addAttachment")}
          title={t("message.addImageOrPdf")}
          disabled={submitting}
          onClick={() => fileInputRef.current?.click()}
        >
          <Plus aria-hidden="true" />
        </button>
        <div className="user-message-edit-spacer" />
        <div className="user-message-edit-actions">
          <button
            className="user-message-edit-button secondary"
            type="button"
            disabled={submitting}
            onClick={onCancel}
          >
            {t("common.cancel")}
          </button>
          <button
            className="composer-action-button composer-send-button"
            type="button"
            aria-label={t("composer.send")}
            title={t("composer.send")}
            disabled={!canSubmit || submitting}
            onClick={submit}
          >
            <Send aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}

function AgentMessageContent({
  turnID,
  item,
  cwd,
  onOpenFile,
  pendingCompanionReasoning,
  onStreamFrame,
}: {
  turnID: string;
  item: ThreadItem;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  /**
   * True when the turn has a reasoning block that the model just finished
   * writing. The first answer item waits a short beat so the reasoning
   * cursor can fully settle before the text cursor starts animating.
   */
  pendingCompanionReasoning?: boolean;
  onStreamFrame: () => void;
}): JSX.Element {
  const streamKeyValue = streamTextKey(turnID, item.id, "text");
  // isLive is driven entirely by `item.status`: once the back-end marks
  // the item completed the surface must settle, no matter what the
  // streaming buffer looks like. This is what makes "two places
  // streaming at once" impossible — there's exactly one source of
  // liveness and it changes atomically when the back-end commits.
  const isLive = item.status === "in_progress";
  // Hold the cursor back when a just-completed reasoning block is still
  // visually settling. The reasoning and text streams are sequential on
  // the wire, but the cursor reveal and the next text's reveal can briefly
  // race in the UI.
  const [cursorArmed, setCursorArmed] = useState<boolean>(
    !pendingCompanionReasoning,
  );
  useEffect(() => {
    if (!pendingCompanionReasoning) {
      setCursorArmed(true);
      return;
    }
    // 240ms is enough to let the reasoning cursor finish its tail reveal
    // (it's bound by max cps but typically clears in ~150ms for short
    // reasoning). Tuned by hand; bump up if you can still see overlap.
    const timer = window.setTimeout(() => {
      setCursorArmed(true);
    }, 240);
    return () => {
      window.clearTimeout(timer);
    };
  }, [pendingCompanionReasoning]);

  const hasBufferedStream = streamTextStore.has(streamKeyValue);
  const canReleaseBufferedStream = !isLive && typeof item.text === "string" && item.text.length > 0;

  return (
    <StreamingMarkdown
      streamKey={streamKeyValue}
      initialText={
        isLive && hasBufferedStream
          ? streamTextStore.seedValue(streamKeyValue)
          : item.text
      }
      cwd={cwd}
      onOpenFile={onOpenFile}
      isLive={isLive && cursorArmed}
      phase={
        item.terminal || item.status === "in_progress"
          ? "final_answer"
          : "commentary"
      }
      onFrame={onStreamFrame}
      onSettled={
        canReleaseBufferedStream
          ? () => streamTextStore.clearItem(turnID, item.id)
          : undefined
      }
    />
  );
}

function ReasoningContent({
  turnID,
  item,
  cwd,
  onOpenFile,
  onStreamFrame,
}: {
  turnID: string;
  item: ThreadItem;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onStreamFrame: () => void;
}): JSX.Element {
  const streamKeyValue = streamTextKey(turnID, item.id, "text");
  const isLive = item.status === "in_progress";

  const hasBufferedStream = streamTextStore.has(streamKeyValue);
  const canReleaseBufferedStream = !isLive && typeof item.text === "string" && item.text.length > 0;

  return (
    <StreamingMarkdown
      streamKey={streamKeyValue}
      initialText={
        isLive && hasBufferedStream
          ? streamTextStore.seedValue(streamKeyValue)
          : item.text
      }
      className="streaming-markdown rich-content reasoning-stream"
      cwd={cwd}
      onOpenFile={onOpenFile}
      isLive={isLive}
      phase="commentary"
      onFrame={onStreamFrame}
      onSettled={
        canReleaseBufferedStream
          ? () => streamTextStore.clearItem(turnID, item.id)
          : undefined
      }
    />
  );
}
