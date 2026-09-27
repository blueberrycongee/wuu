import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DesktopProject, RuntimeContext, Thread } from "../shared/protocol";
import {
  isThreadRunning,
  isThreadUnread,
} from "./AppState";
import { SIDEBAR_SECTION_PINNED } from "./AppSidebar";
import {
  mergeSidebarThreadSnapshots,
  useSidebarWorkspaceState,
  type SidebarWorkspaceStateController,
} from "./SidebarWorkspaceState";

let mountedRoots: Root[] = [];

afterEach(() => {
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  document.body.innerHTML = "";
  window.localStorage.clear();
  Reflect.deleteProperty(window, "wuu");
  vi.restoreAllMocks();
});

async function flushEffects(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

function project(id: string, path = `/tmp/${id}`): DesktopProject {
  return {
    id,
    name: id,
    path,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function thread(id: string, cwd: string): Thread {
  return {
    id,
    title: id,
    preview: id,
    cwd,
    status: "idle",
    pinned: false,
    archived: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  } as unknown as Thread;
}

describe("mergeSidebarThreadSnapshots", () => {
  it("stabilizes when the cached thread contains a longer turn history", () => {
    const firstTurn = {
      id: "turn-first",
      items: [],
      items_view: "full" as const,
      status: "completed" as const,
    };
    const listed = {
      ...thread("thread-alpha", "/tmp/alpha"),
      turns: [firstTurn],
    };
    const cached = {
      ...listed,
      turns: [
        { ...firstTurn },
        {
          id: "turn-second",
          items: [],
          items_view: "full" as const,
          status: "completed" as const,
        },
      ],
    };

    const reconciled = mergeSidebarThreadSnapshots([cached], [listed]);
    const repeated = mergeSidebarThreadSnapshots(reconciled, [listed]);

    expect(repeated).toBe(reconciled);
  });

  it.each(["in_progress", "completed", "interrupted", "failed"] as const)("stabilizes when a listed %s turn omits cached trailing items", (status) => {
    const listedItem = {
      id: "item-listed",
      type: "user_message" as const,
      content: [{ type: "input_text" as const, text: "question" }],
    };
    const cachedItem = {
      id: "item-cached",
      type: "user_message" as const,
      content: [{ type: "input_text" as const, text: "follow-up" }],
    };
    const listed = {
      ...thread("thread-alpha", "/tmp/alpha"),
      turns: [{
        id: "turn-alpha",
        items: [listedItem],
        items_view: "full" as const,
        status,
      }],
    };
    const cached = {
      ...listed,
      turns: [{
        ...listed.turns[0],
        items: [listedItem, cachedItem],
      }],
    };

    const reconciled = mergeSidebarThreadSnapshots([cached], [listed]);
    const repeated = mergeSidebarThreadSnapshots(reconciled, [listed]);

    expect(reconciled[0]?.turns[0]?.items).toEqual(
      status === "in_progress" ? [listedItem, cachedItem] : [listedItem],
    );
    expect(repeated).toBe(reconciled);
  });
});

async function renderSidebarWorkspaceState({
  projects = [],
  threads = [],
  activeContext,
  activeWorkspaceID,
  backgroundLoadingEnabled = true,
}: {
  backgroundLoadingEnabled?: boolean;
  projects?: DesktopProject[];
  threads?: Thread[];
  activeContext?: RuntimeContext;
  activeWorkspaceID?: string;
} = {}): Promise<{
  get: () => SidebarWorkspaceStateController;
  rerender: (next: {
    backgroundLoadingEnabled?: boolean;
    projects?: DesktopProject[];
    threads?: Thread[];
    activeContext?: RuntimeContext;
    activeWorkspaceID?: string;
  }) => Promise<void>;
}> {
  let latest: SidebarWorkspaceStateController | undefined;
  let props = { projects, threads, activeContext, activeWorkspaceID, backgroundLoadingEnabled };

  function Probe(nextProps: typeof props) {
    latest = useSidebarWorkspaceState({
      backgroundLoadingEnabled: nextProps.backgroundLoadingEnabled,
      projects: nextProps.projects,
      threads: nextProps.threads,
      activeContext: nextProps.activeContext,
      activeWorkspaceID: nextProps.activeWorkspaceID,
      setStatus: vi.fn(),
    });
    return null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  async function rerender(next: Partial<typeof props>): Promise<void> {
    props = { ...props, ...next };
    await act(async () => {
      root.render(createElement(Probe, props));
      await flushEffects();
    });
  }

  await rerender(props);

  return {
    get: () => {
      if (!latest) {
        throw new Error("sidebar project state was not rendered");
      }
      return latest;
    },
    rerender,
  };
}

describe("useSidebarWorkspaceState", () => {
  it.each(["project", "all"])("keeps titles learned during a workspace switch when an older %s list resolves", async (catalog) => {
    const alpha = project("alpha");
    const beta = project("beta");
    const alphaThread = thread("thread-alpha", alpha.path);
    const oldBetaThread = thread("thread-beta", beta.path);
    const unseen = thread("unseen-beta", beta.path);
    const renamedBetaThread = { ...oldBetaThread, title: "Beta release investigation" };
    const betaContext: RuntimeContext = { kind: "project", project_id: beta.id, cwd: beta.path };
    const alphaContext: RuntimeContext = { kind: "project", project_id: alpha.id, cwd: alpha.path };
    let resolveList!: (result: { threads: Thread[] }) => void;
    const listThreads = vi.fn(() => new Promise<{ threads: Thread[] }>((resolve) => { resolveList = resolve; }));
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: catalog === "project" ? { listThreads } : { listAllThreads: listThreads },
    });
    if (catalog === "project") {
      window.localStorage.setItem("wuu.desktop.expandedSidebarSectionIDs", JSON.stringify([beta.id]));
    }
    const hook = await renderSidebarWorkspaceState({
      projects: [alpha, beta], threads: [alphaThread], activeContext: alphaContext, activeWorkspaceID: alpha.id,
    });
    expect(listThreads).toHaveBeenCalledOnce();

    await hook.rerender({ threads: [renamedBetaThread], activeContext: betaContext, activeWorkspaceID: beta.id });
    await hook.rerender({ threads: [alphaThread], activeContext: alphaContext, activeWorkspaceID: alpha.id });
    await act(async () => { resolveList({ threads: [oldBetaThread, unseen] }); });

    expect(hook.get().workspaceThreadsByWorkspaceID.beta.find((item) => item.id === oldBetaThread.id)?.title).toBe(renamedBetaThread.title);
    expect(hook.get().workspaceThreadsByWorkspaceID.beta.map((item) => item.id)).toContain(unseen.id);
    expect(hook.get().workspaceThreadsByWorkspaceID.alpha[0]?.title).toBe(alphaThread.title);
  });

  it("keeps a session that started while the global catalog was still empty", async () => {
    const beta = project("beta");
    const created = {
      ...thread("new-session", beta.path),
      workspace_id: beta.id,
      workspace_kind: "project" as const,
    };
    let resolveList!: (result: { threads: Thread[] }) => void;
    const listAllThreads = vi.fn(() => new Promise<{ threads: Thread[] }>((resolve) => { resolveList = resolve; }));
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { listAllThreads },
    });
    const hook = await renderSidebarWorkspaceState({
      projects: [beta], backgroundLoadingEnabled: false,
    });
    await hook.rerender({ backgroundLoadingEnabled: true });
    expect(listAllThreads).toHaveBeenCalledOnce();

    act(() => {
      hook.get().syncSidebarServerEvent({
        kind: "notification",
        workdir: beta.path,
        message: { method: "thread/started", params: { thread: created } },
      });
    });
    expect(hook.get().workspaceThreadsByWorkspaceID.beta.map((item) => item.id)).toEqual([created.id]);

    await act(async () => { resolveList({ threads: [] }); });
    expect(hook.get().workspaceThreadsByWorkspaceID.beta.map((item) => item.id)).toEqual([created.id]);
  });

  it("refreshes unchanged rows without resurrecting a session removed during the request", async () => {
    const beta = project("beta");
    const removed = thread("removed", beta.path);
    const existing = thread("existing", beta.path);
    const hook = await renderSidebarWorkspaceState({ projects: [beta] });
    act(() => { hook.get().cacheSidebarThreads([removed, existing]); });
    let resolveList!: (result: { threads: Thread[] }) => void;
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: { listThreads: () => new Promise<{ threads: Thread[] }>((resolve) => { resolveList = resolve; }) },
    });
    let loading!: Promise<void>;
    act(() => { loading = hook.get().loadWorkspaceThreads(beta); });
    act(() => { hook.get().removeCachedSidebarThread(removed.id); });
    const renamed = { ...existing, title: "Changed while disconnected" };
    await act(async () => {
      resolveList({ threads: [removed, renamed] });
      await loading;
    });
    expect(hook.get().workspaceThreadsByWorkspaceID.beta).toEqual([renamed]);
  });

  it("retains a title-only resume update after leaving a scratch workspace", async () => {
    const alphaThread = thread("thread-alpha", "/tmp/scratch-alpha");
    const betaThread = thread("thread-beta", "/tmp/scratch-beta");
    const hook = await renderSidebarWorkspaceState({
      threads: [alphaThread], activeContext: { kind: "no_project", cwd: alphaThread.cwd },
    });
    const renamed = { ...alphaThread, title: "Investigate alpha deployment", preview: "Alpha deployment" };
    await hook.rerender({ threads: [renamed] });
    await hook.rerender({ threads: [betaThread], activeContext: { kind: "no_project", cwd: betaThread.cwd } });

    expect(hook.get().cachedScratchThreads.find((item) => item.id === alphaThread.id)?.title).toBe(renamed.title);
  });

  it("waits for bootstrap before fetching background catalogs, then populates the sidebar", async () => {
    const listed = thread("background-thread", "/tmp/other");
    const listAllThreads = vi.fn().mockResolvedValue({ threads: [listed] });
    const listThreads = vi.fn().mockResolvedValue({ threads: [listed] });
    Object.defineProperty(window, "wuu", { configurable: true, value: { listAllThreads, listThreads } });
    window.localStorage.setItem("wuu.desktop.expandedSidebarSectionIDs", JSON.stringify(["other"]));
    const hook = await renderSidebarWorkspaceState({
      projects: [project("other")], backgroundLoadingEnabled: false,
    });
    expect(listAllThreads).not.toHaveBeenCalled();
    expect(listThreads).not.toHaveBeenCalled();
    await hook.rerender({ backgroundLoadingEnabled: true });
    expect(listAllThreads).toHaveBeenCalledOnce();
    expect(hook.get().workspaceThreadsByWorkspaceID.other.map(item => item.id)).toEqual([listed.id]);
  });

  it("prunes missing project IDs while preserving pseudo section collapse IDs", async () => {
    window.localStorage.setItem(
      "wuu.desktop.collapsedProjectIDs",
      JSON.stringify(["missing-project", SIDEBAR_SECTION_PINNED]),
    );

    const hook = await renderSidebarWorkspaceState({ projects: [] });

    expect([...hook.get().collapsedSidebarSectionIDs]).toEqual([
      SIDEBAR_SECTION_PINNED,
    ]);
  });

  it("toggles the pinned pseudo section with one click", async () => {
    const hook = await renderSidebarWorkspaceState();

    act(() => {
      hook.get().toggleSidebarSectionCollapsed(SIDEBAR_SECTION_PINNED);
    });
    expect(hook.get().collapsedSidebarSectionIDs.has(SIDEBAR_SECTION_PINNED)).toBe(true);

    act(() => {
      hook.get().toggleSidebarSectionCollapsed(SIDEBAR_SECTION_PINNED);
    });
    expect(hook.get().collapsedSidebarSectionIDs.has(SIDEBAR_SECTION_PINNED)).toBe(false);
  });

  it("mirrors active project threads into the sidebar cache", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const alphaThread = thread("thread-alpha", "/tmp/alpha");
    const scratchThread = thread("thread-scratch", "/tmp/other");
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: alpha.id,
      cwd: alpha.path,
    };

    const hook = await renderSidebarWorkspaceState({
      projects: [alpha],
      threads: [alphaThread, scratchThread],
      activeContext,
      activeWorkspaceID: alpha.id,
    });

    expect(
      hook.get().workspaceThreadsByWorkspaceID.alpha?.map((item) => item.id),
    ).toEqual(["thread-alpha"]);
  });

  it("stabilizes the active project cache when listed turns are unchanged", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const alphaThread = {
      ...thread("thread-alpha", alpha.path),
      turns: [{
        id: "turn-alpha",
        items: [],
        items_view: "full" as const,
        status: "completed" as const,
      }],
    };
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: alpha.id,
      cwd: alpha.path,
    };

    const hook = await renderSidebarWorkspaceState({ projects: [alpha] });
    act(() => {
      hook.get().cacheSidebarThreads([{
        ...alphaThread,
        turns: alphaThread.turns.map((turn) => ({ ...turn })),
      }]);
    });
    await hook.rerender({
      threads: [alphaThread],
      activeContext,
      activeWorkspaceID: alpha.id,
    });
    const cached = hook.get().workspaceThreadsByWorkspaceID.alpha;
    await hook.rerender({ threads: [alphaThread] });

    expect(hook.get().workspaceThreadsByWorkspaceID.alpha).toBe(cached);
  });

  it("marks expanded project sessions as loading until their snapshot arrives", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    window.localStorage.setItem(
      "wuu.desktop.expandedSidebarSectionIDs",
      JSON.stringify([alpha.id]),
    );
    let resolveThreads: ((value: { threads: Thread[] }) => void) | undefined;
    Object.defineProperty(window, "wuu", {
      configurable: true,
      value: {
        listThreads: vi.fn(
          () =>
            new Promise((resolve) => {
              resolveThreads = resolve;
            }),
        ),
      },
    });

    const hook = await renderSidebarWorkspaceState({ projects: [alpha] });
    expect(hook.get().loadingWorkspaceThreadIDs.has(alpha.id)).toBe(true);

    await act(async () => {
      resolveThreads?.({ threads: [thread("thread-alpha", alpha.path)] });
      await flushEffects();
    });

    expect(hook.get().loadingWorkspaceThreadIDs.has(alpha.id)).toBe(false);
    expect(hook.get().workspaceThreadsByWorkspaceID.alpha?.map((item) => item.id)).toEqual([
      "thread-alpha",
    ]);
  });

  it("keeps cached sessions while an active project snapshot is incomplete", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const first = thread("thread-first", "/tmp/alpha");
    const second = thread("thread-second", "/tmp/alpha");
    const activeContext: RuntimeContext = {
      kind: "project",
      project_id: alpha.id,
      cwd: alpha.path,
    };
    const hook = await renderSidebarWorkspaceState({
      projects: [alpha],
      threads: [first, second],
      activeContext,
      activeWorkspaceID: alpha.id,
    });

    await hook.rerender({ threads: [second] });

    expect(
      hook.get().workspaceThreadsByWorkspaceID.alpha?.map((item) => item.id).sort(),
    ).toEqual(["thread-first", "thread-second"]);
  });

  it("keeps folded workspace session status current from background server events", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const beta = project("beta", "/tmp/beta");
    const betaThread = thread("thread-beta", beta.path);
    const hook = await renderSidebarWorkspaceState({ projects: [alpha, beta] });

    act(() => {
      hook.get().cacheSidebarThreads([betaThread]);
      hook.get().syncSidebarServerEvent({
        kind: "notification",
        workdir: beta.path,
        message: {
          method: "turn/started",
          params: {
            thread_id: betaThread.id,
            turn: {
              id: "turn-beta",
              items: [],
              items_view: "full",
              status: "in_progress",
            },
          },
        },
      });
    });

    expect(hook.get().expandedSidebarSectionIDs.has(beta.id)).toBe(false);
    const runningThread = hook.get().workspaceThreadsByWorkspaceID.beta?.[0];
    expect(runningThread?.turns.at(-1)).toMatchObject({
      id: "turn-beta",
      status: "in_progress",
    });
    expect(isThreadRunning(runningThread)).toBe(true);

    act(() => {
      hook.get().syncSidebarServerEvent({
        kind: "notification",
        workdir: beta.path,
        message: {
          method: "turn/completed",
          params: {
            thread_id: betaThread.id,
            turn: {
              id: "turn-beta",
              items: [],
              items_view: "full",
              status: "completed",
            },
          },
        },
      });
    });

    const completedThread = hook.get().workspaceThreadsByWorkspaceID.beta?.[0];
    expect(completedThread?.turns.at(-1)).toMatchObject({
      id: "turn-beta",
      status: "completed",
    });
    expect(isThreadRunning(completedThread)).toBe(false);
    expect(isThreadUnread(completedThread, undefined)).toBe(true);
  });

  it("caches search results across projects without requiring sidebar expansion", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const beta = project("beta", "/tmp/beta");
    const scratch = thread("thread-scratch", "/tmp/scratch");
    const hook = await renderSidebarWorkspaceState({ projects: [alpha, beta] });

    act(() => {
      hook.get().cacheSidebarThreads([
        thread("thread-alpha", alpha.path),
        thread("thread-beta", beta.path),
        scratch,
      ]);
    });

    expect(hook.get().workspaceThreadsByWorkspaceID.alpha?.map((item) => item.id)).toEqual([
      "thread-alpha",
    ]);
    expect(hook.get().workspaceThreadsByWorkspaceID.beta?.map((item) => item.id)).toEqual([
      "thread-beta",
    ]);
    expect(hook.get().cachedScratchThreads.map((item) => item.id)).toEqual([
      "thread-scratch",
    ]);
  });

  it("patches a cached project session pin immediately", async () => {
    const alpha = project("alpha", "/tmp/alpha");
    const cached = thread("thread-alpha", alpha.path);
    const hook = await renderSidebarWorkspaceState({ projects: [alpha] });
    act(() => {
      hook.get().cacheSidebarThreads([cached]);
    });

    act(() => {
      hook.get().updateCachedSidebarThreadPinned(cached.id, true);
    });

    expect(hook.get().workspaceThreadsByWorkspaceID.alpha?.[0]?.pinned).toBe(true);
  });

  it("keeps other workspaces' scratch sessions when switching no-project workspaces", async () => {
    const hook = await renderSidebarWorkspaceState({
      projects: [],
      threads: [thread("thread-a", "/tmp/a")],
      activeContext: { kind: "no_project", cwd: "/tmp/a" },
    });
    expect(hook.get().cachedScratchThreads.map((item) => item.id)).toEqual([
      "thread-a",
    ]);

    await hook.rerender({
      threads: [thread("thread-b", "/tmp/b")],
      activeContext: { kind: "no_project", cwd: "/tmp/b" },
    });

    expect(
      hook.get().cachedScratchThreads.map((item) => item.id).sort(),
    ).toEqual(["thread-a", "thread-b"]);
  });
});
