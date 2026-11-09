import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ChatGPTLogin } from "./ChatGPTLogin";

let container: HTMLDivElement;
let root: Root | undefined;
const start = vi.fn();
const poll = vi.fn();
const cancel = vi.fn();
const authenticated = vi.fn();
beforeEach(() => {
  vi.useFakeTimers();
  start.mockReset().mockResolvedValue({ login_id: "fixture", interval_ms: 1000, expires_in: 900 });
  poll.mockReset().mockResolvedValue({ status: "pending",  });
  cancel.mockReset().mockResolvedValue({ ok: true });
  authenticated.mockReset();
  vi.stubGlobal("wuu", { startChatGPTLogin: start, pollChatGPTLogin: poll, cancelChatGPTLogin: cancel });
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<ChatGPTLogin onAuthenticated={authenticated} />}</SettingsControlsProvider>));
});
afterEach(() => {
  act(() => root?.unmount());
  container.remove();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
const button = (id: string) => container.querySelector<HTMLButtonElement>(`[data-testid="${id}"]`)!;
it("only launches on explicit click and ignores repeated starts", async () => {
  expect(start).not.toHaveBeenCalled();
  await act(async () => { button("chatgpt-login-start").click(); button("chatgpt-login-start").click(); });
  expect(start).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(authenticated).not.toHaveBeenCalled();
  poll.mockResolvedValue({ status: "success", account_id: "account-a" });
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(authenticated).toHaveBeenCalledWith("account-a");
});
it("cancels on dismissal even when process admission finishes late", async () => {
  let resolve!: (value: unknown) => void;
  start.mockReturnValue(new Promise((done) => { resolve = done; }));
  await act(async () => button("chatgpt-login-start").click());
  act(() => { root!.unmount(); root = undefined; });
  await act(async () => resolve({ login_id: "late", interval_ms: 1000 }));
  expect(cancel).toHaveBeenCalledWith("late");
  expect(poll).not.toHaveBeenCalled();
  expect(authenticated).not.toHaveBeenCalled();
});
it("cancels explicitly and ignores a late success response", async () => {
  let resolve!: (value: unknown) => void;
  poll.mockReturnValue(new Promise((done) => { resolve = done; }));
  await act(async () => button("chatgpt-login-start").click());
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  await act(async () => button("chatgpt-login-cancel").click());
  await act(async () => resolve({ status: "success", account_id: "account-a" }));
  expect(cancel).toHaveBeenCalledWith("fixture");
  expect(authenticated).not.toHaveBeenCalled();
  expect(button("chatgpt-login-start").disabled).toBe(false);
});
it("keeps failed login retryable without reporting authentication", async () => {
  poll.mockResolvedValue({ status: "failed", error: "fixture login failed" });
  await act(async () => button("chatgpt-login-start").click());
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(container.textContent).toContain("fixture login failed");
  expect(authenticated).not.toHaveBeenCalled();
  expect(button("chatgpt-login-start").disabled).toBe(false);
});
it("waits for cancellation acknowledgement before allowing another login", async () => {
  let resolveCancel!: (value: unknown) => void;
  cancel.mockReturnValue(new Promise((done) => { resolveCancel = done; }));
  await act(async () => button("chatgpt-login-start").click());
  await act(async () => button("chatgpt-login-cancel").click());
  expect(button("chatgpt-login-start").disabled).toBe(true);
  await act(async () => resolveCancel({ ok: true }));
  expect(button("chatgpt-login-start").disabled).toBe(false);
});

it("retains a successful-login advisory in the owner when the account control remounts", async () => {
  function AccountOwner(): JSX.Element {
    const [account, setAccount] = useState("pending");
    const [notice, setNotice] = useState("");
    return <><ChatGPTLogin key={account} onAuthenticated={setAccount} onAdvisory={setNotice} /><p role="status">{notice}</p></>;
  }
  act(() => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<AccountOwner />}</SettingsControlsProvider>));
  poll.mockResolvedValue({ status: "success", account_id: "account-a", error: "Account connected; catalog temporarily unavailable" });
  await act(async () => button("chatgpt-login-start").click());
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(container.textContent).toContain("Account connected; catalog temporarily unavailable");
  expect(button("chatgpt-login-start").disabled).toBe(false);
});

it("does not offer host-local login when the browser adapter excludes it", async () => {
  vi.stubGlobal("wuu", { startChatGPTLogin: start, pollChatGPTLogin: poll, cancelChatGPTLogin: cancel,
    unsupportedMethods: ["startChatGPTLogin", "pollChatGPTLogin", "cancelChatGPTLogin"] });
  act(() => root!.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<ChatGPTLogin onAuthenticated={authenticated} />}</SettingsControlsProvider>));
  expect(container.querySelector('[data-testid="chatgpt-login-start"]')).toBeNull();
  expect(container.querySelector('[role="status"]')).not.toBeNull();
  expect(start).not.toHaveBeenCalled();
  expect(poll).not.toHaveBeenCalled();
  expect(cancel).not.toHaveBeenCalled();
});

it.each(["admission", "polling"])("retires only the captured login scope during a %s rebind", async phase => {
  type Login = Awaited<ReturnType<ReturnType<typeof createApplicationSettingsControls>["startChatGPTLogin"]>>;
  let admit!: (value: Login) => void;
  const oldCancel = vi.fn().mockResolvedValue({ ok: true });
  const oldPoll = vi.fn().mockResolvedValue({ status: "pending" });
  const first = { ...createApplicationSettingsControls(), cancelChatGPTLogin: oldCancel, pollChatGPTLogin: oldPoll,
    startChatGPTLogin: vi.fn(() => phase === "admission" ? new Promise<Login>(resolve => { admit = resolve; })
      : Promise.resolve({ login_id: "old", interval_ms: 1000, expires_in: 900, status: "pending" as const })) };
  const newCancel = vi.fn().mockResolvedValue({ ok: true });
  const second = { ...createApplicationSettingsControls(), cancelChatGPTLogin: newCancel,
    startChatGPTLogin: vi.fn().mockResolvedValue({ login_id: "new", interval_ms: 1000 }) };
  act(() => { root!.unmount(); root = createRoot(container); });
  const render = (controls: typeof first | typeof second) => root!.render(<SettingsControlsProvider controls={controls}><ChatGPTLogin onAuthenticated={authenticated} onBusyChange={() => {}} /></SettingsControlsProvider>);
  act(() => render(first));
  const trigger = button("chatgpt-login-start");
  await act(async () => trigger.click());
  act(() => render(first));
  expect(oldCancel).not.toHaveBeenCalled(); // New inline callback is not new authority.
  await act(async () => render(second));
  expect(button("chatgpt-login-start")).toBe(trigger);
  expect(trigger.disabled).toBe(false);
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(oldPoll).not.toHaveBeenCalled();
  await act(async () => trigger.click());
  if (phase === "admission") await act(async () => admit({ login_id: "old", interval_ms: 1000, expires_in: 900, status: "pending" }));
  expect(oldCancel).toHaveBeenCalledExactlyOnceWith("old");
  expect(newCancel).not.toHaveBeenCalled();
  expect(trigger.disabled).toBe(true);
  expect(authenticated).not.toHaveBeenCalled();
  act(() => { root!.unmount(); root = undefined; });
  expect(newCancel).toHaveBeenCalledExactlyOnceWith("new");
});

it("does not show an expired-scope error when login admission retires before new props arrive", async () => {
  const { scopeApplicationSettingsControls } = await import("../../../packages/plugin-sdk/src/ui");
  const { retainPresentationReads } = await import("./plugins/PresentationApplication");
  const { PluginLifecycleCancellationError } = await import("../shared/PluginLifecycle");
  const lifetime = new AbortController();
  const source = createApplicationSettingsControls();
  start.mockReturnValue(new Promise(() => {}));
  const scoped = scopeApplicationSettingsControls(() => source, lifetime.signal, () => lifetime.signal.throwIfAborted());
  const controls = retainPresentationReads({ settingsControls: scoped } as import("../../../packages/plugin-sdk/src/ui").ApplicationServiceV1, lifetime.signal).settingsControls;
  act(() => root!.render(<SettingsControlsProvider controls={controls}><ChatGPTLogin onAuthenticated={authenticated} /></SettingsControlsProvider>));
  await act(async () => button("chatgpt-login-start").click());
  await act(async () => lifetime.abort(new PluginLifecycleCancellationError()));
  expect(container.querySelector('[role="alert"]')).toBeNull();
  expect(authenticated).not.toHaveBeenCalled();
  expect(start).toHaveBeenCalledOnce();
});
