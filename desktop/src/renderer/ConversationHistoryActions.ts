import type { Dispatch, MutableRefObject, SetStateAction } from "react";
import type { InputFile, InputImage, MessageContentPart, ResponseSelection, Thread, ThreadItem } from "../shared/protocol";
import type { ForkMode } from "./ConversationForkDialog";
import {
  cloneComposerDraft,
  composerDraftHasContent,
  createThreadSessionTab,
  ensureSessionTab,
  emptyComposerDraft,
  initialSplitComposerDrafts,
  isThreadRunning,
  openForkThreadAsPrimary,
  requireThread,
  sameRuntimeContext,
  updateThreadByID,
  upsertTurn,
  upsertThread,
  type AppState,
  type ComposerDraftState,
  type ConversationPaneID,
} from "./AppState";
import {
  composerContextFromMessage,
  createComposerMessage,
  createOptimisticTurn,
  type ComposerFile,
  type ComposerImage,
  type QueuedComposerMessage,
} from "./ComposerMessages";
import { lastUserMessageAnchor, scrollToUserMessage } from "./TurnViewHelpers";
import { localizedText, translateCurrent as t } from "./i18n";
import { showErrorToast } from "./Toast";
import { rememberCollapsedPromptParts } from "./ComposerCollapsedPrompt";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type PendingForkState = {
  sourceThread: Thread;
  turnID: string;
  itemID: string;
};

export type HistoryMessageEditState = {
  threadID: string;
  turnID: string;
  itemID: string;
  pane?: ConversationPaneID;
  submitting: boolean;
};

export type ConversationHistoryActionsDeps = {
  appStateRef: MutableRefObject<AppState>;
  setAppState: SetAppState;
  getPendingFork: () => PendingForkState | undefined;
  setPendingFork: (update: SetStateAction<PendingForkState | undefined>) => void;
  setHistoryMessageEdit: (
    update: SetStateAction<HistoryMessageEditState | undefined>,
  ) => void;
  
  getPrompt: () => string;
  getComposerImages: () => ComposerImage[];
  getComposerFiles: () => ComposerFile[];
  getComposerSelections: () => ResponseSelection[];
  setComposerSelections: Dispatch<SetStateAction<ResponseSelection[]>>;
  getSplitComposerDrafts: () => Record<ConversationPaneID, ComposerDraftState>;
  setPrompt: Dispatch<SetStateAction<string>>;
  setComposerImages: Dispatch<SetStateAction<ComposerImage[]>>;
  setComposerFiles: Dispatch<SetStateAction<ComposerFile[]>>;
  setSplitComposerDrafts: Dispatch<
    SetStateAction<Record<ConversationPaneID, ComposerDraftState>>
  >;
  restorePrimaryComposerDraft: (draft: ComposerDraftState) => void;
  closeConversationSearch: (options?: { immediate?: boolean }) => void;
  clearEnvironmentDialog: () => void;
  scheduleGitStatusRefresh: (delayMs: number) => void;
  disableConversationAutoFollow: () => void;
  enableConversationAutoFollow: () => void;
  /**
   * Snapshot the conversation viewport's scrollTop and auto-follow state so
   * the caller can put the user back exactly where they came from after a
   * transient interaction (currently: cancelling a history-message edit).
   */
  rememberConversationScrollForEdit: () => void;
  /** Restore the snapshot captured by `rememberConversationScrollForEdit`. */
  restoreConversationScrollForEdit: () => void;
  threadHasPendingComposerMessages: (threadID: string) => boolean;
  sendComposerMessageToThread: (
    message: QueuedComposerMessage,
    targetThread: Thread,
    prepareThread?: () => Promise<Thread>,
  ) => Promise<boolean>;
  worktreeForkNonGitReason: string;
};

export type ConversationHistoryActions = {
  choosePendingFork: (mode: ForkMode) => Promise<void>;
  forkThreadFromMessage: (
    sourceThread: Thread,
    turnID: string,
    itemID: string,
  ) => Promise<void>;
  startEditingThreadMessageFromHistory: (
    sourceThread: Thread,
    turnID: string,
    item: ThreadItem,
    pane?: ConversationPaneID,
  ) => void;
  cancelEditingThreadMessage: () => void;
  submitEditedThreadMessageFromHistory: (
    sourceThread: Thread,
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
    pane?: ConversationPaneID,
  ) => Promise<void>;
};

export function createConversationHistoryActions(
  deps: ConversationHistoryActionsDeps,
): ConversationHistoryActions {
  function isForkTargetLatest(
    sourceThread: Thread,
    turnID: string,
    itemID: string,
  ): boolean {
    const latest = lastUserMessageAnchor(sourceThread);
    return Boolean(
      latest && latest.turnID === turnID && latest.itemID === itemID,
    );
  }

  async function executeForkFromMessage(
    sourceThread: Thread,
    turnID: string,
    itemID: string,
    mode: ForkMode,
  ): Promise<void> {
    const origin = deps.appStateRef.current;
    const activeContext = origin.activeContext;
    if (!activeContext || sourceThread.read_only) {
      return;
    }
    const ownsCurrentView = (): boolean => {
      const current = deps.appStateRef.current;
      return sameRuntimeContext(current.activeContext, activeContext)
        && current.activeContext?.cwd === activeContext.cwd
        && current.activeSessionTabID === origin.activeSessionTabID
        && current.thread?.id === origin.thread?.id
        && current.secondaryThread?.id === origin.secondaryThread?.id
        && current.activePane === origin.activePane;
    };
    if (mode === "worktree") {
      let gitStatus = deps.appStateRef.current.gitStatus;
      if (!gitStatus) {
        gitStatus = await window.wuu.gitStatus();
        if (!ownsCurrentView()) {
          return;
        }
        const refreshedStatus = gitStatus;
        deps.setAppState((current) => ({
          ...current,
          gitStatus: refreshedStatus,
        }));
      }
      if (gitStatus.is_repo === false) {
        deps.setAppState((current) => ({
          ...current,
          status: deps.worktreeForkNonGitReason,
        }));
        throw new Error(deps.worktreeForkNonGitReason);
      }
    }
    
    deps.setAppState((current) => ({
      ...current,
      status: localizedText("history.forking"),
    }));
    try {
      const targetItem = sourceThread.turns
        .find((turn) => turn.id === turnID)
        ?.items.find((item) => item.id === itemID);
      const fork = requireThread(
        await window.wuu.forkThread(
          sourceThread.id,
          turnID,
          itemID,
          mode,
          targetItem
            ? {
                seq: targetItem.seq,
                source_id: targetItem.source_id,
                type: targetItem.type,
              }
            : undefined,
        ),
        "thread/fork did not return a thread",
      );
      // Keep a completed fork discoverable without taking the view or draft
      // that the user opened while the backend was creating it.
      if (!ownsCurrentView()) {
        deps.setAppState((current) => ({
          ...current,
          sessionTabs: ensureSessionTab(
            current.sessionTabs, createThreadSessionTab(fork, activeContext),
          ),
          threads: sameRuntimeContext(current.activeContext, activeContext)
            ? upsertThread(current.threads, fork)
            : current.threads,
        }));
        return;
      }
      deps.enableConversationAutoFollow();
      const currentState = deps.appStateRef.current;
      const sourcePane =
        currentState.secondaryThread?.id === sourceThread.id
          ? "secondary"
          : "primary";
      const currentSplitConversation = Boolean(
        currentState.thread && currentState.secondaryThread,
      );
      const splitComposerDrafts = deps.getSplitComposerDrafts();
      const splitDrafts = currentSplitConversation
        ? {
            primary: cloneComposerDraft(
              splitComposerDrafts.primary ?? emptyComposerDraft(),
            ),
            secondary: cloneComposerDraft(
              splitComposerDrafts.secondary ?? emptyComposerDraft(),
            ),
          }
        : undefined;
      const sourceDraft = currentSplitConversation
        ? cloneComposerDraft(splitDrafts?.[sourcePane] ?? emptyComposerDraft())
        : cloneComposerDraft({
            prompt: deps.getPrompt(),
            images: deps.getComposerImages(),
            files: deps.getComposerFiles(),
            selections: deps.getComposerSelections(),
          });
      deps.setPrompt("");
      deps.setComposerImages([]);
      deps.setComposerFiles([]);
      deps.setComposerSelections([]);
      deps.setSplitComposerDrafts(initialSplitComposerDrafts());
      deps.setAppState((current) =>
        openForkThreadAsPrimary(current, {
          sourceThread,
          forkThread: fork,
          context: activeContext,
          sourceDraft,
          splitDrafts,
        }),
      );
    } catch (error) {
      showErrorToast(error, t("thread.forkFailed"));
      throw error;
    }
  }

  async function choosePendingFork(mode: ForkMode): Promise<void> {
    const target = deps.getPendingFork();
    if (!target) {
      return;
    }
    await executeForkFromMessage(
      target.sourceThread,
      target.turnID,
      target.itemID,
      mode,
    );
    deps.setPendingFork((current) => current === target ? undefined : current);
  }

  async function forkThreadFromMessage(
    sourceThread: Thread,
    turnID: string,
    itemID: string,
  ): Promise<void> {
    if (!deps.appStateRef.current.activeContext || sourceThread.read_only) {
      return;
    }
    if (isForkTargetLatest(sourceThread, turnID, itemID)) {
      await executeForkFromMessage(sourceThread, turnID, itemID, "local");
      return;
    }
    deps.closeConversationSearch({ immediate: true });
    deps.clearEnvironmentDialog();
    deps.scheduleGitStatusRefresh(0);
    deps.setPendingFork({ sourceThread, turnID, itemID });
  }

  function startEditingThreadMessageFromHistory(
    sourceThread: Thread,
    turnID: string,
    item: ThreadItem,
    pane?: ConversationPaneID,
  ): void {
    if (!deps.appStateRef.current.activeContext || sourceThread.read_only) {
      return;
    }
    if (isThreadRunning(sourceThread)) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("history.waitForReply"),
      }));
      return;
    }
    if (deps.threadHasPendingComposerMessages(sourceThread.id)) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("history.handlePendingFirst"),
      }));
      return;
    }

    // Capture the user's pre-edit scroll state *before* we change anything.
// Cancelling the edit will restore this snapshot so the user is parked
// back at the same scrollTop (and same auto-follow state) they came
// from — otherwise the resize observer triggered by the editor→bubble
// swap would yank them back to the bottom.
deps.rememberConversationScrollForEdit();
    // Disarm auto-follow *before* setting the edit state. The editor's
    // taller height grows the conversation's scrollHeight, which would
    // otherwise read as "user scrolled away from latest" once the
    // bubble-to-editor swap reflows. Disarming up front keeps the
    // "跳到最新" pill hidden while the user is editing.
    deps.disableConversationAutoFollow();
    deps.setHistoryMessageEdit({
      threadID: sourceThread.id,
      turnID,
      itemID: item.id,
      pane,
      submitting: false,
    });
    // Bring the editor into view deliberately — `scrollToUserMessage` is the
    // same helper the query-history popover uses, so the jump matches the
    // existing scroll contract (smooth scroll, 64px headroom, no surprise
    // auto-follow disarms). The highlight pulse is skipped here: replaying
    // the light flash over the bubble as it swaps into the black editor
    // reads as a glitch instead of jump feedback. The helper retries on a
    // short cadence, which covers the case where the editor hasn't mounted
    // yet on the first synchronous attempt.
    scrollToUserMessage(turnID, item.id, { highlight: false });
  }

  function cancelEditingThreadMessage(): void {
    deps.setHistoryMessageEdit(undefined);
    // Restore the viewport to where the user was before they opened the
    // editor. The snapshot also captures auto-follow, so a user who was
    // parked at the bottom is sent back to the bottom via the resize
    // observer, while a user who had scrolled up stays scrolled up —
    // matching their original intent instead of dropping them at latest.
    deps.restoreConversationScrollForEdit();
  }

  async function submitEditedThreadMessageFromHistory(
    sourceThread: Thread,
    turnID: string,
    item: ThreadItem,
    text: string,
    images: InputImage[],
    files: InputFile[],
    contentParts?: MessageContentPart[],
    pane?: ConversationPaneID,
  ): Promise<void> {
    if (!deps.appStateRef.current.activeContext || sourceThread.read_only) {
      return;
    }
    const idSalt = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
    const sessionTabID = deps.appStateRef.current.activeSessionTabID;
    const composerImages: ComposerImage[] = images.map((image, index) => ({
      id: `edit-attach-${index}-${idSalt}`,
      media_type: image.media_type,
      data: image.data,
      remote_ref: image.remote_ref,
    }));
    const composerFiles: ComposerFile[] = files.map((file, index) => ({
      id: `edit-file-${index}-${idSalt}`,
      media_type: file.media_type,
      data: file.data,
      filename: file.filename,
    }));
    const message = createComposerMessage(text, composerImages, composerFiles, contentParts);
    if (!message) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("history.emptyEdit"),
      }));
      return;
    }
    if (isThreadRunning(sourceThread)) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("history.waitForReply"),
      }));
      return;
    }
    if (deps.threadHasPendingComposerMessages(sourceThread.id)) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("history.handlePendingFirst"),
      }));
      return;
    }

    deps.setHistoryMessageEdit((current) =>
      current?.threadID === sourceThread.id &&
      current.turnID === turnID &&
      current.itemID === item.id
        ? { ...current, submitting: true }
        : current,
    );
    
    try {
      let preparedThread: Thread | undefined;
      const sent = await deps.sendComposerMessageToThread(message, sourceThread, async () => {
        // Editing mutates persisted history. Even after Stop, wait for its
        // authoritative result before the sender settles the interrupted turn.
        const result = await window.wuu.editThreadMessage(sourceThread.id, turnID, item.id);
        const thread = requireThread(
          { thread: result.thread },
          "thread/edit-message did not return a thread",
        );
        // The sender owns placement of the replacement user message.
        deps.setHistoryMessageEdit(undefined);
        const adopt = (current: AppState) => updateThreadByID(
          current,
          thread.id,
          (currentThread) => ({
            ...thread,
            child_agents: thread.child_agents ?? currentThread.child_agents,
          }),
        );
        deps.appStateRef.current = adopt(deps.appStateRef.current);
        deps.setAppState(adopt);
        preparedThread = thread;
        return thread;
      });
      if (!preparedThread) {
        deps.setHistoryMessageEdit((current) =>
          current?.threadID === sourceThread.id && current.turnID === turnID && current.itemID === item.id
            ? { ...current, submitting: false }
            : current,
        );
        return;
      }
      const thread = preparedThread;
      if (sent) {
        const editIndex = thread.turns.length;
        const reordered = (latest: Thread): Thread => {
          if (latest.turns.length <= editIndex) return latest;
          const newTurn = latest.turns[latest.turns.length - 1];
          if (latest.turns[editIndex] === newTurn) return latest;
          const turns = latest.turns.slice(0, editIndex);
          turns.push(newTurn);
          return { ...latest, turns };
        };
        deps.appStateRef.current = updateThreadByID(
          deps.appStateRef.current,
          thread.id,
          reordered,
          {},
        );
        deps.setAppState((current) =>
          updateThreadByID(
            current,
            thread.id,
            reordered,
            {},
          ),
        );
      }
      if (!sent) {
        const latest = deps.appStateRef.current;
        const stillOwner = pane === undefined
          ? latest.activeSessionTabID === sessionTabID && !latest.secondaryThread && latest.thread?.id === thread.id
          : Boolean(latest.secondaryThread) && (pane === "primary" ? latest.thread : latest.secondaryThread)?.id === thread.id;
        const latestDraft = pane === undefined ? {
          prompt: deps.getPrompt(), images: deps.getComposerImages(),
          files: deps.getComposerFiles(), selections: deps.getComposerSelections(),
        } : deps.getSplitComposerDrafts()[pane];
        if (!stillOwner || composerDraftHasContent(latestDraft)) {
          const failedTurn = {
            ...createOptimisticTurn(message, Date.now()),
            status: "failed" as const,
            error: { message: t("composer.sendFailed") },
          };
          const preserve = (state: AppState) => updateThreadByID(state, thread.id, (current) => upsertTurn(current, failedTurn));
          deps.appStateRef.current = preserve(deps.appStateRef.current);
          deps.setAppState(preserve);
          return;
        }
        const context = composerContextFromMessage(message.text, message.contentParts);
        rememberCollapsedPromptParts(thread.id, context.prompt, context.contentParts);
        const draft = cloneComposerDraft({
          prompt: context.prompt,
          images: message.images,
          files: message.files,
          selections: context.selections,
        });
        if (pane === undefined) {
          deps.restorePrimaryComposerDraft(draft);
        } else {
          deps.setSplitComposerDrafts((current) => ({
            ...current,
            [pane]: draft,
          }));
        }
      }
    } catch (error) {
      deps.setHistoryMessageEdit((current) =>
        current?.threadID === sourceThread.id &&
        current.turnID === turnID &&
        current.itemID === item.id
          ? { ...current, submitting: false }
          : current,
      );
      showErrorToast(error, t("history.editFailed"));
    }
  }

  return {
    choosePendingFork,
    forkThreadFromMessage,
    startEditingThreadMessageFromHistory,
    cancelEditingThreadMessage,
    submitEditedThreadMessageFromHistory,
  };
}
