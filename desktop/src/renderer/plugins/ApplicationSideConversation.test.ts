import { afterEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, RuntimeContext, SideThreadEventEnvelope, Thread } from "../../shared/protocol";
import { initialState, type AppState } from "../AppState";
import { ApplicationSideConversation } from "./ApplicationSideConversation";
const original = window.wuu;
const stops: (() => void)[] = [];
afterEach(() => { stops.splice(0).forEach(stop => stop()); Object.defineProperty(window, "wuu", { configurable: true, value: original }); });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes; }); return { promise, resolve }; }
const context: RuntimeContext = { kind: "no_project", cwd: "/repo" };
const page = { kind: "website" as const, pageId: "page-1", title: "Source", url: "https://example.test", text: "captured body", selection: "selected", capturedAt: "2026-01-01" };
function thread(id = "main"): Thread { return { id, title: id, preview: "", cwd: "/repo", model_provider: "openai", model: "model", status: "idle", created_at: "", updated_at: "", turns: [] }; }
function initialized(): InitializeResult {
  return { protocol_version: "1", workspace_root: "/repo", provider: "openai", model: "model", effort: "", variant: "", features: {}, providers: [{ name: "openai", type: "openai", model: "model", api_key_configured: true }], extension_inventory: [{
    id: "plugin:bundled:side", kind: "plugin", name: "Side", state: "active", enabled: true, approval_state: "official", runtime_state: "active",
    provenance: { kind: "plugin", source: "bundled", scope: "bundled", plugin_id: "side", official: true },
    contributions: { commands: [{ id: "side", title: "Side chat", kind: "side_conversation", template: "Side policy" }, { id: "other", title: "Other", kind: "side_conversation", template: "Other policy" }] },
  }] };
}
function fixture() {
  let state: AppState = { ...initialState, activeContext: context, initialized: initialized(), thread: thread(), threads: [thread(), thread("other")] };
  const listeners = new Set<() => void>();
  const install = (next: AppState) => { state = next; listeners.forEach(listener => listener()); };
  let event: ((value: SideThreadEventEnvelope) => void) | undefined;
  const summary = { side_thread_id: "side-1", main_thread_id: "main", status: "running" as const, revision: 1, created_at: "", updated_at: "" };
  const api = {
    openSideThread: vi.fn().mockResolvedValue({ summary: null }), getSideThreadHistory: vi.fn().mockResolvedValue(null),
    sendSideThreadMessage: vi.fn().mockResolvedValue({ summary, user_message_id: "user-1" }),
    interruptSideThread: vi.fn().mockResolvedValue({ ok: true }), resetSideThread: vi.fn().mockResolvedValue({ ok: true }),
    onSideThreadEvent: vi.fn((handler: typeof event) => { event = handler; return () => { event = undefined; }; }),
    startThread: vi.fn().mockResolvedValue({ thread: thread("created") }),
  };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
  const submitBackground = vi.fn().mockResolvedValue(true), cacheThread = vi.fn(), openPanel = vi.fn(), closePanel = vi.fn();
  const runtime = { pending: false, params: { engine: "wuu" } };
  const owner = new ApplicationSideConversation({ getState: () => state, updateState: update => install(typeof update === "function" ? update(state) : update), subscribeState: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, changed: vi.fn(), isNavigationPending: () => false,
    activateThread: async id => install({ ...state, thread: state.threads.find(row => row.id === id) }), openPanel, closePanel, getContextChatRuntime: () => runtime, submitBackground, cacheThread });
  stops.push(owner.start());
  return { owner, api, install, get: () => state, runtime, submitBackground, cacheThread, openPanel, closePanel, emit: (value: SideThreadEventEnvelope) => event?.(value) };
}
describe("application side conversation", () => {
  it("opens lazily and retains draft and background replies after closing", async () => {
    const h = fixture(); await h.owner.invoke("side.open", { parentThreadId: "main" });
    expect(h.api.getSideThreadHistory).not.toHaveBeenCalled();
    await h.owner.invoke("side.send", { parentThreadId: "main", text: "hello" });
    await h.owner.invoke("side.set-draft", { parentThreadId: "main", text: "next" });
    await h.owner.invoke("side.close", { parentThreadId: "main" });
    h.emit({ workdir: "/repo", event: { type: "message", side_thread_id: "side-1", main_thread_id: "main", revision: 2, message: { id: "reply", side_thread_id: "side-1", role: "assistant", text: "done", status: "completed", created_at: "" } } });
    const view = h.owner.getSnapshot();
    expect(view.entry).toMatchObject({ open: false, draft: "next" });
    expect(view.entry?.messages.at(-1)?.text).toBe("done");
    expect(Object.isFrozen(view.entry?.messages)).toBe(true);
  });
  it("does not substitute another command when the selected extension command disappears", async () => {
    const h = fixture(); await h.owner.invoke("side.open", { parentThreadId: "main", commandId: "side.side" });
    const next = structuredClone(h.get()); next.initialized!.extension_inventory![0].contributions!.commands!.shift(); h.install(next);
    await expect(h.owner.invoke("side.send", { parentThreadId: "main", text: "hello" })).rejects.toThrow();
    expect(h.api.sendSideThreadMessage).not.toHaveBeenCalled(); expect(h.closePanel).toHaveBeenCalled();
  });
  it("preserves captured page text and submits through the shared background owner", async () => {
    const h = fixture(); const result = await h.owner.invoke("context-chat.submit", { text: "explain", page, context });
    expect(result).toEqual({ sessionId: "created", sent: true });
    expect(h.api.startThread).toHaveBeenCalledWith({ engine: "wuu" }, context);
    expect(h.get().thread?.id).toBe("main"); expect(h.get().allowThreadAutoActivation).toBe(false);
    const [message, target, source] = h.submitBackground.mock.calls[0];
    expect(target.id).toBe("created"); expect(source).toEqual(context);
    expect(message.contentParts).toEqual(expect.arrayContaining([expect.objectContaining({ type: "text", text: "explain" }), expect.objectContaining({ type: "text", text: expect.stringContaining("captured body") })]));
  });
  it("keeps a late creation in its captured workspace without polluting the successor", async () => {
    const h = fixture(), created = deferred<{ thread: Thread }>(); h.api.startThread.mockReturnValueOnce(created.promise);
    const request = { text: "explain", page: { ...page }, context };
    const pending = h.owner.invoke("context-chat.submit", request);
    await expect(h.owner.invoke("context-chat.submit", request)).rejects.toThrow("already submitting");
    request.page.text = "new body";
    h.install({ ...h.get(), activeContext: { kind: "no_project", cwd: "/next" }, threads: [] });
    created.resolve({ thread: thread("created") }); await pending;
    expect(h.cacheThread).toHaveBeenCalledWith(thread("created"), context); expect(h.get().threads).toEqual([]);
    expect(h.submitBackground.mock.calls[0][0].text).toContain("captured body"); expect(h.submitBackground.mock.calls[0][2]).toEqual(context);
  });
  it("reuses available sessions and rejects stale source, archived sessions and pending runtime", async () => {
    const h = fixture(); await h.owner.invoke("context-chat.submit", { text: "follow up", page, context, sessionId: "other" }); expect(h.api.startThread).not.toHaveBeenCalled();
    const next = { ...h.get(), threads: [thread(), { ...thread("other"), archived: true }] }; h.install(next);
    await expect(h.owner.invoke("context-chat.submit", { text: "follow up", page, context, sessionId: "other" })).rejects.toThrow("no longer available");
    h.runtime.pending = true;
    await expect(h.owner.invoke("context-chat.submit", { text: "new", page, context })).rejects.toThrow("still loading");
    await expect(h.owner.invoke("context-chat.submit", { text: "new", page, context: { kind: "no_project", cwd: "/wrong" } })).rejects.toThrow("workspace changed");
    expect(h.api.startThread).not.toHaveBeenCalled();
  });
});


it("keeps response and file selections on the addressed parent", async () => {
  const h = fixture();
  const response = { type: "response" as const, response: { id: "selection", text: "quote", source: { thread_id: "other", turn_id: "turn", item_id: "item", start_offset: 0, end_offset: 5 } } };
  await h.owner.invoke("side.open", { parentThreadId: "other", selection: response });
  response.response.text = "changed after admission";
  expect(h.get().thread?.id).toBe("other");
  await h.owner.invoke("side.send", { parentThreadId: "other", text: "explain" });
  expect(h.api.sendSideThreadMessage).toHaveBeenCalledWith(expect.objectContaining({ main_thread_id: "other", selection: expect.objectContaining({ response: expect.objectContaining({ text: "quote" }) }) }));
  await expect(h.owner.invoke("side.open", { parentThreadId: "main", selection: response })).rejects.toThrow("another conversation");
  const file = { type: "file" as const, file: { workspace: "/repo", path: "first.ts", start_line: 1, start_column: 1, end_line: 1, end_column: 5, quote: "first", revision: "revision" } };
  await h.owner.invoke("side.set-selection", { parentThreadId: "other", selection: file });
  file.file.quote = "changed";
  expect(h.owner.getSnapshot().entry?.draftSelection).toMatchObject({ file: { quote: "first" } });
});
