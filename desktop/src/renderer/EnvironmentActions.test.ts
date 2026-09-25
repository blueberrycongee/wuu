import { afterEach, describe, expect, it, vi } from "vitest";
import type { GitStatusResult, RuntimeContext } from "../shared/protocol";
import { initialState, type AppState } from "./AppState";
import { createEnvironmentActions } from "./EnvironmentActions";
import type { EnvironmentPanelMenu } from "./EnvironmentPanel";
import { translateCurrent as t } from "./i18n";

const originalWuu = (window as unknown as { wuu?: unknown }).wuu;

function restoreWuu(): void {
  if (originalWuu === undefined) {
    delete (window as unknown as { wuu?: unknown }).wuu;
    return;
  }
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: originalWuu,
  });
}

afterEach(() => {
  vi.useRealTimers();
  restoreWuu();
});

function projectContext(): RuntimeContext {
  return { kind: "project", project_id: "project-1", cwd: "/tmp/project-1" };
}

function gitStatus(branch = "main"): GitStatusResult {
  return { is_repo: true, branch, dirty_count: 0 };
}

function installWuuApi(): {
  checkoutGitBranch: ReturnType<typeof vi.fn>;
  gitStatus: ReturnType<typeof vi.fn>;
  commitGitChanges: ReturnType<typeof vi.fn>;
  createCheckoutGitBranch: ReturnType<typeof vi.fn>;
} {
  const checkoutGitBranch = vi.fn().mockResolvedValue(gitStatus("feature"));
  const gitStatusMock = vi.fn().mockResolvedValue(gitStatus("main"));
  const createCheckoutGitBranch = vi.fn().mockResolvedValue({ status: gitStatus("feature") });
  const commitGitChanges = vi.fn().mockResolvedValue({
    commit: "abc123",
    status: gitStatus("main"),
  });
  Object.defineProperty(window, "wuu", {
    configurable: true,
    value: {
      checkoutGitBranch,
      gitStatus: gitStatusMock,
      commitGitChanges,
      createCheckoutGitBranch,
      createPullRequest: vi.fn().mockResolvedValue({
        url: "https://example.test/pr/1",
        already_exists: false,
        status: gitStatus("main"),
      }),
    },
  });
  return { checkoutGitBranch, gitStatus: gitStatusMock, commitGitChanges, createCheckoutGitBranch };
}

function buildActions({
  initial = { ...initialState, activeContext: projectContext(), status: "ready" },
  environmentRoot = "/tmp/project-1",
  environmentPanelVisible = false,
  panelContainsFocus = false,
}: {
  initial?: AppState;
  environmentRoot?: string;
  environmentPanelVisible?: boolean;
  panelContainsFocus?: boolean;
} = {}) {
  let appState = initial;
  let currentEnvironmentRoot = environmentRoot;
  let activeMenu: EnvironmentPanelMenu = null;
  let panelOpen = false;
  let panelDismissed = false;
  const closeProjectMenus = vi.fn();
  const closeRuntimeMenus = vi.fn();
  const focusEnvironmentToggle = vi.fn();
  const actions = createEnvironmentActions({
    getAppState: () => appState,
    getEnvironmentRoot: () => currentEnvironmentRoot,
    setAppState: (update) => {
      appState = typeof update === "function" ? update(appState) : update;
    },
    closeProjectMenus,
    setEnvironmentPanelOpen: (open) => {
      panelOpen = open;
    },
    setEnvironmentPanelDismissed: (dismissed) => {
      panelDismissed = dismissed;
    },
    setEnvironmentPanelMenu: (menu) => {
      activeMenu = menu;
    },
    closeRuntimeMenus,
    getEnvironmentPanelVisible: () => environmentPanelVisible,
    environmentPanelContainsActiveElement: () => panelContainsFocus,
    focusEnvironmentToggle,
    gitRefreshTimerRef: { current: undefined },
    gitRefreshInFlightRef: { current: false },
    gitRefreshQueuedRef: { current: false },
  });

  return {
    actions,
    getAppState: () => appState,
    setEnvironmentRoot: (root: string) => {
      currentEnvironmentRoot = root;
    },
    getPanelState: () => ({ activeMenu, panelOpen, panelDismissed }),
    closeProjectMenus,
    closeRuntimeMenus,
    focusEnvironmentToggle,
  };
}

describe("createEnvironmentActions", () => {
  it("checks out a branch and keeps ready status unchanged", async () => {
    const api = installWuuApi();
    const harness = buildActions();

    await harness.actions.checkoutBranch("feature");

    expect(api.checkoutGitBranch).toHaveBeenCalledWith("feature", "/tmp/project-1");
    expect(harness.closeProjectMenus).toHaveBeenCalled();
    expect(harness.getAppState().gitStatus?.branch).toBe("feature");
    expect(harness.getAppState().status).toBe("ready");
  });

  it("allows a new session to check out while other sessions are running", async () => {
    const api = installWuuApi();
    const harness = buildActions({ initial: { ...initialState, activeContext: projectContext(), running: true } });
    await harness.actions.checkoutBranch("feature");
    expect(api.checkoutGitBranch).toHaveBeenCalledWith("feature", "/tmp/project-1");
    expect(harness.getAppState().gitStatus?.branch).toBe("feature");
  });

  it.each(["checkoutBranch", "createAndCheckoutBranch"] as const)("surfaces host occupancy conflicts for %s without silently succeeding", async (action) => {
    const api = installWuuApi();
    const mutation = action === "checkoutBranch" ? api.checkoutGitBranch : api.createCheckoutGitBranch;
    mutation.mockRejectedValueOnce(new Error("Error invoking remote method 'wuu:git-checkout': Error: cannot run Git actions while a thread is running in this working tree"));
    const harness = buildActions();

    await expect(harness.actions[action]("feature")).rejects.toThrow(t("git.checkoutBlockedByRunningThread"));
    expect(mutation).toHaveBeenCalledWith("feature", "/tmp/project-1");
    expect(harness.getAppState().gitStatus).toBeUndefined();
    expect(harness.closeProjectMenus).not.toHaveBeenCalled();

    await harness.actions[action]("feature");
    expect(harness.getAppState().gitStatus?.branch).toBe("feature");
  });

  it("preserves Git conflict details without the Electron wrapper", async () => {
    const api = installWuuApi();
    api.checkoutGitBranch.mockRejectedValueOnce(new Error("Error invoking remote method 'wuu:git-checkout': Error: Your local changes would be overwritten by checkout"));
    const harness = buildActions();
    await expect(harness.actions.checkoutBranch("feature")).rejects.toThrow(/^Your local changes would be overwritten by checkout$/);
    expect(harness.closeProjectMenus).not.toHaveBeenCalled();
  });

  it("returns to ready after committing environment changes", async () => {
    const api = installWuuApi();
    const harness = buildActions();

    await harness.actions.commitEnvironmentChanges({
      message: "commit message",
      includeUnstaged: true,
    });

    expect(api.commitGitChanges).toHaveBeenCalledWith(
      {
        message: "commit message",
        include_unstaged: true,
      },
      "/tmp/project-1",
    );
    expect(harness.getAppState().status).toBe("ready");
  });

  it("discards a Git status response after the session workspace changes", async () => {
    vi.useFakeTimers();
    const api = installWuuApi();
    let resolveStatus: ((status: GitStatusResult) => void) | undefined;
    api.gitStatus.mockImplementationOnce(
      () => new Promise<GitStatusResult>((resolve) => {
        resolveStatus = resolve;
      }),
    );
    const harness = buildActions();

    harness.actions.scheduleGitStatusRefresh(0);
    await vi.runOnlyPendingTimersAsync();
    expect(api.gitStatus).toHaveBeenCalledWith("/tmp/project-1");

    harness.setEnvironmentRoot("/tmp/project-1-worktree");
    resolveStatus?.(gitStatus("stale-branch"));
    await Promise.resolve();

    expect(harness.getAppState().gitStatus).toBeUndefined();
  });

  it("opens and closes the environment panel with focus restoration", () => {
    installWuuApi();
    const harness = buildActions({
      environmentPanelVisible: true,
      panelContainsFocus: true,
    });

    harness.actions.openEnvironmentPanel();
    expect(harness.getPanelState().panelOpen).toBe(true);
    expect(harness.getPanelState().panelDismissed).toBe(false);
    expect(harness.closeRuntimeMenus).toHaveBeenCalled();

    harness.actions.toggleEnvironmentPanel();
    expect(harness.getPanelState()).toEqual({
      activeMenu: null,
      panelOpen: false,
      panelDismissed: true,
    });
    expect(harness.focusEnvironmentToggle).toHaveBeenCalled();
  });
});
