import type { Dispatch, SetStateAction } from "react";
import type { Thread } from "../shared/protocol";
import {
  activeThreadForState,
  createSkillsSessionTab,
  ensureSessionTab,
  initialSplitComposerDrafts,
  persistActiveSessionTabDraft,
  type AppState,
  type ComposerDraftState,
} from "./AppState";
import type { ComposerFile, ComposerImage } from "./ComposerMessages";
import type { ContextCompositionEntry } from "./ContextCompositionCard";
import type { InstructionFilesEntry } from "./InstructionFilesCard";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import type { SettingsPage } from "./SettingsView";
import { localizedText, translateCurrent } from "./i18n";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type WorkspaceActionsDeps = {
  getAppState: () => AppState;
  setAppState: SetAppState;
  getActiveTitle: () => string;
  getPrimaryComposerDraft: () => ComposerDraftState;
  setSplitComposerDrafts: Dispatch<
    SetStateAction<Record<"primary" | "secondary", ComposerDraftState>>
  >;
  setPrompt: Dispatch<SetStateAction<string>>;
  setComposerImages: Dispatch<SetStateAction<ComposerImage[]>>;
  setComposerFiles: Dispatch<SetStateAction<ComposerFile[]>>;
  cancelViewSwitch: () => void;
  setContextCompositionEntries: Dispatch<
    SetStateAction<ContextCompositionEntry[]>
  >;
  setInstructionFilesEntries: Dispatch<
    SetStateAction<InstructionFilesEntry[]>
  >;
  scheduleStreamScroll: () => void;
  closeWorkspaceMenus: () => void;
  setSettingsInitialPage: (page: SettingsPage) => void;
  setSettingsOpen: (open: boolean) => void;
};

export type WorkspaceActions = {
  openSkillsTab: () => void;
  dismissContextCompositionEntry: (id: string) => void;
  dismissInstructionFilesEntry: (id: string) => void;
  openInstructions: () => void;
  openContextComposition: () => void;
};

function createContextCompositionEntryID(): string {
  return `context-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

function createInstructionFilesEntryID(): string {
  return `instructions-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function createWorkspaceActions(
  deps: WorkspaceActionsDeps,
): WorkspaceActions {
  function openSkillsTab(): void {
    const state = deps.getAppState();
    if (!state.activeContext) {
      return;
    }
    const tab = createSkillsSessionTab(state.activeContext);
    
    deps.setSplitComposerDrafts(initialSplitComposerDrafts());
    deps.setAppState((current) => ({
      ...persistActiveSessionTabDraft(current, deps.getPrimaryComposerDraft()),
      secondaryThread: undefined,
      activePane: "primary",
      sessionTabs: ensureSessionTab(current.sessionTabs, tab),
      activeSessionTabID: tab.id,
      allowThreadAutoActivation: false,
      running: false,
      status: "ready",
    }));
  }

  function dismissContextCompositionEntry(id: string): void {
    deps.setContextCompositionEntries((entries) =>
      entries.filter((entry) => entry.id !== id),
    );
  }

  function dismissInstructionFilesEntry(id: string): void {
    deps.setInstructionFilesEntries((entries) =>
      entries.filter((entry) => entry.id !== id),
    );
  }

  function openInstructions(): void {
    const activeThread = activeThreadForActions(deps.getAppState());
    if (!activeThread) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("contextComposition.noCurrentConversation"),
      }));
      return;
    }
    const threadID = activeThread.id;
    const title = activeThread.preview || deps.getActiveTitle();
    const entryID = createInstructionFilesEntryID();
    deps.setInstructionFilesEntries((entries) => [
      ...entries,
      {
        id: entryID,
        threadID,
        title,
        loading: true,
      },
    ]);
    deps.scheduleStreamScroll();
    void (async () => {
      try {
        const result = await window.wuu.listInstructionFiles();
        deps.setInstructionFilesEntries((entries) =>
          entries.map((entry) =>
            entry.id === entryID
              ? { ...entry, loading: false, result, error: undefined }
              : entry,
          ),
        );
        deps.scheduleStreamScroll();
      } catch (error) {
        deps.setInstructionFilesEntries((entries) =>
          entries.map((entry) =>
            entry.id === entryID
              ? {
                  ...entry,
                  loading: false,
                  error: desktopApiErrorMessage(error, translateCurrent("instructions.readFailed")),
                }
              : entry,
          ),
        );
      }
    })();
  }

  function openContextComposition(): void {
    const activeThread = activeThreadForActions(deps.getAppState());
    if (!activeThread) {
      deps.setAppState((current) => ({
        ...current,
        status: localizedText("contextComposition.noCurrentConversation"),
      }));
      return;
    }
    const threadID = activeThread.id;
    const title = activeThread.preview || deps.getActiveTitle();
    const entryID = createContextCompositionEntryID();
    const afterTurnID = activeThread.turns.at(-1)?.id;
    deps.setContextCompositionEntries((entries) => [
      ...entries,
      {
        id: entryID,
        threadID,
        afterTurnID,
        title,
        loading: true,
      },
    ]);
    deps.scheduleStreamScroll();
    void (async () => {
      try {
        const result = await window.wuu.getThreadContextComposition(threadID);
        deps.setContextCompositionEntries((entries) =>
          entries.map((entry) =>
            entry.id === entryID
              ? {
                  ...entry,
                  loading: false,
                  result,
                  error: undefined,
                }
              : entry,
          ),
        );
        deps.scheduleStreamScroll();
      } catch (error) {
        deps.setContextCompositionEntries((entries) =>
          entries.map((entry) =>
            entry.id === entryID
              ? {
                  ...entry,
                  loading: false,
                  error: desktopApiErrorMessage(error, translateCurrent("contextComposition.readFailed")),
                }
              : entry,
          ),
        );
      }
    })();
  }

  return {
    openSkillsTab,
    dismissContextCompositionEntry,
    dismissInstructionFilesEntry,
    openInstructions,
    openContextComposition,
  };
}

function activeThreadForActions(state: AppState): Thread | undefined {
  return activeThreadForState(state);
}
