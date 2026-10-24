import { scopeApplicationOnboarding } from "../../../../packages/plugin-sdk/src/ui";
import { resetWorkspaceBrowserNavigationForTests } from "../WorkspaceBrowserNavigation";
import { describe, expect, it, vi } from "vitest";
import * as RuntimeLoader from "../RuntimeLoadState";
import { ApplicationController } from "./ApplicationController";
import { streamTextKey, streamTextStore } from "../StreamText";
import { initialState, type AppState } from "../AppState";
import type { CodexPetCommand, WuuDesktopApi } from "../../shared/protocol";

const submissionInitialized = { provider: "fixture", model: "fixture", providers: [{ name: "fixture", type: "openai", model: "fixture", api_key_configured: true }] } as AppState["initialized"];
async function settleSubmission(controller: ApplicationController, result: Awaited<ReturnType<ApplicationController["invoke"]>>) {
  const operationID = (result as { operationID?: string }).operationID;
  expect(operationID).toBeTruthy();
  await vi.waitFor(() => expect(controller.getSnapshot().submission.operations.find(operation => operation.operationID === operationID)?.phase).toMatch(/completed|failed|cancelled/));
  return controller.getSnapshot().submission.operations.find(operation => operation.operationID === operationID)!;
}

// Public failure cases: two shells observe one state, navigation completions
// cannot cross generation disposal, and failed submissions retain the draft.
describe("shared application state", () => {
  it("keeps pet submission with the host and its captured workspace across UI retirement", async () => {
    const context = { kind: "no_project" as const, cwd: "/captured" };
    let command!: (value: CodexPetCommand) => void;
    let engines!: (value: unknown) => void;
    const off = vi.fn();
    const thread = { id: "pet-thread", cwd: context.cwd, preview: "", model_provider: "", model: "", created_at: "", updated_at: "", status: "idle" as const, turns: [] };
    const send = vi.fn().mockResolvedValue({ turn: { id: "pet-turn", status: "in_progress", items_view: "full", items: [] } });
    const resolve = vi.fn();
    const feed = vi.fn().mockResolvedValue(undefined);
    window.wuu = { hostKind: "desktop", onCodexPetCommand: (listener: (value: CodexPetCommand) => void) => { command = listener; return off; }, resolveCodexPetCommand: resolve, updateCodexPetFeed: feed,
      listEngines: vi.fn(() => new Promise(done => { engines = done; })), startThread: vi.fn().mockResolvedValue({ thread }), startTurn: send,
      listAllThreads: vi.fn().mockResolvedValue({ threads: [] }), listProjects: vi.fn().mockResolvedValue({ projects: [] }), listArchivedThreads: vi.fn().mockResolvedValue({ threads: [] }),
    } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context });
    const stop = controller.start();
    const ui = new AbortController(); controller.createDesktopScope(ui.signal);
    try {
      expect(command).toBeTypeOf("function");
      command({ kind: "submit", id: "pet-command", text: "hello", target: { workspace: context } });
      await vi.waitFor(() => expect(engines).toBeTypeOf("function"));
      ui.abort();
      controller.updateState(state => ({ ...state, activeContext: { kind: "no_project", cwd: "/next" } }));
      engines({ engines: [], settings: { default_engine: "native" } });
      await vi.waitFor(() => expect(resolve).toHaveBeenCalledExactlyOnceWith("pet-command", { ok: true }));
      expect(window.wuu.startThread).toHaveBeenCalledWith(expect.objectContaining({ engine: "native", model: "", effort: "", permission_mode: "unconfined" }), context);
      expect(send.mock.calls[0][7]).toEqual(context);
      expect(controller.getState().activeContext?.cwd).toBe("/next");
      expect(controller.getState().thread).toBeUndefined();
      expect(off).not.toHaveBeenCalled();
      expect(feed).toHaveBeenCalledWith(expect.objectContaining({ workspace: expect.objectContaining({ context }) }));
    } finally { stop(); }
    expect(off).toHaveBeenCalledOnce();
  });

  it("keeps history retries and recovery sends bound to the requested conversation", async () => {
    const context = { kind: "no_project" as const, cwd: "/foreground" };
    const thread = { id: "background", cwd: "/background", preview: "", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", status: "idle" as const, turns: [], history_cursor: "page:one" };
    const history = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
    const send = vi.fn().mockResolvedValue({ turn: { id: "sent", items: [], items_view: "full", status: "in_progress" } });
    window.wuu = { loadEarlierThreadHistory: history, startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context,
      threads: [thread], activeSessionTabID: "draft:foreground", sessionTabs: [{ id: "draft:foreground", kind: "draft", context, title: "", createdAt: 1, prompt: "untouched", images: [], files: [] }] });
    await expect(controller.invoke("conversation.load-earlier", { threadId: thread.id, cursor: "stale" })).rejects.toThrow();
    expect(history).not.toHaveBeenCalled();
    await expect(controller.invoke("conversation.load-earlier", { threadId: thread.id, cursor: "page:one" })).rejects.toThrow("offline");
    await controller.invoke("conversation.load-earlier", { threadId: thread.id, cursor: "page:one" });
    expect(history).toHaveBeenCalledTimes(2);
    await expect(controller.invoke("conversation.continue", { threadId: "missing", text: "continue" })).resolves.toEqual({ accepted: false });
    expect(send).not.toHaveBeenCalled();
    await expect(controller.invoke("conversation.continue", { threadId: thread.id, text: "continue" })).resolves.toEqual({ accepted: true });
    expect(send.mock.calls[0][0]).toBe(thread.id);
    expect(send.mock.calls[0][7]).toEqual({ kind: "no_project", cwd: "/background" });
    expect(controller.getState().activeContext).toEqual(context);
    expect(controller.getState().activeSessionTabID).toBe("draft:foreground");
    expect(controller.getState().sessionTabs[0]).toMatchObject({ prompt: "untouched" });
  });

  it("hydrates only the captured remote item and rejects stale, mismatched, or cancelled reads", async () => {
    const remote = { id: "item", type: "agent_message" as const, remote_content_ref: "ref:one", text: "preview" };
    const thread = { id: "remote", cwd: "/fixture", preview: "", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", status: "idle" as const,
      turns: [{ id: "turn", status: "completed" as const, items_view: "full" as const, items: [remote] }] };
    let finish!: (item: typeof remote) => void;
    const read = vi.fn(() => new Promise<typeof remote>(resolve => { finish = resolve; }));
    window.wuu = { readRemoteItem: read } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, thread, threads: [thread] });
    const target = { threadId: "remote", turnId: "turn", itemId: "item" };
    await expect(controller.invoke("conversation.load-item", { ...target, threadId: "missing" })).rejects.toThrow();
    expect(read).not.toHaveBeenCalled();
    const stale = controller.invoke("conversation.load-item", target);
    controller.updateState(state => ({ ...state, thread: { ...thread, turns: [{ ...thread.turns[0], items: [{ ...remote, remote_content_ref: "ref:two" }] }] } }));
    finish({ ...remote, text: "old content" });
    await expect(stale).rejects.toThrow();
    expect(controller.getState().thread?.turns[0].items[0].text).toBe("preview");
    const cancelled = new AbortController();
    const pending = controller.invoke("conversation.load-item", target, { signal: cancelled.signal });
    cancelled.abort(); finish({ ...remote, remote_content_ref: "ref:two", text: "cancelled content" });
    await expect(pending).rejects.toThrow();
    expect(controller.getState().thread?.turns[0].items[0].text).toBe("preview");
    const mismatch = controller.invoke("conversation.load-item", target);
    finish({ ...remote, id: "other" }); await expect(mismatch).rejects.toThrow();
    const loading = controller.invoke("conversation.load-item", target);
    finish({ ...remote, remote_content_ref: "ref:two", text: "full content" });
    const loaded = await loading;
    expect(loaded.text).toBe("full content");
    expect(Object.isFrozen(loaded)).toBe(true);
    expect(controller.getState().thread?.turns[0].items[0].text).toBe("full content");
    expect(controller.getState().threads[0].turns[0].items[0].text).toBe("full content");
    expect(read).toHaveBeenLastCalledWith("ref:two");
  });

  it("settles admitted onboarding imports after their initiating UI retires", async () => {
    let finish!: (value: unknown) => void;
    window.wuu = { importBrowserCookies: vi.fn(() => new Promise(resolve => { finish = resolve; })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, onboardingComplete: false, onboarding: {
      step: "browser", selectedPluginIDs: new Set(), initializedPluginSelection: true, applyingPlugins: false, savingProvider: false,
      connected: true, savingRuntime: false, finishing: false, importingBrowser: "", browserImportError: "", browserImportResults: {}, browserImported: false,
      scanningWorkspaces: false, choosingWorkspace: false, selectedPaths: new Set(), scanAttempt: 0, scannedRevision: 0, error: null, selectedEngine: "wuu",
    } });
    const generation = new AbortController();
    const scoped = scopeApplicationOnboarding(() => controller.onboarding, generation.signal, () => generation.signal.throwIfAborted());
    const importing = scoped.run({ kind: "browser", profilePath: "/fixture/profile" });
    const rejected = expect(importing).rejects.toThrow();
    expect(controller.onboarding.getSnapshot().workflow?.importingBrowser).toBe("/fixture/profile");
    generation.abort(); await rejected;
    finish({ imported: 1, skipped: 0, failed: 0 });
    await vi.waitFor(() => expect(controller.onboarding.getSnapshot().workflow?.importingBrowser).toBe(""));
    expect(controller.onboarding.getSnapshot().workflow?.browserImported).toBe(true);
    expect(window.wuu.importBrowserCookies).toHaveBeenCalledExactlyOnceWith("/fixture/profile");
  });

  it("uses the active worktree for environment operations and publishes shared Git status", async () => {
    const context = { kind: "project" as const, project_id: "project", cwd: "/project" };
    const thread = { id: "worktree", cwd: "/worktree", preview: "", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", status: "idle" as const, turns: [] };
    const status = { is_repo: true, branch: "next", dirty_count: 0 };
    window.wuu = { checkoutGitBranch: vi.fn().mockResolvedValue(status) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread });
    await controller.environment.checkoutBranch("next");
    expect(window.wuu.checkoutGitBranch).toHaveBeenCalledExactlyOnceWith("next", "/worktree");
    expect(controller.getState().gitStatus).toBe(status);
    expect(controller.environment.getSnapshot().root).toBe("/worktree");
    expect(controller.environment.getSnapshot().status?.branch).toBe("next");
  });

  it("marks authoritative draft resets without treating ordinary input as a reset", async () => {
    window.wuu = {} as WuuDesktopApi;
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:revision",
      sessionTabs: [{ id: "draft:revision", kind: "draft", context, title: "", createdAt: 1, prompt: "", images: [], files: [] }] });
    const initial = controller.getSnapshot().drafts.primary.revision;
    await controller.invoke("composer.set-draft", { ownerTabId: "draft:revision", text: "typing" });
    controller.updateState(state => ({ ...state, status: "unrelated" }));
    expect(controller.getSnapshot().drafts.primary.revision).toBe(initial);
    controller.setDraft("");
    const cleared = controller.getSnapshot().drafts.primary.revision;
    expect(cleared).toBeGreaterThan(initial);
    controller.setDraft("");
    expect(controller.getSnapshot().drafts.primary.revision).toBeGreaterThan(cleared);
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
  });

  it("projects the active pane worktree and preserves focused preview tabs when opening URLs", async () => {
    const context = { kind: "project" as const, project_id: "project", cwd: "/project" };
    const thread = { id: "secondary", cwd: "/worktree", title: "Secondary", preview: "", created_at: "", updated_at: "version", model_provider: "fixture", model: "fixture", status: "idle" as const, turns: [] };
    window.wuu = { openExternal: vi.fn().mockResolvedValue(undefined) } as unknown as WuuDesktopApi;
    resetWorkspaceBrowserNavigationForTests();
    const controller = new ApplicationController({ ...initialState, activeContext: context, activePane: "secondary", secondaryThread: thread,
      activeSessionTabID: "thread:secondary", sessionTabs: [{ id: "thread:secondary", kind: "thread", threadID: "secondary", context, title: "Secondary", prompt: "", images: [], files: [] }] });
    await controller.invoke("preview.open-tool", { toolId: "terminal" });
    await controller.invoke("preview.set-visibility", { open: true });
    await controller.invoke("preview.open-url", { url: "https://example.test/page" });
    const view = controller.getSnapshot().previewWorkspace;
    expect(view).toMatchObject({ ownerTabId: "thread:secondary", threadId: "secondary", workspaceContext: { cwd: "/worktree" }, activeContext: { cwd: "/project" }, conversationTitle: "Secondary", fileRefreshKey: "version", activeTabId: "terminal", browserNavigation: { url: "https://example.test/page" } });
    expect(view.tabs.some(tab => tab.kind === "browser")).toBe(true);
    expect(Object.isFrozen(view.workspaceContext)).toBe(true);
    await controller.invoke("preview.open-url", { url: "https://example.test/external", modifiers: { metaKey: true } });
    expect(window.wuu.openExternal).toHaveBeenCalledExactlyOnceWith("https://example.test/external");
    resetWorkspaceBrowserNavigationForTests();
  });

  it("completes onboarding through canonical workspace navigation without resuming an old thread", async () => {
    const context = { kind: "project" as const, project_id: "imported", cwd: "/imported" };
    const workspace = { projects: [{ id: "imported", path: "/imported", name: "Imported", created_at: "", updated_at: "" }], active_context: context };
    window.wuu = { importWorkspaces: vi.fn().mockResolvedValue(workspace), selectProject: vi.fn().mockResolvedValue(workspace),
      completeOnboarding: vi.fn().mockResolvedValue(undefined) } as unknown as WuuDesktopApi;
    const load = vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: context, projects: workspace.projects });
    try {
      const controller = new ApplicationController({ ...initialState, onboardingComplete: false });
      await controller.onboarding.complete({ paths: ["/imported"] });
      expect(load).toHaveBeenCalledExactlyOnceWith(workspace, { resumeLatestThread: false });
      expect(controller.getSnapshot().workspaces.activeRoot).toBe("/imported");
      expect(controller.getState().sessionTabs.find(tab => tab.id === controller.getSnapshot().tabs.activeTabId)?.kind).toBe("draft");
      expect(controller.onboarding.getSnapshot().completed).toBe(true);
      expect(window.wuu.completeOnboarding).toHaveBeenCalledOnce();
    } finally { load.mockRestore(); }
  });

  it("merges browser takeover into the canonical immutable activity view", async () => {
    const activity = { id: "activity", kind: "browser", workdir: "/fixture", thread_id: "thread", execution_id: "run", state: "user_controlled", controller: "user", created_at: "2026-10-24T00:00:00Z", updated_at: "2026-10-24T00:00:01Z" };
    const off = () => () => {};
    window.wuu = { browserSurface: vi.fn(), browserCommand: vi.fn(), onBrowserSurface: off, onBrowserTabAdopted: off, onBrowserUserInput: off,
      onBrowserDock: off, onBrowserInvalidate: off, reportBrowserBounds: vi.fn(), suppressBrowserOverlay: vi.fn(), takeoverActivity: vi.fn().mockResolvedValue({ activity }) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController(initialState);
    const lease = controller.browser.acquireSurface({ workdir: "/fixture", tabID: "tab", threadID: "thread" });
    await lease.takeOver({ id: "activity", thread_id: "thread", execution_id: "run" });
    const value = controller.getSnapshot().activities[0];
    expect(value).toMatchObject({ state: "user_controlled", execution_id: "run" });
    expect(Object.isFrozen(value)).toBe(true);
    activity.state = "stopped";
    expect(value.state).toBe("user_controlled");
    lease.dispose();
  });

  it("writes file comments into the canonical composer without consuming its attachments", () => {
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    window.wuu = {} as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context,
      activeSessionTabID: "draft:file", sessionTabs: [{ id: "draft:file", kind: "draft", context, title: "", createdAt: 1, prompt: "Unsent", images: [],
        files: [{ id: "attachment", filename: "keep.txt", media_type: "text/plain", data: "QQ==" }] }] });
    const target = { ownerTabId: "draft:file" };
    const source = { workspace: "/fixture", path: "source.ts", start_line: 1, start_column: 1, end_line: 1, end_column: 4, quote: "text", revision: "revision" };
    expect(controller.fileSelection.comment({ target, source, comment: "Explain" })).toBe(true);
    const comment = controller.fileSelection.getSnapshot(target).comments[0];
    expect(controller.getSnapshot().drafts.primary.text).toContain("Unsent");
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual(["attachment"]);
    expect(controller.fileSelection.removeComment({ target, id: comment.id })).toBe(true);
    expect(controller.getSnapshot().drafts.primary.text).toBe("Unsent");
  });

  it("reconciles released project membership into the shared thread and sidebar", async () => {
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    const thread = { id: "member", cwd: context.cwd, preview: "Member", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "",
      status: "idle", turns: [], source: "project", project_id: "project" } as never;
    window.wuu = { projectWork: vi.fn(), projectSession: vi.fn().mockResolvedValue({ thread: { ...thread as object, source: undefined, project_id: undefined } }) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: { ...submissionInitialized!, features: { project_agent: true } } as never,
      activeContext: context, thread, threads: [thread] });
    await controller.projects.releaseSession({ projectId: "project", threadId: "member" });
    expect(controller.getSnapshot().navigationWorkspace.threads.find(row => row.id === "member")?.project_id).toBeUndefined();
    expect(controller.getSnapshot().conversationWorkspace.threads.find(row => row.id === "member")?.project_id).toBeUndefined();
  });

  it("resolves search through the shared navigation and rejects a superseded target", async () => {
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    const thread = { id: "search", cwd: context.cwd, preview: "Found", title: "Found", model_provider: "fixture", model: "fixture", created_at: "", updated_at: "", status: "idle", turns: [
      { id: "turn", status: "completed", items_view: "full", items: [{ id: "answer", seq: 7, type: "agent_message", text: "needle" }] },
    ] };
    window.wuu = { searchThreads: vi.fn().mockResolvedValue({ results: [{ thread, message_seq: 7, snippet: "needle" }] }), resumeThread: vi.fn().mockResolvedValue({ thread }) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context });
    await controller.invoke("search.run", { query: "needle" });
    const row = controller.getSnapshot().search.results[0];
    const result = await controller.invoke("search.locate", { resultId: row.resultId });
    expect(controller.getSnapshot().tabs.activeTabId).toBe("thread:search");
    expect(controller.getSnapshot().search.target).toMatchObject({ id: result.targetId, phase: "ready", itemId: "answer" });
    await controller.invoke("search.query", { query: "changed", composing: true });
    await expect(controller.invoke("search.locate", { resultId: row.resultId })).rejects.toThrow("no longer current");
  });

  it("retains imported history attachments until the shared edit is cancelled", async () => {
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    const thread = { id: "history", cwd: context.cwd, preview: "", model_provider: "fixture", status: "idle", turns: [
      { id: "turn", status: "completed", items_view: "full", items: [{ id: "user", type: "user_message", text: "Original", images: [{ media_type: "image/png", data: "QQ==" }] }] },
    ] } as never;
    window.wuu = {} as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, thread,
      activeSessionTabID: "thread:history", sessionTabs: [{ id: "thread:history", kind: "thread", threadID: "history", context, title: "", prompt: "Unsent", images: [], files: [] }] });
    const target = { threadId: "history", turnId: "turn", itemId: "user" };
    await controller.invoke("history.begin-edit", target);
    const id = controller.getSnapshot().conversationActions.editing!.draft.imageIds[0];
    expect(controller.getSnapshot().drafts.attachments.find(item => item.id === id)?.data).toBe("QQ==");
    controller.updateState(state => ({ ...state, status: "updated" }));
    expect((await controller.invoke("composer.resolve-attachment", { attachmentId: id })).data).toBe("QQ==");
    await expect(controller.invoke("composer.release-attachments", { attachmentIds: [id] })).rejects.toThrow("owned by a draft");
    await controller.invoke("history.cancel-edit", {});
    expect(controller.getSnapshot().drafts.primary.text).toBe("Unsent");
    await expect(controller.invoke("composer.resolve-attachment", { attachmentId: id })).rejects.toThrow("Unknown composer attachment");
  });

  it("keeps cancellation connected after submission admission returns", async () => {
    const context = { kind: "no_project" as const, cwd: "/fixture" };
    let finish!: (value: unknown) => void;
    window.wuu = { startThread: vi.fn(() => new Promise(resolve => { finish = resolve; })),
      deleteThread: vi.fn().mockResolvedValue({}), startTurn: vi.fn() } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context,
      initialized: { provider: "fixture", model: "fixture", providers: [{ name: "fixture", type: "openai", model: "fixture", api_key_configured: true }] } as never,
      activeSessionTabID: "draft:a", sessionTabs: [{ id: "draft:a", kind: "draft", context, title: "", createdAt: 1, prompt: "hello", images: [], files: [] }] });
    const lifetime = new AbortController();
    const result = await controller.invoke("composer.submit", { ownerTabId: "draft:a" }, { signal: lifetime.signal });
    expect(result.accepted).toBe(true);
    lifetime.abort();
    finish({ thread: { id: "late", cwd: context.cwd, turns: [], status: "idle" } });
    await vi.waitFor(() => expect(window.wuu.deleteThread).toHaveBeenCalledWith("late"));
    expect(window.wuu.startTurn).not.toHaveBeenCalled();
    expect(controller.getSnapshot().drafts.primary.text).toBe("hello");
  });

  it("clears pending navigation when its generation aborts before a workspace response", async () => {
    const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
    let finish!: (value: unknown) => void;
    window.wuu = { selectProject: vi.fn(id => id === "b" ? new Promise(resolve => { finish = resolve; }) : Promise.resolve({ projects: [], active_context: source })) } as unknown as WuuDesktopApi;
    const load = vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: { kind: "project", project_id: "b", cwd: "/b" } });
    try {
      const controller = new ApplicationController({ ...initialState, activeContext: source });
      const lifetime = new AbortController();
      const navigation = controller.invoke("workspace.start-draft", { workspaceId: "b" }, { signal: lifetime.signal });
      lifetime.abort();
      expect(controller.getSnapshot().tabs.pending).toBeUndefined();
      finish({ projects: [] });
      await expect(navigation).rejects.toThrow();
      expect(controller.getSnapshot().workspaces.activeRoot).toBe("/a");
      expect(window.wuu.selectProject).toHaveBeenLastCalledWith("a");
    } finally { load.mockRestore(); }
  });

  it("cancels only the requested navigation and restores source transport after a late response", async () => {
    const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
    let finish!: (value: unknown) => void;
    window.wuu = { selectProject: vi.fn(id => id === "b" ? new Promise(resolve => { finish = resolve; }) : Promise.resolve({ projects: [], active_context: source })) } as unknown as WuuDesktopApi;
    const load = vi.spyOn(RuntimeLoader, "loadRuntime").mockResolvedValue({ activeContext: { kind: "project", project_id: "b", cwd: "/b" } });
    try {
      const controller = new ApplicationController({ ...initialState, activeContext: source });
      const navigation = controller.invoke("workspace.start-draft", { workspaceId: "b" });
      const requestId = controller.getSnapshot().tabs.pending!.requestId!;
      await controller.invoke("navigation.cancel", { requestId: requestId - 1 });
      expect(controller.getSnapshot().tabs.pending?.requestId).toBe(requestId);
      await controller.invoke("navigation.cancel", { requestId });
      expect(controller.getSnapshot().tabs.pending).toBeUndefined();
      expect(controller.getSnapshot().tabs.navigationRevision).toBeGreaterThan(requestId);
      finish({ projects: [] });
      await expect(navigation).rejects.toThrow();
      expect(window.wuu.selectProject).toHaveBeenLastCalledWith("a");
    } finally { load.mockRestore(); }
  });

  it("opens a related split without losing drafts and rejects a stale split completion", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    let finish!: (value: unknown) => void;
    window.wuu = { resumeThread: vi.fn(() => new Promise(resolve => { finish = resolve; })) } as unknown as WuuDesktopApi;
    const primary = { id: "primary", cwd: context.cwd, status: "idle", turns: [] } as never;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread: primary, activeSessionTabID: "thread:primary", sessionTabs: [
      { id: "thread:primary", kind: "thread", threadID: "primary", context, title: "P", prompt: "keep", images: [], files: [] },
    ] });
    const opening = controller.invoke("conversation.open-split", { threadId: "child" });
    await controller.invoke("composer.set-draft", { text: "latest primary" });
    finish({ thread: { id: "child", cwd: context.cwd, status: "idle", turns: [] } });
    await opening;
    expect(controller.getSnapshot().tabs.secondaryThreadId).toBe("child");
    expect(controller.getSnapshot().drafts.primary.text).toBe("latest primary");
    const stale = controller.invoke("conversation.open-split", { threadId: "other" });
    await controller.invoke("navigation.cancel", {});
    finish({ thread: { id: "other", cwd: context.cwd, status: "idle", turns: [] } });
    await stale;
    expect(controller.getSnapshot().tabs.secondaryThreadId).toBe("child");
  });

  it("projects complete immutable conversation content and observes host-owned live fields", async () => {
    const item = { id: "tool", type: "tool_call", status: "in_progress", arguments: "{", result: "seed",
      result_detail: { content: [{ type: "image", uri: "wuu-artifact://owned", artifact: { sha256: "digest" } }] } };
    const thread = { id: "stream-thread", model_provider: "test", turns: [{ id: "stream-turn", status: "in_progress", items: [item],
      error: { message: "failure", recovery: { attempt_count: 2 } } }], child_agents: [{ id: "child", status: "running" }], history_cursor: "older" } as never;
    const controller = new ApplicationController({ ...initialState, thread, threads: [thread], turnStreamStatus: { "stream-turn": { text: "Reconnecting", liveProgress: true } } });
    expect(controller.getSnapshot().conversationWorkspace.turnStreamStatus["stream-turn"]).toEqual({ text: "Reconnecting", liveProgress: true });
    expect(Object.isFrozen(controller.getSnapshot().conversationWorkspace.turnStreamStatus["stream-turn"])).toBe(true);
    const view = controller.getSnapshot().conversationWorkspace.threads[0];
    expect(view.history_cursor).toBe("older");
    expect(view.child_agents?.[0].id).toBe("child");
    expect(view.turns[0].items[0].result_detail?.content?.[0].artifact?.sha256).toBe("digest");
    expect(Object.isFrozen(view.turns[0].items[0].result_detail?.content?.[0])).toBe(true);
    const target = { threadId: "stream-thread", turnId: "stream-turn", itemId: "tool", field: "result" as const };
    const key = streamTextKey(target.turnId, target.itemId, target.field);
    const listener = vi.fn();
    const stop = controller.conversationText.subscribe(target, listener);
    vi.useFakeTimers();
    try {
      streamTextStore.set(key, "streamed result");
      await vi.runAllTimersAsync();
      expect(listener).toHaveBeenCalled();
      expect(controller.conversationText.getSnapshot(target)).toBe("streamed result");
      expect(controller.conversationText.getSnapshot({ ...target, threadId: "unrelated" })).toBeUndefined();
      controller.setSettings(true);
      const updated = controller.getSnapshot().conversationWorkspace.threads[0];
      expect(updated.turns[0].items[0].result).toBe("streamed result");
      expect(view.turns[0].items[0].result).toBe("seed");
      listener.mockClear();
      streamTextStore.clearItem(target.turnId, target.itemId);
      controller.updateState(state => ({ ...state, thread: { ...state.thread!, turns: state.thread!.turns.map(turn => ({ ...turn,
        items: turn.items.map(item => ({ ...item, result: "settled result" })) })) } }));
      expect(controller.conversationText.getSnapshot(target)).toBe("settled result");
      expect(listener).toHaveBeenCalledTimes(1);
      listener.mockClear();
      streamTextStore.set(key, "resumed result");
      await vi.runAllTimersAsync();
      expect(listener).toHaveBeenCalledTimes(1);
      stop(); stop();
      listener.mockClear();
      streamTextStore.set(key, "later");
      await vi.runAllTimersAsync();
      expect(listener).not.toHaveBeenCalled();
    } finally { stop(); streamTextStore.clearItem(target.turnId, target.itemId); vi.useRealTimers(); }
  });

  it("starts catalog content atomically and never overwrites a superseding draft", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, nativeSection: "catalog", activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "original", images: [], files: [] },
    ] });
    controller.setSettings(true);
    const starting = controller.invoke("draft.start-with-content", { text: "skill prompt", sourceTabId: "draft:a", navigationRevision: 0 });
    await controller.invoke("navigation.cancel", {});
    expect(await starting).toEqual({ activated: false });
    expect(controller.getSnapshot().drafts.primary.text).toBe("original");
    const navigationRevision = controller.getSnapshot().tabs.navigationRevision;
    const accepted = await controller.invoke("draft.start-with-content", { text: "skill prompt", sourceTabId: "draft:a", navigationRevision });
    expect(accepted).toEqual({ activated: true, tabId: "draft:a" });
    expect(controller.getSnapshot().drafts.primary).toMatchObject({ text: "skill prompt", imageIds: [], fileIds: [], selections: [] });
    expect(controller.getSnapshot().settings.open).toBe(false);
    expect(controller.getSnapshot().navigation.section).toBe("conversations");
    expect(controller.getSnapshot().navigationWorkspace.composerFocusRequest?.tabId).toBe("draft:a");
    expect(await controller.invoke("draft.start-with-content", { text: "stale", sourceTabId: "draft:a", navigationRevision: 0 })).toEqual({ activated: false });
    expect(controller.getSnapshot().drafts.primary.text).toBe("skill prompt");
  });

  it("routes runtime choices through the same per-draft owner across navigation", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a",
      initialized: { protocol_version: "test", workspace_root: context.cwd, provider: "api", model: "a", permissions: { mode: "standard" },
        providers: [{ name: "api", type: "openai", model: "a", models: [{ id: "a" }, { id: "b" }] }] },
      sessionTabs: ["a", "b"].map(id => ({ id: `draft:${id}`, kind: "draft" as const, context, title: id, createdAt: 1, prompt: "", images: [], files: [] })),
    });
    await controller.invoke("composer.choose-runtime", { ownerTabId: "draft:a", selection: { kind: "provider-model", provider: "api", model: "b" } });
    expect(controller.getSnapshot().composerRuntime.owners.find(owner => owner.ownerTabId === "draft:a")?.selection.model).toBe("b");
    await controller.invoke("tab.select", { tabId: "draft:b" });
    expect(controller.getSnapshot().composerRuntime.owners.find(owner => owner.ownerTabId === "draft:b")?.selection.model).toBe("a");
    await controller.invoke("tab.select", { tabId: "draft:a" });
    expect(controller.getSnapshot().composerRuntime.owners.find(owner => owner.ownerTabId === "draft:a")?.selection.model).toBe("b");
    expect(controller.getState().initialized?.model).toBe("a");
  });

  it("admits files into their source draft before returning and releases closed draft resources", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
      { id: "draft:b", kind: "draft", context, title: "B", createdAt: 2, prompt: "", images: [], files: [] },
    ] });
    const file = new File(["a"], "note.txt", { type: "text/plain" });
    Object.defineProperty(file, "arrayBuffer", { value: async () => new Uint8Array([65]).buffer });
    const adding = controller.invoke("composer.attach", { files: [file], ownerTabId: "draft:a" });
    await controller.invoke("tab.select", { tabId: "draft:b" });
    const { attachments } = await adding;
    await controller.invoke("tab.select", { tabId: "draft:a" });
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual([attachments[0].id]);
    await controller.invoke("tab.close", { tabId: "draft:a" });
    await expect(controller.invoke("composer.resolve-attachment", { attachmentId: attachments[0].id })).rejects.toThrow("Unknown composer attachment");
  });

  it("routes navigation through the shared cache and removes deleted sidebar rows", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "cached", status: "idle", model_provider: "", model: "", created_at: "", updated_at: "", turns: [] } as never;
    window.wuu = { listAllThreads: vi.fn().mockResolvedValue({ threads: [thread] }), getSessionOrganization: vi.fn().mockResolvedValue({ organization: { folders: [] } }),
      deleteThread: vi.fn().mockResolvedValue({}) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context });
    await controller.invoke("navigation.refresh", {});
    expect(controller.getSnapshot().navigationWorkspace.threads.map(thread => thread.id)).toEqual(["a"]);
    await controller.invoke("settings.delete-archived", { threadId: "a" });
    expect(controller.getSnapshot().navigationWorkspace.threads).toEqual([]);
  });

  it("returns force-retry metadata from navigation archive without dropping the active thread", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "running", status: "in_progress", model_provider: "", turns: [{ id: "turn", status: "in_progress", items: [] }] } as never;
    window.wuu = { archiveThread: vi.fn() } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread, threads: [thread], activeSessionTabID: "thread:a", sessionTabs: [
      { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "", images: [], files: [] },
    ] });
    await expect(controller.invoke("navigation.archive-thread", { threadId: "a" })).resolves.toMatchObject({ ok: false, forceRetryable: true });
    expect(window.wuu.archiveThread).not.toHaveBeenCalled();
    expect(controller.getSnapshot().tabs.activeTabId).toBe("thread:a");
  });

  it("submits independent split draft owners while blocking duplicate admission to one owner", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = (id: string) => ({ id, cwd: context.cwd, preview: "", status: "idle", model_provider: "", turns: [] }) as never;
    const finish = new Map<string, (value: unknown) => void>();
    const send = vi.fn((id: string) => new Promise(resolve => { finish.set(id, resolve); }));
    window.wuu = { startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, thread: thread("a"), secondaryThread: thread("b"), activePane: "primary", activeSessionTabID: "thread:a", sessionTabs: ["a", "b"].map(id => ({
      id: `thread:${id}`, kind: "thread", threadID: id, context, title: id, prompt: `draft ${id}`, images: [], files: [],
    })) });
    const primary = controller.invoke("composer.submit", { ownerTabId: "thread:a" });
    await expect(controller.invoke("composer.submit", { ownerTabId: "thread:a" })).resolves.toMatchObject({ accepted: false });
    const secondary = controller.invoke("composer.submit", { ownerTabId: "thread:b" });
    await vi.waitFor(() => expect(send.mock.calls.map(args => args[0])).toEqual(["a", "b"]));
    finish.get("b")!({ turn: { id: "turn-b", status: "completed", items: [] } });
    await settleSubmission(controller, await secondary);
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
    expect(controller.getSnapshot().drafts.secondary.text).toBe("");
    finish.get("a")!({ turn: { id: "turn-a", status: "completed", items: [] } });
    await settleSubmission(controller, await primary);
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
  });

  it("keeps the image preview alive while editing a queued message back into its draft", async () => {
    const originalRevoke = URL.revokeObjectURL;
    const revoke = vi.fn();
    URL.revokeObjectURL = revoke;
    try {
      const context = { kind: "no_project" as const, cwd: "/workspace" };
      const thread = { id: "a", cwd: context.cwd, preview: "", status: "in_progress", model_provider: "", turns: [{ id: "running", status: "in_progress", items: [] }] } as never;
      window.wuu = { queueTurn: vi.fn().mockResolvedValue({ queued: { id: "queued" } }), dequeueTurn: vi.fn().mockResolvedValue({ ok: true }) } as unknown as WuuDesktopApi;
      const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, thread, activeSessionTabID: "thread:a", sessionTabs: [
        { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "image", images: [{ id: "image", media_type: "image/png", data: "QQ==", previewSrc: "blob:owned" }], files: [] },
      ] });
      await settleSubmission(controller, await controller.invoke("composer.submit", { ownerTabId: "thread:a", mode: "queue" }));
      await expect(controller.invoke("composer.release-attachments", { attachmentIds: ["image"] })).rejects.toThrow("pending message");
      await controller.invoke("composer.release-attachments", { attachmentIds: ["image"], ifUnreferenced: true });
      expect(controller.getSnapshot().drafts.attachments[0].id).toBe("image");
      await controller.invoke("composer.edit-pending", { ownerTabId: "thread:a", messageId: controller.getSnapshot().composerPending[0].queued[0].id, kind: "queue" });
      expect(revoke).not.toHaveBeenCalled();
      expect(controller.getSnapshot().drafts.attachments[0].previewUrl).toBe("blob:owned");
      await controller.invoke("composer.checkpoint", { activeTabId: "thread:a", primary: { text: "", imageIds: [], fileIds: [], selections: [] } });
      expect(revoke).toHaveBeenCalledWith("blob:owned");
    } finally { URL.revokeObjectURL = originalRevoke; }
  });

  it("owns accepted queue input and restores edits through public commands", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "", status: "in_progress", model_provider: "", turns: [{ id: "turn", status: "in_progress", items: [] }] } as never;
    window.wuu = { queueTurn: vi.fn().mockResolvedValue({ queued: { id: "queued" } }), dequeueTurn: vi.fn().mockResolvedValue({ ok: true }) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, thread, activeSessionTabID: "thread:a", sessionTabs: [
      { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "queued text", images: [], files: [] },
    ] });
    await settleSubmission(controller, await controller.invoke("composer.submit", { ownerTabId: "thread:a", mode: "queue" }));
    const pending = controller.getSnapshot().composerPending[0];
    expect(pending).toMatchObject({ threadId: "a", queued: [{ text: "queued text" }], guides: [] });
    expect(Object.isFrozen(pending.queued[0])).toBe(true);
    const stale = { ownerTabId: "thread:retired", messageId: pending.queued[0].id, kind: "queue" as const };
    await expect(controller.invoke("composer.edit-pending", stale)).rejects.toThrow("owner");
    await expect(controller.invoke("composer.remove-pending", stale)).rejects.toThrow("owner");
    await expect(controller.invoke("composer.toggle-pending-mode", stale)).rejects.toThrow("owner");
    expect(window.wuu.dequeueTurn).not.toHaveBeenCalled();
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
    await controller.invoke("composer.edit-pending", { ownerTabId: "thread:a", messageId: pending.queued[0].id, kind: "queue" });
    expect(controller.getSnapshot().composerPending).toEqual([]);
    expect(controller.getSnapshot().drafts.primary.text).toBe("queued text");
    expect(window.wuu.dequeueTurn).toHaveBeenCalledWith("a", pending.queued[0].id);
  });

  it("deletes an archived split owner while retaining the surviving draft", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = (id: string) => ({ id, cwd: context.cwd, preview: "", status: "idle", model_provider: "", archived: id === "a", turns: [] }) as never;
    window.wuu = { deleteThread: vi.fn().mockResolvedValue({}) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread: thread("a"), secondaryThread: thread("b"),
      threads: [thread("a"), thread("b")], activePane: "primary", activeSessionTabID: "thread:a", sessionTabs: ["a", "b"].map(id => ({
        id: `thread:${id}`, kind: "thread", threadID: id, context, title: id, prompt: `draft ${id}`, images: [], files: [],
      })) });
    await controller.invoke("settings.delete-archived", { threadId: "a" });
    expect(window.wuu.deleteThread).toHaveBeenCalledWith("a", { onlyIfArchived: true });
    expect(controller.getSnapshot().drafts).toMatchObject({ split: false, activeTabId: "thread:b", primary: { text: "draft b", ownerTabId: "thread:b" } });
    expect(controller.getSnapshot().settings.archivedThreads).toEqual([]);
  });

  it("closes the archived conversation only after confirmation and keeps the populated fallback", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "", status: "idle", model_provider: "", turns: [] } as never;
    let confirm!: (value: unknown) => void;
    window.wuu = { archiveThread: vi.fn(() => new Promise(resolve => { confirm = resolve; })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread, threads: [thread], activeSessionTabID: "thread:a", sessionTabs: [
      { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "", images: [], files: [] },
      { id: "draft:b", kind: "draft", context, title: "B", createdAt: 1, prompt: "keep this", images: [], files: [] },
    ] });
    const archiving = controller.invoke("thread.archive", { threadId: "a", archived: true });
    expect(controller.getSnapshot().drafts.activeTabId).toBe("thread:a");
    confirm({ thread: { id: "a" } });
    await archiving;
    expect(controller.getSnapshot().drafts).toMatchObject({ activeTabId: "draft:b", primary: { text: "keep this" } });
    expect(controller.getSnapshot().settings.archivedThreads.map(thread => thread.id)).toEqual(["a"]);
  });

  it("reuses a populated context draft after archived thread deletion", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "", status: "idle", model_provider: "", archived: true, turns: [] } as never;
    window.wuu = { deleteThread: vi.fn().mockResolvedValue({}) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread, threads: [thread], activeSessionTabID: "thread:a", sessionTabs: [
      { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "", images: [], files: [] },
      { id: "draft:b", kind: "draft", context, title: "B", createdAt: 1, prompt: "keep this", images: [], files: [] },
    ] });
    await controller.invoke("settings.delete-archived", { threadId: "a" });
    expect(controller.getSnapshot().drafts).toMatchObject({ activeTabId: "draft:b", primary: { text: "keep this" } });
    expect(controller.getSnapshot().tabs.items).toHaveLength(1);
  });

  it("addresses each split pane attachment owner independently of the selected tab", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = (id: string) => ({ id, cwd: context.cwd, preview: "", status: "idle", model_provider: "", turns: [] }) as never;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread: thread("a"), secondaryThread: thread("b"), activePane: "secondary", activeSessionTabID: "thread:b", sessionTabs: ["a", "b"].map(id => ({
      id: `thread:${id}`, kind: "thread", threadID: id, context, title: id, prompt: "", images: [], files: [],
    })) });
    expect(controller.getSnapshot().drafts.primary.ownerTabId).toBe("thread:a");
    expect(controller.getSnapshot().drafts.secondary.ownerTabId).toBe("thread:b");
    await expect(controller.invoke("composer.checkpoint", { activeTabId: "thread:b",
      primary: { ...controller.getSnapshot().drafts.primary, ownerTabId: "stale-primary" },
      secondary: controller.getSnapshot().drafts.secondary })).rejects.toThrow("Composer pane ownership changed");
    await controller.invoke("composer.set-draft", { ownerTabId: controller.getSnapshot().drafts.primary.ownerTabId, text: "primary" });
    expect(controller.getSnapshot().drafts.primary.text).toBe("primary");
    expect(controller.getSnapshot().drafts.secondary.text).toBe("");
    await controller.invoke("conversation.activate-pane", { pane: "primary" });
    expect(controller.getSnapshot().tabs).toMatchObject({ activePane: "primary", primaryThreadId: "a", secondaryThreadId: "b" });
    await controller.invoke("conversation.close-pane", { pane: "secondary" });
    expect(controller.getSnapshot().drafts).toMatchObject({ split: false, primary: { text: "primary", ownerTabId: "thread:a" } });
  });

  it.each([false, true])("settles failed admitted attachment recovery (discard=%s)", async discard => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
    ] });
    let fail!: (error: Error) => void;
    const file = new File(["a"], "note.txt", { type: "text/plain" });
    Object.defineProperty(file, "arrayBuffer", { value: vi.fn().mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }))
      .mockResolvedValue(new Uint8Array([65]).buffer) });
    const { attachments } = await controller.invoke("composer.attach", { files: [file] });
    const attachmentId = attachments[0].id;
    const reading = controller.invoke("composer.resolve-attachment", { attachmentId });
    await controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: { text: "", imageIds: [], fileIds: [], selections: [] } });
    if (discard) await controller.invoke("composer.release-attachments", { attachmentIds: [attachmentId] });
    fail(new Error("encoding failed"));
    await expect(reading).rejects.toThrow("encoding failed");
    if (discard) {
      await expect(controller.invoke("composer.retry-attachment", { attachmentId })).rejects.toThrow("Unknown composer attachment");
      return;
    }
    await controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: { text: "retry", imageIds: [], fileIds: [attachmentId], selections: [] } });
    await expect(controller.invoke("composer.retry-attachment", { attachmentId })).resolves.toMatchObject({ status: "ready", data: "QQ==" });
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual([attachmentId]);
  });

  it("imports ready captures with their checkpoint atomically and rejects stale imports", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
    ] });
    const attachment = { id: "capture", kind: "file" as const, name: "note.txt", mediaType: "text/plain", status: "ready" as const, data: "QQ==" };
    const primary = { text: "capture", imageIds: [], fileIds: [attachment.id], selections: [] };
    await expect(controller.invoke("composer.checkpoint", { activeTabId: "stale", primary, attachments: [attachment] })).rejects.toThrow("Draft owner changed");
    await expect(controller.invoke("composer.resolve-attachment", { attachmentId: attachment.id })).rejects.toThrow("Unknown composer attachment");
    await controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary, attachments: [attachment] });
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual([attachment.id]);
    expect(controller.getSnapshot().drafts.attachments[0]).toMatchObject({ id: attachment.id, data: "QQ==" });
  });

  it("finishes admitted attachment reads after the owning draft removes the resource", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
    ] });
    let finish!: (bytes: ArrayBuffer) => void;
    const file = new File(["a"], "note.txt", { type: "text/plain" });
    Object.defineProperty(file, "arrayBuffer", { value: () => new Promise<ArrayBuffer>(resolve => { finish = resolve; }) });
    const { attachments } = await controller.invoke("composer.attach", { files: [file] });
    const attachmentId = attachments[0].id;
    const reading = controller.invoke("composer.resolve-attachment", { attachmentId });
    await controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: { text: "", imageIds: [], fileIds: [], selections: [] } });
    finish(new Uint8Array([65]).buffer);
    await expect(reading).resolves.toMatchObject({ id: attachmentId, status: "ready", data: "QQ==" });
    await expect(controller.invoke("composer.resolve-attachment", { attachmentId })).rejects.toThrow("Unknown composer attachment");
    expect(controller.getSnapshot().drafts.attachments).toEqual([]);
  });

  it("rolls back earlier attachment resources when a later batch file is rejected", async () => {
    const originalCreate = URL.createObjectURL, originalRevoke = URL.revokeObjectURL;
    const revoke = vi.fn();
    URL.createObjectURL = vi.fn(() => "blob:batch"); URL.revokeObjectURL = revoke;
    try {
      const context = { kind: "no_project" as const, cwd: "/workspace" };
      const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
        { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
      ] });
      const image = new File(["a"], "image.png", { type: "image/png" });
      Object.defineProperty(image, "arrayBuffer", { value: () => new Promise(() => {}) });
      const oversized = new File([], "large.txt", { type: "text/plain" });
      Object.defineProperty(oversized, "size", { value: 100 * 1024 * 1024 });
      await expect(controller.invoke("composer.attach", { files: [image, oversized] })).rejects.toThrow();
      expect(controller.getSnapshot().drafts.primary.imageIds).toEqual([]);
      expect(revoke).toHaveBeenCalledWith("blob:batch");
    } finally { URL.createObjectURL = originalCreate; URL.revokeObjectURL = originalRevoke; }
  });

  it("imports only ready attachment values and rejects conflicting resource identities", async () => {
    const controller = new ApplicationController();
    const attachment = { id: "ready", kind: "file" as const, name: "note.txt", mediaType: "text/plain", status: "ready" as const, data: "QQ==" };
    await controller.invoke("composer.import-attachments", { attachments: [attachment] });
    await expect(controller.invoke("composer.import-attachments", { attachments: [{ ...attachment, data: "Qg==" }] })).rejects.toThrow("conflict");
    expect(await controller.invoke("composer.resolve-attachment", { attachmentId: "ready" })).toMatchObject({ data: "QQ==" });
    await controller.invoke("composer.release-attachments", { attachmentIds: ["ready"] });
    await expect(controller.invoke("composer.resolve-attachment", { attachmentId: "ready" })).rejects.toThrow("Unknown composer attachment");
  });

  it.each(["send", "queue", "steer"] as const)("submits owned attachment bytes through %s and retains them on failure", async mode => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const send = vi.fn().mockRejectedValue(new Error("delivery failed"));
    window.wuu = { startTurn: send, queueTurn: send, steerTurn: send } as unknown as WuuDesktopApi;
    const thread = { id: "thread", cwd: context.cwd, preview: "", status: mode === "send" ? "idle" : "in_progress", model_provider: "", turns: mode === "send" ? [] : [{ id: "active", status: "in_progress", items: [] }] } as never;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, thread, activeSessionTabID: "thread:thread", sessionTabs: [
      { id: "thread:thread", kind: "thread", threadID: "thread", context, title: "Thread", prompt: "", images: [], files: [{ id: "file", filename: "note.txt", media_type: "text/plain", data: "QQ==" }] },
    ] });
    expect(await settleSubmission(controller, await controller.invoke("composer.submit", { ownerTabId: "thread:thread", mode }))).toMatchObject({ phase: "failed", error: "delivery failed" });
    const args = send.mock.calls[0];
    expect(args[mode === "send" ? 3 : mode === "queue" ? 4 : 5]).toEqual([{ filename: "note.txt", media_type: "text/plain", data: "QQ==" }]);
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual(["file"]);
  });

  it("reselects source transport and retains latest draft while a workspace switch is pending", async () => {
    const source = { kind: "project" as const, project_id: "a", cwd: "/a" };
    const target = { kind: "project" as const, project_id: "b", cwd: "/b" };
    let finishTarget!: (value: never) => void;
    const select = vi.spyOn(RuntimeLoader, "selectRuntimeContext").mockImplementation(async context => {
      if (context.cwd === "/b") return new Promise(resolve => { finishTarget = resolve; });
      return { projects: [], active_context: source } as never;
    });
    const configure = vi.spyOn(RuntimeLoader, "loadRuntimeConfiguration").mockResolvedValue({});
    try {
      const controller = new ApplicationController({ ...initialState, activeContext: source, activeSessionTabID: "draft:a", sessionTabs: [
        { id: "draft:a", kind: "draft", context: source, title: "A", createdAt: 1, prompt: "before", images: [], files: [] },
        { id: "draft:b", kind: "draft", context: target, title: "B", createdAt: 2, prompt: "destination", images: [], files: [] },
      ] });
      const leaving = controller.invoke("tab.select", { tabId: "draft:b" });
      await controller.invoke("composer.set-draft", { text: "latest source" });
      await controller.invoke("tab.select", { tabId: "draft:a" });
      expect(select).toHaveBeenLastCalledWith(source);
      finishTarget({ projects: [], active_context: target } as never);
      await leaving;
      expect(controller.getSnapshot().tabs.activeTabId).toBe("draft:a");
      expect(controller.getSnapshot().composer.draftText).toBe("latest source");
    } finally { select.mockRestore(); configure.mockRestore(); }
  });

  it("creates a separate draft during admission without stealing focus on completion", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    let finish!: (result: unknown) => void;
    window.wuu = { renameThread: vi.fn().mockResolvedValue({}), startThread: () => new Promise(resolve => { finish = resolve; }),
      startTurn: vi.fn(async () => ({ turn: { id: "sent", status: "in_progress", items: [] } })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "first", images: [], files: [] },
    ] });
    await controller.invoke("composer.set-draft", { text: "edited before admission" });
    const sending = controller.invoke("composer.submit", { ownerTabId: controller.getState().activeSessionTabID });
    expect(controller.getSnapshot().navigationWorkspace.pendingConversations).toMatchObject([{ id: "draft:a", title: "edited before admission" }]);
    const next = await controller.invoke("tab.new", {});
    expect(next.tabId).not.toBe("draft:a");
    await controller.invoke("composer.set-draft", { text: "next" });
    finish({ thread: { id: "created", model_provider: "", preview: "", updated_at: "2026-10-24T00:00:00Z", cwd: context.cwd, turns: [], status: "idle" } });
    await settleSubmission(controller, await sending);
    expect(controller.getSnapshot().tabs.activeTabId).toBe(next.tabId);
    expect(controller.getSnapshot().composer.draftText).toBe("next");
    expect(controller.getSnapshot().tabs.items.find(tab => tab.threadId === "created")?.draftText).toBe("");
  });

  it("keeps a custom project draft title when its mode is selected again", async () => {
    const context = { kind: "project" as const, project_id: "workspace", cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "My project", project: true, createdAt: 1, prompt: "", images: [], files: [] },
    ] });
    await controller.invoke("draft.set-project-mode", { project: true });
    expect(controller.getState().sessionTabs[0].title).toBe("My project");
  });

  it("keeps pending attachment ownership across draft navigation and rejects stale checkpoints", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "", images: [], files: [] },
      { id: "draft:b", kind: "draft", context, title: "B", createdAt: 2, prompt: "", images: [], files: [] },
    ] });
    let finish!: (bytes: ArrayBuffer) => void;
    const file = new File(["document"], "note.txt", { type: "text/plain" });
    Object.defineProperty(file, "arrayBuffer", { value: () => new Promise<ArrayBuffer>(resolve => { finish = resolve; }) });
    const attached = await controller.invoke("composer.attach", { files: [file] });
    const id = attached.attachments[0].id;
    const draft = { text: "with file", imageIds: [], fileIds: [id], selections: [] };
    await controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: draft });
    await controller.invoke("tab.select", { tabId: "draft:b" });
    await expect(controller.invoke("composer.checkpoint", { activeTabId: "draft:a", primary: draft })).rejects.toThrow("Draft owner changed");
    finish(new Uint8Array([65]).buffer);
    const resolved = await controller.invoke("composer.resolve-attachment", { attachmentId: id });
    expect(resolved).toMatchObject({ status: "ready", data: "QQ==" });
    await controller.invoke("tab.select", { tabId: "draft:a" });
    expect(controller.getSnapshot().drafts.primary.fileIds).toEqual([id]);
    expect(controller.getSnapshot().drafts.primary.text).toBe("with file");
    expect(Object.isFrozen(controller.getSnapshot().drafts.attachments[0])).toBe(true);
  });

  it("moves between draft tabs without dropping attachments or newer text", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const image = { id: "image", media_type: "image/png", data: "abc" };
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "a", images: [image], files: [] },
      { id: "draft:b", kind: "draft", context, title: "B", createdAt: 2, prompt: "b", images: [], files: [] },
    ] });
    await controller.invoke("composer.set-draft", { text: "new a" });
    await controller.invoke("tab.select", { tabId: "draft:b" });
    expect(controller.getSnapshot().composer.draftText).toBe("b");
    await controller.invoke("tab.select", { tabId: "draft:a" });
    expect(controller.getSnapshot().composer.draftText).toBe("new a");
    expect(controller.getSnapshot().tabs.items[0].hasAttachments).toBe(true);
    expect(controller.getState().sessionTabs[0]).toMatchObject({ images: [image] });
  });

  it("keeps the active tab when loading its close destination fails", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    window.wuu = { resumeThread: vi.fn().mockRejectedValue(new Error("resume failed")) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "retain", images: [], files: [] },
      { id: "thread:b", kind: "thread", context, title: "B", threadID: "b", prompt: "", images: [], files: [] },
    ] });
    await expect(controller.invoke("tab.close", { tabId: "draft:a" })).rejects.toThrow("resume failed");
    expect(controller.getSnapshot().tabs.activeTabId).toBe("draft:a");
    expect(controller.getSnapshot().tabs.items).toHaveLength(2);
    expect(controller.getSnapshot().tabs.pending).toBeUndefined();
  });

  it("retains catalog detail across section changes until explicitly closed", async () => {
    const controller = new ApplicationController();
    await controller.invoke("catalog.select-plugin", { pluginId: "sample" });
    await controller.invoke("catalog.select-section", { section: "skills" });
    await controller.invoke("navigation.show-section", { section: "catalog" });
    expect(controller.getSnapshot().navigation).toMatchObject({ section: "catalog", catalogSection: "skills", catalogPluginId: "sample" });
    await controller.invoke("navigation.show-section", { section: "conversations" });
    expect(controller.getSnapshot().navigation.catalogPluginId).toBe("sample");
    await controller.invoke("catalog.select-plugin", { pluginId: "" });
    expect(controller.getSnapshot().navigation.catalogPluginId).toBe("");
  });
  it("rejects unknown commands and protects nested public snapshot values", async () => {
    const controller = new ApplicationController();
    const snapshot = controller.getSnapshot();
    expect(Reflect.set(snapshot.composer.model!, "label", "Injected")).toBe(false);
    expect(Object.isFrozen(snapshot.composer.availableSubmissionModes)).toBe(true);
    await expect(controller.invoke("missing.action" as "runtime.refresh", {})).rejects.toThrow("Unknown application action");
    expect(controller.getSnapshot()).toBe(snapshot);
  });
  it("refreshes the local activation lifetime after successful connection restoration while retaining drafts", async () => {
    let restore!: () => Promise<void>;
    const initialized = { extension_inventory: [] };
    const initialize = vi.fn().mockRejectedValueOnce(new Error("restore failed")).mockResolvedValue(initialized);
    window.wuu = {
      onRuntimeRestore: (listener: () => Promise<void>) => { restore = listener; return () => {}; },
      onServerEvent: () => () => {},
      listProjects: async () => ({ projects: [] }), initialize,
      listAllThreads: async () => ({ threads: [] }), listArchivedThreads: async () => ({ threads: [] }),
    } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: initialized as never });
    await controller.invoke("composer.set-draft", { text: "Survive reconnect" });
    const stop = controller.start();
    try {
      await expect(restore()).rejects.toThrow("restore failed");
      expect(controller.getState().runtimeConnectionGeneration ?? 0).toBe(0);
      await restore();
      expect(controller.getState().runtimeConnectionGeneration).toBe(1);
      expect(controller.getSnapshot().composer.draftText).toBe("Survive reconnect");
      await restore();
      expect(controller.getState().runtimeConnectionGeneration).toBe(2);
    } finally { stop(); }
  });

  it("publishes the canonical state to every subscriber without duplicating it", () => {
    const controller = new ApplicationController();
    const changed = vi.fn();
    controller.subscribe(changed);
    const before = controller.getSnapshot();
    expect(controller.getSnapshot()).toBe(before);
    controller.updateState(current => ({ ...current, status: "connection failed" }));
    expect(controller.getSnapshot()).not.toBe(before);
    expect(controller.getSnapshot().connection.message).toBe("connection failed");
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("does not apply an old generation's delayed thread selection", async () => {
    let resolve!: (value: unknown) => void;
    window.wuu = { resumeThread: vi.fn(() => new Promise(done => { resolve = done; })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController();
    const lifetime = new AbortController();
    const selecting = controller.invoke("thread.select", { threadId: "old" }, { signal: lifetime.signal });
    lifetime.abort();
    resolve({ thread: { id: "old", title: "Old", turns: [] } });
    await expect(selecting).rejects.toThrow();
    expect(controller.getSnapshot().conversation.threadId).toBeUndefined();
  });

  it("does not replay a failed send and keeps its draft", async () => {
    const send = vi.fn(async () => { throw new Error("uncertain delivery"); });
    window.wuu = { startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: { kind: "no_project", cwd: "/workspace" }, thread: { id: "thread", model_provider: "fixture", model: "fixture", preview: "", created_at: "", updated_at: "", cwd: "/workspace", turns: [], status: "idle" } as never, activeSessionTabID: "thread:thread", sessionTabs: [{ id: "thread:thread", kind: "thread", threadID: "thread", context: { kind: "no_project", cwd: "/workspace" }, title: "", prompt: "", images: [], files: [] }] });
    await controller.invoke("composer.set-draft", { text: "Keep me" });
    expect(await settleSubmission(controller, await controller.invoke("composer.submit", { ownerTabId: controller.getState().activeSessionTabID }))).toMatchObject({ phase: "failed", error: "uncertain delivery" });
    expect(send).toHaveBeenCalledTimes(1);
    expect(controller.getSnapshot().composer.draftText).toBe("Keep me");
  });

  it("preserves the draft when creating a thread succeeds but submitting fails", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const send = vi.fn(async () => { throw new Error("uncertain delivery"); });
    window.wuu = { startThread: vi.fn(async () => ({ thread: { id: "created", model_provider: "fixture", model: "fixture", preview: "", created_at: "", updated_at: "", cwd: context.cwd, turns: [], status: "idle" } })), startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [{ id: "draft:a", kind: "draft", context, title: "", createdAt: 1, prompt: "", images: [], files: [] }] });
    await controller.invoke("composer.set-draft", { text: "Keep the first message" });
    expect(await settleSubmission(controller, await controller.invoke("composer.submit", { ownerTabId: controller.getState().activeSessionTabID }))).toMatchObject({ phase: "failed", error: "uncertain delivery" });
    expect(controller.getSnapshot().conversation.threadId).toBe("created");
    expect(controller.getSnapshot().composer.draftText).toBe("Keep the first message");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("keeps edits made during thread creation after the original draft is submitted", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    let created!: (value: unknown) => void;
    const send = vi.fn(async () => ({ turn: { id: "sent", status: "in_progress", started_at: "2026-10-24T00:00:00Z", items: [] } }));
    window.wuu = { startThread: vi.fn(() => new Promise(resolve => { created = resolve; })), startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, initialized: submissionInitialized, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [{ id: "draft:a", kind: "draft", context, title: "", createdAt: 1, prompt: "", images: [], files: [] }] });
    await controller.invoke("composer.set-draft", { text: "Original" });
    const submitting = controller.invoke("composer.submit", { ownerTabId: controller.getState().activeSessionTabID });
    await controller.invoke("composer.set-draft", { text: "Next message" });
    created({ thread: { id: "created", model_provider: "", cwd: context.cwd, preview: "", updated_at: "2026-10-24T00:00:00Z", turns: [], status: "idle" } });
    await settleSubmission(controller, await submitting);
    expect(send).toHaveBeenCalledWith("created", "Original", [], [], "standard", undefined, undefined, context, expect.any(String));
    expect(controller.getSnapshot().composer.draftText).toBe("Next message");
  });
  it("selects the target tab and restores its saved draft through the public action", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const first = { id: "first", cwd: context.cwd, turns: [], status: "idle" } as never;
    const second = { id: "second", cwd: context.cwd, turns: [], status: "idle" } as never;
    window.wuu = { resumeThread: vi.fn(async () => ({ thread: second })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread: first,
      threads: [first, second], activeSessionTabID: "thread:first", sessionTabs: [
        { id: "thread:first", kind: "thread", context, threadID: "first", title: "First", prompt: "First draft", images: [], files: [] },
        { id: "thread:second", kind: "thread", context, threadID: "second", title: "Second", prompt: "Second draft", images: [], files: [] },
      ] });
    await controller.invoke("composer.set-draft", { text: "Fresh outgoing draft" });
    await controller.invoke("thread.select", { threadId: "second" });
    expect(controller.getState().sessionTabs.find(tab => tab.id === "thread:first")).toMatchObject({ prompt: "Fresh outgoing draft" });
    expect(controller.getSnapshot().conversation.threadId).toBe("second");
    expect(controller.getSnapshot().composer.draftText).toBe("Second draft");
  });

});
