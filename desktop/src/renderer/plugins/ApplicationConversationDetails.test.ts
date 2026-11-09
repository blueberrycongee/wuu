import { describe, expect, it, vi } from "vitest";
import { ApplicationConversationDetails } from "./ApplicationConversationDetails";
import { initialState } from "../AppState";
import type { Thread, WuuDesktopApi } from "../../shared/protocol";

// Public failure cases: old results must not recreate dismissed cards or cross
// source workspaces, and caller cancellation must remove its loading entry.
describe("conversation detail ownership", () => {
  const thread: Thread = { id: "thread", title: "Thread", preview: "", model_provider: "test", model: "model", cwd: "/a",
    status: "idle", created_at: "", updated_at: "", turns: [{ id: "turn", items: [], items_view: "full", status: "completed" }] };
  it("retains immutable context diagnostics and ignores a dismissed result", async () => {
    let finish!: (value: unknown) => void;
    window.wuu = { getThreadContextComposition: vi.fn(() => new Promise(resolve => { finish = resolve; })) } as unknown as WuuDesktopApi;
    const state = { ...initialState, activeContext: { kind: "no_project" as const, cwd: "/a" } };
    const details = new ApplicationConversationDetails({ getState: () => state, getThread: () => thread, changed: vi.fn() });
    const reading = details.invoke("conversation.open-context", { threadId: "thread" });
    const entry = details.getSnapshot()[0];
    expect(entry).toMatchObject({ threadID: "thread", afterTurnID: "turn", loading: true });
    const result = { thread_id: "thread", available: true, categories: [{ id: "tools", label: "Tools", contributes: true, tokens: 4 }] };
    finish(result); await reading;
    expect(details.getSnapshot()[0]).toMatchObject({ loading: false, result });
    const completed = details.getSnapshot()[0];
    expect(completed.kind === "context" && Object.isFrozen(completed.result?.categories?.[0])).toBe(true);
    expect(entry.loading).toBe(true);
    const next = details.invoke("conversation.open-context", { threadId: "thread" });
    await details.invoke("conversation.dismiss-detail", { entryId: details.getSnapshot()[1].id });
    finish(result); await next;
    expect(details.getSnapshot()).toHaveLength(1);
  });

  it("drops source-changed instructions and removes a cancelled loading card", async () => {
    let finish!: (value: unknown) => void;
    window.wuu = { listInstructionFiles: vi.fn(() => new Promise(resolve => { finish = resolve; })) } as unknown as WuuDesktopApi;
    let state = { ...initialState, activeContext: { kind: "no_project" as const, cwd: "/a" } };
    const details = new ApplicationConversationDetails({ getState: () => state, getThread: () => thread, changed: vi.fn() });
    const reading = details.invoke("conversation.open-instructions", { threadId: "thread" });
    state = { ...state, activeContext: { kind: "no_project", cwd: "/b" } };
    finish({ files: [] }); await reading;
    expect(details.getSnapshot()).toEqual([]);
    const lifetime = new AbortController();
    const cancelled = details.invoke("conversation.open-instructions", { threadId: "thread" }, lifetime.signal);
    lifetime.abort();
    expect(details.getSnapshot()).toEqual([]);
    finish({ files: [] }); await expect(cancelled).rejects.toThrow();
  });
});
