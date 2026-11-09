import type { SetStateAction } from "react";
import {
  createDraftSessionTab,
  ensureSessionTab,
  isThreadRunning,
  persistComposerDrafts,
  threadSessionTabID,
  type AppState,
  type ConversationPaneID,
  type ComposerDraftSnapshot,
} from "./AppState";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type ConversationPaneActionsDeps = {
  setAppState: SetAppState;
  getComposerDraftSnapshot: () => ComposerDraftSnapshot;
  moveSplitDraftToGlobalComposer: (pane: ConversationPaneID) => void;
};

export type ConversationPaneActions = {
  activateConversationPane: (pane: ConversationPaneID) => void;
  closeConversationPane: (pane: ConversationPaneID) => void;
};

export function createConversationPaneActions(
  deps: ConversationPaneActionsDeps,
): ConversationPaneActions {
  function activateConversationPane(pane: ConversationPaneID): void {
    deps.setAppState((current) => {
      if (pane === "secondary" && !current.secondaryThread) {
        return current;
      }
      const thread =
        pane === "secondary" ? current.secondaryThread : current.thread;
      return {
        ...current,
        activePane: pane,
        activeSessionTabID: thread
          ? threadSessionTabID(thread.id)
          : current.activeSessionTabID,
        running: isThreadRunning(thread),
      };
    });
  }

  function closeConversationPane(pane: ConversationPaneID): void {
    const outgoingDraft = deps.getComposerDraftSnapshot();
    deps.setAppState((state) => {
      const current = persistComposerDrafts(state, outgoingDraft);
      if (pane === "secondary") {
        return {
          ...current,
          secondaryThread: undefined,
          activePane: "primary",
          activeSessionTabID: current.thread
            ? threadSessionTabID(current.thread.id)
            : current.activeSessionTabID,
          running: isThreadRunning(current.thread),
          status: "ready",
        };
      }
      if (current.secondaryThread) {
        return {
          ...current,
          thread: current.secondaryThread,
          secondaryThread: undefined,
          activePane: "primary",
          activeSessionTabID: threadSessionTabID(current.secondaryThread.id),
          running: isThreadRunning(current.secondaryThread),
          status: "ready",
        };
      }
      if (!current.activeContext) {
        return current;
      }
      const nextTab = createDraftSessionTab(
        `draft:closed:${Date.now()}`,
        current.activeContext,
      );
      return {
        ...current,
        thread: undefined,
        activePane: "primary",
        sessionTabs: ensureSessionTab(current.sessionTabs, nextTab),
        activeSessionTabID: nextTab.id,
        running: false,
        status: "ready",
      };
    });
    deps.moveSplitDraftToGlobalComposer(
      pane === "secondary" ? "primary" : "secondary",
    );
  }

  return { activateConversationPane, closeConversationPane };
}
