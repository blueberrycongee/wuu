// The irreversible action must keep its confirmed scope despite filters,
// navigation, concurrent restores, partial failure, and repeated clicks.
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread } from "../shared/protocol";
import { useArchiveDeletion } from "./useArchiveDeletion";
import { confirmAction } from "./ConfirmDialog";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";

vi.mock("./ConfirmDialog", () => ({ confirmAction: vi.fn() }));
const archived = (id: string): Thread => ({ id, archived: true } as Thread);
let root: Root | undefined;
let container: HTMLDivElement | undefined;
afterEach(() => { act(() => root?.unmount()); container?.remove(); vi.resetAllMocks(); });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
function mount(deleteThread = vi.fn().mockResolvedValue(undefined)) {
  let controller!: ReturnType<typeof useArchiveDeletion>;
  const refresh = vi.fn();
  const source = { remove: deleteThread, list: async () => {
    const { threads } = await window.wuu.listArchivedThreads(); refresh(threads); return threads;
  } };
  function Harness() { controller = useArchiveDeletion(source); return null; }
  container = document.createElement("div"); document.body.append(container);
  root = createRoot(container);
  act(() => root!.render(createElement(Harness)));
  return { get current() { return controller; }, deleteThread, refresh };
}
function list(...responses: Array<Thread[] | Error>) {
  const api = vi.fn();
  for (const response of responses) {
    if (response instanceof Error) api.mockRejectedValueOnce(response);
    else api.mockResolvedValueOnce({ threads: response });
  }
  Object.defineProperty(window, "wuu", { configurable: true, value: { listArchivedThreads: api } });
  return api;
}

describe("archive deletion scope and recovery", () => {
  it.each(["listing", "confirmation", "deleting", "reconciling"] as const)("retires a pending archive %s without continuing it or unlocking the successor", async phase => {
    const oldGate = deferred<unknown>(); const nextGate = deferred<readonly Thread[]>();
    const previous = { list: vi.fn().mockResolvedValue([archived("old-one"), archived("old-two")]), remove: vi.fn().mockResolvedValue(undefined) };
    const next = { list: vi.fn().mockReturnValue(nextGate.promise), remove: vi.fn().mockResolvedValue(undefined) };
    vi.mocked(confirmAction).mockResolvedValue(true);
    if (phase === "listing") previous.list.mockReturnValueOnce(oldGate.promise);
    if (phase === "confirmation") vi.mocked(confirmAction).mockReturnValueOnce(oldGate.promise as Promise<boolean>);
    if (phase === "deleting") previous.remove.mockReturnValueOnce(oldGate.promise);
    if (phase === "reconciling") previous.list.mockResolvedValueOnce([archived("old-one")]).mockReturnValueOnce(oldGate.promise);
    let current!: ReturnType<typeof useArchiveDeletion>;
    function Harness({ source }: { source: Parameters<typeof useArchiveDeletion>[0] }) { current = useArchiveDeletion(source); return null; }
    container = document.createElement("div"); document.body.append(container); root = createRoot(container);
    const render = (source: Parameters<typeof useArchiveDeletion>[0]) => act(async () => root!.render(<Harness source={source} />));
    await render(previous);
    let oldWork!: Promise<void>; let nextWork!: Promise<void>;
    await act(async () => { oldWork = current.removeAll(); });
    await render(next);
    expect(current.pending).toBe(false);
    await act(async () => { nextWork = current.removeAll(); });
    expect(next.list).toHaveBeenCalledTimes(1);
    const removals = previous.remove.mock.calls.length;
    await act(async () => {
      if (phase === "reconciling") oldGate.reject(new Error("retired reconciliation failed"));
      else oldGate.resolve(phase === "listing" ? [archived("old-one")] : phase === "confirmation" ? true : undefined);
      await oldWork;
    });
    expect(previous.remove).toHaveBeenCalledTimes(removals);
    expect(current.pending).toBe(true);
    expect(current.error).toBe("");
    expect(current.result).toBeUndefined();
    await act(async () => { await current.removeAll(); });
    expect(next.list).toHaveBeenCalledTimes(1);
    await act(async () => { nextGate.resolve([]); await nextWork; });
    expect(current.pending).toBe(false);
  });

  it.each([true, false])("does not continue a retired deletion while keeping genuine failure recovery (retired: %s)", async retired => {
    vi.mocked(confirmAction).mockResolvedValue(true);
    list([archived("one"), archived("two")], [archived("one"), archived("two")]);
    const cause = retired ? new PluginLifecycleCancellationError() : new Error("delete failed");
    const remove = vi.fn().mockRejectedValue(cause);
    const h = mount(remove);
    await act(async () => { await h.current.removeAll(); });
    expect(h.current.error).toBe(retired ? "" : cause.message);
    expect(remove).toHaveBeenCalledTimes(retired ? 1 : 2);
    expect(h.current.pending).toBe(false);
  });
  it("uses a fresh complete snapshot and never adds conversations after confirmation", async () => {
    const prompt = deferred<boolean>();
    vi.mocked(confirmAction).mockReturnValue(prompt.promise);
    list([archived("one"), archived("two"), archived("one")], [archived("new")]);
    const h = mount();
    let pending!: Promise<void>;
    await act(async () => { pending = h.current.removeAll(); });
    expect(h.current.pending).toBe(true);
    expect(h.deleteThread).not.toHaveBeenCalled();
    await act(async () => { await h.current.removeAll(); });
    expect(confirmAction).toHaveBeenCalledTimes(1);
    await act(async () => { prompt.resolve(true); await pending; });
    expect(h.deleteThread.mock.calls).toEqual([["one"], ["two"]]);
    expect(h.current.result).toMatchObject({ deleted: 2, failed: 0 });
    expect(h.refresh).toHaveBeenLastCalledWith([archived("new")]);
  });

  it("stops a confirmed batch when its settings module has been unloaded", async () => {
    const prompt = deferred<boolean>();
    vi.mocked(confirmAction).mockReturnValue(prompt.promise);
    list([archived("one")]);
    const h = mount();
    let pending!: Promise<void>;
    await act(async () => { pending = h.current.removeAll(); });
    act(() => root!.unmount());
    root = undefined;
    await act(async () => { prompt.resolve(true); await pending; });
    expect(h.deleteThread).not.toHaveBeenCalled();
  });

  it("does nothing when confirmation is canceled or authoritative listing fails", async () => {
    vi.mocked(confirmAction).mockResolvedValue(false);
    list([archived("one")], new Error("catalog unavailable"));
    const h = mount();
    await act(async () => { await h.current.removeAll(); });
    await act(async () => { await h.current.removeAll(); });
    expect(h.deleteThread).not.toHaveBeenCalled();
    expect(confirmAction).toHaveBeenCalledTimes(1);
    expect(h.current.error).toContain("catalog unavailable");
    expect(h.current.pending).toBe(false);
  });

  it("continues after a failure and retries only remaining confirmed IDs", async () => {
    vi.mocked(confirmAction).mockResolvedValue(true);
    list([archived("one"), archived("two"), archived("three")], [archived("two"), archived("new")],
      [archived("two"), archived("new")], [archived("new")]);
    const remove = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("busy"))
      .mockRejectedValueOnce(new Error("response lost")).mockResolvedValue(undefined);
    const h = mount(remove);
    await act(async () => { await h.current.removeAll(); });
    expect(h.current.result).toMatchObject({ deleted: 1, skipped: 1, failed: 1 });
    expect(h.current.failedIDs).toEqual(["two"]);
    await act(async () => { await h.current.retry(); });
    expect(remove.mock.calls).toEqual([["one"], ["two"], ["three"], ["two"]]);
    expect(h.current.failedIDs).toEqual([]);
  });

  it("holds the lock through pending deletion and preserves failures when refresh fails", async () => {
    vi.mocked(confirmAction).mockResolvedValue(true);
    list([archived("one"), archived("two")], new Error("refresh failed"));
    const gate = deferred<void>();
    const remove = vi.fn().mockReturnValueOnce(gate.promise).mockRejectedValueOnce(new Error("busy"));
    const h = mount(remove);
    let pending!: Promise<void>;
    await act(async () => { pending = h.current.removeAll(); });
    await act(async () => { await h.current.removeAll(); await h.current.retry(); });
    expect(remove).toHaveBeenCalledTimes(1);
    await act(async () => { gate.resolve(); await pending; });
    expect(h.current.result).toMatchObject({ deleted: 1, failed: 1 });
    expect(h.current.failedIDs).toEqual(["two"]);
    expect(h.current.error).toContain("refresh failed");
    expect(h.current.pending).toBe(false);
  });

  it("handles an empty authoritative archive without a destructive prompt", async () => {
    list([]);
    const h = mount();
    await act(async () => { await h.current.removeAll(); });
    expect(confirmAction).not.toHaveBeenCalled();
    expect(h.deleteThread).not.toHaveBeenCalled();
    expect(h.refresh).toHaveBeenCalledWith([]);
    expect(h.current.pending).toBe(false);
  });
});
