import { afterEach, describe, expect, it, vi } from "vitest";
import { ApplicationEnvironment } from "./ApplicationEnvironment";
import type { GitStatusResult } from "../../shared/protocol";
import { translateCurrent as t } from "../i18n";

const originalWuu = window.wuu;
afterEach(() => { vi.useRealTimers(); window.wuu = originalWuu; });
const status = (branch = "main"): GitStatusResult => ({ is_repo: true, branch, dirty_count: 0 });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
function fixture() {
  let source: { contextKey: string; root?: string; status?: GitStatusResult } = { contextKey: "project:one", root: "/one" };
  let active = true;
  const publishStatus = vi.fn((value: GitStatusResult) => { source = { ...source, status: value }; });
  const reportError = vi.fn();
  const api = {
    gitActionBusy: vi.fn().mockResolvedValue(false),
    listGitChanges: vi.fn().mockResolvedValue({ is_repo: true, root: "/one", files: [] }),
    readGitFileDiff: vi.fn().mockResolvedValue({ is_repo: true, path: "a.ts", status: "modified", additions: 1, deletions: 0, patch: "", truncated: false }),
    checkoutGitBranch: vi.fn().mockResolvedValue(status("feature")),
    createCheckoutGitBranch: vi.fn().mockResolvedValue({ status: status("feature") }),
    gitStatus: vi.fn().mockResolvedValue(status()),
    commitGitChanges: vi.fn().mockResolvedValue({ status: status(), commit: "abc", message: "commit" }),
    generateCommitMessage: vi.fn().mockResolvedValue({ message: "generated" }),
    createPullRequest: vi.fn().mockResolvedValue({ status: status(), url: "https://example.test/pr/1", already_exists: false }),
  };
  window.wuu = api as unknown as typeof window.wuu;
  const service = new ApplicationEnvironment({ getSource: () => source, publishStatus, reportError,
    assertActive: () => { if (!active) throw new Error("generation disposed"); } });
  return { service, api, publishStatus, reportError, change: (patch: Partial<typeof source>) => { source = { ...source, ...patch }; service.notifyChanged(); }, retire: () => { active = false; service.dispose(); } };
}

describe("ApplicationEnvironment", () => {
  it("keeps authoritative busy conservative and fences old contexts without polling stream deltas", async () => {
    const h = fixture();
    const old = deferred<boolean>(), next = deferred<boolean>();
    h.api.gitActionBusy.mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    expect(h.service.getSnapshot().actionBusy).toBe(true);
    expect(h.service.getSnapshot().actionBusyPending).toBe(true);
    await Promise.resolve();
    expect(h.api.gitActionBusy).toHaveBeenCalledExactlyOnceWith("/one");
    h.change({ contextKey: "project:two", root: "/two" });
    expect(h.service.getSnapshot().actionBusy).toBe(true);
    await Promise.resolve(); next.resolve(false); await Promise.resolve(); await Promise.resolve();
    expect(h.service.getSnapshot().actionBusy).toBe(false);
    expect(h.service.getSnapshot().actionBusyPending).toBe(false);
    old.resolve(true); await Promise.resolve(); await Promise.resolve();
    expect(h.service.getSnapshot().actionBusy).toBe(false);
    for (let i = 0; i < 20; i++) h.service.receive({ kind: "notification", workdir: "/two", message: { method: "item/agentMessage/delta", params: {} } });
    await Promise.resolve(); expect(h.api.gitActionBusy).toHaveBeenCalledTimes(2);
    h.api.gitActionBusy.mockRejectedValueOnce(new Error("offline"));
    h.service.receive({ kind: "notification", workdir: "/two", message: { method: "turn/started", params: {} } });
    h.service.receive({ kind: "notification", workdir: "/two", message: { method: "thread/resumed", params: {} } });
    expect(h.service.getSnapshot().actionBusy).toBe(true);
    await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
    expect(h.api.gitActionBusy).toHaveBeenCalledTimes(3);
    expect(h.service.getSnapshot().actionBusy).toBe(true);
    expect(h.service.getSnapshot().actionBusyPending).toBe(false);
    h.change({ contextKey: "", root: undefined });
    expect(h.service.getSnapshot().actionBusy).toBe(false);
    h.retire();
  });

  it("cancels a retired caller refresh without cancelling its successor", async () => {
    vi.useFakeTimers(); const h = fixture();
    const old = new AbortController(), next = new AbortController();
    h.service.scheduleStatusRefresh(10, { signal: old.signal }); old.abort();
    await vi.advanceTimersByTimeAsync(10); expect(h.api.gitStatus).not.toHaveBeenCalled();
    const active = new AbortController(); const pending = deferred<GitStatusResult>();
    h.api.gitStatus.mockReturnValueOnce(pending.promise);
    h.service.scheduleStatusRefresh(0, { signal: active.signal }); await vi.advanceTimersByTimeAsync(0);
    h.service.scheduleStatusRefresh(0, { signal: next.signal }); await vi.advanceTimersByTimeAsync(0);
    active.abort(); pending.resolve(status("retired")); await Promise.resolve();
    expect(h.publishStatus).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(150);
    expect(h.api.gitStatus).toHaveBeenCalledTimes(2); expect(h.service.getSnapshot().status?.branch).toBe("main");
    expect(h.reportError).not.toHaveBeenCalled();
    const stalled = deferred<GitStatusResult>(); const scope = new AbortController();
    const detach = vi.spyOn(scope.signal, "removeEventListener");
    h.api.gitStatus.mockReturnValueOnce(stalled.promise);
    h.service.scheduleStatusRefresh(0, { signal: scope.signal }); await vi.advanceTimersByTimeAsync(0);
    h.retire();
    expect(detach).toHaveBeenCalledWith("abort", expect.any(Function));
    stalled.resolve(status("disposed")); await Promise.resolve();
  });

  it("publishes branch and commit status and maps public parameters once", async () => {
    const h = fixture();
    await vi.waitFor(() => expect(h.service.getSnapshot().actionBusy).toBe(false));
    const listener = vi.fn(); h.service.subscribe(listener);
    await h.service.checkoutBranch("feature");
    expect(h.api.checkoutGitBranch).toHaveBeenCalledWith("feature", "/one");
    expect(h.publishStatus).toHaveBeenLastCalledWith(status("feature"), false);
    await h.service.createAndCheckoutBranch("new");
    expect(h.api.createCheckoutGitBranch).toHaveBeenCalledWith("new", "/one");
    const result = await h.service.commit({ message: "commit", includeUnstaged: true });
    expect(h.api.commitGitChanges).toHaveBeenCalledWith({ message: "commit", include_unstaged: true }, "/one");
    expect(h.publishStatus).toHaveBeenLastCalledWith(status(), true);
    expect(Object.isFrozen(result.status)).toBe(true);
    expect(listener).toHaveBeenCalledTimes(3);
  });
  it.each(["checkoutBranch", "createAndCheckoutBranch"] as const)("preserves host occupancy errors for %s without replay", async name => {
    const h = fixture(); const call = name === "checkoutBranch" ? h.api.checkoutGitBranch : h.api.createCheckoutGitBranch;
    call.mockRejectedValue(new Error("Error invoking remote method 'wuu:git-checkout': Error: cannot run Git actions while a thread is running in this working tree"));
    await expect(h.service[name]("feature")).rejects.toThrow(t("git.checkoutBlockedByRunningThread"));
    expect(call).toHaveBeenCalledTimes(1); expect(h.publishStatus).not.toHaveBeenCalled();
  });
  it("returns generated messages and PR data from the captured root", async () => {
    const h = fixture();
    expect(await h.service.generateCommitMessage({ includeUnstaged: false })).toBe("generated");
    expect(h.api.generateCommitMessage).toHaveBeenCalledWith({ include_unstaged: false }, "/one");
    const result = await h.service.createPullRequest({ title: "title", body: "body", draft: true });
    expect(h.api.createPullRequest).toHaveBeenCalledWith({ title: "title", body: "body", draft: true }, "/one");
    expect(result.url).toBe("https://example.test/pr/1");
  });
  it.each([{ root: "/two" }, { contextKey: "project:two" }])("fences late mutations across source changes %j", async patch => {
    const h = fixture(); const pending = deferred<GitStatusResult>(); h.api.checkoutGitBranch.mockReturnValue(pending.promise);
    const result = h.service.checkoutBranch("feature"); h.change(patch); pending.resolve(status("old"));
    await expect(result).rejects.toThrow("workspace changed"); expect(h.publishStatus).not.toHaveBeenCalled();
  });
  it("coalesces in-flight refresh requests and discards stale results", async () => {
    vi.useFakeTimers(); const h = fixture(); const pending = deferred<GitStatusResult>(); h.api.gitStatus.mockReturnValueOnce(pending.promise);
    h.service.scheduleStatusRefresh(0); await vi.advanceTimersByTimeAsync(0);
    h.service.scheduleStatusRefresh(0); await vi.advanceTimersByTimeAsync(0);
    h.service.scheduleStatusRefresh(0); await vi.advanceTimersByTimeAsync(0);
    expect(h.api.gitStatus).toHaveBeenCalledTimes(1);
    h.change({ root: "/two" }); pending.resolve(status("stale")); await Promise.resolve();
    expect(h.publishStatus).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(150);
    expect(h.api.gitStatus).toHaveBeenCalledTimes(2); expect(h.api.gitStatus).toHaveBeenLastCalledWith("/two");
    expect(h.service.getSnapshot().status?.branch).toBe("main");
  });
  it("clears timers and rejects late completion after generation retirement", async () => {
    vi.useFakeTimers(); const h = fixture(); const pending = deferred<GitStatusResult>(); h.api.checkoutGitBranch.mockReturnValue(pending.promise);
    const result = h.service.checkoutBranch("feature"); h.service.scheduleStatusRefresh(10); h.retire(); pending.resolve(status());
    await expect(result).rejects.toThrow("disposed"); await vi.runAllTimersAsync();
    expect(h.api.gitStatus).not.toHaveBeenCalled(); expect(h.publishStatus).not.toHaveBeenCalled();
    await expect(h.service.commit({ message: "no", includeUnstaged: false })).rejects.toThrow("disposed");
    expect(h.api.commitGitChanges).not.toHaveBeenCalled();
  });
  it("rejects abort after admission without publishing or replaying the effect", async () => {
    const h = fixture(); const controller = new AbortController(); const pending = deferred<GitStatusResult>(); h.api.checkoutGitBranch.mockReturnValue(pending.promise);
    const result = h.service.checkoutBranch("feature", { signal: controller.signal }); controller.abort(); pending.resolve(status());
    await expect(result).rejects.toThrow(); expect(h.api.checkoutGitBranch).toHaveBeenCalledTimes(1); expect(h.publishStatus).not.toHaveBeenCalled();
  });
  it("caches detached immutable snapshots and refuses missing working directories", async () => {
    const h = fixture(); const value = status(); value.branches = ["main"]; h.change({ status: value });
    const snapshot = h.service.getSnapshot(); expect(h.service.getSnapshot()).toBe(snapshot);
    value.branches.push("new"); expect(snapshot.status?.branches).toEqual(["main"]); expect(Object.isFrozen(snapshot.status?.branches)).toBe(true);
    h.change({ root: undefined }); await expect(h.service.commit({ message: "no", includeUnstaged: true })).rejects.toThrow("unavailable");
    expect(h.api.commitGitChanges).not.toHaveBeenCalled();
  });
  it("reads immutable review data only for the explicitly selected source", async () => {
    const h = fixture(); const target = { contextKey: "project:one", root: "/one" };
    const changes = await h.service.listChanges(target);
    expect(h.api.listGitChanges).toHaveBeenCalledWith("/one");
    expect(Object.isFrozen(changes.files)).toBe(true);
    await h.service.readDiff({ ...target, path: "a.ts" });
    expect(h.api.readGitFileDiff).toHaveBeenCalledWith("a.ts", "/one");
    await expect(h.service.listChanges({ ...target, contextKey: "project:old" })).rejects.toThrow("workspace changed");
    expect(h.api.listGitChanges).toHaveBeenCalledTimes(1);
  });
  it.each(["resolve", "reject"] as const)("fences late diff %s after source changes", async outcome => {
    const h = fixture(); let resolve!: (value: unknown) => void; let reject!: (reason: unknown) => void;
    h.api.readGitFileDiff.mockReturnValue(new Promise((yes, no) => { resolve = yes; reject = no; }));
    const result = h.service.readDiff({ contextKey: "project:one", root: "/one", path: "a.ts" });
    h.change({ contextKey: "project:two" });
    if (outcome === "resolve") resolve({ is_repo: false }); else reject(new Error("old secret error"));
    await expect(result).rejects.toThrow("workspace changed");
    expect(h.reportError).not.toHaveBeenCalled();
  });

});
