import type { MutableRefObject, SetStateAction } from "react";
import type { GitCommitResult, GitPullRequestResult } from "../shared/protocol";
import { sameRuntimeContext, type AppState } from "./AppState";
import type { EnvironmentPanelMenu } from "./EnvironmentPanel";
import { translateCurrent } from "./i18n";
import { showErrorToast, toastErrorMessage } from "./Toast";

type SetAppState = (update: SetStateAction<AppState>) => void;

export type EnvironmentActionsDeps = {
  getAppState: () => AppState;
  getEnvironmentRoot: () => string | undefined;
  setAppState: SetAppState;
  closeWorkspaceMenus: () => void;
  setEnvironmentPanelOpen: (open: boolean) => void;
  setEnvironmentPanelDismissed: (dismissed: boolean) => void;
  setEnvironmentPanelMenu: (menu: EnvironmentPanelMenu) => void;
  closeRuntimeMenus: () => void;
  getEnvironmentPanelVisible: () => boolean;
  environmentPanelContainsActiveElement: () => boolean;
  focusEnvironmentToggle: () => void;
  gitRefreshTimerRef: MutableRefObject<number | undefined>;
  gitRefreshInFlightRef: MutableRefObject<boolean>;
  gitRefreshQueuedRef: MutableRefObject<boolean>;
};

export type EnvironmentActions = {
  checkoutBranch: (branch: string) => Promise<void>;
  scheduleGitStatusRefresh: (delayMs: number) => void;
  createAndCheckoutBranch: (branch: string) => Promise<void>;
  commitEnvironmentChanges: (params: {
    message: string;
    includeUnstaged: boolean;
  }) => Promise<GitCommitResult>;
  generateEnvironmentCommitMessage: (params: {
    includeUnstaged: boolean;
  }) => Promise<string>;
  createEnvironmentPullRequest: (params: {
    title: string;
    body: string;
    draft: boolean;
  }) => Promise<GitPullRequestResult>;
  toggleEnvironmentPanel: () => void;
  openEnvironmentPanel: () => void;
  closeEnvironmentPanel: (options?: { dismissed?: boolean }) => void;
};

export function createEnvironmentActions(
  deps: EnvironmentActionsDeps,
): EnvironmentActions {
  function setStatus(status: string): void {
    showErrorToast(status);
  }

  function environmentRootIsCurrent(root: string): boolean {
    return deps.getEnvironmentRoot() === root;
  }

  function branchError(error: unknown, fallback: string): Error {
    const message = toastErrorMessage(error, fallback);
    return new Error(message === "cannot run Git actions while a thread is running in this working tree"
      ? translateCurrent("git.checkoutBlockedByRunningThread")
      : message);
  }

  async function checkoutBranch(branch: string): Promise<void> {
    const root = deps.getEnvironmentRoot();
    if (!branch || !root) {
      return;
    }
    try {
      // The host checks the actual target worktree at mutation time. A cached
      // renderer busy flag may belong to a previous context or failed lookup.
      const gitStatus = await window.wuu.checkoutGitBranch(branch, root);
      if (!environmentRootIsCurrent(root)) {
        return;
      }
      deps.setAppState((current) => ({
        ...current,
        gitStatus,
        status: current.status === "ready" ? "ready" : current.status,
      }));
      deps.closeWorkspaceMenus();
    } catch (error) {
      if (!environmentRootIsCurrent(root)) {
        return;
      }
      throw branchError(error, translateCurrent("git.checkoutFailed"));
    }
  }

  async function refreshGitStatus(): Promise<void> {
    const context = deps.getAppState().activeContext;
    const root = deps.getEnvironmentRoot();
    if (!context || !root) {
      return;
    }
    if (deps.gitRefreshInFlightRef.current) {
      deps.gitRefreshQueuedRef.current = true;
      return;
    }
    deps.gitRefreshInFlightRef.current = true;
    try {
      const gitStatus = await window.wuu.gitStatus(root);
      if (
        !sameRuntimeContext(deps.getAppState().activeContext, context) ||
        deps.getEnvironmentRoot() !== root
      ) {
        return;
      }
      deps.setAppState((current) => ({
        ...current,
        gitStatus,
        status: current.status === "ready" ? "ready" : current.status,
      }));
    } catch (error) {
      if (
        !sameRuntimeContext(deps.getAppState().activeContext, context) ||
        deps.getEnvironmentRoot() !== root
      ) {
        return;
      }
      setStatus(
        error instanceof Error
          ? error.message
          : translateCurrent("git.refreshFailed"),
      );
    } finally {
      deps.gitRefreshInFlightRef.current = false;
      if (deps.gitRefreshQueuedRef.current) {
        deps.gitRefreshQueuedRef.current = false;
        scheduleGitStatusRefresh(150);
      }
    }
  }

  function scheduleGitStatusRefresh(delayMs: number): void {
    if (!deps.getAppState().activeContext || !deps.getEnvironmentRoot()) {
      return;
    }
    if (deps.gitRefreshTimerRef.current !== undefined) {
      window.clearTimeout(deps.gitRefreshTimerRef.current);
    }
    deps.gitRefreshTimerRef.current = window.setTimeout(() => {
      deps.gitRefreshTimerRef.current = undefined;
      void refreshGitStatus();
    }, delayMs);
  }

  async function createAndCheckoutBranch(branch: string): Promise<void> {
    const root = deps.getEnvironmentRoot();
    if (!branch || !root) {
      return;
    }
    try {
      const result = await window.wuu.createCheckoutGitBranch(branch, root);
      if (!environmentRootIsCurrent(root)) {
        return;
      }
      deps.setAppState((current) => ({
        ...current,
        gitStatus: result.status,
        status: current.status === "ready" ? "ready" : current.status,
      }));
      deps.setEnvironmentPanelMenu(null);
    } catch (error) {
      if (!environmentRootIsCurrent(root)) {
        throw error;
      }
      throw branchError(error, translateCurrent("git.branch.createFailed"));
    }
  }

  async function commitEnvironmentChanges(params: {
    message: string;
    includeUnstaged: boolean;
  }): Promise<GitCommitResult> {
    const root = deps.getEnvironmentRoot();
    if (!root) {
      throw new Error("session working directory is unavailable");
    }
    const result = await window.wuu.commitGitChanges(
      {
        message: params.message,
        include_unstaged: params.includeUnstaged,
      },
      root,
    );
    if (environmentRootIsCurrent(root)) {
      deps.setAppState((current) => ({
        ...current,
        gitStatus: result.status,
        status: "ready",
      }));
    }
    return result;
  }

  async function generateEnvironmentCommitMessage(params: {
    includeUnstaged: boolean;
  }): Promise<string> {
    const root = deps.getEnvironmentRoot();
    if (!root) {
      throw new Error("session working directory is unavailable");
    }
    const result = await window.wuu.generateCommitMessage(
      { include_unstaged: params.includeUnstaged },
      root,
    );
    return result.message;
  }

  async function createEnvironmentPullRequest(params: {
    title: string;
    body: string;
    draft: boolean;
  }): Promise<GitPullRequestResult> {
    const root = deps.getEnvironmentRoot();
    if (!root) {
      throw new Error("session working directory is unavailable");
    }
    const result = await window.wuu.createPullRequest(
      {
        title: params.title,
        body: params.body,
        draft: params.draft,
      },
      root,
    );
    if (environmentRootIsCurrent(root)) {
      deps.setAppState((current) => ({
        ...current,
        gitStatus: result.status,
        status: "ready",
      }));
    }
    return result;
  }

  function toggleEnvironmentPanel(): void {
    if (deps.getEnvironmentPanelVisible()) {
      closeEnvironmentPanel({ dismissed: true });
      return;
    }
    openEnvironmentPanel();
  }

  function openEnvironmentPanel(): void {
    deps.setEnvironmentPanelOpen(true);
    deps.setEnvironmentPanelDismissed(false);
    deps.closeRuntimeMenus();
  }

  function closeEnvironmentPanel({
    dismissed = false,
  }: { dismissed?: boolean } = {}): void {
    restoreEnvironmentPanelFocus();
    deps.setEnvironmentPanelOpen(false);
    if (dismissed) {
      deps.setEnvironmentPanelDismissed(true);
    }
    deps.setEnvironmentPanelMenu(null);
  }

  function restoreEnvironmentPanelFocus(): void {
    if (deps.environmentPanelContainsActiveElement()) {
      deps.focusEnvironmentToggle();
    }
  }

  return {
    checkoutBranch,
    scheduleGitStatusRefresh,
    createAndCheckoutBranch,
    commitEnvironmentChanges,
    generateEnvironmentCommitMessage,
    createEnvironmentPullRequest,
    toggleEnvironmentPanel,
    openEnvironmentPanel,
    closeEnvironmentPanel,
  };
}
