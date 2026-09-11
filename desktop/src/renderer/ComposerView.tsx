import {
  ChevronDown,
  ChevronUp,
  Folder,
  FolderOpen,
  FolderX,
  Send,
  Square
} from "lucide-react";
import {
  type ClipboardEvent as ReactClipboardEvent,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type Ref,
  type RefObject,
  type ReactNode,
  forwardRef,
  memo,
  startTransition,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore
} from "react";
import type {
  DesktopProject,
  EngineInfo,
  GitStatusResult,
  InitializeResult,
  MessageContentPart,
  RuntimeContext,
  SkillSummary
} from "../shared/protocol";
import {
  buildComposerSlashCommands,
  composerSlashPrompt,
  filterComposerSlashCommands,
  firstEnabledSlashCommandIndex,
  isComposerTextComposing,
  nextEnabledSlashCommandIndex,
  parseComposerSlashDraft,
  runtimeFastModelTarget,
  type ComposerSlashCommand,
  type ComposerSlashDraft
} from "./ComposerSlashCommands";
import {
  emptyHandoffDraft,
  canSubmitHandoffDraft,
  handoffPromptFromIntent,
  handoffPromptFromSelection,
  reduceHandoffDraft,
  type HandoffCatalog,
  type HandoffDraft,
} from "./HandoffDraft";
import { translateCurrent as translate, useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import {
  CollapsedComposerPromptCard,
  useCollapsedComposerPrompt
} from "./ComposerCollapsedPrompt";
import {
  WORKSPACE_FILE_DRAG_MIME,
  appendWorkspacePathToPrompt,
  type ComposerFile,
  type ComposerImage,
  type QueuedComposerMessage
} from "./ComposerMessages";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { ComposerContextMenu } from "./ComposerContextMenu";
import { ComposerAttachmentStrip, ComposerQueueStrip } from "./ComposerInputSections";
import { ComposerCameraPanel } from "./ComposerCamera";
import { WorkspaceDocumentDrawerContext } from "./WorkspaceDocumentTurnDock";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";
import type { PluginHost } from "./plugins/PluginHost";
import { PluginSlot } from "./plugins/PluginSlot";
import { ComposerPluginToolbar } from "./plugins/ComposerPluginToolbar";
import {
  AccessMenu,
  ComposerPlusButton,
  ProjectPickerMenu,
  RuntimePicker,
  RuntimeModelMenu,
  SlashCommandIcon,
  permissionModeFromSummary,
  permissionModeOption
} from "./ComposerRuntimeMenus";
import { useComposerQueryHistory } from "./ComposerQueryHistory";
import type {
  CodexModelLoadState,
  CodexRuntimeMenu,
  ComposerVariant,
  PermissionMode
} from "./ComposerTypes";
import { composerStatusIsLiveProgress, composerStatusText } from "./ComposerTypes";
import type { WorkspacePanelView } from "./WorkspacePanels";
import { ComposerRuntimeMeters } from "./ComposerRuntimeMeters";
import { ComposerPresentation } from "./plugins/ComposerPresentation";
import { ComposerVoiceInput, type ComposerVoiceInputHandle } from "./ComposerVoiceInput";
import { ENABLE_VOICE_INPUT } from "./FeatureFlags";
import { focusComposerTextarea, isTouchWebShell } from "./ComposerFocus";
import { useWorkbenchConnected } from "./WorkbenchConnectionContext";
import type { TurnContextUsage } from "./AppState";

const MemoizedComposerPluginSlot = memo(PluginSlot);
const MemoizedComposerPluginToolbar = memo(ComposerPluginToolbar);

type ExpandedComposerDrawer = "pending" | null;

export type {
  CodexModelLoadState,
  CodexRuntimeMenu,
  ComposerVariant,
  FloatingMenuOwner,
  FloatingMenuPlacement,
  FloatingMenuAlign,
  PermissionMode
} from "./ComposerTypes";
export { FloatingMenuPortal, isInsideFloatingMenu } from "./ComposerFloatingMenu";
export { ComposerAttachmentStrip, SplitPaneComposer } from "./ComposerInputSections";
export { permissionModeFromSummary, permissionModeHasAdvancedOverrides } from "./ComposerRuntimeMenus";

export function Composer({
  variant = "dock",
  canSelectProject = variant === "hero",
  mainConversation = false,
  topAccessory,
  leadingActions,
  containerRef,
  prompt: committedPrompt,
  promptRevision = 0,
  setPrompt: commitPrompt,
  files,
  images,
  queuedMessages,
  guideMessages,
  running,
  sendDisabled = false,
  forceStopWhileRunning = false,
  runtimeControlsDisabled = running,
  status,
  statusLiveProgress,
  readOnly,
  initialized,
  projects,
  activeContext,
  activeProject,
  compactDisabledReason,
  sideThreadDisabledReason,
  handoffDisabledReason,
  codexModels,
  codexRuntimeMenu,
  codexRuntimeRef,
  menuOpen,
  accessMenuOpen,
  menuRef,
  accessMenuRef,
  projectFilter,
  setProjectFilter,
  onToggleMenu,
  onToggleAccessMenu,
  onToggleCodexRuntimeMenu,
  onSelectRuntimeModel,
  onSelectRuntimeEffort,
  engines,
  activeEngine,
  engineLocked,
  engineModel,
  engineEffort,
  onSelectEngine,
  onSelectEngineModel,
  onSelectEngineEffort,
  onSelectPermissionMode,
  onOpenSettings,
  onOpenSkillsCatalog,
  onSelectProject,
  onSelectNoProject,
  onCreateProject,
  onOpenProject,
  onStartNewThread,
  onHandoffSession,
  onOpenSideThread,
  onOpenWorkspaceTool,
  onOpenContextComposition = () => {},
  onCompactContext = () => {},
  onOpenInstructions = () => {},
  onPasteAttachmentFiles,
  onRemoveFile,
  onRemoveImage,
  onRemoveQueuedMessage,
  onRemoveGuideMessage,
  onGuideQueuedMessage,
  onEditQueuedMessage,
  onEditGuideMessage,
  onSend,
  onSteer,
  onQueue,
  onInterrupt,
  telemetryTurnID,
  tokensPerSecond = 0,
  tokenSpeedSampledAt,
  tokenSpeedSource,
  contextUsage,
  queryHistorySessionID,
  queryHistory = [],
  requestedHandoffIntent,
  hideRuntimeControls = false,
  hidePlusButton = false,
  hidePermissionControl = false,
  hideExpandButton = false,
  placeholder,
  maxLength,
  textOnly = false,
  slashCommandsEnabled = true,
  slashCommandsOverride,
  onResetSideThread,
  pluginHost = desktopPluginHost,
}: {
  variant?: ComposerVariant;
  canSelectProject?: boolean;
  mainConversation?: boolean;
  topAccessory?: ReactNode;
  leadingActions?: ReactNode;
  containerRef?: Ref<HTMLElement>;
  prompt: string;
  // Changes only for programmatic clear/restore operations. This lets the
  // editor accept an external empty string even if App's delayed snapshot was
  // already empty while the user still had newer local text.
  promptRevision?: number;
  setPrompt: (value: string) => void;
  files: ComposerFile[];
  images: ComposerImage[];
  queuedMessages: QueuedComposerMessage[];
  guideMessages: QueuedComposerMessage[];
  running: boolean;
  sendDisabled?: boolean;
  forceStopWhileRunning?: boolean;
  runtimeControlsDisabled?: boolean;
  status: string;
  statusLiveProgress?: boolean;
  readOnly: boolean;
  initialized?: InitializeResult;
  gitStatus?: GitStatusResult;
  projects: DesktopProject[];
  activeContext?: RuntimeContext;
  activeProject?: DesktopProject;
  compactDisabledReason?: string;
  sideThreadDisabledReason?: string;
  handoffDisabledReason?: string;
  codexModels: CodexModelLoadState;
  codexRuntimeMenu: CodexRuntimeMenu;
  codexRuntimeRef: RefObject<HTMLDivElement | null>;
  menuOpen: boolean;
  accessMenuOpen: boolean;
  branchMenuOpen: boolean;
  menuRef: RefObject<HTMLDivElement | null>;
  accessMenuRef: RefObject<HTMLDivElement | null>;
  projectFilter: string;
  setProjectFilter: (value: string) => void;
  onToggleMenu: () => void;
  onToggleAccessMenu: () => void;
  onToggleCodexRuntimeMenu: (menu: Exclude<CodexRuntimeMenu, null>) => void;
  onSelectRuntimeModel: (provider: string, model: string, variant?: string) => void | Promise<boolean>;
  onSelectRuntimeEffort: (variant: string) => void | Promise<boolean>;
  engines?: EngineInfo[];
  activeEngine?: string;
  engineLocked?: boolean;
  engineModel?: string;
  engineEffort?: string;
  onSelectEngine?: (id: string) => void;
  onSelectEngineModel?: (model: string, effort: string) => void;
  onSelectEngineEffort?: (effort: string) => void;
  onSelectPermissionMode: (mode: PermissionMode) => void;
  onToggleBranchMenu: () => void;
  onOpenSettings: () => void;
  onOpenSkillsCatalog: () => void;
  onSelectProject: (id: string) => void;
  onSelectNoProject: () => void;
  onSelectGitBranch: (branch: string) => void;
  onCreateProject: () => void;
  onOpenProject: () => void;
  onStartNewThread: () => void;
  onHandoffSession?: (input: { provider: string; model: string; effort?: string; intent: string }) => void;
  // Open or focus the side thread attached to the active main conversation.
  // When prompt is non-empty, also send it as the side-thread query.
  onOpenSideThread?: (prompt?: string) => void;
  onOpenWorkspaceTool: (view: WorkspacePanelView) => void;
  onOpenContextComposition?: () => void;
  onCompactContext?: () => void;
  onOpenInstructions?: () => void;
  onPasteAttachmentFiles: (files: File[]) => void;
  onRemoveFile: (id: string) => void;
  onRemoveImage: (id: string) => void;
  onRemoveQueuedMessage: (id: string) => void;
  onRemoveGuideMessage: (id: string) => void;
  onGuideQueuedMessage: (id: string) => void;
  onEditQueuedMessage: (id: string) => void;
  onEditGuideMessage: (id: string) => void;
  onSend: (promptOverride?: string, contentParts?: MessageContentPart[]) => void;
  onSteer?: (promptOverride?: string, contentParts?: MessageContentPart[]) => void;
  onQueue?: (promptOverride?: string, contentParts?: MessageContentPart[]) => void;
  onInterrupt: () => void;
  telemetryTurnID?: string;
  tokensPerSecond?: number;
  tokenSpeedSampledAt?: number;
  tokenSpeedSource?: "real" | "estimated" | "none";
  // contextUsage drives the model-adjacent context meter. When the active
  // model has no catalog window yet the AppState selector returns
  // undefined and the meter hides entirely.
  contextUsage?: TurnContextUsage | null;
  queryHistorySessionID?: string;
  queryHistory?: string[];
  requestedHandoffIntent?: string;
  // Suppress the model/context/token runtime chrome on the bar's right edge.
  // Side-thread composers reuse this input without a separate runtime picker.
  hideRuntimeControls?: boolean;
  hidePlusButton?: boolean;
  hidePermissionControl?: boolean;
  hideExpandButton?: boolean;
  // A shared composer can be embedded in a conversation surface whose
  // transport accepts text only. The editor, keyboard handling, expansion,
  // context menu, and send/stop controls remain the canonical Composer; only
  // unsupported attachment, slash-command, and permission affordances
  // are removed.
  textOnly?: boolean;
  // Some shared composer surfaces accept attachments and rich input but do
  // not own the main-conversation runtime commands (for example Channels).
  slashCommandsEnabled?: boolean;
  placeholder?: string;
  maxLength?: number;
  // Replaces the built-in main-conversation command list with a
  // surface-specific one (e.g. the side chat's /reset). The menu, keyboard
  // handling, and action dispatch stay the canonical Composer machinery, so
  // providing an override re-enables slash input even in textOnly mode.
  slashCommandsOverride?: ComposerSlashCommand[];
  // Reset the side thread this composer is embedded in.
  onResetSideThread?: () => void;
  pluginHost?: PluginHost;
}): JSX.Element {
  const { locale, t } = useI18n();
  const workbenchConnected = useWorkbenchConnected();
  const disconnected = !workbenchConnected;
  // A disconnected remote workbench keeps every composer draft editable (the
  // textarea stays enabled and focused), but sending and runtime controls must
  // not dispatch into a dead bridge. Gate only the action paths, never readOnly.
  const effectiveSendDisabled = sendDisabled || !workbenchConnected;
  const effectiveRuntimeControlsDisabled = runtimeControlsDisabled || !workbenchConnected;
  // Keep the controlled textarea on a small, synchronous state path. The
  // canonical draft still lives above Composer, but updating it makes App
  // recalculate the whole desktop shell. Marking that propagation as a
  // transition lets React yield to subsequent keystrokes instead of putting
  // the shell's render cost directly on the input event.
  const [prompt, setLocalPrompt] = useState(committedPrompt);
  const [lastCommittedPrompt, setLastCommittedPrompt] = useState(committedPrompt);
  const [lastPromptRevision, setLastPromptRevision] = useState(promptRevision);
  const optimisticPromptQueueRef = useRef<string[]>([]);
  // Parent draft clearing crosses an async/transition boundary. Close the
  // interval where two gestures can still read the same rendered draft, while
  // allowing a real input change (even to the same eventual text) to re-arm it.
  const submittedDraftSignatureRef = useRef<string | null>(null);
  if (promptRevision !== lastPromptRevision) {
    setLastPromptRevision(promptRevision);
    setLastCommittedPrompt(committedPrompt);
    optimisticPromptQueueRef.current.length = 0;
    submittedDraftSignatureRef.current = null;
    setLocalPrompt(committedPrompt);
  } else if (committedPrompt !== lastCommittedPrompt) {
    setLastCommittedPrompt(committedPrompt);
    const optimisticPromptIndex = optimisticPromptQueueRef.current.lastIndexOf(committedPrompt);
    if (optimisticPromptIndex >= 0) {
      // Parent draft updates run in a transition, so an earlier value can be
      // committed after the textarea has already accepted more keystrokes.
      // Re-applying that stale echo changes the controlled DOM value and makes
      // Chromium terminate an active CJK composition. Acknowledge echoes
      // without replacing the newer local value.
      optimisticPromptQueueRef.current.splice(0, optimisticPromptIndex + 1);
    } else {
      optimisticPromptQueueRef.current.length = 0;
      submittedDraftSignatureRef.current = null;
      setLocalPrompt(committedPrompt);
    }
  }
  function setPrompt(value: string): void {
    optimisticPromptQueueRef.current.length = 0;
    submittedDraftSignatureRef.current = null;
    setLocalPrompt(value);
    commitPrompt(value);
  }
  const statusText = composerStatusText(status);
  const statusIsLiveProgress = composerStatusIsLiveProgress(statusLiveProgress);
  const className = `composer-wrap ${
    variant === "hero"
      ? "hero-composer-wrap"
      : variant === "document"
        ? "dock-composer-wrap document-composer-wrap"
        : "dock-composer-wrap"
  }`;
  const hasAttachments = images.length > 0 || files.length > 0;
  const hasDraft = prompt.trim().length > 0 || hasAttachments;
  const pluginTranslate = useMemo(() => t, [locale]);
  const pluginSlotContext = useMemo(() => Object.freeze({
    threadId: queryHistorySessionID,
    translate: pluginTranslate,
    variant,
    mainConversation,
    running,
    readOnly: Boolean(readOnly),
    hasDraft,
    attachmentCount: files.length + images.length,
  }), [
    files.length,
    hasDraft,
    images.length,
    mainConversation,
    queryHistorySessionID,
    readOnly,
    running,
    pluginTranslate,
    variant,
  ]);
  // The action button is a stop control ONLY while a turn runs AND the input
  // is empty. The moment there is something to send, it flips back to a send
  // button. Its submit action below deliberately follows the same steer/queue
  // decision as Enter, while preserving the stop affordance for an empty input.
  const showComposerStop = running && (forceStopWhileRunning || !hasDraft);
  const composerSendLabel = running && hasDraft && onSteer
    ? t("composer.steerSend")
    : running
      ? t("composer.queueSend")
      : t("composer.send");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const composerShellRef = useRef<HTMLDivElement>(null);
  const composerFrameRef = useRef<HTMLDivElement>(null);
  const collapsedComposerFrameHeightRef = useRef<number | null>(null);
  const attachmentInputRef = useRef<HTMLInputElement>(null);
  const photosInputRef = useRef<HTMLInputElement>(null);
  const submitAfterCompositionRef = useRef(false);
  const documentDrawer = useContext(WorkspaceDocumentDrawerContext);
  const [isComposerExpanded, setIsComposerExpanded] = useState(false);
  const [voiceRecording, setVoiceRecording] = useState(false);
  const [voiceSendPending, setVoiceSendPending] = useState(false);
  const voiceInputRef = useRef<ComposerVoiceInputHandle>(null);
  const voiceSendPendingRef = useRef(false);
  const showComposerStopAction =
    showComposerStop && !voiceRecording && !voiceSendPending;
  const voiceActionLabel =
    (voiceRecording || voiceSendPending) && running && onSteer
      ? t("composer.steerSend")
      : composerSendLabel;
  const [expandedDrawer, setExpandedDrawer] = useState<ExpandedComposerDrawer>(null);
  const [dropActive, setDropActive] = useState(false);
  const [selectedSlashIndex, setSelectedSlashIndex] = useState(0);
  const [slashDismissedValue, setSlashDismissedValue] = useState("");
  const [compositionSubmitRequest, setCompositionSubmitRequest] = useState(0);
  const [submissionClearRevision, setSubmissionClearRevision] = useState(0);
  const [cameraOpen, setCameraOpen] = useState(false);
  const mobileWeb = useMemo(() => isTouchWebShell(), []);
  useEffect(() => {
    setCameraOpen(false);
  }, [readOnly, textOnly, queryHistorySessionID]);
  const hasPendingMessages = guideMessages.length > 0 || queuedMessages.length > 0;
  const hasHeldMessages = [...guideMessages, ...queuedMessages].some((message) => message.held);
  const previousHeldMessagesRef = useRef(false);
  const previousDrawerSessionRef = useRef(queryHistorySessionID);

  useEffect(() => {
    if (previousDrawerSessionRef.current === queryHistorySessionID) {
      return;
    }
    previousDrawerSessionRef.current = queryHistorySessionID;
    previousHeldMessagesRef.current = false;
    setExpandedDrawer(null);
  }, [queryHistorySessionID]);

  useEffect(() => {
    if (documentDrawer?.documentResultExpanded) {
      setExpandedDrawer(null);
    }
  }, [documentDrawer?.documentResultExpanded]);

  useEffect(() => {
    if (hasHeldMessages && !previousHeldMessagesRef.current) {
      setComposerDrawer("pending");
    }
    previousHeldMessagesRef.current = hasHeldMessages;
  }, [hasHeldMessages]);

  useEffect(() => {
    setExpandedDrawer((current) => {
      if (current === "pending" && !hasPendingMessages) return null;
      return current;
    });
  }, [hasPendingMessages]);

  function setComposerDrawer(next: ExpandedComposerDrawer): void {
    setExpandedDrawer(next);
    if (next) {
      documentDrawer?.collapseDocumentResult();
    }
  }

  const [slashSkills, setSlashSkills] = useState<SkillSummary[]>([]);
  const [composerContextMenu, setComposerContextMenu] = useState<{
    x: number;
    y: number;
    hasSelection: boolean;
  } | null>(null);
  const {
    blocks: activeCollapsedPromptBlocks,
    hasBlocks: hasCollapsedPromptBlocks,
    prefix: collapsedPromptPrefix,
    visiblePrompt: visiblePromptValue,
    listRef: collapsedPromptListRef,
    handlePaste: handleCollapsedComposerPaste,
    revealBlock: revealCollapsedPromptBlock,
    removeBlock: removeCollapsedPromptBlock,
    contentPartsForPrompt: collapsedContentPartsForPrompt,
  } = useCollapsedComposerPrompt({
    prompt,
    setPrompt,
    focusComposerSoon,
    storageKey: queryHistorySessionID
  });
  const composerPlaceholder = placeholder ?? (readOnly
    ? t("composer.readOnly")
    : hasCollapsedPromptBlocks
      ? t("composer.followupChanges")
      : hasAttachments
        ? t("composer.addDescription")
        : t("composer.placeholder"));
  const slashDraft = slashCommandsEnabled && !(textOnly && !slashCommandsOverride)
    ? parseComposerSlashDraft(prompt)
    : undefined;
  const handoffCatalog = useMemo<HandoffCatalog>(() => ({
    providers: (initialized?.providers ?? []).map((provider) => ({
      id: provider.name,
      label: provider.name,
      models: (provider.models ?? []).map((model) => ({
        id: model.id,
        label: model.display_name || model.id,
      })),
      allowCustomModel: true,
    })),
  }), [initialized]);
  const [handoffRevision, setHandoffRevision] = useState(1);
  const appliedHandoffIntentRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (requestedHandoffIntent === undefined || appliedHandoffIntentRef.current === requestedHandoffIntent) {
      return;
    }
    appliedHandoffIntentRef.current = requestedHandoffIntent;
    setHandoffRevision((current) => current + 1);
    setPrompt(handoffPromptFromIntent(requestedHandoffIntent));
  }, [requestedHandoffIntent]);
  const handoffDraft = useMemo<HandoffDraft>(() => {
    if (slashDraft?.query !== "handoff") {
      return emptyHandoffDraft(handoffRevision);
    }
    return reduceHandoffDraft(slashDraft.args, handoffCatalog, handoffRevision);
  }, [handoffCatalog, handoffRevision, slashDraft]);
  const handoffMode = slashDraft?.query === "handoff";
  const handoffUnavailableReason = handoffDisabledReason
    || (running ? t("slash.taskRunning") : readOnly || effectiveSendDisabled || !onHandoffSession ? t("handoff.card.disabled") : undefined);
  const canConfirmHandoff = canSubmitHandoffDraft(handoffDraft) && !handoffUnavailableReason;
  const slashQuery = slashDraft?.query ?? "";
  const slashSkillContextKey = activeContext ? composerRuntimeContextKey(activeContext) : "";
  const slashSkillCountKey = initialized?.extension_trust?.main_session?.skills?.count ?? 0;
  const slashRuntimeReady = Boolean(activeContext && initialized);
  const subscribePluginHost = useCallback(
    (listener: () => void) => pluginHost.subscribe(listener),
    [pluginHost],
  );
  const getPluginCommands = useCallback(() => pluginHost.getCommands(), [pluginHost]);
  const registeredPluginCommands = useSyncExternalStore(
    subscribePluginHost,
    getPluginCommands,
    getPluginCommands,
  );
  const availablePluginRuntimeCommands = useMemo(
    () => new Set(registeredPluginCommands.map((command) => `${command.pluginId}:${command.id}`)),
    [registeredPluginCommands],
  );
  const builtinSlashCommands = useMemo(
    () => buildComposerSlashCommands({
      activeContext,
      initialized,
      running,
      compactDisabledReason,
      sideThreadDisabledReason,
      handoffDisabledReason,
      skills: slashSkills,
      availablePluginRuntimeCommands,
    }),
    [activeContext, availablePluginRuntimeCommands, compactDisabledReason, handoffDisabledReason, initialized, locale, running, sideThreadDisabledReason, slashSkills]
  );
  const slashCommands = slashCommandsOverride ?? builtinSlashCommands;
  const fastModelTarget = useMemo(() => runtimeFastModelTarget(initialized), [initialized]);
  const permissionMode = permissionModeFromSummary(initialized?.permissions);
  const permissionOption = permissionModeOption(permissionMode, activeEngine);
  const permissionChipLabel = permissionOption.chipLabel;
  const projectPillLabel = heroProjectPillLabel(activeContext, activeProject);
  const projectPillTitle =
    activeContext?.kind === "project" && activeProject?.path
      ? activeProject.path
      : projectPillLabel;
  const ProjectPillIcon =
    activeContext?.kind === "no_project"
      ? FolderX
      : activeContext?.kind === "project"
        ? Folder
        : FolderOpen;
  const visibleSlashCommands = useMemo(
    () => filterComposerSlashCommands(slashCommands, slashQuery),
    [slashCommands, slashQuery]
  );
  const slashMenuOpen = Boolean(
    !readOnly
    && slashDraft
    && slashDraft.query !== "handoff"
    && slashDismissedValue !== prompt
  );
  const selectedSlashCommand = slashMenuOpen ? visibleSlashCommands[selectedSlashIndex] : undefined;
  const slashMenuID = `composer-slash-commands-${variant}`;
  const { resetQueryHistoryNavigation, handleQueryHistoryKeyDown } = useComposerQueryHistory({
    disabled: readOnly || hasAttachments || hasCollapsedPromptBlocks,
    prompt,
    queryHistory,
    queryHistorySessionID,
    setPrompt,
    textareaRef
  });
  useEffect(() => {
    setSelectedSlashIndex(firstEnabledSlashCommandIndex(visibleSlashCommands));
  }, [visibleSlashCommands]);

  useEffect(() => {
    if (!slashCommandsEnabled || !slashRuntimeReady || readOnly || textOnly) {
      setSlashSkills([]);
      return;
    }
    let cancelled = false;
    void loadSlashSkills();
    return () => {
      cancelled = true;
    };

    async function loadSlashSkills(): Promise<void> {
      try {
        const result = await window.wuu.listSkills();
        if (!cancelled) {
          setSlashSkills(result.skills);
        }
      } catch {
        if (!cancelled) {
          setSlashSkills([]);
        }
      }
    }
  }, [readOnly, slashCommandsEnabled, slashRuntimeReady, slashSkillContextKey, slashSkillCountKey, textOnly]);

  useEffect(() => {
    if (readOnly) {
      setIsComposerExpanded(false);
    }
  }, [readOnly]);

  useLayoutEffect(() => {
    const frame = composerFrameRef.current;
    if (!frame) {
      return;
    }
    if (!isComposerExpanded) {
      frame.style.removeProperty("--composer-expanded-offset");
      return;
    }
    const collapsedHeight = collapsedComposerFrameHeightRef.current;
    if (!collapsedHeight) {
      frame.style.removeProperty("--composer-expanded-offset");
      return;
    }
    const expandedHeight = Math.ceil(frame.offsetHeight);
    const offset = Math.max(0, expandedHeight - collapsedHeight);
    frame.style.setProperty("--composer-expanded-offset", `${offset}px`);
  }, [
    files.length,
    guideMessages.length,
    images.length,
    isComposerExpanded,
    queuedMessages.length,
    activeCollapsedPromptBlocks.length
  ]);

  function focusComposerSoon(): void {
    focusComposerTextarea(textareaRef.current);
  }

  function focusComposerAtEndSoon(): void {
    focusComposerTextarea(textareaRef.current, "end");
  }

  function openCamera(): void {
    textareaRef.current?.blur();
    setIsComposerExpanded(false);
    setExpandedDrawer(null);
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

  function toggleComposerExpansion(): void {
    if (readOnly) {
      return;
    }
    setIsComposerExpanded((expanded) => {
      if (!expanded) {
        collapsedComposerFrameHeightRef.current = composerFrameRef.current
          ? Math.ceil(composerFrameRef.current.offsetHeight)
          : null;
      }
      return !expanded;
    });
    focusComposerSoon();
  }

  function submitComposerWith(
    onSubmit: (promptOverride?: string, contentParts?: MessageContentPart[]) => void,
    promptOverride = prompt,
  ): void {
    // A disconnected (or otherwise send-disabled) composer must not clear the
    // local draft before the bridge has a chance to reject the dispatch.
    if (effectiveSendDisabled) {
      return;
    }
    resetQueryHistoryNavigation();
    const submitSlashDraft = slashCommandsEnabled && !(textOnly && !slashCommandsOverride)
      ? parseComposerSlashDraft(promptOverride)
      : undefined;
    const actionCommand = submitSlashDraft
      ? exactActionSlashCommand(slashCommands, submitSlashDraft)
      : undefined;
    if (submitSlashDraft?.query === "handoff") {
      submitHandoffDraft();
      return;
    }
    if (actionCommand) {
      applySlashCommand(actionCommand, submitSlashDraft);
      return;
    }
    const draftSignature = JSON.stringify([
      promptOverride,
      images.map((image) => image.id),
      files.map((file) => file.id),
    ]);
    if (submittedDraftSignatureRef.current === draftSignature) {
      return;
    }
    submittedDraftSignatureRef.current = draftSignature;
    optimisticPromptQueueRef.current.length = 0;
    setLocalPrompt("");
    const contentParts = collapsedContentPartsForPrompt(promptOverride);
    if (contentParts) {
      onSubmit(promptOverride, contentParts);
    } else {
      onSubmit(promptOverride);
    }
    setSubmissionClearRevision((current) => current + 1);
    // Restore focus within the user action so software keyboards can open.
    // An already-focused editor must not blur/refocus or scroll on send.
    const textarea = textareaRef.current;
    if (textarea && document.activeElement !== textarea) {
      textarea.focus({ preventScroll: true });
    }
  }

  function submitHandoffDraft(): void {
    const provider = handoffDraft.providerId;
    const model = handoffDraft.modelId;
    const effort = handoffDraft.variant || undefined;
    if (!canConfirmHandoff) {
      return;
    }
    onHandoffSession?.({
      provider,
      model,
      effort,
      intent: handoffDraft.intent,
    });
    if (codexRuntimeMenu === "model") onToggleCodexRuntimeMenu("model");
    setPrompt("");
  }

  function submitComposer(): void {
    if (voiceRecording) {
      void stopVoiceAndSubmit();
      return;
    }
    submitDraft();
  }

  function submitDraft(promptOverride = prompt): void {
    if (handoffMode) {
      submitHandoffDraft();
      return;
    }
    const submittingHasDraft = promptOverride.trim().length > 0 || hasAttachments;
    submitComposerWith(
      running && submittingHasDraft && onSteer ? onSteer : onSend,
      promptOverride,
    );
  }

  async function stopVoiceAndSubmit(): Promise<void> {
    if (voiceSendPendingRef.current) return;
    voiceSendPendingRef.current = true;
    setVoiceSendPending(true);
    try {
      const finalPrompt = await voiceInputRef.current?.stop();
      if (!finalPrompt?.trim()) return;
      const sendFinalPrompt = running && onSteer ? onSteer : onSend;
      submitComposerWith(() => sendFinalPrompt(finalPrompt), finalPrompt);
    } finally {
      voiceSendPendingRef.current = false;
      setVoiceSendPending(false);
    }
  }

  function updateVisiblePrompt(value: string): void {
    resetQueryHistoryNavigation();
    setSlashDismissedValue("");
    submittedDraftSignatureRef.current = null;
    const nextPrompt = hasCollapsedPromptBlocks ? `${collapsedPromptPrefix}${value}` : value;
    setLocalPrompt(nextPrompt);
    optimisticPromptQueueRef.current.push(nextPrompt);
    startTransition(() => commitPrompt(nextPrompt));
  }

  function handleComposerPaste(event: ReactClipboardEvent<HTMLTextAreaElement>): void {
    handleCollapsedComposerPaste(event, {
      readOnly,
      fileAttachmentsEnabled: !textOnly,
      onPasteAttachmentFiles,
      onFold: () => {
        resetQueryHistoryNavigation();
        setSlashDismissedValue("");
      }
    });
  }

  // A drop carries either a workspace path reference (file tree drag) or
  // external files (Finder/Desktop). Path drops insert plain text and stay
  // available on text-only composers; file drops reuse the paste pipeline.
  // During dragover only dataTransfer.types is readable, so acceptance is
  // decided by MIME alone — anything else keeps its native behavior (e.g.
  // dragging selected text into the textarea).
  function composerDropAcceptsPayload(dataTransfer: DataTransfer): boolean {
    const types = Array.from(dataTransfer.types ?? []);
    if (types.includes(WORKSPACE_FILE_DRAG_MIME)) {
      return true;
    }
    return !textOnly && types.includes("Files");
  }

  function handleComposerDragOver(event: ReactDragEvent<HTMLDivElement>): void {
    if (readOnly || !composerDropAcceptsPayload(event.dataTransfer)) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setDropActive(true);
  }

  function handleComposerDragLeave(event: ReactDragEvent<HTMLDivElement>): void {
    if (event.currentTarget.contains(event.relatedTarget as Node | null)) {
      return;
    }
    setDropActive(false);
  }

  function handleComposerDrop(event: ReactDragEvent<HTMLDivElement>): void {
    setDropActive(false);
    if (readOnly) {
      return;
    }
    const workspacePath = event.dataTransfer.getData(WORKSPACE_FILE_DRAG_MIME);
    if (workspacePath) {
      event.preventDefault();
      resetQueryHistoryNavigation();
      setSlashDismissedValue("");
      setPrompt(appendWorkspacePathToPrompt(prompt, workspacePath));
      focusComposerAtEndSoon();
      return;
    }
    if (textOnly) {
      return;
    }
    const dropped = Array.from(event.dataTransfer.files ?? []);
    if (dropped.length === 0) {
      return;
    }
    event.preventDefault();
    onPasteAttachmentFiles(dropped);
  }

  function applySlashCommand(command: ComposerSlashCommand | undefined, draft: ComposerSlashDraft | undefined): void {
    if (!command || command.disabledReason) {
      return;
    }
    setSlashDismissedValue("");
    if (command.kind === "prompt" || command.kind === "skill") {
      setPrompt(composerSlashPrompt(command, draft?.args ?? ""));
      focusComposerAtEndSoon();
      return;
    }
    setPrompt("");
    switch (command.action) {
      case "new-thread":
        onStartNewThread();
        break;
      case "open-side-thread":
        onOpenSideThread?.(draft?.args.trim() || undefined);
        break;
      case "reset-side-thread":
        onResetSideThread?.();
        break;
      case "open-review":
        onOpenWorkspaceTool("review");
        break;
      case "open-skills":
        onOpenSkillsCatalog();
        break;
      case "open-files":
        onOpenWorkspaceTool("files");
        break;
      case "open-terminal":
        onOpenWorkspaceTool("terminal");
        break;
      case "open-project":
        onOpenProject();
        break;
      case "no-project":
        onSelectNoProject();
        break;
      case "context":
        onOpenContextComposition();
        break;
      case "compact":
        onCompactContext();
        break;
      case "handoff":
        setPrompt(composerSlashPrompt(command, draft?.args ?? ""));
        focusComposerAtEndSoon();
        break;
      case "instructions":
        onOpenInstructions();
        break;
      case "plugin": {
        const pluginCommand = pluginHost.getCommands().find((candidate) =>
          candidate.pluginId === command.pluginId
          && candidate.id === command.pluginCommandId);
        if (!pluginCommand) break;
        void Promise.resolve(pluginCommand.execute(Object.freeze({
          source: "slash-command",
          commandId: command.pluginCommandId,
          args: draft?.args ?? "",
          threadId: queryHistorySessionID,
          context: activeContext,
        }))).catch((error: unknown) => {
          console.error(`Plugin slash command ${command.pluginId}:${command.pluginCommandId} failed`, error);
        });
        break;
      }
      case "model":
        onToggleCodexRuntimeMenu("model");
        break;
      case "fast":
        if (fastModelTarget && !fastModelTarget.current) {
          onSelectRuntimeModel(fastModelTarget.provider, fastModelTarget.model);
        }
        break;
      case "effort":
        // Reasoning effort now lives inside the model panel (pills under the
        // selected model), so the slash command opens the same panel.
        onToggleCodexRuntimeMenu("model");
        break;
      case "settings":
        onOpenSettings();
        break;
    }
    focusComposerSoon();
  }

  function handleComposerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>): void {
    if (readOnly) {
      return;
    }
    if (isComposerTextComposing(event)) {
      return;
    }
    if (slashMenuOpen) {
      if (event.key === "Enter" && !event.shiftKey && slashDraft && exactRunnableSlashCommand(slashCommands, slashDraft)) {
        event.preventDefault();
        submitComposer();
        return;
      }
      if (event.key === "ArrowDown" && visibleSlashCommands.length > 0) {
        event.preventDefault();
        setSelectedSlashIndex((current) => nextEnabledSlashCommandIndex(visibleSlashCommands, current, 1));
        return;
      }
      if (event.key === "ArrowUp" && visibleSlashCommands.length > 0) {
        event.preventDefault();
        setSelectedSlashIndex((current) => nextEnabledSlashCommandIndex(visibleSlashCommands, current, -1));
        return;
      }
      if ((event.key === "Enter" || event.key === "Tab") && visibleSlashCommands.length > 0) {
        event.preventDefault();
        const fallbackCommand = visibleSlashCommands[firstEnabledSlashCommandIndex(visibleSlashCommands)];
        applySlashCommand(selectedSlashCommand?.disabledReason ? fallbackCommand : (selectedSlashCommand ?? fallbackCommand), slashDraft);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        setSlashDismissedValue(prompt);
        return;
      }
    }
    if (handleQueryHistoryKeyDown(event)) {
      return;
    }
    if (event.key === "Escape" && running && !effectiveSendDisabled && !event.repeat) {
      event.preventDefault();
      onInterrupt();
      return;
    }
    const currentPrompt = hasCollapsedPromptBlocks
      ? `${collapsedPromptPrefix}${event.currentTarget.value}`
      : event.currentTarget.value;
    const currentHasDraft = currentPrompt.trim().length > 0 || hasAttachments;
    if (
      event.key === "Tab" &&
      !event.shiftKey &&
      !event.metaKey &&
      !event.ctrlKey &&
      !event.altKey &&
      running &&
      currentHasDraft &&
      onQueue
    ) {
      event.preventDefault();
      submitComposerWith(onQueue, currentPrompt);
      return;
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      if (voiceRecording) {
        submitComposer();
      } else {
        submitDraft(currentPrompt);
      }
    }
  }

  // Suppress the browser's native context menu on the textarea and open
  // our composer context menu anchored at the cursor instead. We still
  // allow the gesture in a read-only composer so the user can copy
  // existing prompt text out — copy/select-all remain enabled; only
  // mutating actions (cut/paste/delete) are blocked.
  function handleComposerContextMenu(event: ReactMouseEvent<HTMLTextAreaElement>): void {
    event.preventDefault();
    const textarea = textareaRef.current;
    const hasSelection = textarea
      ? textarea.selectionStart !== textarea.selectionEnd
      : false;
    setComposerContextMenu({
      x: event.clientX,
      y: event.clientY,
      hasSelection
    });
  }

  const content = (
    <div className={`composer-stack${isComposerExpanded ? " is-expanded" : ""}`} data-wuu-component="composer">
      {topAccessory ? <div className="composer-top-accessory">{topAccessory}</div> : null}
      <MemoizedComposerPluginSlot host={pluginHost} id="composer.above" context={pluginSlotContext} />
      <div className="composer-shell" ref={composerShellRef}>
        {slashMenuOpen ? (
          <FloatingMenuPortal
            anchorRef={composerShellRef}
            owner="composer-slash"
            placement="above"
            align="left"
            offset={variant === "hero" ? 10 : 8}
            width={320}
            matchAnchorWidth
          >
            <div
              className="composer-context-menu composer-plus-menu slash-command-menu"
              id={slashMenuID}
              role="listbox"
              aria-label={t("composer.slashCommands")}
            >
              {visibleSlashCommands.length > 0 ? (
                <div className="slash-command-list scrollbar-hidden">
                  {visibleSlashCommands.map((command, index) => {
                    const selected = index === selectedSlashIndex;
                    const optionID = `${slashMenuID}-${command.id}`;
                    return (
                      <Tooltip
                        content={
                          command.disabledReason || command.kind === "skill"
                            ? undefined
                            : command.description
                        }
                        key={command.id}
                      >
                        <button
                          className={`slash-command-item${selected ? " selected" : ""}`}
                          data-command-name={command.name}
                          id={optionID}
                          role="option"
                          type="button"
                          aria-selected={selected}
                          disabled={Boolean(command.disabledReason)}
                          onMouseEnter={() => {
                            if (!command.disabledReason) {
                              setSelectedSlashIndex(index);
                            }
                          }}
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => applySlashCommand(command, slashDraft)}
                        >
                          <SlashCommandIcon command={command} />
                          <span className="composer-plus-menu-item-title slash-command-name">
                            /{command.name}
                          </span>
                          {command.disabledReason || command.title ? (
                            <span
                              className={`composer-plus-menu-item-desc ${
                                command.disabledReason ? "slash-command-meta" : "slash-command-summary"
                              }`}
                            >
                              {command.disabledReason ?? command.title}
                            </span>
                          ) : null}
                        </button>
                      </Tooltip>
                    );
                  })}
                </div>
              ) : (
                <div className="slash-command-empty">{t("composer.noMatchingCommand", { query: slashQuery })}</div>
              )}
            </div>
          </FloatingMenuPortal>
        ) : null}
        <ComposerQueueStrip
          guideMessages={guideMessages}
          queuedMessages={queuedMessages}
          expanded={expandedDrawer === "pending"}
          onExpandedChange={(expanded) => setComposerDrawer(expanded ? "pending" : null)}
          onRemoveGuideMessage={onRemoveGuideMessage}
          onRemoveQueuedMessage={onRemoveQueuedMessage}
          onGuideQueuedMessage={onGuideQueuedMessage}
          onEditGuideMessage={onEditGuideMessage}
          onEditQueuedMessage={onEditQueuedMessage}
        />
        <div className="composer-frame-shell">
          {cameraOpen && !textOnly && !readOnly ? (
            <ComposerCameraPanel onCapture={captureCamera} onClose={closeCamera} />
          ) : null}
          <div
            className={`composer-frame${dropActive ? " composer-frame-drop-active" : ""}`}
            data-wuu-component="composer-frame"
            ref={composerFrameRef}
            onDragOver={handleComposerDragOver}
            onDragLeave={handleComposerDragLeave}
            onDrop={handleComposerDrop}
          >
          <div className={`composer${hasCollapsedPromptBlocks ? " has-collapsed-prompt" : ""}`}>
            {textOnly ? null : (
              <>
                <ComposerAttachmentStrip files={files} images={images} onRemoveFile={onRemoveFile} onRemoveImage={onRemoveImage} />
                <input
                  ref={attachmentInputRef}
                  className="composer-file-input"
                  type="file"
                  accept="image/*,application/pdf"
                  multiple
                  tabIndex={-1}
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
                  onChange={(event) => {
                    const selected = Array.from(event.currentTarget.files ?? []);
                    event.currentTarget.value = "";
                    if (selected.length > 0) {
                      onPasteAttachmentFiles(selected);
                    }
                  }}
                />
              </>
            )}
            {hasCollapsedPromptBlocks ? (
              <div className="composer-collapsed-prompt-list" ref={collapsedPromptListRef} aria-label={t("composer.collapsedLongText")}>
                {activeCollapsedPromptBlocks.map((block, index) => (
                  <CollapsedComposerPromptCard
                    text={block.text}
                    key={block.id}
                    onReveal={() => revealCollapsedPromptBlock(index)}
                    onRemove={() => removeCollapsedPromptBlock(index)}
                  />
                ))}
              </div>
            ) : null}
            <ComposerTextarea
              ref={textareaRef}
              value={visiblePromptValue}
              valueRevision={promptRevision}
              submissionClearRevision={submissionClearRevision}
              placeholder={composerPlaceholder}
              maxLength={maxLength}
              disabled={readOnly}
              ariaControls={slashMenuOpen ? slashMenuID : undefined}
              ariaActiveDescendant={
                selectedSlashCommand
                  ? `${slashMenuID}-${selectedSlashCommand.id}`
                  : undefined
              }
              ariaExpanded={slashMenuOpen || undefined}
              onValueChange={updateVisiblePrompt}
              onPaste={handleComposerPaste}
              onBlurValue={(value) => {
                if (slashMenuOpen) {
                  setSlashDismissedValue(value);
                }
              }}
              onKeyDown={handleComposerKeyDown}
              onContextMenu={handleComposerContextMenu}
            />
            {!hideExpandButton ? (
              <button
                className="composer-expand-button"
                type="button"
                aria-label={isComposerExpanded ? t("composer.collapseInput") : t("composer.expandInput")}
                aria-pressed={isComposerExpanded}
                title={readOnly ? t("composer.readOnlyCannotExpand") : isComposerExpanded ? t("composer.collapseInput") : t("composer.expandInput")}
                disabled={readOnly}
                onClick={toggleComposerExpansion}
              >
                {isComposerExpanded ? <ChevronDown aria-hidden="true" /> : <ChevronUp aria-hidden="true" />}
              </button>
            ) : null}
            <div
              className={`composer-bar${voiceRecording ? " is-voice-recording" : ""}`}
              data-wuu-component="composer-toolbar"
            >
              <div className="composer-bar-left">
                {leadingActions}
                {canSelectProject ? (
                  // Project selection depends on whether a session has been
                  // created, independently of where the composer is placed.
                  <div className="hero-project-pill-anchor composer-project-control" ref={menuRef}>
                    <Tooltip
                      content={projectPillTitle}
                      disabled={projectPillTitle === projectPillLabel}
                    >
                      <button
                        className="hero-project-pill"
                        type="button"
                        aria-haspopup="menu"
                        aria-expanded={menuOpen}
                        aria-label={t("composer.switchProject", { project: projectPillLabel })}
                        onPointerDown={(event) => { if (mobileWeb) event.preventDefault(); }}
                        onClick={onToggleMenu}
                      >
                        <span className="hero-project-pill-icon" aria-hidden="true">
                          <ProjectPillIcon />
                        </span>
                        <span className="hero-project-pill-text">{projectPillLabel}</span>
                        <ChevronDown className="hero-project-pill-chevron" aria-hidden="true" />
                      </button>
                    </Tooltip>
                    {menuOpen ? (
                      <FloatingMenuPortal
                        anchorRef={menuRef}
                        owner="composer-runtime"
                        placement="above"
                        align="left"
                        width={300}
                        mobileSheet={{ label: t("composer.switchProject", { project: projectPillLabel }), onClose: onToggleMenu }}
                      >
                        <ProjectPickerMenu
                          projects={projects}
                          activeContext={activeContext}
                          query={projectFilter}
                          setQuery={setProjectFilter}
                          onSelectProject={onSelectProject}
                          onSelectNoProject={onSelectNoProject}
                          onCreateProject={onCreateProject}
                          onOpenProject={onOpenProject}
                        />
                      </FloatingMenuPortal>
                    ) : null}
                  </div>
                ) : null}
                {!textOnly && !hidePlusButton ? (
                  <ComposerPlusButton
                    variant={variant}
                    disabled={readOnly}
                    commands={slashCommands}
                    menuAnchorRef={composerShellRef}
                    onAddAttachment={() => attachmentInputRef.current?.click()}
                    mobileAttachments={
                      mobileWeb
                        ? {
                            onTakePhoto: openCamera,
                            onPickPhotos: () => photosInputRef.current?.click(),
                            onPickFiles: () => attachmentInputRef.current?.click(),
                          }
                        : undefined
                    }
                    onSelectCommand={(command) => applySlashCommand(command, undefined)}
                  />
                ) : null}
                {!textOnly && !hidePermissionControl ? (
                  <div className="permission-menu-anchor" ref={accessMenuRef}>
                    <button
                      className={`permission-chip tone-${permissionOption.tone}`}
                      type="button"
                      aria-haspopup="menu"
                      aria-expanded={accessMenuOpen}
                      aria-label={t("composer.permissionMode", { mode: permissionChipLabel })}
                      disabled={!initialized || readOnly || running}
                      onPointerDown={(event) => { if (mobileWeb) event.preventDefault(); }}
                      onClick={onToggleAccessMenu}
                    >
                      <permissionOption.icon aria-hidden="true" />
                      <span>{permissionChipLabel}</span>
                      <ChevronDown aria-hidden="true" />
                    </button>
                    {accessMenuOpen ? (
                      <FloatingMenuPortal
                        anchorRef={accessMenuRef}
                        owner="composer-access"
                        placement="above"
                        align="left"
                        offset={6}
                        width={176}
                        mobileSheet={{ label: t("composer.permissionMode", { mode: permissionChipLabel }), onClose: onToggleAccessMenu }}
                      >
                        <AccessMenu
                          permissions={initialized?.permissions}
                          engine={activeEngine}
                          disabled={!initialized || readOnly || running}
                          onSelect={onSelectPermissionMode}
                        />
                      </FloatingMenuPortal>
                    ) : null}
                  </div>
                ) : null}
                <MemoizedComposerPluginToolbar host={pluginHost} context={pluginSlotContext} />
              </div>
              <div className="composer-bar-right">
                {hideRuntimeControls ? null : (
                  <>
                    <ComposerRuntimeMeters
                      running={running}
                      turnID={telemetryTurnID}
                      fallbackTokensPerSecond={tokensPerSecond}
                      fallbackSampledAt={tokenSpeedSampledAt}
                      fallbackSource={tokenSpeedSource}
                      fallbackContextUsage={contextUsage}
                      activeEngine={activeEngine}
                    />
                    {initialized ? (
                      <RuntimePicker
                        initialized={initialized}
                        state={codexModels}
                        openMenu={codexRuntimeMenu}
                        anchorRef={codexRuntimeRef}
                        running={effectiveRuntimeControlsDisabled}
                        engines={engines}
                        activeEngine={activeEngine}
                        engineLocked={engineLocked}
                        engineModel={engineModel}
                        engineEffort={engineEffort}
                        onSelectEngine={onSelectEngine}
                        onSelectEngineModel={onSelectEngineModel}
                        onSelectEngineEffort={onSelectEngineEffort}
                        onToggleMenu={onToggleCodexRuntimeMenu}
                        onSelectModel={onSelectRuntimeModel}
                        onHandoffModel={(provider, model) => {
                          if (codexRuntimeMenu === "model") onToggleCodexRuntimeMenu("model");
                          setPrompt(handoffPromptFromSelection(provider, model, ""));
                          focusComposerAtEndSoon();
                        }}
                        onSelectEffort={onSelectRuntimeEffort}

                      />
                    ) : (
                      <>
                        <button className="provider-pill" type="button" onClick={onOpenSettings}>
                          provider
                        </button>
                        <button className="model-label" type="button" onClick={onOpenSettings}>
                          model
                        </button>
                      </>
                    )}
                  </>
                )}
                {statusText ? (
                  <span className="status-label">
                    <TruncatedText
                      className={`status-label-text${statusIsLiveProgress ? " live-progress-chip" : ""}`}
                      text={statusText}
                    />
                  </span>
                ) : null}
                {ENABLE_VOICE_INPUT ? (
                  <ComposerVoiceInput
                    ref={voiceInputRef}
                    prompt={prompt}
                    setPrompt={setPrompt}
                    disabled={readOnly}
                    locale={locale}
                    onRecordingChange={setVoiceRecording}
                  />
                ) : null}
                <button
                  className={`composer-action-button ${showComposerStopAction ? "composer-stop-button" : "composer-send-button"}`}
                  data-wuu-component="composer-send"
                  data-wuu-state={showComposerStopAction ? "stop" : "send"}
                  type="button"
                  onPointerDown={(event) => {
                    if (!showComposerStopAction && event.button === 0 && document.activeElement === textareaRef.current) {
                      // Keep editing focus while the pointer activates Send;
                      // keyboard navigation and the Stop action stay native.
                      event.preventDefault();
                    }
                  }}
                  onClick={showComposerStopAction ? (disconnected ? undefined : onInterrupt) : submitComposer}
                  aria-label={showComposerStopAction ? t("composer.pause") : voiceActionLabel}
                  title={showComposerStopAction ? t("composer.pauseShortcut") : voiceActionLabel}
                  disabled={
                    !showComposerStopAction &&
                    (voiceSendPending || effectiveSendDisabled || readOnly || (!voiceRecording && !hasDraft) || (handoffMode && !canConfirmHandoff))
                  }
                >
                  {showComposerStopAction ? <Square aria-hidden="true" /> : <Send aria-hidden="true" />}
                </button>
              </div>
            </div>
          </div>
          </div>
        </div>
        {handoffMode && initialized && !handoffUnavailableReason ? (
          <FloatingMenuPortal
            anchorRef={composerFrameRef}
            owner="composer-handoff"
            placement="above"
            align="left"
            width={224}
          >
            <RuntimeModelMenu
              initialized={initialized}
              state={codexModels}
              selectedProvider={handoffDraft.providerId || initialized.provider}
              selectedModel={handoffDraft.modelId}
              selectedVariant={handoffDraft.variant}
              engineOptions={[{ id: "wuu", label: "Wuu" }]}
              selectedEngine="wuu"
              engineLocked
              running={false}
              onSelectEngine={() => {}}
              hideHandoff
              filterQuery={canSubmitHandoffDraft(handoffDraft) ? "" : handoffDraft.filterQuery}
              forcedView={canSubmitHandoffDraft(handoffDraft) ? "summary" : handoffDraft.pickerView}
              onSelectProvider={(provider) => {
                setHandoffRevision((current) => current + 1);
                setPrompt(handoffPromptFromSelection(provider, "", handoffDraft.intent));
              }}
              onSelectModel={(provider, model, effort) => {
                setPrompt(handoffPromptFromSelection(provider, model, handoffDraft.intent, effort));
              }}
              onSelectEffort={(effort) => {
                setPrompt(handoffPromptFromSelection(handoffDraft.providerId, handoffDraft.modelId, handoffDraft.intent, effort));
              }}
            />
          </FloatingMenuPortal>
        ) : null}
      </div>
      {composerContextMenu ? (
        <ComposerContextMenu
          textareaRef={textareaRef}
          x={composerContextMenu.x}
          y={composerContextMenu.y}
          hasSelection={composerContextMenu.hasSelection}
          disabled={readOnly}
          onClose={() => setComposerContextMenu(null)}
          onValueChange={updateVisiblePrompt}
        />
      ) : null}
    </div>
  );
  const nativeComposer = variant === "hero" ? (
    <div
      className={className}
      data-main-conversation-composer={mainConversation ? variant : undefined}
    >
      {content}
    </div>
  ) : (
    <footer
      className={className}
      data-main-conversation-composer={mainConversation ? variant : undefined}
      ref={containerRef}
    >
      {content}
    </footer>
  );
  const availableSubmissionModes = running
    ? ([...(onSteer ? ["steer" as const] : []), ...(onQueue ? ["queue" as const] : [])])
    : (["send" as const]);
  const activeSubmissionMode = running && onSteer ? "steer" : running && onQueue ? "queue" : "send";
  return (
    <ComposerPresentation
      enabled={mainConversation}
      fallback={nativeComposer}
      draftText={prompt}
      files={files}
      images={images}
      queuedMessages={queuedMessages}
      pendingMessages={guideMessages}
      running={running}
      readOnly={readOnly}
      sendDisabled={effectiveSendDisabled}
      variant={variant}
      threadId={queryHistorySessionID}
      initialized={initialized}
      contextUsage={contextUsage}
      disabledReason={readOnly || effectiveSendDisabled ? statusText || undefined : undefined}
      activeSubmissionMode={activeSubmissionMode}
      availableSubmissionModes={availableSubmissionModes}
      attachmentInputRef={attachmentInputRef}
      attachmentsEnabled={!textOnly}
      onSetDraft={setPrompt}
      onRemoveFile={onRemoveFile}
      onRemoveImage={onRemoveImage}
      onSubmit={submitDraft}
      onStop={onInterrupt}
    />
  );
}

type ComposerTextareaProps = {
  value: string;
  valueRevision: number;
  submissionClearRevision: number;
  placeholder: string;
  maxLength?: number;
  disabled: boolean;
  ariaControls?: string;
  ariaActiveDescendant?: string;
  ariaExpanded?: boolean;
  onValueChange: (value: string) => void;
  onBlurValue: (value: string) => void;
  onPaste: (event: ReactClipboardEvent<HTMLTextAreaElement>) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLTextAreaElement>) => void;
  onContextMenu: (event: ReactMouseEvent<HTMLTextAreaElement>) => void;
};

// Keep the browser's input-critical value below the rest of Composer. The
// textarea paints synchronously, while slash menus, plugin presenters, meters,
// and the surrounding chrome update in an interruptible transition.
const ComposerTextarea = forwardRef<HTMLTextAreaElement, ComposerTextareaProps>(
  function ComposerTextarea({
    value: committedValue,
    valueRevision,
    submissionClearRevision,
    placeholder,
    maxLength,
    disabled,
    ariaControls,
    ariaActiveDescendant,
    ariaExpanded,
    onValueChange,
    onBlurValue,
    onPaste,
    onKeyDown,
    onContextMenu,
  }, ref): JSX.Element {
    const [value, setValue] = useState(committedValue);
    const [lastCommittedValue, setLastCommittedValue] = useState(committedValue);
    const [lastValueRevision, setLastValueRevision] = useState(valueRevision);
    const [lastSubmissionClearRevision, setLastSubmissionClearRevision] = useState(
      submissionClearRevision,
    );
    const optimisticValueQueueRef = useRef<string[]>([]);
    const compositionActiveRef = useRef(false);
    const pendingProgrammaticValueRef = useRef<{ value: string } | null>(null);

    if (submissionClearRevision !== lastSubmissionClearRevision) {
      setLastSubmissionClearRevision(submissionClearRevision);
      compositionActiveRef.current = false;
      pendingProgrammaticValueRef.current = null;
      optimisticValueQueueRef.current.length = 0;
      setValue("");
    } else if (valueRevision !== lastValueRevision) {
      setLastValueRevision(valueRevision);
      setLastCommittedValue(committedValue);
      optimisticValueQueueRef.current.length = 0;
      if (compositionActiveRef.current) {
        pendingProgrammaticValueRef.current = { value: committedValue };
      } else {
        pendingProgrammaticValueRef.current = null;
        setValue(committedValue);
      }
    } else if (committedValue !== lastCommittedValue) {
      setLastCommittedValue(committedValue);
      const optimisticValueIndex = optimisticValueQueueRef.current.lastIndexOf(committedValue);
      if (optimisticValueIndex >= 0) {
        optimisticValueQueueRef.current.splice(0, optimisticValueIndex + 1);
      } else if (compositionActiveRef.current) {
        pendingProgrammaticValueRef.current = { value: committedValue };
      } else {
        optimisticValueQueueRef.current.length = 0;
        setValue(committedValue);
      }
    }

    return (
      <textarea
        ref={ref}
        data-wuu-component="composer-input"
        value={value}
        placeholder={placeholder}
        maxLength={maxLength}
        disabled={disabled}
        aria-readonly={disabled}
        aria-controls={ariaControls}
        aria-activedescendant={ariaActiveDescendant}
        aria-expanded={ariaExpanded}
        onChange={(event) => {
          const pendingValue = pendingProgrammaticValueRef.current;
          if (!compositionActiveRef.current && pendingValue !== null) {
            pendingProgrammaticValueRef.current = null;
            optimisticValueQueueRef.current.length = 0;
            setValue(pendingValue.value);
            return;
          }
          const nextValue = event.target.value;
          setValue(nextValue);
          optimisticValueQueueRef.current.push(nextValue);
          startTransition(() => onValueChange(nextValue));
        }}
        onCompositionStart={() => {
          compositionActiveRef.current = true;
        }}
        onCompositionEnd={() => {
          compositionActiveRef.current = false;
          const pendingValue = pendingProgrammaticValueRef.current;
          if (pendingValue === null) {
            return;
          }
          optimisticValueQueueRef.current.length = 0;
          setValue(pendingValue.value);
          queueMicrotask(() => {
            if (pendingProgrammaticValueRef.current === pendingValue) {
              pendingProgrammaticValueRef.current = null;
            }
          });
        }}
        onPaste={onPaste}
        onBlur={() => onBlurValue(value)}
        onKeyDown={onKeyDown}
        onContextMenu={onContextMenu}
      />
    );
  },
);

function composerRuntimeContextKey(context: RuntimeContext): string {
  return context.kind === "project" ? `project:${context.project_id}` : `no_project:${context.cwd}`;
}

function heroProjectPillLabel(activeContext: RuntimeContext | undefined, activeProject: DesktopProject | undefined): string {
  if (activeContext?.kind === "project") {
    return activeProject?.name ?? translate("composer.currentProject");
  }
  if (activeContext?.kind === "no_project") {
    // The no-project workspace is surfaced everywhere else — sidebar group,
    // session tab — as "对话", so the draft's cwd control matches that name
    // rather than the older, inconsistent "无项目".
    return translate("composer.conversation");
  }
  return translate("composer.selectProject");
}

function exactRunnableSlashCommand(commands: ComposerSlashCommand[], draft: ComposerSlashDraft): ComposerSlashCommand | undefined {
  return commands.find(
    (command) =>
      !command.disabledReason &&
      (command.kind === "prompt" || command.kind === "skill") &&
      command.name.toLowerCase() === draft.query
  );
}

function exactActionSlashCommand(commands: ComposerSlashCommand[], draft: ComposerSlashDraft): ComposerSlashCommand | undefined {
  return commands.find(
    (command) =>
      !command.disabledReason &&
      command.kind === "action" &&
      command.name.toLowerCase() === draft.query
  );
}
