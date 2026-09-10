// @vitest-environment jsdom
import { act, createElement, useContext, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { b64encode, buildPairURI } from "@wuu/remote-core";
import { WorkbenchConnectionContext } from "../../../desktop/src/renderer/WorkbenchConnectionContext";

const remote = vi.hoisted(() => ({ pair: vi.fn(), construct: vi.fn(), connect: vi.fn(), disconnect: vi.fn(), install: vi.fn(), wake: vi.fn() }));
const credentials = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn(), clear: vi.fn() }));
vi.mock("../src/lib/credStore", () => ({ webCredStore: credentials }));
vi.mock("../src/lib/desktopBridge", () => ({
  RemoteDesktopBridge: class {
    constructor(saved: unknown) { remote.construct(saved); }
    static pair = remote.pair;
    connect = remote.connect;
    disconnect = remote.disconnect;
    install = remote.install;
    wake = remote.wake;
    subscribeConnection = (listener: () => void) => {
      connectionListeners.add(listener);
      return () => connectionListeners.delete(listener);
    };
    getConnectionSnapshot = () => connection;
  },
}));
vi.mock("../src/WebWorkspace", () => ({ default: () => {
  const connected = useContext(WorkbenchConnectionContext);
  const [draft, setDraft] = useState("");
  return createElement("div", null, "Connected workbench",
    createElement("textarea", { value: draft, onChange: (event: React.ChangeEvent<HTMLTextAreaElement>) => setDraft(event.target.value) }),
    createElement("button", { disabled: !connected }, "Send"));
} }));
let connection = { phase: "connected", revision: 1 };
const connectionListeners = new Set<() => void>();
import App from "../src/App";
let container: HTMLDivElement;
let root: Root;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function click(text: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
  expect(button, `button ${text}`).toBeTruthy();
  await act(async () => button!.click());
}
async function startPair() {
  const input = container.querySelector("textarea")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, "wuu://pair?test");
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click("连接电脑");
}
beforeEach(async () => {
  vi.resetAllMocks();
  window.history.replaceState(null, "", "/");
  connection = { phase: "connected", revision: 1 };
  connectionListeners.clear();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  credentials.load.mockResolvedValue(null);
  credentials.clear.mockResolvedValue(undefined);
  credentials.save.mockResolvedValue(undefined);
  remote.disconnect.mockResolvedValue(undefined);
  remote.connect.mockResolvedValue(undefined);
  container = document.createElement("div"); document.body.append(container);
  root = createRoot(container);
  await act(async () => root.render(createElement(App)));
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.restoreAllMocks(); });

describe("Web connection ownership", () => {
  it("checks transport on foreground and network recovery, then removes lifecycle listeners on unmount", async () => {
    remote.pair.mockResolvedValue({ host_pub: "computer" });
    await startPair();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("online"));
    expect(remote.wake).not.toHaveBeenCalled();
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("pageshow"));
    window.dispatchEvent(new Event("online"));
    window.dispatchEvent(new Event("focus"));
    expect(remote.wake).toHaveBeenCalledTimes(4);
    await act(async () => root.unmount());
    remote.wake.mockClear();
    document.dispatchEvent(new Event("visibilitychange"));
    window.dispatchEvent(new Event("pageshow"));
    window.dispatchEvent(new Event("online"));
    window.dispatchEvent(new Event("focus"));
    expect(remote.wake).not.toHaveBeenCalled();
  });

  it("keeps the focused draft editable through disconnect, restore and failure without enabling sends", async () => {
    remote.pair.mockResolvedValue({ host_pub: "computer" });
    await startPair();
    const input = container.querySelector("textarea")!;
    input.focus();
    for (const phase of ["reconnecting", "restoring", "error", "connected"]) {
      await act(async () => {
        connection = { phase, revision: connection.revision + 1 };
        connectionListeners.forEach((listener) => listener());
      });
      expect(container.querySelector("textarea")).toBe(input);
      expect(document.activeElement).toBe(input);
      expect(input.closest("[inert]")).toBeNull();
      expect(input.disabled).toBe(false);
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(input, `draft during ${phase}`);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      expect(input.value).toBe(`draft during ${phase}`);
      expect([...container.querySelectorAll("button")].find((button) => button.textContent === "Send")!.disabled)
        .toBe(phase !== "connected");
    }
  });

  it("does not save or connect a pairing response after the user cancels it", async () => {
    const pending = deferred<unknown>(); remote.pair.mockReturnValue(pending.promise);
    await startPair(); await click("清除旧配对");
    await act(async () => pending.resolve({ host_pub: "cancelled-computer" }));
    expect(credentials.save).not.toHaveBeenCalled();
    expect(remote.connect).not.toHaveBeenCalled();
    expect(container.textContent).toContain("连接电脑");
  });

  it("ignores a late failure from a cancelled pairing while another pairing runs", async () => {
    const old = deferred<unknown>(), next = deferred<unknown>();
    remote.pair.mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    await startPair(); await click("清除旧配对"); await startPair();
    await act(async () => old.reject(new Error("old pairing failed")));
    expect(container.textContent).toContain("正在连接电脑");
    expect(container.textContent).not.toContain("old pairing failed");
  });

  it("returns to pairing immediately while an old connection is still closing", async () => {
    const closing = deferred<void>(), connecting = deferred<void>();
    remote.pair.mockResolvedValue({ host_pub: "computer" });
    remote.connect.mockReturnValue(connecting.promise);
    remote.disconnect.mockReturnValue(closing.promise);
    await startPair(); await click("清除旧配对");
    expect(container.textContent).toContain("连接电脑");
    await act(async () => connecting.resolve());
    expect(remote.install).not.toHaveBeenCalled();
    await act(async () => closing.resolve());
  });

  it("disconnects and invalidates an in-flight connection when the shell unmounts", async () => {
    const connecting = deferred<void>();
    remote.pair.mockResolvedValue({ host_pub: "computer" });
    remote.connect.mockReturnValue(connecting.promise);
    await startPair();
    await act(async () => root.unmount());
    expect(remote.disconnect).toHaveBeenCalled();
    await act(async () => connecting.resolve());
    expect(remote.install).not.toHaveBeenCalled();
  });
});

it("pairs from a scanned Web link without pasting and removes the offer from history", async () => {
  await act(async () => root.unmount());
  const uri = buildPairURI("wss://relay.example/v1/connect", "single-use", new Uint8Array(32).fill(2), new Uint8Array(32).fill(3));
  window.history.replaceState(null, "", `/#${new URLSearchParams({ pair: uri })}`);
  const paired = { host_pub: b64encode(new Uint8Array(32).fill(3)) };
  remote.pair.mockResolvedValue(paired);
  credentials.load.mockResolvedValue({ host_pub: b64encode(new Uint8Array(32).fill(4)) });
  root = createRoot(container);
  await act(async () => root.render(createElement(App)));
  expect(remote.pair).toHaveBeenCalledWith(uri, expect.any(String));
  expect(credentials.save).toHaveBeenCalledWith(paired);
  expect(remote.construct).toHaveBeenCalledWith(paired);
  expect(remote.connect).toHaveBeenCalled();
  expect(window.location.hash).toBe("");
  expect(container.textContent).toContain("Connected workbench");
});

describe("saved identity with a single-use invitation", () => {
  const host = new Uint8Array(32).fill(1);
  const saved = { v: 1, host_pub: b64encode(host), device_seed: b64encode(new Uint8Array(32).fill(5)), relay_url: "wss://relay.example/v1/connect" };
  // A copied invitation can outlive both its pairing window and its LAN address.
  const uri = buildPairURI("ws://192.168.8.154:8787/v1/connect", "used-offer", new Uint8Array(32).fill(2), host);
  async function openInvitation() {
    await act(async () => root.unmount());
    window.history.replaceState(null, "", `/#${new URLSearchParams({ pair: uri })}`);
    root = createRoot(container);
    await act(async () => root.render(createElement(App)));
  }

  it("reuses the saved identity and relay every time an old invitation is opened", async () => {
    credentials.load.mockResolvedValue(saved);
    remote.pair.mockRejectedValue(new Error("pairing rejected: no_such_pairing"));
    await openInvitation();
    await openInvitation();
    expect(remote.construct).toHaveBeenCalledTimes(2);
    expect(remote.construct).toHaveBeenNthCalledWith(1, saved);
    expect(remote.construct).toHaveBeenNthCalledWith(2, saved);
    expect(remote.install).toHaveBeenCalledTimes(2);
    expect(remote.pair).not.toHaveBeenCalled();
    expect(credentials.save).not.toHaveBeenCalled();
    expect(credentials.clear).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("");
  });

  it("keeps the identity on connection failure and retries without pairing", async () => {
    credentials.load.mockResolvedValue(saved);
    remote.connect.mockRejectedValueOnce(new Error("offline"));
    await openInvitation();
    expect(remote.install).not.toHaveBeenCalled();
    await click("重试连接");
    expect(remote.install).toHaveBeenCalledTimes(1);
    expect(remote.construct).toHaveBeenLastCalledWith(saved);
    expect(remote.pair).not.toHaveBeenCalled();
    expect(credentials.clear).not.toHaveBeenCalled();
  });

  it("still exchanges the invitation on a browser without saved credentials", async () => {
    remote.pair.mockResolvedValue(saved);
    await openInvitation();
    expect(remote.pair).toHaveBeenCalledWith(uri, expect.any(String));
    expect(credentials.save).toHaveBeenCalledWith(saved);
    expect(remote.install).toHaveBeenCalledTimes(1);
  });

  it("does not connect or pair after cancelling a pending credential lookup", async () => {
    const pending = deferred<unknown>();
    credentials.load.mockReturnValue(pending.promise);
    await openInvitation();
    await click("清除旧配对");
    await act(async () => pending.resolve(saved));
    expect(remote.construct).not.toHaveBeenCalled();
    expect(remote.pair).not.toHaveBeenCalled();
  });
});

it("ignores a scanned pairing response after cancellation", async () => {
  await act(async () => root.unmount());
  window.history.replaceState(null, "", `/#${new URLSearchParams({ pair: "wuu://pair?test=cancelled" })}`);
  const pending = deferred<unknown>();
  remote.pair.mockReturnValue(pending.promise);
  root = createRoot(container);
  await act(async () => root.render(createElement(App)));
  const cancel = container.querySelector<HTMLButtonElement>("button")!;
  await act(async () => cancel.click());
  await act(async () => pending.resolve({ host_pub: "cancelled-host" }));
  expect(credentials.save).not.toHaveBeenCalled();
  expect(remote.connect).not.toHaveBeenCalled();
});

it("shows recovery instead of an empty pairing form when a scanned code is unavailable", async () => {
  await act(async () => root.unmount());
  window.history.replaceState(null, "", `/#${new URLSearchParams({ pair: "wuu://pair?test=expired" })}`);
  remote.pair.mockRejectedValue(new Error("pairing rejected: no_such_pairing"));
  root = createRoot(container);
  await act(async () => root.render(createElement(App)));
  expect(container.querySelector("textarea")).toBeNull();
  expect(container.querySelector("button")).not.toBeNull();
  expect(container.textContent).not.toContain("no_such_pairing");
  await act(async () => container.querySelector<HTMLButtonElement>("button")!.click());
  expect(container.querySelector("textarea")).not.toBeNull();
});
