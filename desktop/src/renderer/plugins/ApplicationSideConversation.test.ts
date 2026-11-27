import { afterEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult, RuntimeContext, Thread } from "../../shared/protocol";
import { initialState, type AppState } from "../AppState";
import { ApplicationSideConversation } from "./ApplicationSideConversation";
const original = window.wuu;
afterEach(() => { Object.defineProperty(window, "wuu", { configurable: true, value: original }); });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes; }); return { promise, resolve }; }
const context: RuntimeContext = { kind: "no_project", cwd: "/repo" };
const page = { kind: "website" as const, pageId: "page-1", title: "Source", url: "https://example.test", text: "captured body", selection: "selected", capturedAt: "2026-01-01" };
function thread(id = "main"): Thread { return { id, title: id, preview: "", cwd: "/repo", model_provider: "openai", model: "model", status: "idle", created_at: "", updated_at: "", turns: [] }; }
function initialized(): InitializeResult {
  return { protocol_version: "1", workspace_root: "/repo", provider: "openai", model: "model", effort: "", variant: "", features: {}, providers: [{ name: "openai", type: "openai", model: "model", api_key_configured: true }] };
}
function fixture() {
  let state: AppState = { ...initialState, activeContext: context, initialized: initialized(), thread: thread(), threads: [thread(), thread("other")] };
  const install = (next: AppState) => { state = next; };
  const api = {
    startThread: vi.fn().mockResolvedValue({ thread: thread("created") }),
  };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
  const submitBackground = vi.fn().mockResolvedValue(true), cacheThread = vi.fn();
  const runtime = { pending: false, requiresHostModel: true, params: { engine: "wuu" } };
  const owner = new ApplicationSideConversation({ getState: () => state, updateState: update => install(typeof update === "function" ? update(state) : update),
    changed: vi.fn(), isNavigationPending: () => false, open: vi.fn(), remove: vi.fn(), getContextChatRuntime: () => runtime, submitBackground, cacheThread });
  return { owner, api, install, get: () => state, runtime, submitBackground, cacheThread };
}

describe("context chat", () => {
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


it("allows context chat through a model-free selected wuu engine without host credentials", async () => {
  const h = fixture(); h.runtime.requiresHostModel = false;
  h.install({ ...h.get(), initialized: { ...initialized(), providers: [] } });
  await expect(h.owner.invoke("context-chat.submit", { text: "explain", page, context })).resolves.toMatchObject({ sent: true });
  expect(h.api.startThread).toHaveBeenCalledOnce();
});
