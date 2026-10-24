import { afterEach, describe, expect, it, vi } from "vitest";
import type { CodexPetCommand } from "../../shared/protocol";
import type { ChromeOverlayEvent, ChromeOverlayRequest } from "../../shared/chromeOverlay";
import { ApplicationDesktop } from "./ApplicationDesktop";

const original = window.wuu;
afterEach(() => { window.wuu = original; });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
function setup(api: object = {}) {
  window.wuu = { hostKind: "desktop", platform: "darwin", ...api } as typeof window.wuu;
  let ready = false;
  let title = "First";
  const submitPetText = vi.fn(async () => true);
  const jumpToThread = vi.fn(async () => {});
  const desktop = new ApplicationDesktop({ getPetState: () => ({ ready, feed: { hints: [{ thread_id: "one", title, status: "running", preview: "", attention: false, updated_at: 1 }] } }), submitPetText, jumpToThread });
  return { desktop, submitPetText, jumpToThread, ready: (value: boolean) => { ready = value; }, title: (value: string) => { title = value; } };
}
const flush = async () => { for (let index = 0; index < 12; index++) await Promise.resolve(); };

describe("ApplicationDesktop generation and host lifecycle", () => {
  it("owns standalone window controls without attaching or clearing the main-window pet feed", async () => {
    const feed = vi.fn(async () => {}); const pet = vi.fn(); const close = vi.fn(async () => {});
    window.wuu = { hostKind: "desktop", updateCodexPetFeed: feed, onCodexPetCommand: pet, windowClose: close } as unknown as typeof window.wuu;
    const desktop = new ApplicationDesktop();
    const scope = desktop.createScope(new AbortController().signal);
    await scope.close!(); desktop.syncPet(); desktop.dispose(); await flush();
    expect(close).toHaveBeenCalledOnce(); expect(feed).not.toHaveBeenCalled(); expect(pet).not.toHaveBeenCalled();
  });
  it("preserves absent and explicitly unsupported capabilities without invoking them", async () => {
    const notification = vi.fn();
    const { desktop } = setup({ showSystemNotification: notification, unsupportedMethods: ["showSystemNotification"], hostKind: "web" });
    const controls = desktop.createScope(new AbortController().signal);
    expect(controls.hostKind).toBe("web");
    expect(controls.showNotification).toBeUndefined();
    expect(controls.account).toBeUndefined();
    expect(controls.popOut.kind).toBeNull();
    expect(Object.isFrozen(controls.popOut)).toBe(true);
    expect(notification).not.toHaveBeenCalled();
    desktop.dispose();
  });
  it("unsubscribes and rejects late account results and stale window commands", async () => {
    const result = deferred<{ username: string }>();
    const off = vi.fn(); let resize!: (value: { resizing: boolean }) => void;
    const close = vi.fn(async () => {});
    const { desktop } = setup({ remoteAccount: () => result.promise, windowClose: close, onWindowResizeState: (listener: typeof resize) => { resize = listener; return off; } });
    const life = new AbortController(); const controls = desktop.createScope(life.signal); const listener = vi.fn();
    controls.onResizeState!(listener); resize({ resizing: true });
    const account = controls.account!("status"); const rejected = expect(account).rejects.toThrow();
    life.abort(); resize({ resizing: false }); result.resolve({ username: "old" });
    await rejected;
    expect(listener).toHaveBeenCalledTimes(1); expect(off).toHaveBeenCalledTimes(1);
    await expect(controls.close!()).rejects.toThrow(); expect(close).not.toHaveBeenCalled();
    desktop.dispose();
  });
  it("serializes native caption retirement before a successor can reveal its controls", async () => {
    const first = deferred<void>();
    const setControls = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    const { desktop } = setup({ windowSetControls: setControls });
    const a = new AbortController(); const old = desktop.createScope(a.signal);
    const pending = old.setControls!({ visible: true, x: 1, y: 2 }); const rejection = expect(pending).rejects.toThrow();
    await flush(); a.abort();
    const next = desktop.createScope(new AbortController().signal);
    const shown = next.setControls!({ visible: true, x: 30, y: 40 });
    first.resolve(); await rejection; await shown;
    expect(setControls.mock.calls.map(([value]) => value)).toEqual([{ visible: true, x: 1, y: 2 }, { visible: false, x: 0, y: 0 }, { visible: true, x: 30, y: 40 }]);
    desktop.dispose(); await flush();
  });
  it("isolates overlay tokens and hides a retired pending presentation", async () => {
    const shown = deferred<void>(); const hide = vi.fn(async () => {}); let event!: (value: ChromeOverlayEvent) => void;
    const show = vi.fn((_request: ChromeOverlayRequest) => shown.promise); const off = vi.fn();
    const { desktop } = setup({ chromeOverlay: { show, hide, onEvent: (listener: typeof event) => { event = listener; return off; } } });
    const life = new AbortController(); const api = desktop.createScope(life.signal).chromeOverlay!; const received = vi.fn(); api.onEvent(received);
    const request = { token: "ribbon", generation: 1 } as ChromeOverlayRequest;
    const pending = api.show(request); const rejection = expect(pending).rejects.toThrow(); await flush();
    const token = show.mock.calls[0][0].token;
    event({ token, generation: 1, command: { type: "new-tab" } });
    expect(received).toHaveBeenCalledWith({ token: "ribbon", generation: 1, command: { type: "new-tab" } });
    life.abort(); event({ token, generation: 1, command: { type: "new-tab" } }); shown.resolve(); await rejection; await flush();
    expect(hide).toHaveBeenCalledWith(token, 1); expect(received).toHaveBeenCalledTimes(1); expect(off).toHaveBeenCalledTimes(1);
    desktop.dispose();
  });
  it("attaches pet commands only when ready and captures submit targets before later state changes", async () => {
    let command!: (value: CodexPetCommand) => void; const off = vi.fn(); const resolve = vi.fn();
    const subscribe = vi.fn((listener: typeof command) => { command = listener; return off; });
    const { desktop, ready, submitPetText } = setup({ onCodexPetCommand: subscribe, resolveCodexPetCommand: resolve });
    desktop.syncPet(); expect(subscribe).not.toHaveBeenCalled(); ready(true); desktop.syncPet(); desktop.syncPet();
    const target = { workspace: { kind: "no_project" as const, cwd: "/original" } };
    command({ kind: "submit", id: "id", text: "hello", target }); target.workspace.cwd = "/changed";
    await flush(); expect(submitPetText).toHaveBeenCalledWith("hello", { workspace: { kind: "no_project", cwd: "/original" } });
    expect(resolve).toHaveBeenCalledWith("id", { ok: true }); expect(subscribe).toHaveBeenCalledTimes(1);
    desktop.dispose(); expect(off).toHaveBeenCalledTimes(1);
  });
  it("settles pending submits once on retirement and ignores late success", async () => {
    let command!: (value: CodexPetCommand) => void; const resolve = vi.fn(); const pending = deferred<boolean>();
    const { desktop, ready, submitPetText } = setup({ onCodexPetCommand: (listener: typeof command) => { command = listener; return vi.fn(); }, resolveCodexPetCommand: resolve });
    submitPetText.mockReturnValue(pending.promise); ready(true); desktop.syncPet();
    command({ kind: "submit", id: "id", text: "hello", target: { thread_id: "original" } }); await flush();
    desktop.dispose(); pending.resolve(true); await flush();
    expect(resolve.mock.calls).toEqual([["id", { ok: false }]]);
  });
  it("coalesces feed changes behind pending transport without reordering", async () => {
    const first = deferred<void>(); const update = vi.fn().mockReturnValueOnce(first.promise).mockResolvedValue(undefined);
    const { desktop, ready, title } = setup({ updateCodexPetFeed: update });
    ready(true); desktop.syncPet(); await flush(); title("Second"); desktop.syncPet(); title("Latest"); desktop.syncPet();
    expect(update).toHaveBeenCalledTimes(1); first.resolve(); await flush();
    expect(update.mock.calls.map(([feed]) => feed.hints[0]?.title)).toEqual(["First", "Latest"]);
    desktop.dispose(); await flush(); expect(update.mock.calls.at(-1)?.[0]).toEqual({ hints: [] });
  });
});
