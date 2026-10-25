import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type KeyboardEvent, type MutableRefObject, type PointerEvent, type RefObject } from "react";
import type { ApplicationGenerationAPI, ApplicationServiceV1, UICompositionV1, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationAttachmentV1 } from "../../../../packages/plugin-sdk/src/ui-composer";
import type { InputFile, InputImage, MessageContentPart, Thread, ThreadItem, Turn, UserQuestionRequest } from "../../shared/protocol";
import { ArtifactsProvider } from "../ArtifactsContext";
import { registerConversationSlots } from "../ConversationSlots";
import { BrowserControlsProvider } from "../BrowserControlsContext";
import { PreviewContentProvider } from "../PreviewContentContext";
import { ConversationControlsProvider } from "../ConversationControlsContext";
import { CachedConversationPanes } from "../CachedConversationPanes";
import { ConversationSplitPane } from "../ConversationSplitPane";
import { retainCachedConversationPaneThreads, selectCachedConversationPaneIDs } from "../ConversationPaneCache";
import { ConversationForkDialog } from "../ConversationForkDialog";
import { EmptyConversationHome, RuntimeLoading } from "../LoadingViews";
import { EmptyHomeOverview } from "../EmptyHomeOverview";
import { FloatingMenuPortal, composerMenuWidth, handleFloatingMenuKeyDown, menuOpeningKey, useFloatingMenuFocus } from "../ComposerFloatingMenu";
import { WorkbenchConnectionContext } from "../WorkbenchConnectionContext";
import { ImagePreviewProvider } from "../ImagePreview";
import { TurnView } from "../TurnView";
import { isThreadRunning } from "../AppState";
import type { ContextCompositionEntry } from "../ContextCompositionCard";
import type { InstructionFilesEntry } from "../InstructionFilesCard";
import type { TurnFileDiffSelection } from "../TurnFileDiffTypes";
import { showErrorToast } from "../Toast";
import { greetingFor, useCurrentHour } from "../greetings";
import { useI18n } from "../i18n";
import { CONVERSATION_SPLIT_MAX_PERCENT, CONVERSATION_SPLIT_MIN_PERCENT } from "../AppLayoutState";

type Pane = "primary" | "secondary";
/** The shell retains DOM scroll and splitter mechanics, never conversation authority. */
export interface DefaultConversationLayout {
  scrollRef?: RefObject<HTMLDivElement | null>;
  contentRef?: RefObject<HTMLDivElement | null>;
  splitPaneRefs?: MutableRefObject<Record<Pane, HTMLElement | null>>;
  visible?: boolean;
  restoring?: boolean;
  onScroll?: (node: HTMLElement) => void;
  onStreamFrame?: () => void;
  onCollapseComplete?: () => void;
  splitLeftPercent?: number;
  onSplitResizeStart?: (event: PointerEvent<HTMLDivElement>) => void;
  onSplitSeparatorKey?: (event: KeyboardEvent<HTMLDivElement>) => void;
  onSplitSeparatorDoubleClick?: () => void;
}
const noop = () => {};

export function DefaultConversationSurface({ application, slots, ...layout }: DefaultConversationLayout & { application: ApplicationServiceV1; slots: UICompositionV1 }): JSX.Element {
  return <ConversationControlsProvider application={application} slots={slots} slotOwner="wuu.default-conversation"><ArtifactsProvider application={application}><PreviewContentProvider service={application.previewContent} slots={slots}><BrowserControlsProvider service={application.browser}><ImagePreviewProvider><ConversationSurface application={application} {...layout} /></ImagePreviewProvider></BrowserControlsProvider></PreviewContentProvider></ArtifactsProvider></ConversationControlsProvider>;
}

function ConversationSurface({ application, ...layout }: DefaultConversationLayout & { application: ApplicationServiceV1 }): JSX.Element {
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const { t } = useI18n(), hour = useCurrentHour();
  const run = useCallback((result: Promise<unknown>) => { void result.catch(showErrorToast); }, []);
  // Leaf renderers only read histories; edits go through named application actions.
  // Keep the frozen public values instead of serializing every loaded attachment.
  const threads = view.conversationWorkspace.threads as readonly Thread[];
  const current = useMemo(() => new Map(threads.map(thread => [thread.id, thread])), [threads]);
  const primary = current.get(view.conversationWorkspace.primaryThreadId ?? ""), secondary = current.get(view.conversationWorkspace.secondaryThreadId ?? "");
  const cache = useRef<{ ids: string[]; threads: ReadonlyMap<string, Thread> }>({ ids: [], threads: new Map() });
  const open = new Set(cache.current.ids); if (primary) open.add(primary.id);
  const available = new Map([...cache.current.threads, ...current]);
  const ids = selectCachedConversationPaneIDs({ activeThreadID: primary?.id, previousThreadIDs: cache.current.ids, openThreadIDs: open, threadsByID: available });
  const cached = retainCachedConversationPaneThreads({ threadIDs: ids, currentThreadsByID: current, previousThreadsByID: cache.current.threads });
  cache.current = { ids, threads: cached };
  const details = useMemo(() => structuredClone(view.conversationDetails), [view.conversationDetails]);
  const contexts = details.filter(entry => entry.kind === "context") as ContextCompositionEntry[];
  const instructions = details.filter(entry => entry.kind === "instructions") as InstructionFilesEntry[];
  const editing = view.conversationActions.editing;
  const editState = editing ? { threadID: editing.threadId, turnID: editing.turnId, itemID: editing.itemId, pane: editing.pane, submitting: editing.submitting } : undefined;
  const questions = useMemo(() => structuredClone(view.conversationActions.questions) as UserQuestionRequest[], [view.conversationActions.questions]);
  const pending = view.submission.pendingCreations.find(row => row.ownerTabId === view.tabs.activeTabId);
  const split = Boolean(primary && secondary);
  const empty = !pending && !primary?.turns.length && !details.some(entry => entry.threadID === primary?.id);
  const switching = Boolean(view.tabs.pending && !view.tabs.pending.contentReady);
  const onStreamFrame = layout.onStreamFrame ?? noop, onCollapseComplete = layout.onCollapseComplete ?? onStreamFrame;
  const canEdit = useCallback((thread: Thread) => !thread.read_only && !isThreadRunning(thread) && !application.getSnapshot().composerPending.some(row => row.threadId === thread.id && (row.queued.length || row.guides.length)), [application]);
  const fork = useCallback((thread: Thread, turnId: string, itemId: string) => run(application.invoke("history.request-fork", { threadId: thread.id, turnId, itemId })), [application, run]);
  const editTarget = useCallback((thread: Thread, turnId: string, itemId: string) => {
    const workspace = application.getSnapshot().conversationWorkspace;
    const pane = workspace.secondaryThreadId ? (workspace.secondaryThreadId === thread.id ? "secondary" as const : "primary" as const) : undefined;
    return { threadId: thread.id, turnId, itemId, ...(pane ? { pane } : {}) };
  }, [application]);
  const edit = useCallback((thread: Thread, turnId: string, item: ThreadItem) => run(application.invoke("history.begin-edit", editTarget(thread, turnId, item.id))), [application, editTarget, run]);
  const cancelEdit = useCallback(() => run(application.invoke("history.cancel-edit", {})), [application, run]);
  const submitEdit = useCallback(async (thread: Thread, turnId: string, item: ThreadItem, text: string, images: InputImage[], files: InputFile[], contentParts?: MessageContentPart[]) => {
    const attachments: ApplicationAttachmentV1[] = [
      ...images.map(image => ({ id: crypto.randomUUID(), kind: "image" as const, name: "image", mediaType: image.media_type, status: "ready" as const, data: image.data, remoteRef: image.remote_ref, width: image.width, height: image.height })),
      ...files.map(file => ({ id: crypto.randomUUID(), kind: "file" as const, name: file.filename ?? "file", mediaType: file.media_type, status: "ready" as const, data: file.data, remoteRef: file.remote_ref })),
    ];
    const target = editTarget(thread, turnId, item.id);
    try {
      const selected = application.getSnapshot().conversationActions.editing;
      if (!selected || selected.threadId !== target.threadId || selected.turnId !== target.turnId || selected.itemId !== target.itemId || selected.pane !== target.pane) {
        const result = await application.invoke("history.begin-edit", target);
        if (!result.accepted) return;
      }
      if (attachments.length) await application.invoke("composer.import-attachments", { attachments });
      await application.invoke("history.update-edit", { ...target, draft: { text, imageIds: attachments.filter(item => item.kind === "image").map(item => item.id), fileIds: attachments.filter(item => item.kind === "file").map(item => item.id), contentParts } });
      await application.invoke("history.submit-edit", target);
    } catch (error) { showErrorToast(error); }
    finally { if (attachments.length) await application.invoke("composer.release-attachments", { attachmentIds: attachments.map(item => item.id), ifUnreferenced: true }).catch(showErrorToast); }
  }, [application, editTarget]);
  const openFile = useCallback((thread: Thread, path: string) => {
    const workspace = application.getSnapshot().workspaces.workspaces.find(row => row.root === thread.cwd);
    run(application.invoke("preview.open-file", { path, context: workspace ? { kind: "project", project_id: workspace.id, cwd: workspace.root } : { kind: "no_project", cwd: thread.cwd } }));
  }, [application, run]);
  const openURL = useCallback((url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => run(application.invoke("preview.open-url", { url, modifiers })), [application, run]);
  const openAgent = useCallback((agent: { id: string }) => run(application.invoke("thread.activate-child", { threadId: agent.id })), [application, run]);
  const openDiff = useCallback((thread: Thread, selection: TurnFileDiffSelection) => run(application.invoke("preview.open-diff", { threadId: thread.id, path: selection.path, selection })), [application, run]);
  const dismiss = useCallback((entryId: string) => run(application.invoke("conversation.dismiss-detail", { entryId })), [application, run]);
  const answer = useCallback((requestId: string, answer: { answers: { id: string; selected: string[]; custom?: string }[] }) => application.invoke("question.answer", { requestId, answer }), [application]);
  const cancelQuestion = useCallback((requestId: string) => application.invoke("question.cancel", { requestId }), [application]);
  const [workspaceMenu, setWorkspaceMenu] = useState(false);
  const workspaceTriggerRef = useRef<HTMLButtonElement>(null);
  const workspaceMenuRef = useRef<HTMLDivElement>(null);
  const [workspaceMenuFromEnd, setWorkspaceMenuFromEnd] = useState(false);
  useFloatingMenuFocus(workspaceMenuRef, "", workspaceMenu, workspaceMenuFromEnd);
  useEffect(() => {
    if (!workspaceMenu) return;
    const dismiss = (event: globalThis.PointerEvent) => {
      if (event.target instanceof Node && !workspaceTriggerRef.current?.contains(event.target) && !workspaceMenuRef.current?.contains(event.target)) setWorkspaceMenu(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [workspaceMenu]);
  const selectedWorkspace = view.workspaces.workspaces.find(row => row.id === view.workspaces.activeWorkspaceId);
  const greeting = selectedWorkspace ? greetingFor(hour, { kind: "workspace", workspaceName: "{workspace}" }).split("{workspace}") : undefined;
  const title = greeting ? <>{greeting[0]}<button ref={workspaceTriggerRef} type="button" className="empty-home-workspace-link" aria-haspopup="menu" aria-expanded={workspaceMenu}
    onClick={() => { setWorkspaceMenuFromEnd(false); setWorkspaceMenu(value => !value); }}
    onKeyDown={event => { const edge = menuOpeningKey(event); if (edge) { setWorkspaceMenuFromEnd(edge === "end"); setWorkspaceMenu(true); } }}
  >{selectedWorkspace!.name}</button>{greeting[1]}</> : greetingFor(hour, { kind: "wuu" });
  const activeDraft = view.drafts[view.drafts.activePane];
  const activeTab = view.tabs.items.find(tab => tab.id === view.tabs.activeTabId);
  if (view.phase === "starting" || view.conversation.loading) return <RuntimeLoading status={view.connection.message ?? "connecting"} />;
  if (view.phase === "error" && !primary) return <RuntimeLoading status={view.conversation.error ?? view.connection.message ?? t("sideThread.unavailable")} />;
  return <WorkbenchConnectionContext.Provider value={view.connection.status === "connected"}>
    <div ref={layout.scrollRef} data-pip-anchor-host="conversation" data-scroll-fade={!empty && !split ? "" : undefined}
      className={`scroll-region${empty ? " empty-scroll-region" : ""}${split ? " split-scroll-region" : ""}`}
      inert={layout.visible === false || switching} style={layout.restoring ? { visibility: "hidden" } : undefined}
      onScroll={event => layout.onScroll?.(event.currentTarget)}>
      <div ref={layout.contentRef} className="scroll-region-content">
        {view.conversation.error ? <div role="alert">{view.conversation.error}</div> : null}
        {split ? <div className="conversation-split" style={{ "--conversation-split-left": `${layout.splitLeftPercent ?? 50}%` } as CSSProperties}>
          {(["primary", "secondary"] as const).map(pane => <SplitConversation key={pane} pane={pane} thread={pane === "primary" ? primary! : secondary!} application={application} onStreamFrame={onStreamFrame}
            onScroll={layout.onScroll} onBodyRef={node => { if (layout.splitPaneRefs) layout.splitPaneRefs.current[pane] = node; }}
            fork={fork} edit={edit} submitEdit={submitEdit} cancelEdit={cancelEdit} canEdit={canEdit} openFile={openFile} openURL={openURL} openAgent={openAgent} openDiff={openDiff}
            separator={pane === "primary" ? <div className="conversation-split-resizer" role="separator" aria-orientation="vertical" aria-label={t("shell.resizeSplit")} aria-valuemin={CONVERSATION_SPLIT_MIN_PERCENT} aria-valuemax={CONVERSATION_SPLIT_MAX_PERCENT} aria-valuenow={Math.round(layout.splitLeftPercent ?? 50)} tabIndex={0} onPointerDown={layout.onSplitResizeStart} onDoubleClick={layout.onSplitSeparatorDoubleClick} onKeyDown={layout.onSplitSeparatorKey} /> : undefined} />)}
        </div> : pending ? <div className="conversation-width session-flow"><TurnView turn={pending.turn as Turn} onStreamFrame={onStreamFrame} isLatestTurn cwd={view.workspaces.activeRoot} /></div>
          : empty ? layout.visible === false ? null : <EmptyConversationHome title={activeTab?.kind === "draft" && activeTab.project ? t("projects.newProject") : title} activity={activeDraft.text.trim() || activeDraft.imageIds.length || activeDraft.fileIds.length || activeDraft.selections.length ? "compose" : "idle"}>
            <>{workspaceMenu && greeting && !activeTab?.project ? <FloatingMenuPortal anchorRef={workspaceTriggerRef} owner="conversation-workspace" placement="below" align="left" width={composerMenuWidth(244)} flip>
              <div ref={workspaceMenuRef} role="menu" className="composer-context-menu"
                style={{ width: "var(--floating-menu-available-width)", maxHeight: "min(340px, var(--floating-menu-available-height))" }}
                onKeyDown={event => handleFloatingMenuKeyDown(event, () => setWorkspaceMenu(false), workspaceTriggerRef.current)}>
                {view.workspaces.workspaces.map(workspace => <button key={workspace.id} role="menuitem" type="button" onClick={() => {
                  setWorkspaceMenu(false); workspaceTriggerRef.current?.focus();
                  run(application.invoke("workspace.retarget-draft", { workspaceId: workspace.id }));
                }}>{workspace.name}</button>)}
              </div>
            </FloatingMenuPortal> : null}
            {activeTab?.project ? null : <EmptyHomeOverview application={application} />}</>
          </EmptyConversationHome>
          : <CachedConversationPanes threadIDs={ids} threadsByID={cached} activeThreadID={primary?.id} restoring={layout.restoring} activeContextCwd={view.workspaces.activeRoot}
            contextCompositionEntries={contexts} instructionFilesEntries={instructions} historyMessageEdit={editState} onStreamFrame={onStreamFrame} onCollapseComplete={onCollapseComplete}
            onDismissContextComposition={dismiss} onDismissInstructions={dismiss} canEditThreadMessage={canEdit} onForkMessage={fork} onOpenFile={openFile} onOpenURL={openURL} onOpenAgent={openAgent}
            onEditMessage={edit} onCancelEditMessage={cancelEdit} onSubmitEditMessage={submitEdit} turnStreamStatus={view.conversationWorkspace.turnStreamStatus} onOpenFileDiff={openDiff}
            pendingUserQuestion={questions.find(question => question.thread_id === primary?.id && question.mode !== "offer")} onAnswerUserQuestion={answer} onCancelUserQuestion={cancelQuestion} />}
      </div>
    </div>
    {view.conversationActions.pendingFork ? <ConversationForkDialog worktreeDisabledReason={view.conversationActions.pendingFork.worktreeDisabledReason} onCancel={() => run(application.invoke("history.cancel-fork", {}))} onChoose={mode => application.invoke("history.choose-fork", { mode })} /> : null}
  </WorkbenchConnectionContext.Provider>;
}

function SplitConversation({ pane, thread, application, separator, onBodyRef, onScroll, onStreamFrame, fork, edit, submitEdit, cancelEdit, canEdit, openFile, openURL, openAgent, openDiff }: {
  pane: Pane; thread: Thread; application: ApplicationServiceV1; separator?: import("react").ReactNode;
  onBodyRef: (node: HTMLElement | null) => void; onScroll?: (node: HTMLElement) => void; onStreamFrame: () => void;
  fork: (thread: Thread, turnId: string, itemId: string) => void;
  edit: (thread: Thread, turnId: string, item: ThreadItem) => void;
  submitEdit: (thread: Thread, turnId: string, item: ThreadItem, text: string, images: InputImage[], files: InputFile[], parts?: MessageContentPart[]) => Promise<void>;
  cancelEdit: () => void; canEdit: (thread: Thread) => boolean;
  openFile: (thread: Thread, path: string) => void;
  openURL: (url: string, modifiers?: { metaKey?: boolean; ctrlKey?: boolean; altKey?: boolean; button?: number }) => void;
  openAgent: (agent: { id: string }) => void; openDiff: (thread: Thread, selection: TurnFileDiffSelection) => void;
}): JSX.Element {
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const editing = view.conversationActions.editing?.threadId === thread.id ? view.conversationActions.editing : undefined;
  const question = view.conversationActions.questions.find(row => row.thread_id === thread.id && row.mode !== "offer");
  return <><ConversationSplitPane pane={pane} thread={thread} active={view.conversationWorkspace.activePane === pane}
    ownerTabId={view.drafts[pane].ownerTabId} activeContextCwd={view.workspaces.activeRoot}
    streamStatus={view.conversationWorkspace.turnStreamStatus[thread.turns.at(-1)?.id ?? ""]}
    editingMessage={editing ? { turnID: editing.turnId, itemID: editing.itemId, submitting: editing.submitting } : undefined}
    onActivate={() => { if (application.getSnapshot().conversationWorkspace.activePane !== pane) void application.invoke("conversation.activate-pane", { pane }).catch(showErrorToast); }}
    onClose={() => { void application.invoke("conversation.close-pane", { pane }).catch(showErrorToast); }} onBodyRef={onBodyRef} onScroll={onScroll ?? noop}
    onForkMessage={(turnId, itemId) => fork(thread, turnId, itemId)} onOpenFile={path => openFile(thread, path)} onOpenURL={openURL} onOpenAgent={id => openAgent({ id })}
    onEditMessage={canEdit(thread) ? (turnId, item) => edit(thread, turnId, item) : undefined}
    onCancelEditMessage={cancelEdit} onSubmitEditMessage={(turnId, item, text, images, files, parts) => submitEdit(thread, turnId, item, text, images, files, parts)}
    onStreamFrame={onStreamFrame} onOpenFileDiff={selection => openDiff(thread, selection)} pendingUserQuestion={question ? structuredClone(question) as UserQuestionRequest : undefined}
    onAnswerUserQuestion={(requestId, answer) => application.invoke("question.answer", { requestId, answer })}
    onCancelUserQuestion={requestId => application.invoke("question.cancel", { requestId })}
  />{separator}</>;
}

export function activate(api: ApplicationGenerationAPI): void {
  registerConversationSlots(api);
  api.registerUISlot({ id: "application.composer", cardinality: "one", service: "ui.composer" });
  api.registerUIContribution({ id: "ui.conversation", target: "wuu.default-shell/application.conversation",
    render: ({ application, slots, context }: UISlotRenderProps) => <DefaultConversationSurface application={application} slots={slots} {...context as DefaultConversationLayout} /> });
}
