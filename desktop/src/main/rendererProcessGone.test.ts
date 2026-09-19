import { EventEmitter } from "node:events";
import type { App, BrowserWindow } from "electron";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installRendererRecovery, sendToWindow } from "./rendererProcessGone";
import { TerminalSessionManager } from "./terminalSessions";

vi.mock("node-pty", () => ({
  spawn: () => ({ kill: vi.fn(), write: vi.fn(), onData: vi.fn(), onExit: vi.fn() }),
}));

const apps: EventEmitter[] = [];
function fixture(id = 1, stopTerminals = vi.fn<(ownerID: number) => void>()) {
  const app = new EventEmitter();
  apps.push(app);
  const frame = { isDestroyed: vi.fn(() => false), detached: false, send: vi.fn() };
  const contents = Object.assign(new EventEmitter(), {
    id,
    mainFrame: frame,
    isDestroyed: vi.fn(() => false),
    isCrashed: vi.fn(() => false),
  });
  const window = Object.assign(new EventEmitter(), {
    webContents: contents,
    isDestroyed: vi.fn(() => false),
    close: vi.fn(() => window.emit("closed")),
  });
  const handle = window as unknown as BrowserWindow;
  const load = vi.fn<() => Promise<void>>().mockResolvedValue(undefined);
  const prompt = vi.fn<() => Promise<"reload" | "close">>(() => new Promise(() => {}));
  installRendererRecovery(handle, { app: app as App, load, prompt, stopTerminals });
  return {
    app, window, contents, frame, load, prompt, stopTerminals,
    gone: (reason = "crashed") => contents.emit("render-process-gone", {}, { reason, exitCode: 1 }),
    loaded: () => { contents.emit("dom-ready"); contents.emit("did-finish-load"); },
    send: () => sendToWindow(handle, "event", { value: 1 }),
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  for (const app of apps.splice(0)) app.emit("before-quit");
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("renderer recovery lifecycle", () => {
  it("delays a second crash inside the cooldown instead of leaving the window dead", async () => {
    const f = fixture();
    f.gone();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.load).toHaveBeenCalledTimes(1);
    f.loaded();
    await vi.advanceTimersByTimeAsync(1_000);
    f.gone("oom");
    await vi.advanceTimersByTimeAsync(3_999);
    expect(f.load).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(f.load).toHaveBeenCalledTimes(2);
    f.loaded();
    f.send();
    expect(f.frame.send).toHaveBeenCalledOnce();
  });

  it("bounds repeated crashes even if each replacement finishes loading, then accepts manual reload", async () => {
    const f = fixture();
    let choose!: (choice: "reload") => void;
    f.prompt.mockImplementation(() => new Promise((resolve) => { choose = resolve; }));
    for (let i = 0; i < 3; i++) {
      f.gone();
      await vi.advanceTimersByTimeAsync(i === 0 ? 0 : 5_000);
      f.loaded();
    }
    f.gone();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(f.load).toHaveBeenCalledTimes(3);
    expect(f.prompt).toHaveBeenCalledOnce();
    choose("reload");
    await vi.advanceTimersByTimeAsync(0);
    expect(f.load).toHaveBeenCalledTimes(4);
    f.loaded();
  });

  it("resets the retry budget only after a stable replacement", async () => {
    const f = fixture();
    for (let i = 0; i < 3; i++) {
      f.gone();
      await vi.advanceTimersByTimeAsync(i === 0 ? 0 : 5_000);
      f.loaded();
    }
    await vi.advanceTimersByTimeAsync(60_000);
    f.gone();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.load).toHaveBeenCalledTimes(4);
    expect(f.prompt).not.toHaveBeenCalled();
  });

  it("keeps failure budgets and IPC availability independent between windows", async () => {
    const first = fixture(1);
    const second = fixture(2);
    first.gone();
    await vi.advanceTimersByTimeAsync(0);
    first.gone();
    second.send();
    second.gone();
    await vi.advanceTimersByTimeAsync(0);
    expect(first.load).toHaveBeenCalledTimes(1);
    expect(second.load).toHaveBeenCalledTimes(1);
    expect(second.frame.send).toHaveBeenCalledOnce();
    expect(first.stopTerminals.mock.calls).toEqual([[1], [1]]);
    expect(second.stopTerminals.mock.calls).toEqual([[2]]);
  });

  it.each(["clean-exit", "killed", "integrity-failure"])("cleans up without resurrecting a %s renderer", async (reason) => {
    const f = fixture();
    f.gone(reason);
    await vi.advanceTimersByTimeAsync(120_000);
    f.send();
    expect(f.stopTerminals).toHaveBeenCalledWith(1);
    expect(f.load).not.toHaveBeenCalled();
    expect(f.prompt).not.toHaveBeenCalled();
    expect(f.frame.send).not.toHaveBeenCalled();
  });

  it.each(["closed", "before-quit", "destroyed"])("cancels pending recovery on %s", async (event) => {
    const f = fixture();
    f.gone();
    const target = event === "closed" ? f.window : event === "destroyed" ? f.contents : f.app;
    target.emit(event);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(f.load).not.toHaveBeenCalled();
    expect(f.prompt).not.toHaveBeenCalled();
    expect(f.app.listenerCount("before-quit")).toBe(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("offers native recovery after rejected loads and closes when requested", async () => {
    const f = fixture();
    f.load.mockRejectedValue(new Error("missing renderer entry"));
    f.prompt.mockResolvedValue("close");
    f.gone("launch-failed");
    await vi.advanceTimersByTimeAsync(120_000);
    expect(f.load).toHaveBeenCalledTimes(3);
    expect(f.prompt).toHaveBeenCalledOnce();
    expect(f.window.close).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("times out hung recovery loads and ignores a late dialog response after shutdown", async () => {
    const f = fixture();
    f.load.mockImplementation(() => new Promise(() => {}));
    let choose!: (choice: "reload") => void;
    f.prompt.mockImplementation(() => new Promise((resolve) => { choose = resolve; }));
    f.gone();
    await vi.advanceTimersByTimeAsync(100_000);
    expect(f.prompt).toHaveBeenCalledOnce();
    f.app.emit("before-quit");
    choose("reload");
    await vi.advanceTimersByTimeAsync(120_000);
    expect(f.load).toHaveBeenCalledTimes(3);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("ignores a stale load rejection after a newer renderer has loaded", async () => {
    const f = fixture();
    let reject!: (error: Error) => void;
    f.load.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
    f.gone();
    await vi.advanceTimersByTimeAsync(0);
    f.gone();
    await vi.advanceTimersByTimeAsync(5_000);
    f.loaded();
    reject(new Error("old navigation aborted"));
    await vi.advanceTimersByTimeAsync(120_000);
    expect(f.load).toHaveBeenCalledTimes(2);
    expect(f.prompt).not.toHaveBeenCalled();
    f.send();
    expect(f.frame.send).toHaveBeenCalledOnce();
  });

  it("invalidates lost terminal IDs before replacement without stopping another owner's terminal", async () => {
    const manager = new TerminalSessionManager(() => {});
    const context = { kind: "no_project" as const, cwd: process.cwd() };
    const lost = manager.startInContext(context, {}, 1);
    const other = manager.startInContext(context, {}, 2);
    const f = fixture(1, vi.fn((id) => manager.stopForOwner(id)));
    let writableAtReload: boolean[] = [];
    f.load.mockImplementation(async () => {
      writableAtReload = [
        manager.write(lost.id, "stale command", 1).ok,
        manager.write(other.id, "still running", 2).ok,
      ];
    });
    f.gone();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.load).toHaveBeenCalledOnce();
    expect(writableAtReload).toEqual([false, true]);
    const replacement = manager.startInContext(context, {}, 1);
    expect(manager.write(replacement.id, "new command", 1).ok).toBe(true);
    manager.cleanup();
  });
});

describe("outbound renderer IPC", () => {
  it("drops messages throughout recovery, including cleanup, then uses the replacement frame", async () => {
    const f = fixture();
    f.stopTerminals.mockImplementation(() => f.send());
    f.gone();
    f.send();
    await vi.advanceTimersByTimeAsync(0);
    f.send();
    expect(f.frame.send).not.toHaveBeenCalled();
    const replacement = { ...f.frame, send: vi.fn() };
    f.contents.mainFrame = replacement;
    f.loaded();
    f.send();
    expect(replacement.send).toHaveBeenCalledWith("event", { value: 1 });
  });

  it("avoids Electron's internally logged native send failure on an unavailable frame", () => {
    const f = fixture();
    f.frame.isDestroyed.mockReturnValue(true);
    f.send();
    f.frame.isDestroyed.mockReturnValue(false);
    f.frame.detached = true;
    f.send();
    f.frame.detached = false;
    f.contents.isCrashed.mockReturnValue(true);
    f.send();
    expect(f.frame.send).not.toHaveBeenCalled();
    f.contents.isCrashed.mockReturnValue(false);
    f.send();
    expect(f.frame.send).toHaveBeenCalledOnce();
  });

  it("does not hide unrelated serialization errors", () => {
    const f = fixture();
    f.frame.send.mockImplementation(() => { throw new Error("could not be cloned"); });
    expect(f.send).toThrow("could not be cloned");
  });
});
