import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { WuuDesktopApi, EngineAuthResult } from "../shared/protocol";
import { EngineAuthentication } from "./EngineAuthentication";

let container: HTMLDivElement;
let root: Root | undefined;
const methods: EngineAuthResult = { authenticated: false, methods: [{ id: "browser", name: "Browser" }] };
const discover = vi.fn<WuuDesktopApi["listEngineAuthMethods"]>();
const authenticate = vi.fn<WuuDesktopApi["authenticateEngine"]>();
const cancel = vi.fn<WuuDesktopApi["cancelEngineAuth"]>();

beforeEach(() => {
  discover.mockReset().mockResolvedValue(methods);
  authenticate.mockReset().mockResolvedValue({ methods: [], authenticated: true });
  cancel.mockReset().mockResolvedValue({ ok: true });
  vi.stubGlobal("wuu", { listEngineAuthMethods: discover, authenticateEngine: authenticate, cancelEngineAuth: cancel });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" onReuseLocal={vi.fn()} />));
});

afterEach(() => {
  act(() => root?.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

async function click(id: string): Promise<void> {
  const button = container.querySelector<HTMLButtonElement>(`[data-testid="${id}"]`);
  expect(button).not.toBeNull();
  await act(async () => button!.click());
}

it("separates opening settings, method discovery, and explicit login", async () => {
  const onAuthenticated = vi.fn();
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" onReuseLocal={vi.fn()} onAuthenticated={onAuthenticated} />));
  expect(discover).not.toHaveBeenCalled();
  expect(authenticate).not.toHaveBeenCalled();
  await click("engine-auth-discover");
  expect(discover).toHaveBeenCalledExactlyOnceWith("devin");
  expect(authenticate).not.toHaveBeenCalled();
  expect(onAuthenticated).not.toHaveBeenCalled();
  await click("engine-auth-method");
  expect(authenticate).toHaveBeenCalledExactlyOnceWith("devin", "browser");
  expect(onAuthenticated).toHaveBeenCalledOnce();
  expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  expect(cancel).not.toHaveBeenCalled();
});

it("does not treat an empty discovery as authentication or invoke terminal and environment methods", async () => {
  const onAuthenticated = vi.fn();
  discover.mockResolvedValueOnce({ authenticated: false, methods: [
    { id: "setup", name: "Terminal setup", type: "terminal" },
    { id: "key", name: "Environment key", type: "env_var" },
  ] }).mockResolvedValueOnce({ authenticated: false, methods: [] });
  act(() => root!.render(<EngineAuthentication engineID="future-agent" protocol="acp" onReuseLocal={vi.fn()} onAuthenticated={onAuthenticated} />));
  await click("engine-auth-discover");
  expect(container.querySelector('[data-testid="engine-auth-method"]')).toBeNull();
  await click("engine-auth-discover");
  expect(container.querySelector('[data-testid="engine-auth-method"]')).toBeNull();
  expect(onAuthenticated).not.toHaveBeenCalled();
  expect(authenticate).not.toHaveBeenCalled();
});

it.each(["codex", "claude", "opencode", "dsh", "hermes", "pi"])("reuses %s local configuration without invoking unsupported authentication or copying credentials", async (engineID) => {
  const onReuseLocal = vi.fn();
  const onAuthenticated = vi.fn();
  act(() => root!.render(<EngineAuthentication engineID={engineID} protocol={["dsh", "hermes", "pi"].includes(engineID) ? "acp" : engineID} onReuseLocal={onReuseLocal} onAuthenticated={onAuthenticated} />));
  expect(container.querySelector('[data-testid="engine-auth-discover"]')).toBeNull();
  expect(container.querySelector('[role="status"]')?.textContent).toBeTruthy();
  await click("engine-auth-local");
  expect(onReuseLocal).toHaveBeenCalledOnce();
  expect(discover).not.toHaveBeenCalled();
  expect(authenticate).not.toHaveBeenCalled();
  expect(onAuthenticated).not.toHaveBeenCalled();
});

it("retains the local reuse action when ACP has no invocable login method", async () => {
  const onReuseLocal = vi.fn();
  discover.mockResolvedValue({ methods: [], authenticated: false });
  act(() => root!.render(<EngineAuthentication engineID="future-agent" protocol="acp" onReuseLocal={onReuseLocal} />));
  await click("engine-auth-discover");
  await click("engine-auth-local");
  expect(onReuseLocal).toHaveBeenCalledOnce();
  expect(authenticate).not.toHaveBeenCalled();
});

it("keeps cancellation responsive while login is pending and permits retry after failure", async () => {
  let rejectLogin!: (reason: Error) => void;
  authenticate.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectLogin = reject; }));
  await click("engine-auth-discover");
  await click("engine-auth-method");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-local"]')!.disabled).toBe(true);
  await act(async () => rejectLogin(new Error("native login failed")));
  expect(container.querySelector('[role="status"]')!.textContent).toContain("native login failed");
  await click("engine-auth-discover");
  expect(discover).toHaveBeenCalledTimes(2);
});

it("ignores a cancelled login success and permits a new attempt without waiting for its stale response", async () => {
  let resolveLogin!: (result: EngineAuthResult) => void;
  const onAuthenticated = vi.fn();
  authenticate.mockImplementationOnce(() => new Promise(resolve => { resolveLogin = resolve; }));
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" onReuseLocal={vi.fn()} onAuthenticated={onAuthenticated} />));
  await click("engine-auth-discover");
  await click("engine-auth-method");
  await click("engine-auth-cancel");
  expect(cancel).toHaveBeenCalledExactlyOnceWith("devin");
  expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  await click("engine-auth-discover");
  expect(discover).toHaveBeenCalledTimes(2);
  await act(async () => resolveLogin({ methods: [], authenticated: true }));
  expect(onAuthenticated).not.toHaveBeenCalled();
});

it("keeps a failed cancellation locked until its original request settles", async () => {
  let resolveLogin!: (result: EngineAuthResult) => void;
  const onAuthenticated = vi.fn();
  authenticate.mockImplementationOnce(() => new Promise(resolve => { resolveLogin = resolve; }));
  cancel.mockRejectedValueOnce(new Error("cancel failed"));
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" onReuseLocal={vi.fn()} onAuthenticated={onAuthenticated} />));
  await click("engine-auth-discover");
  await click("engine-auth-method");
  await click("engine-auth-cancel");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.disabled).toBe(true);
  await act(async () => resolveLogin({ methods: [], authenticated: true }));
  expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  expect(onAuthenticated).not.toHaveBeenCalled();
});

it("cancels an admitted discovery when the panel closes and ignores its late result", async () => {
  let resolveDiscovery!: (result: EngineAuthResult) => void;
  discover.mockImplementationOnce(() => new Promise((resolve) => { resolveDiscovery = resolve; }));
  await click("engine-auth-discover");
  act(() => root!.unmount());
  root = undefined;
  expect(cancel).toHaveBeenCalledExactlyOnceWith("devin");
  await act(async () => resolveDiscovery(methods));
  expect(authenticate).not.toHaveBeenCalled();
});

it("isolates a new engine from the old engine's pending discovery and late result", async () => {
  let resolveDiscovery!: (result: EngineAuthResult) => void;
  discover.mockImplementationOnce(() => new Promise((resolve) => { resolveDiscovery = resolve; }));
  await click("engine-auth-discover");
  act(() => root!.render(<EngineAuthentication engineID="cursor" protocol="acp" onReuseLocal={vi.fn()} />));
  expect(cancel).toHaveBeenCalledExactlyOnceWith("devin");
  await click("engine-auth-discover");
  expect(discover).toHaveBeenLastCalledWith("cursor");
  await act(async () => resolveDiscovery({ methods: [{ id: "old", name: "Old method" }], authenticated: false }));
  await click("engine-auth-method");
  expect(authenticate).toHaveBeenCalledExactlyOnceWith("cursor", "browser");
});

it("waits for a local refresh and reports failure without starting login or exposing diagnostics", async () => {
  let rejectRefresh!: (reason: Error) => void;
  const onReuseLocal = vi.fn(() => new Promise<void>((_resolve, reject) => { rejectRefresh = reject; }));
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" compact onReuseLocal={onReuseLocal} />));
  await click("engine-auth-local");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.disabled).toBe(true);
  expect(container.querySelector('[data-testid="engine-auth-cancel"]')).toBeNull();
  await act(async () => rejectRefresh(new Error("synthetic-secret /private/token")));
  expect(container.querySelector('[role="status"]')?.textContent).toBeTruthy();
  expect(container.textContent).not.toContain("synthetic-secret");
  expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  expect(authenticate).not.toHaveBeenCalled();
  expect(cancel).not.toHaveBeenCalled();
});

it("does not cancel a completed engine login while its account refresh is pending", async () => {
  let finishRefresh!: () => void;
  const onAuthenticated = vi.fn(() => new Promise<void>(resolve => { finishRefresh = resolve; }));
  act(() => root!.render(<EngineAuthentication engineID="devin" protocol="acp" onReuseLocal={vi.fn()} onAuthenticated={onAuthenticated} />));
  await click("engine-auth-discover");
  await click("engine-auth-method");
  expect(onAuthenticated).toHaveBeenCalledOnce();
  expect(container.querySelector('[data-testid="engine-auth-cancel"]')).toBeNull();
  act(() => root!.unmount());
  root = undefined;
  expect(cancel).not.toHaveBeenCalled();
  await act(async () => finishRefresh());
});

it("opening the credential entry never discovers methods or starts login", async () => {
  const local = vi.fn();
  act(() => root!.render(<EngineAuthentication entry engineID="devin" protocol="acp" onReuseLocal={local} />));
  expect(container.querySelector('[data-testid="engine-auth-discover"]')).toBeNull();
  await click("engine-auth-entry");
  expect(discover).not.toHaveBeenCalled();
  expect(authenticate).not.toHaveBeenCalled();
  expect(local).not.toHaveBeenCalled();
  await click("engine-auth-local");
  expect(local).toHaveBeenCalledOnce();
  expect(authenticate).not.toHaveBeenCalled();
});
