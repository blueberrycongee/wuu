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
  await act(async () => root!.render(<GrokLogin onAuthenticated={onAuthenticated} />));
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
  await act(async () => root!.render(<Parent />));
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
  await act(async () => root!.render(<GrokLogin onAuthenticated={vi.fn()} onReuseLocal={reuse} />));
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
