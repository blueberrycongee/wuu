import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { translateCurrent } from "./i18n";
import { ChatGPTConnectionDialog } from "./ChatGPTConnectionDialog";

let root: Root | undefined;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); vi.unstubAllGlobals(); });
it("requires an account-bound model before saving and preserves the returned order", async () => {
  let resolveModels!: (value: unknown) => void;
  const save = vi.fn().mockResolvedValue(undefined);
  const models = vi.fn().mockReturnValue(new Promise(resolve => { resolveModels = resolve; }));
  vi.stubGlobal("wuu", {
    listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [{ id: "account-a", email: "a@example.test", status: "connected" }] }),
    listChatGPTModels: models,
  });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ChatGPTConnectionDialog providers={[]} running={false} defaultOn={false} onSave={save} onClose={vi.fn()} onConnected={vi.fn()} />));
  const connect = () => document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!;
  expect(connect().disabled).toBe(true);
  expect(models).toHaveBeenCalledWith("account-a");
  await act(async () => resolveModels({ models: [{ id: "z-model", name: "First recommended" }, { id: "a-model", name: "Second" }] }));
  expect(connect().disabled).toBe(false);
  await act(async () => connect().click());
  expect(save).toHaveBeenCalledWith("chatgpt", "z-model", undefined, { type: "chatgpt", create_provider: true, chatgpt_account_id: "account-a", keep_selection: true });
});

it("keeps inline connection setup in the page and starts login only after the explicit action", async () => {
  const startLogin = vi.fn().mockResolvedValue({ login_id: "login-a", interval_ms: 1000 });
  const cancelLogin = vi.fn().mockResolvedValue(undefined);
  const close = vi.fn();
  vi.stubGlobal("wuu", {
    listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [] }),
    startChatGPTLogin: startLogin,
    cancelChatGPTLogin: cancelLogin,
  });
  container = document.createElement("div"); document.body.append(container);
  const outside = document.createElement("button"); container.append(outside); outside.focus();
  const mount = document.createElement("div"); container.append(mount); root = createRoot(mount);
  await act(async () => root!.render(<ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={vi.fn()} onClose={close} onConnected={vi.fn()} />));
  const page = mount.querySelector<HTMLElement>('[role="region"]')!;
  expect(page).not.toBeNull();
  const labelledBy = page.getAttribute("aria-labelledby");
  expect(page.getAttribute("aria-label") || document.getElementById(labelledBy!)?.textContent).toBe(page.querySelector("h2")?.textContent);
  expect(document.querySelector('[role="dialog"], [aria-modal="true"]')).toBeNull();
  expect(document.activeElement).toBe(page.querySelector("h2"));
  outside.focus();
  expect(document.activeElement).toBe(outside);
  expect(startLogin).not.toHaveBeenCalled();
  const login = page.querySelector<HTMLButtonElement>('[data-testid="chatgpt-login-start"]')!;
  await act(async () => login.click());
  expect(startLogin).toHaveBeenCalledExactlyOnceWith(undefined, false);
  expect(login.disabled).toBe(true);
  await act(async () => page.querySelector<HTMLButtonElement>('[data-testid="settings-connection-back"]')!.click());
  expect(close).toHaveBeenCalledOnce();
  await act(async () => root!.render(null));
  expect(cancelLogin).toHaveBeenCalledExactlyOnceWith("login-a");
});

it("locks inline navigation during a save and restores it with the error if saving fails", async () => {
  let rejectSave!: (reason: Error) => void;
  const save = vi.fn().mockReturnValue(new Promise((_resolve, reject) => { rejectSave = reject; }));
  const close = vi.fn();
  const connected = vi.fn();
  vi.stubGlobal("wuu", {
    listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [{ id: "account-a", status: "connected" }] }),
    listChatGPTModels: vi.fn().mockResolvedValue({ models: [{ id: "model-a", name: "Model A" }] }),
  });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={true} onSave={save} onClose={close} onConnected={connected} />));
  const connect = container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!;
  const back = container.querySelector<HTMLButtonElement>('[data-testid="settings-connection-back"]')!;
  const form = container.querySelector<HTMLFormElement>("form")!;
  await act(async () => {
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
  expect(save).toHaveBeenCalledExactlyOnceWith("chatgpt", "model-a", undefined, { type: "chatgpt", create_provider: true, chatgpt_account_id: "account-a", keep_selection: false });
  expect(connect.disabled).toBe(true);
  expect(back.disabled).toBe(true);
  await act(async () => back.click());
  expect(close).not.toHaveBeenCalled();
  await act(async () => rejectSave(new Error("Connection could not be saved")));
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("Connection could not be saved");
  expect(back.disabled).toBe(false);
  expect(connect.disabled).toBe(false);
  expect(connected).not.toHaveBeenCalled();
});

it("keeps existing web accounts usable without instructions for an unavailable login button", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const start = vi.fn();
  vi.stubGlobal("wuu", {
    unsupportedMethods: ["startChatGPTLogin", "pollChatGPTLogin", "cancelChatGPTLogin"],
    startChatGPTLogin: start,
    listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [{ id: "account-web", status: "connected" }] }),
    listChatGPTModels: vi.fn().mockResolvedValue({ models: [{ id: "web-model", name: "Fixture model" }] }),
  });
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  await act(async () => root!.render(<ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={save} onClose={vi.fn()} onConnected={vi.fn()} />));
  expect(container.querySelector('[data-testid="chatgpt-login-start"]')).toBeNull();
  expect(container.textContent).not.toContain(translateCurrent("provider.chatgptAddAccountHint"));
  const connect = container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!;
  expect(connect.disabled).toBe(false);
  await act(async () => connect.click());
  expect(save).toHaveBeenCalledWith("chatgpt", "web-model", undefined, expect.objectContaining({ chatgpt_account_id: "account-web" }));
  expect(start).not.toHaveBeenCalled();
});
