import { afterEach, describe, expect, it, vi } from "vitest";
import type { ServerEvent, Thread, UserQuestionRequest } from "../../shared/protocol";
import { createThreadSessionTab, emptyComposerDraft, initialState, threadSessionTabID, type AppState, type ComposerDraftState } from "../AppState";
import { ApplicationConversationActions } from "./ApplicationConversationActions";
import { createOptimisticTurn, type QueuedComposerMessage } from "../ComposerMessages";
const events = vi.hoisted(() => ({ receive: undefined as undefined | ((event: ServerEvent) => void) }));
vi.mock("../ServerEvents", () => ({ subscribeServerEvents: (receive: (event: ServerEvent) => void) => { events.receive = receive; return () => { events.receive = undefined; }; } }));
const original = window.wuu;
afterEach(() => { Object.defineProperty(window, "wuu", { configurable: true, value: original }); events.receive = undefined; });
const context = { kind: "project" as const, project_id: "project", cwd: "/tmp/conversation-actions" };
function thread(id = "source", patch: Partial<Thread> = {}): Thread { return {
  id, title: id, preview: id, model_provider: "wuu", model: "model", cwd: context.cwd, status: "idle", pinned: false, archived: false, created_at: "2026-10-24", updated_at: "2026-10-24",
  turns: [{ id: "turn", items_view: "full", status: "completed", items: [{ id: "user", type: "user_message", text: "Original" }, { id: "assistant", type: "agent_message", text: "Response", seq: 42, source_id: "wire-source" }] }], ...patch,
}; }
const target = { threadId: "source", turnId: "turn", itemId: "user" };
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
function harness(source = thread()) {
  let state: AppState = { ...initialState, initialized: { protocol_version: "test", workspace_root: context.cwd, provider: "wuu", model: "model" }, activeContext: context, thread: source, threads: [source], activeSessionTabID: threadSessionTabID(source.id), sessionTabs: [createThreadSessionTab(source, context)], gitStatus: { is_repo: true, dirty_count: 0 } };
  let primary = emptyComposerDraft(), secondary = emptyComposerDraft();
  let pending = false;
  const api = {
    forkThread: vi.fn().mockResolvedValue({ thread: thread("fork") }), gitStatus: vi.fn().mockResolvedValue({ is_repo: true }),
    editThreadMessage: vi.fn().mockResolvedValue({ thread: thread("source", { turns: [] }) }),
    listUserQuestions: vi.fn().mockResolvedValue({ questions: [] }), answerUserQuestion: vi.fn().mockResolvedValue({ ok: true }),
    cancelUserQuestion: vi.fn().mockResolvedValue({ ok: true }), holdUserQuestion: vi.fn().mockResolvedValue({ ok: true }),
    compactThread: vi.fn().mockResolvedValue({ turn: { id: "compact", kind: "compact", status: "in_progress", items: [] } }),
    startThread: vi.fn().mockResolvedValue({ thread: thread("handoff") }),
  };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
  const submit = vi.fn(async (message: QueuedComposerMessage, source: Thread, prepare?: () => Promise<Thread>) => { const prepared = prepare ? await prepare() : source; const next = { ...prepared, turns: [...prepared.turns, createOptimisticTurn(message, Date.now())] }; state = { ...state, thread: state.thread?.id === next.id ? next : state.thread, threads: state.threads.map(row => row.id === next.id ? next : row) }; return true; });
  const steer = vi.fn().mockResolvedValue(true), interrupt = vi.fn().mockResolvedValue(undefined);
  const resolve = vi.fn().mockResolvedValue({ images: [], files: [] });
  const actions = new ApplicationConversationActions({ getState: () => state, updateState: update => { state = typeof update === "function" ? update(state) : update; }, changed: vi.fn(), isNavigationPending: () => false,
    drafts: { get: pane => pane === "secondary" ? secondary : primary, set: (draft, pane) => { if (pane === "secondary") secondary = draft; else primary = draft; }, resolve, importHistory: () => ({ imageIds: [], fileIds: [] }) },
    pendingComposer: { threadHasPendingComposerMessages: () => pending }, submission: { submitToThread: submit, steerToThread: steer, interruptThread: interrupt },
  });
  return { actions, api, submit, steer, interrupt, resolve, state: () => state, setState: (next: AppState) => { state = next; }, pending: (value: boolean) => { pending = value; }, draft: () => primary, setDraft: (draft: ComposerDraftState) => { primary = draft; } };
}
function request(patch: Partial<UserQuestionRequest> = {}): UserQuestionRequest { return { request_id: "question", plugin_id: "plugin", execution_id: "execution", thread_id: "source", turn_id: "turn", created_at: "now", questions: [{ id: "choice", question: "Choose", options: [{ label: "One" }] }], ...patch }; }
function notify(method: string, params: unknown) { events.receive!({ kind: "notification", message: { method, params } } as ServerEvent); }

describe("application conversation domain workflows", () => {
  it("uses the shared admission prepare step for history edits and retains original draft on cancellation", async () => {
    const h = harness(); h.setDraft({ ...emptyComposerDraft(), prompt: "Unsent composer" });
    await h.actions.invoke("history.begin-edit", target);
    const view = h.actions.getSnapshot(); expect(view.editing?.draft.text).toBe("Original"); expect(Object.isFrozen(view.editing?.draft)).toBe(true);
    await h.actions.invoke("history.update-edit", { ...target, draft: { text: "Replacement", imageIds: [], fileIds: [] } });
    await h.actions.invoke("history.cancel-edit", {}); expect(h.draft().prompt).toBe("Unsent composer"); expect(h.api.editThreadMessage).not.toHaveBeenCalled();
    await h.actions.invoke("history.begin-edit", target);
    await h.actions.invoke("history.update-edit", { ...target, draft: { text: "Replacement", imageIds: [], fileIds: [] } });
    await h.actions.invoke("history.submit-edit", target);
    expect(h.api.editThreadMessage).toHaveBeenCalledWith("source", "turn", "user"); expect(h.submit).toHaveBeenCalledTimes(1);
    expect(h.state().thread?.turns[0].items[0].text).toBe("Replacement"); expect(h.actions.getSnapshot().editing).toBeUndefined();
  });
  it.each(["read-only", "running", "pending"])("rejects %s history mutations before any submission", async gate => {
    const h = harness(thread("source", gate === "read-only" ? { read_only: true } : gate === "running" ? { status: "in_progress" } : {})); h.pending(gate === "pending");
    await expect(h.actions.invoke("history.begin-edit", target)).rejects.toThrow(); expect(h.submit).not.toHaveBeenCalled(); expect(h.api.editThreadMessage).not.toHaveBeenCalled();
  });
  it("blocks duplicate edit and refuses a source change while attachments resolve", async () => {
    const h = harness(), ready = deferred<{ images: []; files: [] }>(); h.resolve.mockReturnValue(ready.promise);
    await h.actions.invoke("history.begin-edit", target); const sending = h.actions.invoke("history.submit-edit", target);
    await expect(h.actions.invoke("history.submit-edit", target)).rejects.toThrow("already submitting");
    h.setState({ ...h.state(), activeSessionTabID: "new-draft" }); ready.resolve({ images: [], files: [] });
    await expect(sending).rejects.toThrow("source changed"); expect(h.api.editThreadMessage).not.toHaveBeenCalled();
    expect(h.actions.getSnapshot().editing?.submitting).toBe(false);
  });
  it("preserves host-backed file identity through edit preparation", async () => {
    const h = harness(); h.resolve.mockResolvedValue({ images: [], files: [{ media_type: "application/pdf", data: "", remote_ref: "host-file", filename: "document.pdf" }] });
    await h.actions.invoke("history.begin-edit", target);
    await h.actions.invoke("history.update-edit", { ...target, draft: { text: "Use this file", imageIds: [], fileIds: ["resource"] } });
    await h.actions.invoke("history.submit-edit", target);
    expect(h.submit.mock.calls[0][0].files[0].remote_ref).toBe("host-file");
  });
  it("restores an edited message when mutation succeeds but shared submission fails", async () => {
    const h = harness(); h.submit.mockImplementation(async (_message, _source, prepare) => { await prepare!(); return false; });
    await h.actions.invoke("history.begin-edit", target);
    await h.actions.invoke("history.update-edit", { ...target, draft: { text: "Recover this", imageIds: [], fileIds: [] } });
    await h.actions.invoke("history.submit-edit", target); expect(h.draft().prompt).toBe("Recover this");
  });
  it("forks assistant history with its source identity and does not steal a newer view", async () => {
    const h = harness(), completion = deferred<{ thread: Thread }>(); h.api.forkThread.mockReturnValue(completion.promise);
    await h.actions.invoke("history.request-fork", { ...target, itemId: "assistant" }); expect(h.actions.getSnapshot().pendingFork?.itemId).toBe("assistant");
    const fork = h.actions.invoke("history.choose-fork", { mode: "worktree" });
    await expect(h.actions.invoke("history.choose-fork", { mode: "local" })).rejects.toThrow("in progress");
    const destination = thread("elsewhere"); h.setState({ ...h.state(), thread: destination, activeSessionTabID: "thread:elsewhere" }); h.setDraft({ ...emptyComposerDraft(), prompt: "Newer draft" });
    completion.resolve({ thread: thread("fork") }); await fork;
    expect(h.api.forkThread).toHaveBeenCalledWith("source", "turn", "assistant", "worktree", { seq: 42, source_id: "wire-source", type: "agent_message" });
    expect(h.state().thread?.id).toBe("elsewhere"); expect(h.draft().prompt).toBe("Newer draft"); expect(h.state().sessionTabs.some(tab => tab.id === "thread:fork")).toBe(true);
  });
  it("refuses worktree fork outside a git repository", async () => {
    const h = harness(); h.setState({ ...h.state(), gitStatus: { is_repo: false, dirty_count: 0 } });
    await h.actions.invoke("history.request-fork", { ...target, itemId: "assistant" });
    expect(h.actions.getSnapshot().pendingFork?.worktreeDisabledReason).toBeTruthy();
    await expect(h.actions.invoke("history.choose-fork", { mode: "worktree" })).rejects.toThrow(); expect(h.api.forkThread).not.toHaveBeenCalled();
  });
  it("loads the worktree eligibility reason before exposing an unknown fork choice", async () => {
    const h = harness(); h.setState({ ...h.state(), gitStatus: undefined }); h.api.gitStatus.mockResolvedValue({ is_repo: false });
    await h.actions.invoke("history.request-fork", { ...target, itemId: "assistant" });
    expect(h.actions.getSnapshot().pendingFork?.worktreeDisabledReason).toBeTruthy();
    expect(h.api.forkThread).not.toHaveBeenCalled();
  });
  it("does not resurrect resolved questions from an earlier list or publish after disposal", async () => {
    const h = harness(), listing = deferred<{ questions: UserQuestionRequest[] }>(); h.api.listUserQuestions.mockReturnValue(listing.promise);
    const stop = h.actions.start(); notify("user-question/requested", { request: request() }); notify("user-question/resolved", { request_id: "question" });
    listing.resolve({ questions: [request()] }); await h.actions.refreshQuestions(); expect(h.actions.getSnapshot().questions).toEqual([]);
    const later = deferred<{ questions: UserQuestionRequest[] }>(); h.api.listUserQuestions.mockReturnValue(later.promise); const refresh = h.actions.refreshQuestions(); stop(); later.resolve({ questions: [request({ request_id: "late" })] }); await refresh; expect(h.actions.getSnapshot().questions).toEqual([]);
  });
  it("answers tools without sending another message and steers ordinary offers to their source", async () => {
    const h = harness(); const stop = h.actions.start(); await h.actions.refreshQuestions();
    notify("user-question/requested", { request: request({ mode: "offer", reply_to_tool: true }) });
    await h.actions.invoke("question.send-offer", { requestId: "question", text: "Answer" }); expect(h.api.answerUserQuestion).toHaveBeenCalledWith("question", { answers: [{ id: "choice", selected: [], custom: "Answer" }] }); expect(h.steer).not.toHaveBeenCalled();
    notify("user-question/requested", { request: request({ mode: "offer", reply_to_tool: false }) });
    h.setState({ ...h.state(), thread: thread("different") });
    await h.actions.invoke("question.answer", { requestId: "question", answer: { answers: [{ id: "choice", selected: ["One"] }] } });
    expect(h.steer.mock.calls[0][1].id).toBe("source"); expect(h.steer.mock.calls[0][0].text).toBe("Choose\nOne"); stop();
  });
  it("does not resend an accepted offer when backend cancellation fails", async () => {
    const h = harness(); const stop = h.actions.start(); await h.actions.refreshQuestions();
    notify("user-question/requested", { request: request({ mode: "offer" }) });
    h.api.cancelUserQuestion.mockRejectedValueOnce(new Error("offline"));
    await expect(h.actions.invoke("question.send-offer", { requestId: "question", text: "Continue" })).rejects.toThrow("offline");
    await expect(h.actions.invoke("question.send-offer", { requestId: "question", text: "Continue" })).rejects.toThrow("no longer pending");
    expect(h.steer).toHaveBeenCalledTimes(1); stop();
  });
  it("holds and cancels questions through the backend while keeping failed actions retryable", async () => {
    const h = harness(); const stop = h.actions.start(); await h.actions.refreshQuestions();
    notify("user-question/requested", { request: request({ expires_at: "later" }) });
    await h.actions.invoke("question.hold", { requestId: "question" }); expect(h.actions.getSnapshot().questions[0].expires_at).toBeUndefined();
    h.api.cancelUserQuestion.mockRejectedValueOnce(new Error("offline")); await expect(h.actions.invoke("question.cancel", { requestId: "question" })).rejects.toThrow("offline");
    expect(h.actions.getSnapshot().pendingQuestionIds).toEqual([]); expect(h.actions.getSnapshot().questions).toHaveLength(1);
    await h.actions.invoke("question.cancel", { requestId: "question" }); expect(h.actions.getSnapshot().questions).toEqual([]); stop();
  });
  it("settles failed compaction on its source and delegates stop to the shared submission owner", async () => {
    const h = harness(), completion = deferred<never>(); h.api.compactThread.mockReturnValue(completion.promise);
    const compact = h.actions.invoke("conversation.compact", { threadId: "source" });
    expect(h.state().thread?.turns.at(-1)?.status).toBe("in_progress");
    h.setState({ ...h.state(), thread: thread("elsewhere") }); completion.reject(new Error("compact failure")); await expect(compact).rejects.toThrow("compact failure");
    expect(h.state().thread?.id).toBe("elsewhere"); expect(h.state().threads.find(row => row.id === "source")?.turns.at(-1)?.items.find(item => item.type === "context_compaction")?.status).toBe("failed");
    await h.actions.invoke("conversation.interrupt", { threadId: "source" }); expect(h.interrupt).toHaveBeenCalledWith("source");
  });
  it("rejects external-engine compaction and handoff without backend dispatch", async () => {
    const h = harness(thread("source", { engine_id: "codex" }));
    await expect(h.actions.invoke("conversation.compact", { threadId: "source" })).rejects.toThrow();
    await expect(h.actions.invoke("conversation.handoff", { threadId: "source", provider: "provider", model: "model", intent: "Continue" })).rejects.toThrow();
    expect(h.api.compactThread).not.toHaveBeenCalled(); expect(h.api.startThread).not.toHaveBeenCalled();
  });
  it("keeps a late handoff discoverable without replacing the newly selected conversation", async () => {
    const h = harness(), completion = deferred<{ thread: Thread }>(); h.api.startThread.mockReturnValue(completion.promise);
    const handoff = h.actions.invoke("conversation.handoff", { threadId: "source", provider: "provider", model: "model", effort: "high", intent: "Continue" });
    h.setState({ ...h.state(), thread: thread("elsewhere"), activeSessionTabID: "thread:elsewhere" }); completion.resolve({ thread: thread("handoff") });
    await expect(handoff).resolves.toEqual({ threadId: "handoff" }); expect(h.state().thread?.id).toBe("elsewhere"); expect(h.state().sessionTabs.some(tab => tab.id === "thread:handoff")).toBe(true);
    expect(h.api.startThread.mock.calls[0][0]).toMatchObject({ provider: "provider", model: "model", effort: "high", handoff: { parent_session_id: "source", intent: "Continue", revision: 1 } });
  });
});
