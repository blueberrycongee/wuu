import { ApplicationSideThreadComposer } from "../SideThreadComposer";
import { ArtifactsProvider } from "../ArtifactsContext";
import { ImagePreviewProvider } from "../ImagePreview";
import { SettingsControlsProvider } from "../SettingsControlsContext";
import { focusComposerTextarea } from "../ComposerFocus";
import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore, type Ref } from "react";
import type { ApplicationPresentationAPI, ApplicationServiceV1, UICompositionV1, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationPendingComposerMessageV1 } from "../../../../packages/plugin-sdk/src/ui-composer";
import type { EngineInfo, MessageContentPart, ResponseSelection, RuntimeContext, UserQuestionRequest } from "../../shared/protocol";
import { isPluginLifecycleCancellation, PluginActionNotStartedError } from "../../shared/PluginLifecycle";
import { Composer, SplitPaneComposer } from "../ComposerView";
import { createComposerControls } from "../ComposerControls";
import { ComposerServicesProvider } from "../ComposerServicesContext";
import { FileSelectionProvider } from "../FileSelectionContext";
import type { ComposerConfiguration, ComposerVariant } from "../ComposerTypes";
import type { ComposerFile, ComposerImage, QueuedComposerMessage } from "../ComposerMessages";
import { UserQuestionCard } from "../UserQuestionCard";
import { createApplicationComposerAttachments } from "./ApplicationComposerAttachments";
import { showApplicationActionError } from "../ApplicationActionError";
import { usePresentedNavigation, reportPresentedNavigationError } from "../ConversationPresentationTransition";
import { useI18n } from "../i18n";
import { threadContinuationTarget } from "../TurnContinuation";
import { ScopedToast } from "../Toast";
import { DraftRecoveryNotice } from "../DraftRecoveryNotice";

export interface DefaultComposerLayout {
  side?: { readonly parentThreadId: string };
  variant?: ComposerVariant;
  pane?: "primary" | "secondary";
  ownerTabId?: string;
  visible?: boolean;
  containerRef?: Ref<HTMLElement>;
  runtimeControlsTarget?: HTMLElement | null;
}

/** A selected composer receives geometry only; every operation retains its public draft owner. */
export function DefaultComposerSurface({ application, slots, ...layout }: DefaultComposerLayout & { application: ApplicationServiceV1; slots: UICompositionV1 }): JSX.Element | null {
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot);
  if (layout.side) {
    const parentThreadId = layout.side.parentThreadId;
    const ownerTabId = view.tabs.items.find(tab => tab.threadId === parentThreadId)?.id;
    if (!ownerTabId || view.sideConversation.parentThreadId !== parentThreadId || !view.sideConversation.entry) return null;
    return <OwnedSideComposer application={application} slots={slots} ownerTabId={ownerTabId} parentThreadId={parentThreadId} variant={layout.variant} />;
  }
  const pane = layout.pane ?? "primary";
  const ownerTabId = layout.ownerTabId ?? view.drafts[pane].ownerTabId;
  if (!ownerTabId || view.drafts[pane].ownerTabId !== ownerTabId) return null;
  return <OwnedComposer key={pane} application={application} slots={slots} {...layout} pane={pane} ownerTabId={ownerTabId} />;
}

function OwnedSideComposer({ application, slots, ownerTabId, parentThreadId, variant }: {
  application: ApplicationServiceV1; slots: UICompositionV1; ownerTabId: string; parentThreadId: string; variant?: ComposerVariant;
}): JSX.Element | null {
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const getPresentation = useCallback(() => application.composerPresentation.getSnapshot(ownerTabId), [application, ownerTabId]);
  const presentation = useSyncExternalStore(application.composerPresentation.subscribe, getPresentation);
  const services = useMemo(() => ({ slots, ownerTabId, presentation: application.composerPresentation, view: presentation, catalog: application.catalog, settingsControls: application.settingsControls }), [application, slots, ownerTabId, presentation]);
  if (view.sideConversation.parentThreadId !== parentThreadId) return null;
  return <ArtifactsProvider application={application}><ImagePreviewProvider><SettingsControlsProvider controls={application.settingsControls}><ComposerServicesProvider value={services}>
    <ApplicationSideThreadComposer snapshot={view.sideConversation} invoke={application.invoke} variant={variant}
      onOpenFile={path => { void application.fileSelection.openFile({ target: { ownerTabId }, path }).catch(showApplicationActionError); }} />
  </ComposerServicesProvider></SettingsControlsProvider></ImagePreviewProvider></ArtifactsProvider>;
}

function OwnedComposer({ application, slots, variant = "dock", pane, ownerTabId, containerRef, runtimeControlsTarget, visible = true }: DefaultComposerLayout & { application: ApplicationServiceV1; slots: UICompositionV1; pane: "primary" | "secondary"; ownerTabId: string }): JSX.Element | null {
  const { t } = useI18n();
  const presentNavigation = usePresentedNavigation();
  const view = useSyncExternalStore(application.subscribe, application.getSnapshot);
  const getPresentation = useCallback(() => application.composerPresentation.getSnapshot(ownerTabId), [application, ownerTabId]);
  const livePresentation = useSyncExternalStore(application.composerPresentation.subscribe, getPresentation);
  const liveEnvironment = useSyncExternalStore(application.environment.subscribe, application.environment.getSnapshot);
  const liveRuntime = view.composerRuntime.owners.find(row => row.ownerTabId === ownerTabId);
  const switching = Boolean(view.tabs.pending);
  // Only data survives the pending destination. The controls below always bind
  // the current application/owner, and their interaction gate stays closed.
  const settledVisual = useRef({ presentation: livePresentation, environment: liveEnvironment, runtime: liveRuntime });
  const environmentPending = !liveEnvironment.status || Boolean(liveEnvironment.status.is_repo && liveEnvironment.actionBusyPending);
  const sameEnvironment = settledVisual.current.environment.contextKey === liveEnvironment.contextKey
    && settledVisual.current.environment.root === liveEnvironment.root;
  useLayoutEffect(() => {
    if (!switching) settledVisual.current = { presentation: livePresentation, runtime: liveRuntime,
      environment: environmentPending ? settledVisual.current.environment : liveEnvironment };
  }, [switching, environmentPending, livePresentation, liveEnvironment, liveRuntime]);
  const { presentation, runtime } = switching ? settledVisual.current
    : { presentation: livePresentation, runtime: liveRuntime };
  // A different workspace may keep the control footprint, never the old branch
  // as its fact. Unknown is neither a repository nor a confirmed non-repository.
  const environment = environmentPending && sameEnvironment ? settledVisual.current.environment : liveEnvironment;
  // Pending reads may retain existing Git chrome, but must not invent a new
  // branch control for a known non-repository or an as-yet unknown target.
  const branchPickerPending = environmentPending && liveEnvironment.status?.is_repo !== false && Boolean(liveEnvironment.root)
    && Boolean(liveEnvironment.status?.is_repo || settledVisual.current.environment.status?.is_repo);
  const tab = view.tabs.items.find(row => row.id === ownerTabId);
  const textOwner = JSON.stringify([pane, ownerTabId, tab?.workspaceId, tab?.workspaceRoot]);
  const controls = useMemo(() => createComposerControls(application, { ownerTabId, pane }), [application, ownerTabId, pane]);
  const services = useMemo(() => ({ slots, ownerTabId, presentation: application.composerPresentation, view: presentation, catalog: application.catalog, settingsControls: application.settingsControls }), [application, slots, ownerTabId, presentation]);
  const [, redraw] = useReducer(value => value + 1, 0);
  const attachments = useMemo(() => createApplicationComposerAttachments(application, () => redraw()), [application]);
  useEffect(() => { attachments.resume(); return () => attachments.dispose(); }, [attachments]);
  const [localMenu, setLocalMenu] = useState<"workspace" | "access" | "branch" | null>(null);
  const menu = presentation.runtimeMenu ?? localMenu;
  const mounted = useRef(true);
  useLayoutEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const setMenu = (next: "workspace" | "access" | "branch" | "model" | null) => {
    // A retired shell's pending checkout must not dismiss its replacement's picker.
    if (!mounted.current) return;
    application.composerPresentation.setRuntimeMenu(ownerTabId, next === "model" ? "model" : null);
    setLocalMenu(next === "model" ? null : next);
  };
  const [filter, setFilter] = useState("");
  const contentRef = useRef<HTMLDivElement>(null);
  const runtimeRef = useRef<HTMLDivElement>(null), workspaceRef = useRef<HTMLDivElement>(null), accessRef = useRef<HTMLDivElement>(null);
  const run = useCallback((result: Promise<unknown>) => { void result.catch(reportPresentedNavigationError); }, []);
  const pendingText = useRef<{ owner: string; text: string } | undefined>(undefined);
  const [deferredSubmission, setDeferredSubmission] = useState<{
    application: ApplicationServiceV1; owner: string; mode: "queue" | "steer";
    text?: string; contentParts?: MessageContentPart[]; expectedText: string; attachments: string; context: string;
  }>();
  const submitAfterRebind = useRef<{ application: ApplicationServiceV1; context: string;
    invoke(mode: "queue" | "steer", text?: string, parts?: MessageContentPart[]): Promise<boolean> } | undefined>(undefined);
  const setText = useCallback((text: string) => {
    const edit = { owner: textOwner, text };
    pendingText.current = edit;
    void controls.setText(text).then(() => {
      if (pendingText.current === edit) pendingText.current = undefined;
    }, error => {
      if (!isPluginLifecycleCancellation(error)) {
        if (pendingText.current === edit) pendingText.current = undefined;
        reportPresentedNavigationError(error);
      }
    });
  }, [controls, textOwner]);
  useEffect(() => {
    const edit = pendingText.current;
    if (!edit) return;
    // Only replay text into the same draft after its application rebinds.
    if (edit.owner !== textOwner) { pendingText.current = undefined; return; }
    setText(edit.text);
  }, [setText, textOwner]);
  useEffect(() => {
    if (!deferredSubmission) return;
    if (deferredSubmission.owner !== textOwner) { setDeferredSubmission(undefined); return; }
    if (deferredSubmission.application === application) return;
    const target = submitAfterRebind.current;
    if (!target || target.application !== application) return;
    setDeferredSubmission(undefined);
    const current = view.drafts[pane];
    const text = pendingText.current?.owner === textOwner ? pendingText.current.text
      : contentRef.current?.querySelector("textarea")?.value ?? current.text;
    const attachments = JSON.stringify([current.imageIds, current.fileIds, current.selections]);
    // Only a request proven not to have started may be retried, and only while
    // its original draft still owns the exact input the user chose to send.
    if (text !== deferredSubmission.expectedText || attachments !== deferredSubmission.attachments || target.context !== deferredSubmission.context) {
      showApplicationActionError(new Error(t("composer.sendFailedDraftRetained")));
      return;
    }
    void target.invoke(deferredSubmission.mode, deferredSubmission.text, deferredSubmission.contentParts)
      .then(accepted => { if (!accepted) showApplicationActionError(new Error(t("composer.sendFailedDraftRetained"))); })
      .catch(showApplicationActionError);
  }, [application, deferredSubmission, pane, t, textOwner, view]);
  useEffect(() => { run(controls.refreshEngines()); }, [controls, run, liveRuntime?.engineSelectionPending]);
  const presentationConfiguration = useMemo(() => structuredClone(presentation.configuration), [presentation.configuration]);
  const draft = view.drafts[pane];
  if (!runtime || !tab || draft.ownerTabId !== ownerTabId) return null;
  const Input = view.drafts.split ? SplitPaneComposer : Composer;
  const selection = runtime.selection;
  const context: RuntimeContext = tab.workspaceId ? { kind: "project", project_id: tab.workspaceId, cwd: tab.workspaceRoot } : { kind: "no_project", cwd: tab.workspaceRoot };
  const projects = view.workspaces.workspaces.map(workspace => ({ id: workspace.id, name: workspace.name, path: workspace.root, missing: !workspace.available }));
  const resources = new Map(view.drafts.attachments.map(row => [row.id, row]));
  const images = (ids: readonly string[]) => ids.flatMap(id => resources.has(id) ? [attachments.read(resources.get(id)!) as ComposerImage] : []);
  const files = (ids: readonly string[]) => ids.flatMap(id => resources.has(id) ? [attachments.read(resources.get(id)!) as ComposerFile] : []);
  const pending = view.composerPending.find(row => row.threadId === (presentation.threadId ?? ownerTabId));
  const retained = view.submission.operations.filter(row => row.draftPending);
  const draftPending = retained.some(row => row.ownerTabId === ownerTabId || row.threadId && row.threadId === presentation.threadId);
  const queued = (rows: readonly ApplicationPendingComposerMessageV1[] = []): QueuedComposerMessage[] => rows.filter(row => !retained.some(operation => operation.messageID === row.id)).map(row => ({ ...row, images: images(row.imageIds), files: files(row.fileIds) }));
  const visibleResources = [...draft.imageIds, ...draft.fileIds, ...(pending?.queued ?? []).flatMap(row => [...row.imageIds, ...row.fileIds]), ...(pending?.guides ?? []).flatMap(row => [...row.imageIds, ...row.fileIds])];
  attachments.retainVisible(visibleResources);
  const failedSend = view.submission.operations.find(row => row.mode && row.phase === "failed" && row.error
    && (row.ownerTabId === ownerTabId || Boolean(presentation.threadId && row.threadId === presentation.threadId)));
  const creation = view.submission.pendingCreations.find(row => row.ownerTabId === ownerTabId);
  const stop = view.submission.stopRequests.find(row => row.threadId === presentation.threadId);
  const editingSubmitting = Boolean(view.conversationActions.editing?.threadId === presentation.threadId && view.conversationActions.editing?.submitting);
  const running = Boolean(creation || editingSubmitting || !presentation.readOnly && presentation.turnRunning);
  const configuration = presentationConfiguration && { ...presentationConfiguration, provider: presentation.running ? presentationConfiguration.provider : selection.provider, model: presentation.running ? presentationConfiguration.model : selection.model,
    effort: selection.effort, variant: selection.variant, speed: selection.speed,
    permissions: { mode: selection.permissionMode, approve_for_me: selection.approveForMe } } as ComposerConfiguration;
  const offer = view.conversationActions.questions.find(row => row.thread_id === presentation.threadId && row.mode === "offer");
  const document = variant === "document" ? view.previewWorkspace.tabs.find(row => row.id === view.previewWorkspace.activeTabId) : undefined;
  const submissionContext = JSON.stringify([offer?.request_id, document?.kind === "file" ? ["file", document.path]
    : document?.kind === "artifact" ? ["artifact", document.artifact.id, document.threadID, document.artifact.sha256] : null]);
  const submit = async (mode: "queue" | "steer", text?: string, contentParts?: MessageContentPart[]) => {
    const edit = pendingText.current;
    try {
      const result = await controls.submit(mode, { text, contentParts, consumedOfferID: offer?.request_id,
        ...(document?.kind === "file" ? { activeDocument: { path: document.path } } : {}),
        ...(document?.kind === "artifact" ? { artifact: { id: document.artifact.id, threadId: document.threadID, sha: document.artifact.sha256 ?? "" } } : {}) });
      if (result.accepted && pendingText.current === edit) pendingText.current = undefined;
      return result.accepted;
    } catch (error) {
      if (!(error instanceof PluginActionNotStartedError)) throw error;
      setDeferredSubmission({ application, owner: textOwner, mode, text, contentParts,
        expectedText: text ?? draft.text, attachments: JSON.stringify([draft.imageIds, draft.fileIds, draft.selections]), context: submissionContext });
      return false;
    }
  };
  submitAfterRebind.current = { application, context: submissionContext, invoke: submit };
  const toggle = (next: typeof menu) => setMenu(menu === next ? null : next);
  const selectWorkspace = (workspaceId?: string) => {
    // Picking a destination closes presentation now; provider retirement may
    // cancel the original request after the new workspace has committed.
    setMenu(null); setFilter("");
    run(presentNavigation(() => application.invoke("workspace.retarget-draft", workspaceId ? { workspaceId } : {})));
  };
  const compactReason = !presentation.threadId ? t("app.openConversationFirst") : selection.engine !== "wuu" ? t("slash.compact.externalEngineUnavailable") : undefined;
  const question = offer ? <UserQuestionCard key={offer.request_id} request={structuredClone(offer) as UserQuestionRequest}
    onAnswer={answer => application.invoke("question.answer", { requestId: offer.request_id, answer })}
    onCancel={() => application.invoke("question.cancel", { requestId: offer.request_id })}
    onHold={() => application.invoke("question.hold", { requestId: offer.request_id })}
    onCustom={async text => { if (text?.trim()) await application.invoke("question.send-offer", { requestId: offer.request_id, text }); else focusComposerTextarea(contentRef.current?.querySelector("textarea") ?? null); }} /> : undefined;
  const failedDraftTabID = view.submission.failedDraftTabIds.find(id => view.tabs.items.some(tab => tab.id === id));
  const recoveryNotice = visible && !switching && view.tabs.activePane === pane && failedDraftTabID ?
    <DraftRecoveryNotice interrupted={view.drafts.recoveryNotices.some(notice => notice.ownerTabId === failedDraftTabID)} onOpen={() => {
      void presentNavigation(() => application.invoke("tab.select", { tabId: failedDraftTabID })).catch(reportPresentedNavigationError);
    }} /> : null;
  return <ArtifactsProvider application={application}><ImagePreviewProvider><SettingsControlsProvider controls={application.settingsControls}><ComposerServicesProvider value={services}><FileSelectionProvider application={application} source={{ ownerTabId }}><div ref={contentRef} hidden={!visible} inert={!visible} style={{ display: visible ? "contents" : "none" }}>
    <Input workspaceId={tab.workspaceId} workspaceRoot={runtime.cwd} turnId={presentation.turnId}
      queuedCount={queued(pending?.queued).length} pendingCount={queued(pending?.queued).length + queued(pending?.guides).length}
      variant={variant} mainConversation containerRef={containerRef} topAccessory={question} statusAccessory={recoveryNotice}
      runtimeControlsTarget={visible ? runtimeControlsTarget : undefined}
      canSelectWorkspace={!presentation.threadId && !creation} permissionLocked={runtime.permissionLocked}
      placeholder={tab.project && tab.kind === "draft" ? t("projects.draftPlaceholder") : undefined}
      prompt={draft.text} promptRevision={draft.revision} setPrompt={setText}
      files={files(draft.fileIds)} images={images(draft.imageIds)} selections={structuredClone(draft.selections) as ResponseSelection[]}
      onChangeSelection={selection => run(controls.setSelections(draft.selections.map(row => row.id === selection.id ? selection : row)))}
      onRemoveSelection={id => run(controls.setSelections(draft.selections.filter(row => row.id !== id)))}
      queuedMessages={queued(pending?.queued)} guideMessages={queued(pending?.guides)} running={running}
      sendDisabled={!view.tabs.canSubmit || !runtime.available || runtime.engineSelectionPending || Boolean(stop)}
      clearOnSubmit={false} draftPending={draftPending}
      submitting={editingSubmitting || view.submission.operations.some(row => (row.ownerTabId === ownerTabId || Boolean(presentation.threadId && row.threadId === presentation.threadId)) && (row.phase === "preparing" || row.phase === "sending"))}
      continuation={threadContinuationTarget(view.conversationWorkspace.threads.find(thread => thread.id === presentation.threadId), application.invoke)}
      stopState={stop?.phase} runtimeControlsDisabled={Boolean(creation || editingSubmitting || runtime.busy)}
      telemetryTurnID={presentation.turnId} contextUsage={presentation.contextUsage} status={presentation.status}
      readOnly={presentation.readOnly} interactionDisabled={switching} initialized={configuration}
      engines={structuredClone(runtime.engines) as EngineInfo[]} activeEngine={selection.engine === "wuu" ? "" : selection.engine}
      engineLocked={runtime.engineLocked} engineModel={selection.model} engineEffort={selection.effort} engineSpeed={selection.speed} engineModelOptions={{ ...selection.modelOptions }}
      onSelectSpeed={async speed => (await controls.chooseRuntime({ kind: "speed", speed })).accepted}
      onSelectEngine={engine => run(controls.chooseRuntime({ kind: "engine", engine }))}
      onSelectEngineModel={async (model, effort) => (await controls.chooseRuntime({ kind: "native-model", model, effort })).accepted}
      onSelectEngineEffort={async effort => (await controls.chooseRuntime({ kind: "native-effort", effort })).accepted}
      onSelectEngineModelOptions={async modelOptions => (await controls.chooseRuntime({ kind: "native-options", modelOptions })).accepted}
      gitStatus={environment.status} branchPickerPending={branchPickerPending} branchPickerDisabled={!environment.capabilities.checkout || Boolean(presentation.threadId && environment.actionBusy && (!environmentPending || sameEnvironment))}
      worktree={!presentation.threadId && runtime.worktree.available ? { enabled: runtime.worktree.enabled, startBranch: runtime.worktree.startBranch,
        onSelectStartBranch: branch => { run(controls.chooseWorktree(true, branch)); setMenu(null); } } : undefined}
      projects={projects} activeContext={context} inlineCompletionWorkspace={{ ...context, cwd: runtime.cwd }} activeWorkspace={projects.find(row => row.id === tab.workspaceId)}
      compactDisabledReason={compactReason} handoffDisabledReason={compactReason} sideThreadDisabledReason={!presentation.threadId ? t("app.sendMessageFirst") : undefined}
      codexModels={{ loading: runtime.providerModelsLoading, error: runtime.providerModelsError, models: runtime.providerModels.map(model => ({ slug: model.id, display_name: model.label, supported_reasoning: [...model.efforts], default_reasoning_level: model.defaultEffort, supported_in_api: model.supportedInAPI })) }}
      codexRuntimeMenu={menu === "model" ? "model" : null} codexRuntimeRef={runtimeRef}
      menuOpen={menu === "workspace"} accessMenuOpen={menu === "access"} branchMenuOpen={menu === "branch"} menuRef={workspaceRef} accessMenuRef={accessRef}
      workspaceFilter={filter} setWorkspaceFilter={setFilter} onToggleMenu={() => toggle("workspace")} onToggleAccessMenu={() => toggle("access")}
      onToggleBranchMenu={() => { application.environment.scheduleStatusRefresh(0); toggle("branch"); }}
      onToggleCodexRuntimeMenu={() => { toggle("model"); run(controls.refreshEngines()); run(controls.refreshProvider(selection.provider)); }}
      onSelectRuntimeModel={async (provider, model, variant) => (await controls.chooseRuntime({ kind: "provider-model", provider, model, variant })).accepted}
      onSelectRuntimeEffort={async variant => (await controls.chooseRuntime({ kind: "effort", variant })).accepted}
      onSelectPermissionMode={(mode, approveForMe) => { run(controls.choosePermission(mode, approveForMe)); setMenu(null); }}
      onOpenSettings={() => run(application.invoke("settings.open", { pageId: "providers" }))}
      onOpenSkillsCatalog={() => run(application.invoke("catalog.select-section", { section: "skills" }).then(() => application.invoke("navigation.show-section", { section: "catalog" })))}
      onSelectWorkspace={selectWorkspace} onSelectNoProject={() => selectWorkspace()}
      onCreateWorkspace={() => { setMenu(null); run(application.invoke("workspace.create", { kind: "blank" })); }} onOpenWorkspace={() => { setMenu(null); run(application.invoke("workspace.create", { kind: "folder" })); }}
      onOpenGitWorktree={async path => { setMenu(null); await application.invoke("workspace.open-worktree", { path }); }}
      onSelectGitBranch={async branch => { await application.environment.checkoutBranch(branch); setMenu(null); }} onCreateGitBranch={async branch => { await application.environment.createAndCheckoutBranch(branch); setMenu(null); }}
      onStartNewThread={() => run(presentNavigation(() => application.invoke("draft.start", {})).then(({ tabId }) => application.invoke("navigation.focus-composer", { tabId })))}
      onHandoffSession={presentation.threadId ? input => run(application.invoke("conversation.handoff", { threadId: presentation.threadId!, ...input })) : undefined}
      onOpenSideThread={presentation.threadId ? (prompt, commandId) => run(application.invoke("side.open", { parentThreadId: presentation.threadId!, prompt, commandId })) : undefined}
      onOpenWorkspaceTool={toolId => run(application.invoke("preview.open-tool", { toolId }))}
      onOpenContextComposition={presentation.threadId ? () => run(application.invoke("conversation.open-context", { threadId: presentation.threadId! })) : undefined}
      onCompactContext={presentation.threadId ? () => run(application.invoke("conversation.compact", { threadId: presentation.threadId! })) : undefined}
      onOpenInstructions={presentation.threadId ? () => run(application.invoke("conversation.open-instructions", { threadId: presentation.threadId! })) : undefined}
      onPasteAttachmentFiles={values => run(controls.attach(values).then(results => { for (const result of results) if (result.status === "rejected") showApplicationActionError(result.reason); }))}
      onRemoveFile={id => run(controls.removeAttachment(id))} onRemoveImage={id => run(controls.removeAttachment(id))}
      onRemoveQueuedMessage={messageId => run(application.invoke("composer.remove-pending", { ownerTabId, messageId, kind: "queue" }))}
      onRemoveGuideMessage={messageId => run(application.invoke("composer.remove-pending", { ownerTabId, messageId, kind: "guide" }))}
      onGuideQueuedMessage={messageId => { if (!stop) run(application.invoke("composer.toggle-pending-mode", { ownerTabId, messageId })); }}
      onEditQueuedMessage={messageId => run(application.invoke("composer.edit-pending", { ownerTabId, messageId, kind: "queue" }))}
      onEditGuideMessage={messageId => run(application.invoke("composer.edit-pending", { ownerTabId, messageId, kind: "guide" }))}
      onSend={(text, parts) => submit("queue", text, parts)} onSteer={presentation.running && presentation.canSteer ? (text, parts) => submit("steer", text, parts) : undefined}
      onQueue={creation || presentation.running ? (text, parts) => submit("queue", text, parts) : undefined} onInterrupt={() => run(controls.stop())}
      queryHistorySessionID={presentation.threadId ?? ownerTabId} skillThreadID={presentation.threadId} threadId={presentation.threadId}
      queryHistory={[...presentation.queryHistory]} requestedHandoffIntent={presentation.requestedHandoffIntent} />
    <SubmissionFailureNotice ownerTabId={ownerTabId} operationID={failedSend?.operationID} sourceOwnerTabId={failedSend?.ownerTabId}
      visible={visible && !switching && view.tabs.activePane === pane && view.submission.failedDraftTabIds.length === 0}
      onRetry={() => run(submit(failedSend?.mode === "steer" ? "steer" : "queue"))} />
  </div></FileSelectionProvider></ComposerServicesProvider></SettingsControlsProvider></ImagePreviewProvider></ArtifactsProvider>;
}

function SubmissionFailureNotice({ ownerTabId, operationID, sourceOwnerTabId, visible, onRetry }: {
  ownerTabId: string; operationID?: string; sourceOwnerTabId?: string; visible: boolean; onRetry(): void;
}): JSX.Element | null {
  const { t } = useI18n();
  const observed = useRef({ ownerTabId, operationID });
  const [announcedID, setAnnouncedID] = useState<string>();
  useEffect(() => {
    const navigated = observed.current.ownerTabId !== ownerTabId && observed.current.ownerTabId !== sourceOwnerTabId;
    const newFailure = operationID && operationID !== observed.current.operationID;
    observed.current = { ownerTabId, operationID };
    // This is feedback for the visible action, not a banner replayed from
    // retained history when a conversation or hidden pane is revisited.
    // Binding the same draft to its newly created thread is not navigation.
    if (!visible || navigated || !operationID) setAnnouncedID(undefined);
    else if (newFailure) setAnnouncedID(operationID);
  }, [ownerTabId, operationID, sourceOwnerTabId, visible]);
  if (!visible || observed.current.ownerTabId !== ownerTabId || !operationID || announcedID !== operationID) return null;
  return <ScopedToast message={t("composer.sendFailedDraftRetained")} tone="error" dedupeKey={`submission:${ownerTabId}:${operationID}`}
    action={{ label: t("appState.retryAction"), onClick: () => { setAnnouncedID(undefined); onRetry(); } }} />;

}

export function activatePresentation(api: ApplicationPresentationAPI): void {
  api.registerUISlot({ id: "composer.above", cardinality: "many" });
  api.registerUISlot({ id: "composer.toolbar", cardinality: "many" });
  api.registerUIContribution({ id: "ui.composer", target: "wuu.default-shell/application.composer", render: ({ application, slots, context }: UISlotRenderProps) =>
    <DefaultComposerSurface application={application} slots={slots} {...context as DefaultComposerLayout} /> });
}

// The legacy entry remains available to standalone authoring hosts.
export const activate = activatePresentation;
