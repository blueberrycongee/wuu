import { ConversationAttachmentPreviewContext } from "../ConversationAttachmentPreview";
import * as React from "react";
import { Component, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { PluginUIConversationAttachmentProps, PluginUIConversationComposerProps, PluginUIConversationMessageProps, PluginUIConversationViewProps } from "../../shared/workbench";
import { SplitPaneComposer } from "../ComposerInputSections";
import { FileSelectionProvider } from "../FileSelectionContext";
import { ArtifactThreadContext } from "../ArtifactPreviewContext";
import { SideConversationAvailableContext } from "../ResponseSelection";
import { ThreadItemView } from "../ThreadItemView";
import { AssistantTurnShell } from "../AssistantTurnShell";
import { buildAssistantTurnDisplay } from "../AssistantTurnDisplay";
import { ConversationFlow } from "../ConversationFlow";
import { useAutoFollowScrollContainer } from "../AutoFollowScroll";
import { captureReadingAnchor, readingAnchorScrollTop, type ConversationReadingAnchor } from "../ConversationReadingAnchor";
import { JumpToLatestPill } from "../JumpToLatestPill";
import { Modal } from "../Modal";
import { CornerUpLeft, Download, FileText, LoaderCircle, Search } from "../WuuIcons";
import { formatByteSize } from "../AttachmentFormat";
import { useI18n } from "../i18n";
import { ConversationBubbleContext, useConversationBubbleGesture } from "./ConversationBubbles";
import { PluginHost } from "./PluginHost";
import { UserQuestionCard } from "../UserQuestionCard";
import type { RuntimeContext, ThreadItem, Turn, UserQuestionRequest } from "../../shared/protocol";

// Reuse presentation without handing another transport's messages or drafts
// to Coding presenters, completion providers, history loaders or workspace actions.
const conversationHost = new PluginHost({ react: React });
const noop = () => {};

type ScrollController = ReturnType<typeof useAutoFollowScrollContainer>;
type ReadingPosition = { top: number; following: boolean; anchor?: ConversationReadingAnchor };
type ReadingBoundaryProps = { conversationId: string; loading: boolean; controller: ScrollController; children: React.ReactNode };

// Capture before React changes the message tree, including on agent switches and
// prepended history. The shared follow controller handles resize and user intent.
const readingPositions = new Map<string, ReadingPosition>();
class ConversationReadingBoundary extends Component<ReadingBoundaryProps> {
  private positions = readingPositions;
  componentWillUnmount(): void {
    const { controller, conversationId, loading } = this.props;
    const node = controller.scrollRef.current;
    if (node && !loading) this.positions.set(conversationId, { top: node.scrollTop, following: controller.autoFollowRef.current, anchor: captureReadingAnchor(node) });
  }
  getSnapshotBeforeUpdate(previous: ReadingBoundaryProps): ReadingPosition | null {
    const node = previous.controller.scrollRef.current;
    if (!node || previous.loading) return null;
    const position = { top: node.scrollTop, following: previous.controller.autoFollowRef.current, anchor: captureReadingAnchor(node) };
    this.positions.set(previous.conversationId, position);
    return position;
  }
  componentDidMount(): void { this.restore(); }
  componentDidUpdate(previous: ReadingBoundaryProps, _state: unknown, snapshot: ReadingPosition | null): void {
    this.restore(previous.conversationId === this.props.conversationId && !previous.loading ? snapshot : undefined);
  }
  private restore(snapshot?: ReadingPosition | null): void {
    const { controller, conversationId, loading } = this.props;
    const node = controller.scrollRef.current;
    if (!node || loading) return;
    const position = snapshot ?? this.positions.get(conversationId);
    const top = position?.anchor ? readingAnchorScrollTop(node, position.anchor) ?? position.top : position?.top ?? 0;
    controller.restoreScrollPosition(top, position?.following ?? true);
  }
  render(): React.ReactNode { return this.props.children; }
}

export function PluginConversationView({ presentation = "standard", conversationId, sessionId, sessionScope, onQuestionReply, actions, title, subtitle, avatar, status, loading = false, searchLabel, searchContent, showHistorySearch = true, showJumpToLatest = true, children, composer }: PluginUIConversationViewProps): JSX.Element {
  const { t } = useI18n();
  const controller = useAutoFollowScrollContainer({ observeKey: conversationId });
  const rootRef = useRef<HTMLElement>(null);
  const bubbles = presentation === "bubbles";
  const gesture = useConversationBubbleGesture(bubbles, conversationId, controller.scrollRef);
  const [composerNode, setComposerNode] = useState<HTMLDivElement | null>(null);
  const [searchFor, setSearchFor] = useState<string>();
  const searchOpen = showHistorySearch && searchFor === conversationId;
  const closeSearch = () => setSearchFor(undefined);
  useLayoutEffect(() => setSearchFor(undefined), [conversationId]);
  useLayoutEffect(() => {
    if (!composerNode || !rootRef.current) return;
    const root = rootRef.current;
    const measure = () => root.style.setProperty("--plugin-composer-height", `${composerNode.getBoundingClientRect().height}px`);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(composerNode);
    return () => observer.disconnect();
  }, [composerNode]);
  return <section ref={rootRef} className="plugin-conversation" data-conversation-id={conversationId} data-presentation={presentation}>
    <header className="titlebar plugin-conversation-titlebar" data-wuu-component="conversation-titlebar">
      <div className="title-block"><div className="conversation-title-heading">{avatar}<h1 tabIndex={-1}>{title}</h1>{subtitle && <span className="plugin-conversation-subtitle">{subtitle}</span>}</div></div>
      <div className="title-actions">{status}{showHistorySearch && <button type="button" className="icon-button" data-action="search-history" aria-label={searchLabel} title={searchLabel} aria-expanded={searchOpen} onClick={event => { event.currentTarget.focus({ preventScroll: true }); setSearchFor(conversationId); }}><Search /></button>}{actions}</div>
    </header>
    <ConversationReadingBoundary conversationId={conversationId} loading={loading} controller={controller}>
      <div className="scroll-region plugin-conversation-scroll" ref={controller.scrollRef} onScroll={controller.handleScrollFrame} data-scroll-fade=""
        data-times-revealed={bubbles ? gesture.revealed : undefined}
        data-times-dragging={bubbles ? gesture.dragging : undefined}
        style={bubbles ? { "--bubble-time-offset": `${gesture.offset}px` } as React.CSSProperties : undefined}>
        {bubbles && <button type="button" className="bubble-time-toggle" aria-pressed={gesture.revealed} aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight" onClick={gesture.toggle}>{t(gesture.revealed ? "message.hideTimes" : "message.showTimes")}</button>}
        <ConversationBubbleContext.Provider value={{ enabled: bubbles, revealed: gesture.revealed }}>
        <ConversationFlow className="plugin-conversation-flow" role="log" aria-live="polite" aria-busy={loading}>{children}{sessionId && sessionScope && <PluginConversationQuestions sessionId={sessionId} scope={sessionScope} onReply={onQuestionReply} />}</ConversationFlow>
        </ConversationBubbleContext.Provider>
      </div>
    </ConversationReadingBoundary>
    <div className="plugin-conversation-composer" ref={setComposerNode}>{composer}</div>
    {showJumpToLatest && <JumpToLatestPill containerRef={controller.scrollRef} bottomAnchor={composerNode} scopeKey={conversationId} onJump={() => controller.scrollToBottom({ force: true, animate: true })} />}
    {searchOpen && <Modal ariaLabel={searchLabel} title={searchLabel} onClose={closeSearch} panelClassName="plugin-conversation-search" footer={<button type="button" className="settings-button settings-button-ghost" data-action="close-history-search" onClick={closeSearch}>{t("common.close")}</button>}>{searchContent}</Modal>}
  </section>;
}

function PluginConversationQuestions({ sessionId, scope, onReply }: { sessionId: string; scope: NonNullable<PluginUIConversationViewProps["sessionScope"]>; onReply?: PluginUIConversationViewProps["onQuestionReply"] }): JSX.Element | null {
  const [questions, setQuestions] = useState<UserQuestionRequest[]>([]);
  const [error, setError] = useState("");
  const context = useMemo<RuntimeContext>(() => ({ kind: "user", user_space_id: scope.userSpaceId, product_domain: scope.productDomain, cwd: "" }), [scope.userSpaceId, scope.productDomain]);
  const resolved = useRef(new Set<string>());
  useEffect(() => {
    let current = true, readVersion = 0;
    setQuestions([]); setError(""); resolved.current.clear();
    const read = async () => {
      if (typeof window.wuu?.listUserQuestions !== "function") return;
      const version = ++readVersion;
      try {
        // The host validates the user/domain route. Its question list includes
        // hidden task sessions whose decisions still belong in this conversation.
        const result = await window.wuu.listUserQuestions(undefined, context);
        if (current && version === readVersion) { setQuestions(result.questions.filter(question => !resolved.current.has(question.request_id))); setError(""); }
      } catch (cause) { if (current && version === readVersion) setError(cause instanceof Error ? cause.message : String(cause)); }
    };
    void read();
    const refreshTimer = window.setInterval(read, 5000);
    const off = window.wuu?.onServerEvent?.(event => {
      if (event.kind !== "notification") return;
      // Raw events have no trusted domain route. Never append their request
      // bodies; re-read through the scoped host even for a foreign notification.
      if (["user-question/requested", "user-question/resolved"].includes(event.message.method)) void read();
    });
    return () => { current = false; window.clearInterval(refreshTimer); off?.(); };
  }, [sessionId, context]);
  const remove = (id: string) => { resolved.current.add(id); setQuestions(items => items.filter(item => item.request_id !== id)); };
  const cancel = async (id: string) => { const result = await window.wuu.cancelUserQuestion(id, context); if (!result.resolved) throw new Error("Question is no longer available; refresh the conversation"); remove(id); };
  const replyToOffer = async (question: UserQuestionRequest, prompt: string) => {
    if (!onReply) throw new Error("This conversation cannot submit an offer reply");
    // Admit the reply before resolving the non-blocking offer. The question ID
    // lets the transport retry a lost acknowledgement without sending twice.
    await onReply(question.request_id, prompt, question.relation);
    await cancel(question.request_id);
  };
  if (!questions.length && !error) return null;
  return <div className="plugin-conversation-questions" data-wuu-component="conversation-decisions">
    {error && <p role="alert">{error}</p>}
    {questions.map(question => <UserQuestionCard key={question.request_id} request={question}
      onAnswer={async answer => {
        if (question.mode === "offer" && !question.reply_to_tool) {
          const prompt = question.questions.map(item => { const value = answer.answers.find(entry => entry.id === item.id); return `${item.question}\n${[...(value?.selected ?? []), ...(value?.custom?.trim() ? [value.custom.trim()] : [])].join(", ")}`; }).join("\n\n");
          await replyToOffer(question, prompt); return;
        }
        const result = await window.wuu.answerUserQuestion(question.request_id, answer, context); if (!result.resolved) throw new Error("Question is no longer available; refresh the conversation"); remove(question.request_id);
      }}
      onHold={async () => { const result = await window.wuu.holdUserQuestion(question.request_id, context); if (result.resolved) { remove(question.request_id); return; } setQuestions(items => items.map(item => item.request_id === question.request_id ? { ...item, expires_at: undefined } : item)); }}
      onCustom={async text => {
        if (!text?.trim()) return;
        if (!question.reply_to_tool) { await replyToOffer(question, text.trim()); return; }
        const result = await window.wuu.answerUserQuestion(question.request_id, { answers: [{ id: question.questions[0].id, selected: [], custom: text.trim() }] }, context);
        if (!result.resolved) throw new Error("Question is no longer available; refresh the conversation"); remove(question.request_id);
      }}
      onCancel={() => cancel(question.request_id)} />)}
  </div>;
}

export function PluginConversationMessage({ id: suppliedId, role, content, timestamp, latest = false, streaming = false, turnId, turnStatus = streaming ? "in_progress" : "completed", process, messageActions = "copy-only", onReply, replyLabel, replyPreview, attachments, className, linkPolicy }: PluginUIConversationMessageProps): JSX.Element {
  const bubble = useContext(ConversationBubbleContext);
  const { formatDate } = useI18n();
  const validTime = timestamp && Number.isFinite(Date.parse(timestamp)) ? timestamp : undefined;
  if (bubble.enabled) messageActions = "none";
  const fallbackId = useId();
  const id = suppliedId ?? fallbackId;
  const messageTurnId = turnId ?? `plugin-message-${id}`;
  const item: ThreadItem = { id, origin: role === "user" ? "user" : undefined, type: role === "user" ? "user_message" : "agent_message", text: content, status: streaming ? "in_progress" : "completed", terminal: role === "assistant" && !streaming };
  const attachmentContent = attachments ? <div className="plugin-conversation-attachments">{attachments}</div> : undefined;
  const inlineAttachments = bubble.enabled ? undefined : attachmentContent;
  const processTurn: Turn | undefined = role === "assistant" && process ? {
    id: messageTurnId, status: turnStatus, items_view: "full", answer_ready_at: streaming ? undefined : timestamp,
    items: [...(process.content ? [{ id: `${id}:process`, type: "agent_message" as const, text: process.content, status: "completed" as const }] : []), ...(content ? [item] : [])],
  } : undefined;
  const processDisplay = processTurn ? buildAssistantTurnDisplay(processTurn, id) ?? { entries: [], hasAnswer: false } : undefined;
  const messageContent = processTurn && processDisplay ? <AssistantTurnShell turn={processTurn} display={processDisplay}
        processLabel={process!.label} processContent={process!.details} onStreamFrame={noop} actionableAgentMessageID={id} latestAgentMessageID={latest ? id : undefined}
        messagePresentation={{ pluginHost: conversationHost, linkPolicy, messageActions }} trailingContent={inlineAttachments} /> : <ThreadItemView turnID={messageTurnId} turnStatus={turnStatus} turnStartedAt={timestamp}
        turnAnswerReadyAt={streaming ? undefined : timestamp} streaming={streaming} onStreamFrame={noop} pluginHost={conversationHost}
        actionableAgentMessageID={role === "assistant" ? id : undefined} latestAgentMessageID={latest ? id : undefined}
        messageActions={messageActions} messageAttachments={inlineAttachments} linkPolicy={linkPolicy} item={item} />;
  return <ArtifactThreadContext.Provider value={undefined}><SideConversationAvailableContext.Provider value={false}>
    <div className={["plugin-conversation-message", className].filter(Boolean).join(" ")} data-wuu-component="plugin-ui-conversation-message" data-role={role}
      data-turn-id={role === "assistant" ? messageTurnId : undefined} data-bubble={bubble.enabled || undefined}>
      {bubble.enabled && validTime && <time className="bubble-message-time" dateTime={validTime} aria-hidden={!bubble.revealed} title={formatDate(validTime, { dateStyle: "medium", timeStyle: "short" })}>{formatDate(validTime, { hour: "2-digit", minute: "2-digit" })}</time>}
      {replyPreview}
      {bubble.enabled ? <div className="bubble-message-body">{attachmentContent}{(content.trim() || process) && <div className="bubble-message-content">{messageContent}</div>}{onReply && <button type="button" className="plugin-message-reply" data-action="reply-message" aria-label={replyLabel} title={replyLabel} onClick={event => { event.currentTarget.focus({ preventScroll: true }); onReply(); }}><CornerUpLeft aria-hidden="true" /></button>}</div> : <>{messageContent}{onReply && <button type="button" className="plugin-message-reply" data-action="reply-message" aria-label={replyLabel} title={replyLabel} onClick={event => { event.currentTarget.focus({ preventScroll: true }); onReply(); }}><CornerUpLeft aria-hidden="true" /></button>}</>}
    </div>
  </SideConversationAvailableContext.Provider></ArtifactThreadContext.Provider>;
}

// Compact attachment-specific artwork uses a 20-unit grid so its visible
// contour, not just the SVG box, aligns with the filename.
function ConversationAttachmentIcon({ mediaType }: { mediaType: string }): JSX.Element {
  const kind = mediaType === "text/html" ? "html" : mediaType.startsWith("video/") ? "video" : /zip|archive|compressed/.test(mediaType) ? "archive" : "document";
  return <svg data-attachment-icon={kind} width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    {kind === "html" ? <><circle cx="10" cy="10" r="8.1" /><ellipse cx="10" cy="10" rx="3.5" ry="8.1" /><path d="M2 10h16" /></>
      : kind === "video" ? <><rect x="1.8" y="2.7" width="16.4" height="14.6" rx="2.5" /><path d="M2 8h16M7.1 3l-2.3 5m8-5-2.3 5m7-5-2.3 5" /></>
      : kind === "archive" ? <><rect x="3.1" y="1.7" width="13.8" height="16.6" rx="2.5" /><path d="M9 5h2M9 7.4h2M9 9.8h2m-1 2.4-1.6 3h3.2l-1.6-3Z" /></>
      : <><path d="M11.5 1.8H5.4a2 2 0 0 0-2 2v12.4a2 2 0 0 0 2 2h9.2a2 2 0 0 0 2-2V7l-5.1-5.2Z" /><path d="M11.4 2v5.2h5M6.7 11h6.6m-6.6 3.3h5" /></>}
  </svg>;
}

type PendingAttachmentURL = {
  promise: Promise<string>;
  resolve: (url: string) => void;
  reject: (cause: unknown) => void;
  url?: string;
  error?: string;
  loading: boolean;
};

export function PluginConversationAttachment({ resourceId, onOpen, onRead, name, mediaType, size, url, loading, error, loadLabel, downloadLabel, onLoad }: PluginUIConversationAttachmentProps): JSX.Element {
  const { t, formatNumber } = useI18n();
  const openPreview = useContext(ConversationAttachmentPreviewContext);
  const bubble = useContext(ConversationBubbleContext);
  const fallbackId = useId();
  const localURL = url?.startsWith("blob:") ? url : undefined;
  const raster = ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(mediaType);
  const readFailed = t("conversationAttachment.readFailed");
  const pendingURL = useRef<PendingAttachmentURL | undefined>(undefined);
  const mounted = useRef(true);
  const active = useRef({ open: false, download: false });
  const [opening, setOpening] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [actionError, setActionError] = useState("");
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      pendingURL.current?.reject(new DOMException("Attachment card was unmounted", "AbortError"));
    };
  }, []);
  useLayoutEffect(() => {
    const pending = pendingURL.current;
    if (!pending) return;
    if (localURL) pending.resolve(localURL);
    else if (url && url !== pending.url) pending.reject(new Error(readFailed));
    else if (error && (error !== pending.error || (pending.loading && !loading))) pending.reject(new Error(error));
    pending.url = url;
    pending.error = error;
    pending.loading ||= Boolean(loading);
  }, [url, localURL, loading, error, readFailed]);
  // Keep this adapter until the public attachment API no longer accepts void
  // loaders that publish their verified URL/error through later prop updates.
  const loadURL = (): Promise<string> => {
    if (localURL) return Promise.resolve(localURL);
    if (pendingURL.current) return pendingURL.current.promise;
    let resolve!: (source: string) => void, reject!: (cause: unknown) => void;
    const promise = new Promise<string>((done, fail) => { resolve = done; reject = fail; });
    const pending: PendingAttachmentURL = {
      promise, url, error, loading: Boolean(loading),
      resolve: source => {
        if (pendingURL.current !== pending) return;
        pendingURL.current = undefined;
        resolve(source);
      },
      reject: cause => {
        if (pendingURL.current !== pending) return;
        pendingURL.current = undefined;
        reject(cause);
      },
    };
    pendingURL.current = pending;
    try {
      void Promise.resolve(onLoad()).then(source => {
        if (typeof source !== "string") return;
        if (source.startsWith("blob:")) pending.resolve(source);
        else pending.reject(new Error(readFailed));
      }, pending.reject);
    } catch (cause) { pending.reject(cause); }
    return promise;
  };
  const reportError = (cause: unknown) => {
    if (mounted.current) setActionError(cause instanceof Error ? cause.message : String(cause));
  };
  const download = async () => {
    if (active.current.download) return;
    active.current.download = true;
    setDownloading(true); setActionError("");
    try {
      const source = await loadURL();
      if (!mounted.current) return;
      const link = document.createElement("a");
      link.href = source; link.download = name; document.body.append(link); link.click(); link.remove();
    } catch (cause) { reportError(cause); }
    finally {
      active.current.download = false;
      if (mounted.current) setDownloading(false);
    }
  };
  const open = async () => {
    if (active.current.open) return;
    active.current.open = true;
    setActionError("");
    try {
      if (openPreview && onRead) {
        let pending: Promise<Blob> | undefined;
        onOpen?.();
        openPreview({ resourceId: resourceId ?? fallbackId, name, mediaType, size, read: () => {
          if (!pending) pending = (async () => onRead())().catch(cause => { pending = undefined; throw cause; });
          return pending;
        } });
        return;
      }
      setOpening(true);
      const source = await loadURL();
      if (!mounted.current || !openPreview) return;
      const response = await fetch(source);
      if (!response.ok) throw new Error(readFailed);
      const blob = await response.blob();
      if (!mounted.current) return;
      // Opening may unmount the plugin and revoke its URL. Tabs own bytes, not
      // that card's URL, so complete the legacy snapshot before changing views.
      onOpen?.();
      openPreview({ resourceId: resourceId ?? fallbackId, name, mediaType, size, read: () => Promise.resolve(blob) });
    } catch (cause) { reportError(cause); }
    finally {
      active.current.open = false;
      if (mounted.current) setOpening(false);
    }
  };
  return <div className="plugin-conversation-attachment">
    <div className="conversation-file-card" data-wuu-component="conversation-attachment">
      <button type="button" className="conversation-file-card-main" data-action="open-attachment" aria-label={t("conversationAttachment.open", { name })} disabled={opening} aria-busy={opening} onClick={() => { void open(); }}>
        {opening ? <LoaderCircle className="is-spinning" /> : bubble.enabled ? <ConversationAttachmentIcon mediaType={mediaType} /> : <FileText />}<span className="conversation-file-card-copy"><strong title={`${name} · ${mediaType} · ${formatByteSize(size, formatNumber)}`}>{name}</strong></span>
      </button>
      {!bubble.enabled && (localURL ? <a className="icon-button conversation-file-download" href={localURL} download={name} aria-label={downloadLabel} title={downloadLabel}><Download /></a>
        : <button type="button" className="icon-button conversation-file-download" data-action="load-attachment" aria-label={loadLabel} title={loadLabel} disabled={loading || downloading} onClick={() => { void download(); }}>{loading || downloading ? <LoaderCircle className="is-spinning" /> : <Download />}</button>)}
    </div>
    {(actionError || error) && <p role="alert">{actionError || error}</p>}
    {raster && localURL && <img src={localURL} alt={name} />}
  </div>;
}


export function PluginConversationComposer({ replyContext, conversationId, prompt, placeholder, running, submitting, queuedCount, pendingCount, stopState, disabled = false, sendDisabled, attachments, onChangePrompt, onAddAttachments, onRemoveAttachment, onSend, onStop }: PluginUIConversationComposerProps): JSX.Element {
  const images = attachments.filter(file => ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(file.media_type)).map(file => ({ ...file }));
  const files = attachments.filter(file => !images.some(image => image.id === file.id)).map(file => ({ ...file }));
  return <FileSelectionProvider disabled getPrompt={() => prompt} setPrompt={noop} onEdit={async () => false} onOpenFile={noop}>
    {replyContext && <div className="plugin-composer-reply-context" data-reply-context="" role="group" aria-label={replyContext.label}><div><span>{replyContext.label}</span><p>{replyContext.preview}</p></div><button type="button" className="settings-button settings-button-ghost" data-action="cancel-reply" aria-label={replyContext.cancelLabel} onClick={replyContext.onCancel}>{replyContext.cancelLabel}</button></div>}
    <SplitPaneComposer key={conversationId} pluginHost={conversationHost} allowWorkspaceReferences={false}
      placeholder={placeholder} prompt={prompt} setPrompt={onChangePrompt} images={images} files={files}
      running={running} submitting={submitting} queuedCount={queuedCount} pendingCount={pendingCount} stopState={stopState} readOnly={disabled} sendDisabled={sendDisabled} queryHistorySessionID={`plugin:${conversationId}`}
      onPasteAttachmentFiles={onAddAttachments} onRemoveFile={onRemoveAttachment} onRemoveImage={onRemoveAttachment}
      onSend={text => { onSend(text ?? prompt); return false; }} onInterrupt={onStop} />
  </FileSelectionProvider>;
}
