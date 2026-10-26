import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { GrokLogin } from "./GrokLogin";
let root: Root | undefined;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); vi.unstubAllGlobals(); });
async function mount(api: object, onAuthenticated = vi.fn().mockResolvedValue(undefined)) {
  vi.stubGlobal("wuu", api);
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<GrokLogin onAuthenticated={onAuthenticated} />}</SettingsControlsProvider>));
  return onAuthenticated;
}
it("starts only the fixed local login on explicit click and refreshes after success", async () => {
  let resolve!: (value: unknown) => void;
  const authenticate = vi.fn().mockReturnValue(new Promise(done => { resolve = done; }));
  const refreshed = await mount({ authenticateEngine: authenticate, cancelEngineAuth: vi.fn() });
  expect(authenticate).not.toHaveBeenCalled();
  const start = container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!;
  await act(async () => start.click());
  expect(authenticate).toHaveBeenCalledExactlyOnceWith("grok", "local-cli");
  expect(start.disabled).toBe(true);
  expect(container.querySelector('[role="status"]')).not.toBeNull();
  await act(async () => resolve({ methods: [], authenticated: true }));
  expect(refreshed).toHaveBeenCalledOnce();
  expect(start.disabled).toBe(false);
});
it("cancels without treating a late completion as authentication", async () => {
  let resolve!: (value: unknown) => void;
  const authenticate = vi.fn().mockReturnValue(new Promise(done => { resolve = done; }));
  const cancel = vi.fn().mockResolvedValue(undefined);
  const refreshed = await mount({ authenticateEngine: authenticate, cancelEngineAuth: cancel });
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.click());
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-cancel"]')!.click());
  expect(cancel).toHaveBeenCalledExactlyOnceWith("grok");
  await act(async () => resolve({ methods: [], authenticated: true }));
  expect(refreshed).not.toHaveBeenCalled();
});
it("shows a retryable failure without leaking process output", async () => {
  const authenticate = vi.fn().mockRejectedValue(new Error("private token and process output"));
  await mount({ authenticateEngine: authenticate, cancelEngineAuth: vi.fn() });
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.click());
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(container.textContent).not.toContain("private token");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.disabled).toBe(false);
});
it("directs web clients to the desktop without offering an impossible login", async () => {
  const authenticate = vi.fn();
  await mount({ hostKind: "web", authenticateEngine: authenticate });
  expect(container.querySelector('button')).toBeNull();
  expect(container.textContent?.trim()).toBeTruthy();
  expect(authenticate).not.toHaveBeenCalled();
});

it("releases parent busy state when successful refresh removes the login action", async () => {
  vi.stubGlobal("wuu", { authenticateEngine: vi.fn().mockResolvedValue({ methods: [], authenticated: true }), cancelEngineAuth: vi.fn().mockResolvedValue(undefined) });
  let finishRefresh!: () => void;
  const refreshing = new Promise<void>(resolve => { finishRefresh = resolve; });
  function Parent() {
    const [visible, setVisible] = useState(true);
    const [busy, setBusy] = useState(false);
    return <><button data-testid="back" disabled={busy}>Back</button>{visible ? <GrokLogin onBusyChange={setBusy} onAuthenticated={async () => { setVisible(false); await refreshing; }} /> : null}</>;
  }
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<Parent />}</SettingsControlsProvider>));
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.click());
  expect(container.querySelector('[data-testid="grok-login-start"]')).toBeNull();
  expect(container.querySelector<HTMLButtonElement>('[data-testid="back"]')!.disabled).toBe(false);
  await act(async () => finishRefresh());
});

it("does not report completion when refreshing the authenticated connection fails", async () => {
  const refreshed = vi.fn().mockRejectedValue(new Error("refresh unavailable"));
  await mount({ authenticateEngine: vi.fn().mockResolvedValue({ methods: [], authenticated: true }), cancelEngineAuth: vi.fn() }, refreshed);
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.click());
  expect(refreshed).toHaveBeenCalledOnce();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.disabled).toBe(false);
});

it("checks existing local authentication without launching a browser login and keeps failures retryable", async () => {
  const authenticate = vi.fn();
  const reuse = vi.fn().mockRejectedValueOnce(new Error("missing local session")).mockResolvedValue(undefined);
  vi.stubGlobal("wuu", { authenticateEngine: authenticate, cancelEngineAuth: vi.fn() });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<GrokLogin onAuthenticated={vi.fn()} onReuseLocal={reuse} />}</SettingsControlsProvider>));
  const local = container.querySelector<HTMLButtonElement>('[data-testid="grok-login-local"]');
  expect(local).not.toBeNull();
  await act(async () => local!.click());
  expect(reuse).toHaveBeenCalledOnce();
  expect(authenticate).not.toHaveBeenCalled();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(local!.disabled).toBe(false);
  await act(async () => local!.click());
  expect(reuse).toHaveBeenCalledTimes(2);
  expect(container.querySelector('[role="alert"]')).toBeNull();
});

it("rebinds login authority without canceling on callback-only renders or accepting late old results", async () => {
  let finishOld!: (value: { authenticated: boolean; methods: never[] }) => void;
  const oldCancel = vi.fn().mockResolvedValue(undefined);
  const refreshed = await mount({ authenticateEngine: vi.fn(), cancelEngineAuth: oldCancel });
  const first = { ...createApplicationSettingsControls(), authenticateEngine: vi.fn(() => new Promise<{ authenticated: boolean; methods: never[] }>(resolve => { finishOld = resolve; })), cancelEngineAuth: oldCancel };
  const newCancel = vi.fn().mockResolvedValue(undefined);
  const second = { ...createApplicationSettingsControls(), authenticateEngine: vi.fn(() => new Promise<never>(() => {})), cancelEngineAuth: newCancel };
  act(() => { root!.unmount(); root = createRoot(container); });
  const render = (controls: typeof first | typeof second) => root!.render(<SettingsControlsProvider controls={controls}><GrokLogin onAuthenticated={refreshed} onBusyChange={() => {}} /></SettingsControlsProvider>);
  act(() => render(first));
  const trigger = container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!;
  await act(async () => trigger.click());
  act(() => render(first));
  expect(oldCancel).not.toHaveBeenCalled();
  await act(async () => render(second));
  expect(container.querySelector('[data-testid="grok-login-start"]')).toBe(trigger);
  expect(oldCancel).toHaveBeenCalledExactlyOnceWith("grok");
  expect(trigger.disabled).toBe(false);
  await act(async () => trigger.click());
  await act(async () => finishOld({ authenticated: true, methods: [] }));
  expect(refreshed).not.toHaveBeenCalled();
  expect(trigger.disabled).toBe(true);
  expect(newCancel).not.toHaveBeenCalled();
  act(() => { root!.unmount(); root = undefined; });
  expect(newCancel).toHaveBeenCalledExactlyOnceWith("grok");
});

it("contains proven login retirement in the old-props gap without reporting authentication", async () => {
  const authenticate = vi.fn(() => new Promise<never>(() => {}));
  const refreshed = await mount({ authenticateEngine: authenticate, cancelEngineAuth: vi.fn().mockResolvedValue({ ok: true }) });
  const { scopeApplicationSettingsControls } = await import("../../../packages/plugin-sdk/src/ui");
  const { retainPresentationReads } = await import("./plugins/PresentationApplication");
  const { PluginLifecycleCancellationError } = await import("../shared/PluginLifecycle");
  const lifetime = new AbortController(), source = createApplicationSettingsControls();
  const scoped = scopeApplicationSettingsControls(() => source, lifetime.signal, () => lifetime.signal.throwIfAborted());
  const controls = retainPresentationReads({ settingsControls: scoped } as import("../../../packages/plugin-sdk/src/ui").ApplicationServiceV1, lifetime.signal).settingsControls;
  act(() => root!.render(<SettingsControlsProvider controls={controls}><GrokLogin onAuthenticated={refreshed} /></SettingsControlsProvider>));
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-start"]')!.click());
  await act(async () => lifetime.abort(new PluginLifecycleCancellationError()));
  expect(container.querySelector('[role="alert"]')).toBeNull();
  expect(refreshed).not.toHaveBeenCalled();
  expect(authenticate).toHaveBeenCalledOnce();
});
