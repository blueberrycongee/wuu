import { describe, expect, it, vi } from "vitest";
import * as RuntimeLoader from "../RuntimeLoadState";
import { ApplicationController } from "./ApplicationController";
import { streamTextKey, streamTextStore } from "../StreamText";
import { initialState } from "../AppState";
import type { WuuDesktopApi } from "../../shared/protocol";

// Public failure cases: two shells observe one state, navigation completions
// cannot cross generation disposal, and failed submissions retain the draft.
describe("shared application state", () => {
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
    const controller = new ApplicationController({ ...initialState, thread, threads: [thread] });
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
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread: thread("a"), secondaryThread: thread("b"), activePane: "primary", activeSessionTabID: "thread:a", sessionTabs: ["a", "b"].map(id => ({
      id: `thread:${id}`, kind: "thread", threadID: id, context, title: id, prompt: `draft ${id}`, images: [], files: [],
    })) });
    const primary = controller.invoke("composer.submit", { ownerTabId: "thread:a" });
    await expect(controller.invoke("composer.submit", { ownerTabId: "thread:a" })).rejects.toThrow("already pending");
    const secondary = controller.invoke("composer.submit", { ownerTabId: "thread:b" });
    expect(send.mock.calls.map(args => args[0])).toEqual(["a", "b"]);
    finish.get("b")!({ turn: { id: "turn-b", status: "completed", items: [] } });
    await secondary;
    expect(controller.getSnapshot().drafts.primary.text).toBe("draft a");
    expect(controller.getSnapshot().drafts.secondary.text).toBe("");
    finish.get("a")!({ turn: { id: "turn-a", status: "completed", items: [] } });
    await primary;
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
  });

  it("keeps the image preview alive while editing a queued message back into its draft", async () => {
    const originalRevoke = URL.revokeObjectURL;
    const revoke = vi.fn();
    URL.revokeObjectURL = revoke;
    try {
      const context = { kind: "no_project" as const, cwd: "/workspace" };
      const thread = { id: "a", cwd: context.cwd, preview: "", status: "in_progress", model_provider: "", turns: [] } as never;
      window.wuu = { queueTurn: vi.fn().mockResolvedValue({}), dequeueTurn: vi.fn().mockResolvedValue({ ok: true }) } as unknown as WuuDesktopApi;
      const controller = new ApplicationController({ ...initialState, activeContext: context, thread, activeSessionTabID: "thread:a", sessionTabs: [
        { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "image", images: [{ id: "image", media_type: "image/png", data: "QQ==", previewSrc: "blob:owned" }], files: [] },
      ] });
      await controller.invoke("composer.submit", { mode: "queue" });
      await expect(controller.invoke("composer.release-attachments", { attachmentIds: ["image"] })).rejects.toThrow("pending message");
      await controller.invoke("composer.release-attachments", { attachmentIds: ["image"], ifUnreferenced: true });
      expect(controller.getSnapshot().drafts.attachments[0].id).toBe("image");
      await controller.invoke("composer.edit-pending", { messageId: controller.getSnapshot().composerPending[0].queued[0].id, kind: "queue" });
      expect(revoke).not.toHaveBeenCalled();
      expect(controller.getSnapshot().drafts.attachments[0].previewUrl).toBe("blob:owned");
      await controller.invoke("composer.checkpoint", { activeTabId: "thread:a", primary: { text: "", imageIds: [], fileIds: [], selections: [] } });
      expect(revoke).toHaveBeenCalledWith("blob:owned");
    } finally { URL.revokeObjectURL = originalRevoke; }
  });

  it("owns accepted queue input and restores edits through public commands", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const thread = { id: "a", cwd: context.cwd, preview: "", status: "in_progress", model_provider: "", turns: [{ id: "turn", status: "in_progress", items: [] }] } as never;
    window.wuu = { queueTurn: vi.fn().mockResolvedValue({}), dequeueTurn: vi.fn().mockResolvedValue({ ok: true }) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread, activeSessionTabID: "thread:a", sessionTabs: [
      { id: "thread:a", kind: "thread", threadID: "a", context, title: "A", prompt: "queued text", images: [], files: [] },
    ] });
    await controller.invoke("composer.submit", { mode: "queue" });
    const pending = controller.getSnapshot().composerPending[0];
    expect(pending).toMatchObject({ threadId: "a", queued: [{ text: "queued text", origin: "queue" }], guides: [] });
    expect(Object.isFrozen(pending.queued[0])).toBe(true);
    expect(controller.getSnapshot().drafts.primary.text).toBe("");
    await controller.invoke("composer.edit-pending", { messageId: pending.queued[0].id, kind: "queue" });
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
    const thread = { id: "thread", cwd: context.cwd, preview: "", status: "in_progress", model_provider: "", turns: [{ id: "active", status: "in_progress", items: [] }] } as never;
    const controller = new ApplicationController({ ...initialState, activeContext: context, thread, activeSessionTabID: "thread:thread", sessionTabs: [
      { id: "thread:thread", kind: "thread", threadID: "thread", context, title: "Thread", prompt: "", images: [], files: [{ id: "file", filename: "note.txt", media_type: "text/plain", data: "QQ==" }] },
    ] });
    await expect(controller.invoke("composer.submit", { mode })).rejects.toThrow("delivery failed");
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
    window.wuu = { startThread: () => new Promise(resolve => { finish = resolve; }),
      startTurn: vi.fn(async () => ({ turn: { id: "sent", status: "in_progress", items: [] } })) } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context, activeSessionTabID: "draft:a", sessionTabs: [
      { id: "draft:a", kind: "draft", context, title: "A", createdAt: 1, prompt: "first", images: [], files: [] },
    ] });
    await controller.invoke("composer.set-draft", { text: "edited before admission" });
    const sending = controller.invoke("composer.submit", {});
    expect(controller.getSnapshot().navigationWorkspace.pendingConversations).toMatchObject([{ id: "draft:a", title: "edited before admission" }]);
    const next = await controller.invoke("tab.new", {});
    expect(next.tabId).not.toBe("draft:a");
    await controller.invoke("composer.set-draft", { text: "next" });
    finish({ thread: { id: "created", model_provider: "", preview: "", updated_at: "2026-10-24T00:00:00Z", cwd: context.cwd, turns: [], status: "idle" } });
    await sending;
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
    const controller = new ApplicationController({ ...initialState, thread: { id: "thread", turns: [], status: "idle" } as never });
    await controller.invoke("composer.set-draft", { text: "Keep me" });
    await expect(controller.invoke("composer.submit", {})).rejects.toThrow("uncertain delivery");
    expect(send).toHaveBeenCalledTimes(1);
    expect(controller.getSnapshot().composer.draftText).toBe("Keep me");
  });

  it("preserves the draft when creating a thread succeeds but submitting fails", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    const send = vi.fn(async () => { throw new Error("uncertain delivery"); });
    window.wuu = { startThread: vi.fn(async () => ({ thread: { id: "created", cwd: context.cwd, turns: [], status: "idle" } })), startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context });
    await controller.invoke("composer.set-draft", { text: "Keep the first message" });
    await expect(controller.invoke("composer.submit", {})).rejects.toThrow("uncertain delivery");
    expect(controller.getSnapshot().conversation.threadId).toBe("created");
    expect(controller.getSnapshot().composer.draftText).toBe("Keep the first message");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("keeps edits made during thread creation after the original draft is submitted", async () => {
    const context = { kind: "no_project" as const, cwd: "/workspace" };
    let created!: (value: unknown) => void;
    const send = vi.fn(async () => ({ turn: { id: "sent", status: "in_progress", started_at: "2026-10-24T00:00:00Z", items: [] } }));
    window.wuu = { startThread: vi.fn(() => new Promise(resolve => { created = resolve; })), startTurn: send } as unknown as WuuDesktopApi;
    const controller = new ApplicationController({ ...initialState, activeContext: context });
    await controller.invoke("composer.set-draft", { text: "Original" });
    const submitting = controller.invoke("composer.submit", {});
    await controller.invoke("composer.set-draft", { text: "Next message" });
    created({ thread: { id: "created", model_provider: "", cwd: context.cwd, preview: "", updated_at: "2026-10-24T00:00:00Z", turns: [], status: "idle" } });
    await submitting;
    expect(send).toHaveBeenCalledWith("created", "Original", undefined, undefined, undefined, undefined, undefined, context, expect.any(String));
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
