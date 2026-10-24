import { describe, expect, it, vi } from "vitest";
import { ApplicationController } from "./ApplicationController";
import { initialState } from "../AppState";
import type { WuuDesktopApi } from "../../shared/protocol";

// Public failure cases: two shells observe one state, navigation completions
// cannot cross generation disposal, and failed submissions retain the draft.
describe("shared application state", () => {
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
    created({ thread: { id: "created", cwd: context.cwd, preview: "", updated_at: "2026-10-24T00:00:00Z", turns: [], status: "idle" } });
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
