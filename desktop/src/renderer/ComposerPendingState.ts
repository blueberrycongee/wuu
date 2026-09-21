import { useRef, useState, type MutableRefObject } from "react";
import type {
  MessageContentPart,
  ServerEvent,
  Thread,
  ThreadResumeResult,
} from "../shared/protocol";
import {
  activeThreadIDForState,
  activeTurnIDForThread,
  composerDraftHasContent,
  threadForTab,
  threadItemFromRecord,
  type AppState,
  type ComposerDraftState,
} from "./AppState";
import {
  inputFilesFromComposer,
  inputImagesFromComposer,
  type QueuedComposerMessage,
} from "./ComposerMessages";
import {
  appendPendingComposerMessage,
  applyAuthoritativeComposerSnapshot,
  applyHeldComposerSnapshot,
  emptyThreadPendingComposerMessages,
  findPendingComposerMessage,
  materializedComposerMessageIDs,
  pendingComposerMessagesForThread,
  reconcilePendingComposerMessagesForThread,
  removePendingComposerMessagesByID,
  threadPendingComposerMessagesIsEmpty,
  type LocatedPendingComposerMessage,
  type PendingComposerMessageRemovalScope,
  type PendingComposerMessagesByThread,
  type ThreadPendingComposerMessages,
} from "./ComposerPendingMessages";
import { isRecord, recordValue, stringValue } from "./ToolActivity";
import { localizedText, translateCurrent } from "./i18n";
import { rememberCollapsedPromptParts } from "./ComposerCollapsedPrompt";

function heldComposerMessage(
  value: unknown,
  position: number,
  held = true,
): QueuedComposerMessage | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const id = stringValue(value, "id");
  const origin = stringValue(value, "origin");
  if (!id || (origin !== "queue" && origin !== "steer")) {
    return undefined;
  }
  const images = (Array.isArray(value.images) ? value.images : []).flatMap(
    (candidate, index) => {
      if (!isRecord(candidate)) {
        return [];
      }
      const mediaType = stringValue(candidate, "media_type");
      const data = stringValue(candidate, "data");
      return mediaType && data
        ? [{ id: `${id}-held-image-${index}`, media_type: mediaType, data }]
        : [];
    },
  );
  const files = (Array.isArray(value.files) ? value.files : []).flatMap(
    (candidate, index) => {
      if (!isRecord(candidate)) {
        return [];
      }
      const mediaType = stringValue(candidate, "media_type");
      const data = stringValue(candidate, "data");
      if (!mediaType || !data) {
        return [];
      }
      const filename = stringValue(candidate, "filename");
      return [
        {
          id: `${id}-held-file-${index}`,
          media_type: mediaType,
          data,
          ...(filename ? { filename } : {}),
        },
      ];
    },
  );
  const contentParts = (Array.isArray(value.content_parts) ? value.content_parts : []).flatMap<MessageContentPart>(
    (candidate): MessageContentPart[] => {
      if (!isRecord(candidate)) return [];
      const type = stringValue(candidate, "type");
      const text = stringValue(candidate, "text");
      if ((type !== "text" && type !== "pasted_text") || !text) return [];
      const title = stringValue(candidate, "title");
      return type === "pasted_text"
        ? [{ type: "pasted_text" as const, text, ...(title ? { title } : {}) }]
        : [{ type: "text" as const, text }];
    },
  );
  const activeDocumentPath = isRecord(value.active_document)
    ? stringValue(value.active_document, "path")
    : "";
  return {
    id,
    text: stringValue(value, "prompt") ?? "",
    images,
    files,
    contentParts,
    activeDocument: activeDocumentPath ? { path: activeDocumentPath } : undefined,
    held,
    heldPosition: held ? position : undefined,
    origin,
  };
}

function heldComposerMessagesFromParams(
  params: Record<string, unknown>,
  method: string,
): { threadID: string; messages: QueuedComposerMessage[] } | undefined {
  const thread = recordValue(params, "thread");
  const threadID =
    stringValue(params, "thread_id") ||
    (isRecord(thread) ? stringValue(thread, "id") : "");
  if (!threadID) {
    return undefined;
  }
  const heldRaw = params[
    method === "thread/resumed" ? "held_user_messages" : "messages"
  ];
  const pendingRaw =
    method === "thread/resumed" ? params.pending_user_messages : undefined;
  if (method !== "thread/resumed" && !Array.isArray(heldRaw)) {
    return undefined;
  }
  const heldMessages = (Array.isArray(heldRaw) ? heldRaw : []).flatMap((value, position) => {
    const message = heldComposerMessage(value, position);
    return message ? [message] : [];
  });
  const pendingMessages = (Array.isArray(pendingRaw) ? pendingRaw : []).flatMap(
    (value, position) => {
      const message = heldComposerMessage(value, position, false);
      return message ? [message] : [];
    },
  );
  return { threadID, messages: [...heldMessages, ...pendingMessages] };
}

/**
 * Parse the authoritative pending and held snapshots carried by thread/resume.
 * The desktop renderer restores pending composer messages from this
 * on boot: the `thread/resumed` notification that the server emits alongside
 * the response is filtered out while the app is still loading (the active
 * context is not set yet), so a reload must not rely on it alone.
 */
export function heldComposerMessagesFromResumeResult(
  result: ThreadResumeResult | undefined,
): QueuedComposerMessage[] {
  const heldRaw = result?.held_user_messages;
  const pendingRaw = result?.pending_user_messages;
  const threadID = result?.thread?.id ?? "";
  const heldMessages = (Array.isArray(heldRaw) ? heldRaw : []).flatMap((value, position) => {
    const message = heldComposerMessage({ ...value, thread_id: threadID }, position);
    return message ? [message] : [];
  });
  const pendingMessages = (Array.isArray(pendingRaw) ? pendingRaw : []).flatMap(
    (value, position) => {
      const message = heldComposerMessage(
        { ...value, thread_id: threadID },
        position,
        false,
      );
      return message ? [message] : [];
    },
  );
  return [...heldMessages, ...pendingMessages];
}

export type ComposerPendingStateController = {
  pendingComposerMessagesByThread: PendingComposerMessagesByThread;
  pendingComposerMessagesByThreadRef: MutableRefObject<PendingComposerMessagesByThread>;
  setPendingComposerMessagesByThreadNow: (
    messagesByThread: PendingComposerMessagesByThread,
  ) => void;
  pendingComposerMessagesForThread: (
    threadID: string | undefined,
  ) => ThreadPendingComposerMessages;
  updateThreadPendingComposerMessages: (
    threadID: string,
    update: (
      previous: ThreadPendingComposerMessages,
    ) => ThreadPendingComposerMessages,
  ) => void;
  clearThreadPendingComposerMessages: (threadID: string) => void;
  removePendingComposerMessageByID: (
    threadID: string | undefined,
    id: string,
    scope?: PendingComposerMessageRemovalScope,
  ) => void;
  syncPendingComposerMessagesFromServerEvent: (event: ServerEvent) => void;
  reconcilePendingComposerMessagesForState: (state: AppState) => void;
  seedHeldComposerMessages: (
    threadID: string,
    messages: QueuedComposerMessage[],
  ) => void;
  enqueueComposerMessage: (
    threadID: string,
    message: QueuedComposerMessage,
  ) => void;
  removeQueuedMessage: (id: string) => Promise<boolean>;
  removeGuideMessage: (id: string) => Promise<boolean>;
  editQueuedMessage: (id: string) => Promise<void>;
  editGuideMessage: (id: string) => Promise<void>;
  guideQueuedMessage: (id: string) => Promise<void>;
  threadHasPendingComposerMessages: (threadID: string) => boolean;
};

type ComposerPendingStateOptions = {
  getAppState: () => AppState;
  getPrimaryComposerDraft: () => ComposerDraftState;
  restoreComposerDraftForThread: (
    threadID: string,
    draft: ComposerDraftState,
  ) => void;
  setStatus: (status: string) => void;
  sendComposerMessageToThread: (
    message: QueuedComposerMessage,
    targetThread: Thread,
  ) => Promise<boolean>;
};

export function useComposerPendingState({
  getAppState,
  getPrimaryComposerDraft,
  restoreComposerDraftForThread,
  setStatus,
}: ComposerPendingStateOptions): ComposerPendingStateController {
  const [pendingComposerMessagesByThread, setPendingComposerMessagesByThread] =
    useState<PendingComposerMessagesByThread>({});
  const pendingComposerMessagesByThreadRef =
    useRef<PendingComposerMessagesByThread>({});

  function setPendingComposerMessagesByThreadNow(
    messagesByThread: PendingComposerMessagesByThread,
  ): void {
    pendingComposerMessagesByThreadRef.current = messagesByThread;
    setPendingComposerMessagesByThread(messagesByThread);
  }

  function updateThreadPendingComposerMessages(
    threadID: string,
    update: (
      previous: ThreadPendingComposerMessages,
    ) => ThreadPendingComposerMessages,
  ): void {
    const previousByThread = pendingComposerMessagesByThreadRef.current;
    const previous =
      previousByThread[threadID] ?? emptyThreadPendingComposerMessages();
    const nextForThread = update(previous);
    const nextByThread = { ...previousByThread };
    if (threadPendingComposerMessagesIsEmpty(nextForThread)) {
      delete nextByThread[threadID];
    } else {
      nextByThread[threadID] = nextForThread;
    }
    setPendingComposerMessagesByThreadNow(nextByThread);
  }

  function clearThreadPendingComposerMessages(threadID: string): void {
    const previousByThread = pendingComposerMessagesByThreadRef.current;
    if (!previousByThread[threadID]) {
      return;
    }
    const nextByThread = { ...previousByThread };
    delete nextByThread[threadID];
    setPendingComposerMessagesByThreadNow(nextByThread);
  }

  function removePendingComposerMessageByID(
    threadID: string | undefined,
    id: string,
    scope: PendingComposerMessageRemovalScope = "all",
  ): void {
    setPendingComposerMessagesByThreadNow(
      removePendingComposerMessagesByID(
        pendingComposerMessagesByThreadRef.current,
        threadID,
        id,
        scope,
      ),
    );
  }

  function syncPendingComposerMessagesFromServerEvent(
    event: ServerEvent,
  ): void {
    if (event.kind !== "notification") {
      return;
    }
    const method = event.message.method;
    if (
      method !== "turn/held" &&
      method !== "thread/resumed" &&
      method !== "turn/queued" &&
      method !== "turn/steered" &&
      method !== "turn/unsteered" &&
      method !== "turn/started" &&
      method !== "turn/dequeued" &&
      method !== "item/completed"
    ) {
      return;
    }
    const params = isRecord(event.message.params)
      ? event.message.params
      : undefined;
    if (!params) {
      return;
    }
    const threadID = stringValue(params, "thread_id");
    if (
      method === "turn/held" ||
      method === "thread/resumed"
    ) {
      const snapshot = heldComposerMessagesFromParams(
        params,
        method,
      );
      if (!snapshot) {
        return;
      }
      updateThreadPendingComposerMessages(snapshot.threadID, (previous) =>
        method === "thread/resumed"
          ? applyAuthoritativeComposerSnapshot(previous, snapshot.messages)
          : applyHeldComposerSnapshot(previous, snapshot.messages),
      );
      return;
    }
    if (method === "turn/queued" || method === "turn/steered") {
      const rawMessage = recordValue(params, "message");
      const message = heldComposerMessage(rawMessage, 0, false);
      const messageThreadID = rawMessage
        ? stringValue(rawMessage, "thread_id")
        : "";
      if (!message || !messageThreadID) {
        return;
      }
      updateThreadPendingComposerMessages(messageThreadID, (previous) => {
        const withoutPreviousMode = {
          queued: previous.queued.filter((candidate) => candidate.id !== message.id),
          guides: previous.guides.filter((candidate) => candidate.id !== message.id),
        };
        return appendPendingComposerMessage(
          withoutPreviousMode,
          message.origin === "steer" ? "guide" : "queue",
          message,
        );
      });
      return;
    }
    if (method === "turn/unsteered") {
      const steerID = stringValue(params, "steer_id");
      if (steerID) {
        removePendingComposerMessageByID(threadID, steerID, "guide");
      }
      return;
    }
    if (method === "turn/started") {
      const queueID = stringValue(params, "queue_id");
      if (queueID) {
        removePendingComposerMessageByID(threadID, queueID);
      }
      return;
    }
    if (method === "turn/dequeued") {
      const queueID = stringValue(params, "queue_id");
      if (queueID) {
        removePendingComposerMessageByID(threadID, queueID, "queue");
      }
      return;
    }
    if (method === "item/completed") {
      const item = threadItemFromRecord(recordValue(params, "item"));
      if (item?.type === "user_message" && item.source_id) {
        removePendingComposerMessageByID(threadID, item.source_id);
      }
    }
  }

  function reconcilePendingComposerMessagesForState(state: AppState): void {
    const byThread = pendingComposerMessagesByThreadRef.current;
    let next = byThread;
    for (const threadID of Object.keys(byThread)) {
      const thread = threadForTab(state, threadID);
      if (thread) {
        next = reconcilePendingComposerMessagesForThread(next, thread);
      }
    }
    if (next !== byThread) {
      setPendingComposerMessagesByThreadNow(next);
    }
  }

  function enqueueComposerMessage(
    threadID: string,
    message: QueuedComposerMessage,
  ): void {
    updateThreadPendingComposerMessages(threadID, (previous) =>
      appendPendingComposerMessage(previous, "queue", message),
    );
  }

  function seedHeldComposerMessages(
    threadID: string,
    messages: QueuedComposerMessage[],
  ): void {
    updateThreadPendingComposerMessages(threadID, (previous) =>
      applyAuthoritativeComposerSnapshot(previous, messages),
    );
  }

  async function removeQueuedMessage(id: string): Promise<boolean> {
    const target = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "queue",
      activeThreadIDForState(getAppState()),
    );
    if (!target) {
      return false;
    }
    if (target.message.operationState === "preparing") {
      updateThreadPendingComposerMessages(target.threadID, (previous) => ({
        ...previous,
        queued: previous.queued.filter((message) => message.id !== id),
      }));
      return true;
    }
    const restoreTargetIfAbsent = (): void => {
      updateThreadPendingComposerMessages(target.threadID, (previous) => {
        if (
          previous.queued.some((message) => message.id === id) ||
          previous.guides.some((message) => message.id === id)
        ) {
          return previous;
        }
        const insertAt = Math.min(target.index, previous.queued.length);
        return {
          ...previous,
          queued: [
            ...previous.queued.slice(0, insertAt),
            target.message,
            ...previous.queued.slice(insertAt),
          ],
        };
      });
    };
    updateThreadPendingComposerMessages(target.threadID, (previous) => ({
      ...previous,
      queued: previous.queued.filter((message) => message.id !== id),
    }));
    try {
      const result = await window.wuu.dequeueTurn(target.threadID, id);
      if (!result.ok) {
        const targetThread = threadForTab(getAppState(), target.threadID);
        const materialized = materializedComposerMessageIDs(targetThread).has(id);
        if (!materialized) {
          restoreTargetIfAbsent();
        }
        setStatus(
          localizedText(
            materialized
              ? "composer.queueAlreadyHandled"
              : "composer.queueStillPending",
          ),
        );
        return false;
      }
      return true;
    } catch (error) {
      restoreTargetIfAbsent();
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("composer.cancelQueueFailed"),
      );
      return false;
    }
  }

  async function removeGuideMessage(id: string): Promise<boolean> {
    const target = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "guide",
      activeThreadIDForState(getAppState()),
    );
    if (!target) {
      return false;
    }
    if (target.message.operationState === "preparing") {
      updateThreadPendingComposerMessages(target.threadID, (previous) => ({
        ...previous,
        guides: previous.guides.filter((message) => message.id !== id),
      }));
      return true;
    }
    updateThreadPendingComposerMessages(target.threadID, (previous) => ({
      ...previous,
      guides: previous.guides.filter((message) => message.id !== id),
    }));
    try {
      const result = await window.wuu.unsteerTurn(target.threadID, id);
      if (!result.ok) {
        setStatus(localizedText("composer.guideAlreadyHandled"));
        return false;
      }
      return true;
    } catch (error) {
      updateThreadPendingComposerMessages(target.threadID, (previous) => {
        if (previous.guides.some((message) => message.id === id)) {
          return previous;
        }
        const insertAt = Math.min(target.index, previous.guides.length);
        return {
          ...previous,
          guides: [
            ...previous.guides.slice(0, insertAt),
            target.message,
            ...previous.guides.slice(insertAt),
          ],
        };
      });
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("composer.cancelGuideFailed"),
      );
      return false;
    }
  }

  function restorePendingComposerMessage(
    threadID: string,
    message: QueuedComposerMessage,
  ): void {
    rememberCollapsedPromptParts(threadID, message.text, message.contentParts);
    restoreComposerDraftForThread(threadID, {
      prompt: message.text,
      images: message.images.map((image) => ({ ...image })),
      files: message.files.map((file) => ({ ...file })),
    });
  }

  function canRestorePendingComposerMessage(): boolean {
    if (!composerDraftHasContent(getPrimaryComposerDraft())) {
      return true;
    }
    setStatus(localizedText("composer.clearBeforeEditingQueue"));
    return false;
  }

  async function editQueuedMessage(id: string): Promise<void> {
    const target = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "queue",
      activeThreadIDForState(getAppState()),
    );
    if (!target || !canRestorePendingComposerMessage()) {
      return;
    }
    if (!(await removeQueuedMessage(id))) {
      return;
    }
    restorePendingComposerMessage(target.threadID, target.message);
    setStatus(localizedText("composer.queueRestoredForEditing"));
  }

  async function editGuideMessage(id: string): Promise<void> {
    const target = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "guide",
      activeThreadIDForState(getAppState()),
    );
    if (!target || !canRestorePendingComposerMessage()) {
      return;
    }
    if (await removeGuideMessage(id)) {
      restorePendingComposerMessage(target.threadID, target.message);
    }
  }

  async function guideQueuedMessage(id: string): Promise<void> {
    const queuedTarget = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "queue",
      activeThreadIDForState(getAppState()),
    );
    const guideTarget = findPendingComposerMessage(
      pendingComposerMessagesByThreadRef.current,
      id,
      "guide",
      activeThreadIDForState(getAppState()),
    );
    if (guideTarget && !guideTarget.message.held) {
      await requeueGuideMessage(guideTarget);
      return;
    }
    const target = queuedTarget ?? guideTarget;
    if (!target) {
      return;
    }
    if (target.message.operationState) {
      return;
    }
    const currentState = getAppState();
    const targetThread = threadForTab(currentState, target.threadID);
    if (!targetThread) {
      return;
    }
    const turnID = activeTurnIDForThread(targetThread) ?? "";
    if (!turnID && !target.message.held) {
      setStatus(localizedText("composer.noActiveTurnToGuide"));
      return;
    }
    updateThreadPendingComposerMessages(target.threadID, (previous) => ({
      ...previous,
      queued: previous.queued.map((message) =>
        message.id === id ? { ...message, operationState: "switching" } : message,
      ),
    }));
    try {
      const files = inputFilesFromComposer(target.message.files);
      await window.wuu.steerTurn(
        targetThread.id,
        turnID,
        target.message.text.trim(),
        inputImagesFromComposer(target.message.images),
        target.message.id,
        files,
        target.message.activeDocument,
        ...(target.message.contentParts === undefined
          ? []
          : ([target.message.contentParts] as const)),
      );
      updateThreadPendingComposerMessages(target.threadID, (previous) => {
        const withoutQueue = {
          ...previous,
          queued: previous.queued.filter((message) => message.id !== id),
        };
        return target.message.held
          ? {
              ...withoutQueue,
              guides: previous.guides.filter((message) => message.id !== id),
            }
          : appendPendingComposerMessage(withoutQueue, "guide", {
              ...target.message,
              origin: "steer",
              operationState: undefined,
            });
      });
    } catch (error) {
      updateThreadPendingComposerMessages(target.threadID, (previous) => ({
        ...previous,
        queued: previous.queued.map((message) =>
          message.id === id ? { ...message, operationState: undefined } : message,
        ),
      }));
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("composer.guideFailed"),
      );
    }
  }

  async function requeueGuideMessage(
    target: LocatedPendingComposerMessage,
  ): Promise<void> {
    const { id } = target.message;
    if (target.message.operationState) {
      return;
    }
    updateThreadPendingComposerMessages(target.threadID, (previous) => ({
      ...previous,
      guides: previous.guides.map((message) =>
        message.id === id ? { ...message, operationState: "switching" } : message,
      ),
    }));
    try {
      const result = await window.wuu.requeueTurn(target.threadID, id);
      if (!result.ok) {
        updateThreadPendingComposerMessages(target.threadID, (previous) => ({
          ...previous,
          guides: previous.guides.filter((message) => message.id !== id),
        }));
        setStatus(localizedText("composer.guideAlreadyHandled"));
        return;
      }
      updateThreadPendingComposerMessages(target.threadID, (previous) => {
        const withoutGuide = {
          ...previous,
          guides: previous.guides.filter((message) => message.id !== id),
        };
        return appendPendingComposerMessage(withoutGuide, "queue", {
          ...target.message,
          origin: "queue",
          operationState: undefined,
        });
      });
    } catch (error) {
      updateThreadPendingComposerMessages(target.threadID, (previous) => ({
        ...previous,
        guides: previous.guides.map((message) =>
          message.id === id ? { ...message, operationState: undefined } : message,
        ),
      }));
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("composer.requeueGuideFailed"),
      );
    }
  }

  function threadHasPendingComposerMessages(threadID: string): boolean {
    return !threadPendingComposerMessagesIsEmpty(
      pendingComposerMessagesForThread(
        pendingComposerMessagesByThreadRef.current,
        threadID,
      ),
    );
  }

  return {
    pendingComposerMessagesByThread,
    pendingComposerMessagesByThreadRef,
    setPendingComposerMessagesByThreadNow,
    pendingComposerMessagesForThread: (threadID) =>
      pendingComposerMessagesForThread(
        pendingComposerMessagesByThread,
        threadID,
      ),
    updateThreadPendingComposerMessages,
    clearThreadPendingComposerMessages,
    removePendingComposerMessageByID,
    syncPendingComposerMessagesFromServerEvent,
    reconcilePendingComposerMessagesForState,
    seedHeldComposerMessages,
    enqueueComposerMessage,
    removeQueuedMessage,
    removeGuideMessage,
    editQueuedMessage,
    editGuideMessage,
    guideQueuedMessage,
    threadHasPendingComposerMessages,
  };
}
