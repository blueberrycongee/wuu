import * as React from "react";
import { Component, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
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
import { TurnOutputSummaryCard } from "../TurnOutputSummaryCard";
import { Modal } from "../Modal";
import { Download, FileText, LoaderCircle, Search } from "../WuuIcons";
import { formatByteSize } from "../AttachmentFormat";
import { useI18n } from "../i18n";
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

export function PluginConversationView({ conversationId, sessionId, sessionScope, onQuestionReply, actions, title, subtitle, avatar, status, loading = false, searchLabel, searchContent, children, composer }: PluginUIConversationViewProps): JSX.Element {
  const { t } = useI18n();
  const controller = useAutoFollowScrollContainer({ observeKey: conversationId });
  const rootRef = useRef<HTMLElement>(null);
  const [composerNode, setComposerNode] = useState<HTMLDivElement | null>(null);
  const [searchFor, setSearchFor] = useState<string>();
  const searchOpen = searchFor === conversationId;
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
  return <section ref={rootRef} className="plugin-conversation" data-conversation-id={conversationId}>
    <header className="titlebar plugin-conversation-titlebar" data-wuu-component="conversation-titlebar">
      <div className="title-block"><div className="conversation-title-heading">{avatar}<h1 tabIndex={-1}>{title}</h1>{subtitle && <span className="plugin-conversation-subtitle">{subtitle}</span>}</div></div>
      <div className="title-actions">{status}<button type="button" className="icon-button" data-action="search-history" aria-label={searchLabel} title={searchLabel} aria-expanded={searchOpen} onClick={event => { event.currentTarget.focus({ preventScroll: true }); setSearchFor(conversationId); }}><Search /></button>{actions}</div>
    </header>
    <ConversationReadingBoundary conversationId={conversationId} loading={loading} controller={controller}>
      <div className="scroll-region plugin-conversation-scroll" ref={controller.scrollRef} onScroll={controller.handleScrollFrame} data-scroll-fade="">
        <ConversationFlow className="plugin-conversation-flow" role="log" aria-live="polite" aria-busy={loading}>{children}{sessionId && sessionScope && <PluginConversationQuestions sessionId={sessionId} scope={sessionScope} onReply={onQuestionReply} />}</ConversationFlow>
      </div>
    </ConversationReadingBoundary>
    <div className="plugin-conversation-composer" ref={setComposerNode}>{composer}</div>
    <JumpToLatestPill containerRef={controller.scrollRef} bottomAnchor={composerNode} scopeKey={conversationId} onJump={() => controller.scrollToBottom({ force: true, animate: true })} />
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
    await onReply(question.request_id, prompt);
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

export function PluginConversationMessage({ id: suppliedId, role, content, timestamp, latest = false, streaming = false, turnId, turnStatus = streaming ? "in_progress" : "completed", process, messageActions = "copy-only", attachments, className, linkPolicy }: PluginUIConversationMessageProps): JSX.Element {
  const fallbackId = useId();
  const id = suppliedId ?? fallbackId;
  const messageTurnId = turnId ?? `plugin-message-${id}`;
  const item: ThreadItem = { id, origin: role === "user" ? "user" : undefined, type: role === "user" ? "user_message" : "agent_message", text: content, status: streaming ? "in_progress" : "completed", terminal: role === "assistant" && !streaming };
  const attachmentContent = attachments ? <div className="plugin-conversation-attachments">{attachments}</div> : undefined;
  const processTurn: Turn | undefined = role === "assistant" && process ? {
    id: messageTurnId, status: turnStatus, items_view: "full", answer_ready_at: streaming ? undefined : timestamp,
    items: [...(process.content ? [{ id: `${id}:process`, type: "agent_message" as const, text: process.content, status: "completed" as const }] : []), ...(content ? [item] : [])],
  } : undefined;
  const processDisplay = processTurn ? buildAssistantTurnDisplay(processTurn, id) ?? { entries: [], hasAnswer: false } : undefined;
  return <ArtifactThreadContext.Provider value={undefined}><SideConversationAvailableContext.Provider value={false}>
    <div className={["plugin-conversation-message", className].filter(Boolean).join(" ")} data-wuu-component="plugin-ui-conversation-message" data-role={role}
      data-turn-id={role === "assistant" ? messageTurnId : undefined}>
      {processTurn && processDisplay ? <AssistantTurnShell turn={processTurn} display={processDisplay}
        processLabel={process!.label} processContent={process!.details} onStreamFrame={noop} actionableAgentMessageID={id} latestAgentMessageID={latest ? id : undefined}
        messagePresentation={{ pluginHost: conversationHost, linkPolicy, messageActions }} trailingContent={attachmentContent} /> : <ThreadItemView turnID={messageTurnId} turnStatus={turnStatus} turnStartedAt={timestamp}
        turnAnswerReadyAt={streaming ? undefined : timestamp} streaming={streaming} onStreamFrame={noop} pluginHost={conversationHost}
        actionableAgentMessageID={role === "assistant" ? id : undefined} latestAgentMessageID={latest ? id : undefined}
        messageActions={messageActions} messageAttachments={attachmentContent} linkPolicy={linkPolicy} item={item} />}
    </div>
  </SideConversationAvailableContext.Provider></ArtifactThreadContext.Provider>;
}

export function PluginConversationAttachment({ name, mediaType, size, url, loading, error, loadLabel, downloadLabel, onLoad }: PluginUIConversationAttachmentProps): JSX.Element {
  const { formatNumber } = useI18n();
  // Only caller-verified local bytes may become a preview or download here.
  const localURL = url?.startsWith("blob:") ? url : undefined;
  const raster = ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(mediaType);
  return <div className="plugin-conversation-attachment">
    <TurnOutputSummaryCard component="conversation-attachment" icon={<FileText />} title={name}
      subtitle={formatByteSize(size, formatNumber)} trailing={localURL
        ? <a className="icon-button" href={localURL} download={name} aria-label={downloadLabel} title={downloadLabel}><Download /></a>
        : <button type="button" className="icon-button" data-action="load-attachment" aria-label={loadLabel} title={loadLabel} disabled={loading} onClick={onLoad}>{loading ? <LoaderCircle className="is-spinning" /> : <Download />}</button>} />
    {error && <p role="alert">{error}</p>}
    {raster && localURL && <img src={localURL} alt={name} />}
  </div>;
}

export function PluginConversationComposer({ conversationId, prompt, placeholder, running, submitting, queuedCount, pendingCount, stopState, disabled = false, sendDisabled, attachments, onChangePrompt, onAddAttachments, onRemoveAttachment, onSend, onStop }: PluginUIConversationComposerProps): JSX.Element {
  const images = attachments.filter(file => ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(file.media_type)).map(file => ({ ...file }));
  const files = attachments.filter(file => !images.some(image => image.id === file.id)).map(file => ({ ...file }));
  return <FileSelectionProvider disabled getPrompt={() => prompt} setPrompt={noop} onEdit={async () => false} onOpenFile={noop}>
    <SplitPaneComposer key={conversationId} pluginHost={conversationHost} allowWorkspaceReferences={false}
      placeholder={placeholder} prompt={prompt} setPrompt={onChangePrompt} images={images} files={files}
      running={running} submitting={submitting} queuedCount={queuedCount} pendingCount={pendingCount} stopState={stopState} readOnly={disabled} sendDisabled={sendDisabled} queryHistorySessionID={`plugin:${conversationId}`}
      onPasteAttachmentFiles={onAddAttachments} onRemoveFile={onRemoveAttachment} onRemoveImage={onRemoveAttachment}
      onSend={text => { onSend(text ?? prompt); return false; }} onInterrupt={onStop} />
  </FileSelectionProvider>;
}
