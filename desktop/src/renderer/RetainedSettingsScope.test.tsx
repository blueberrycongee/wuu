import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ApplicationSettingsControlsV1 as Controls } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { scopeApplicationSettingsControls, type ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ExtensionInventoryRecord, ProviderSummary } from "../shared/protocol";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { retainPresentationReads } from "./plugins/PresentationApplication";
import { ChatGPTConnectionDialog } from "./ChatGPTConnectionDialog";
import { ChatGPTAccountRows } from "./ChatGPTAccountRows";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { BrowserSettingsSection } from "./BrowserSettingsSection";
import { EngineAuthentication } from "./EngineAuthentication";
let container: HTMLDivElement;
let root: Root;
beforeEach(() => { container = document.createElement("div"); document.body.append(container); root = createRoot(container); });
afterEach(() => { act(() => root.unmount()); container.remove(); });
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (cause: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; }
function source(overrides: Partial<Controls> = {}): Controls {
  const base = createApplicationSettingsControls(); const empty: [] = [];
  return { ...base, capabilities: { ...base.capabilities, chatGPTLogin: false, diagnostics: true, browser: true, browserReload: true, platform: "darwin" }, getConflicts: () => empty, getDiagnostics: () => empty, subscribe: vi.fn(() => vi.fn()), getPluginDiagnostics: vi.fn().mockResolvedValue({ diagnostics: [] }), ...overrides };
}
function scoped(base: Controls) {
  const lifetime = new AbortController();
  const controls = retainPresentationReads({ settingsControls: scopeApplicationSettingsControls(() => base, lifetime.signal, () => lifetime.signal.throwIfAborted()) } as ApplicationServiceV1, lifetime.signal).settingsControls;
  return { controls, abort: () => lifetime.abort(new PluginLifecycleCancellationError()) };
}
const account = (id: string) => ({ id, email: `${id}@example.test`, status: "connected" as const });
const models = (id: string) => ({ models: [{ id, name: id }] });
const provider = { name: "chatgpt", type: "chatgpt", chatgpt_account_id: "same-account", auth_status: "connected" } as ProviderSummary;
const plugin: ExtensionInventoryRecord = { id: "settings-test", name: "Test", kind: "plugin", provenance: { kind: "plugin", source: "user", scope: "user" as const, plugin_id: "settings-test" }, state: "granted", approval_state: "granted", enabled: true, fingerprint: "same-fingerprint", contributions: { settings: [{ id: "workspace", title: "Workspace setting", type: "string", default: "", scope: "workspace" as const, apply: "live" }, { id: "token", title: "Token", type: "secret", default: "", scope: "user" as const, apply: "live" }] } };
function input(key: string) { return container.querySelector<HTMLInputElement>(`[data-setting-key="${key}"] input`)!; }
async function change(field: HTMLInputElement, value: string) { await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); }); }

it("rebinds account/model reads without remounting and hides only proven gap cancellation", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["listChatGPTAccounts"]>>>();
  const old = scoped(source({ listChatGPTAccounts: vi.fn(() => pending.promise), listChatGPTModels: vi.fn() }));
  const nextBase = source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [account("new-account")] }), listChatGPTModels: vi.fn().mockResolvedValue(models("new-model")) });
  const next = scoped(nextBase);
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={vi.fn()} onClose={vi.fn()} onConnected={vi.fn()} /></SettingsControlsProvider>);
  await act(async () => render(old.controls)); const field = container.querySelector("input");
  await act(async () => old.abort()); expect(container.querySelector('[role="alert"]')).toBeNull();
  await act(async () => render(next.controls)); expect(container.querySelector("input")).toBe(field);
  expect(nextBase.listChatGPTAccounts).toHaveBeenCalledOnce(); expect(nextBase.listChatGPTModels).toHaveBeenCalledWith("new-account");
  await act(async () => pending.resolve({ accounts: [account("old-account")] })); expect(container.textContent).not.toContain("old-account");
  expect(container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!.disabled).toBe(false);
});
it("does not complete an old connection save or carry its account into a new scope", async () => {
  const pending = deferred<void>(); const connected = vi.fn(); const save = vi.fn(() => pending.promise);
  const old = source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [account("old")] }), listChatGPTModels: vi.fn().mockResolvedValue(models("old-model")) });
  const next = source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [account("new")] }), listChatGPTModels: vi.fn().mockResolvedValue(models("new-model")) });
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={save} onClose={vi.fn()} onConnected={connected} /></SettingsControlsProvider>);
  await act(async () => render(old)); await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!.click());
  await act(async () => render(next)); await act(async () => pending.resolve());
  expect(connected).not.toHaveBeenCalled(); expect(next.listChatGPTModels).toHaveBeenCalledWith("new"); expect(next.listChatGPTModels).not.toHaveBeenCalledWith("old");
});
it("moves account rows to the new authority and rejects an old sign-out completion", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["signOutChatGPTAccount"]>>>(); const changed = vi.fn();
  const old = source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [{ ...account("same-account"), email: "old-email" }] }), signOutChatGPTAccount: vi.fn(() => pending.promise) });
  const next = source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [{ ...account("same-account"), email: "new-email" }] }) });
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><ChatGPTAccountRows provider={provider} disabled={false} onProvidersChanged={changed} onRefresh={vi.fn()} /></SettingsControlsProvider>);
  await act(async () => render(old)); await act(async () => [...container.querySelectorAll<HTMLButtonElement>("button")].at(-1)!.click());
  await act(async () => render(next)); expect(container.textContent).toContain("new-email");
  await act(async () => pending.resolve({ ok: true, providers: [], revocation_confirmed: true })); expect(changed).not.toHaveBeenCalled(); expect(container.textContent).not.toContain("old-email");
});
it("fences workspace drafts, secret drafts, subscriptions and pending writes across scope rebind", async () => {
  const oldWrite = deferred<Awaited<ReturnType<Controls["setPluginSetting"]>>>(); const newRead = deferred<Awaited<ReturnType<Controls["getPluginSetting"]>>>();
  const oldBase = source({ getPluginSetting: vi.fn(async ({ id, key }) => ({ id, key, value: key === "workspace" ? "workspace-a" : "", configured: false, scope: "workspace" as const })), setPluginSetting: vi.fn(() => oldWrite.promise) });
  const nextBase = source({ getPluginSetting: vi.fn(({ id, key }) => key === "workspace" ? newRead.promise : Promise.resolve({ id, key, value: "", configured: false, scope: "user" as const })), setPluginSetting: vi.fn(async params => ({ ...params, scope: "workspace" as const })) });
  const old = scoped(oldBase), next = scoped(nextBase);
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><PluginSettingsEditor plugin={{ ...plugin, contributions: { settings: plugin.contributions!.settings!.map(setting => ({ ...setting, scope: "workspace" })) } }} workspaceRoot={controls === old.controls ? "/workspace-a" : "/workspace-b"} /></SettingsControlsProvider>);
  await act(async () => render(old.controls)); const field = input("workspace");
  await change(field, "old draft"); await change(input("token"), "old-secret"); await act(async () => field.dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  await act(async () => old.abort()); expect(container.textContent).not.toContain("scope is no longer active");
  const oldStops = vi.mocked(oldBase.subscribe).mock.results.map(item => item.value); expect(oldStops.length).toBeGreaterThan(0); for (const stop of oldStops) expect(stop).toHaveBeenCalledTimes(1);
  await act(async () => render(next.controls)); expect(input("workspace")).toBe(field); expect(field.disabled).toBe(true); expect(field.value).not.toBe("old draft"); expect(input("token").value).toBe("");
  await act(async () => field.dispatchEvent(new FocusEvent("focusout", { bubbles: true }))); expect(nextBase.setPluginSetting).not.toHaveBeenCalled();
  await act(async () => { newRead.resolve({ id: plugin.id, key: "workspace", scope: "workspace" as const, value: "workspace-b" }); oldWrite.resolve({ id: plugin.id, key: "workspace", scope: "workspace" as const, value: "old write" }); }); expect(field.value).toBe("workspace-b");
  await change(field, "new draft"); await act(async () => field.dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  expect(nextBase.setPluginSetting).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ value: "new draft" })); expect(nextBase.subscribe).toHaveBeenCalled(); for (const stop of oldStops) expect(stop).toHaveBeenCalledTimes(1);
});
it("does not let a late setting response replace a new target's value", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["setPluginSetting"]>>>();
  const controls = source({ getPluginSetting: vi.fn(async ({ id, key }) => ({ id, key, scope: "user" as const, value: id })), setPluginSetting: vi.fn(() => pending.promise) });
  const record = { ...plugin, contributions: { settings: plugin.contributions!.settings!.slice(0, 1) } };
  const render = (id: string) => root.render(<SettingsControlsProvider controls={controls}><PluginSettingsEditor plugin={{ ...record, id }} /></SettingsControlsProvider>);
  await act(async () => render("old-target")); await change(input("workspace"), "draft"); await act(async () => input("workspace").dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  await act(async () => render("new-target")); await act(async () => pending.resolve({ id: "old-target", key: "workspace", scope: "user" as const, value: "old reply" })); expect(input("workspace").value).toBe("new-target");
});
it("rebinds browser reads and prevents old refresh responses from painting a new scope", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["listBrowserExtensions"]>>>();
  const old = source({ listBrowserProfiles: vi.fn().mockResolvedValue([]), listBrowserExtensions: vi.fn().mockResolvedValueOnce([]).mockReturnValue(pending.promise) });
  const next = source({ listBrowserProfiles: vi.fn().mockResolvedValue([]), listBrowserExtensions: vi.fn().mockResolvedValue([{ id: "new", name: "New extension", version: "1", path: "/new", enabled: true, loaded: true }]) });
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><BrowserSettingsSection /></SettingsControlsProvider>);
  await act(async () => render(old)); await act(async () => container.querySelector<HTMLButtonElement>("button")!.click()); await act(async () => render(next)); expect(next.listBrowserExtensions).toHaveBeenCalledOnce();
  await act(async () => pending.resolve([{ id: "old", name: "Old extension", version: "1", path: "/old", enabled: true, loaded: true }])); expect(container.textContent).toContain("New extension"); expect(container.textContent).not.toContain("Old extension");
});
it("ends browser reload recovery on proven retirement but displays real reload failures", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["reloadBrowserExtension"]>>>(); const extension = { id: "fixture", name: "Fixture", version: "1", path: "/fixture", enabled: true, loaded: true };
  const base = source({ listBrowserProfiles: vi.fn().mockResolvedValue([]), listBrowserExtensions: vi.fn().mockResolvedValue([extension]), reloadBrowserExtension: vi.fn(() => pending.promise) }); const old = scoped(base);
  await act(async () => root.render(<SettingsControlsProvider controls={old.controls}><BrowserSettingsSection /></SettingsControlsProvider>));
  const reload = () => [...container.querySelectorAll<HTMLButtonElement>("button")].find(button => /Reload|重新加载/.test(button.getAttribute("aria-label") ?? ""))!;
  expect(reload()).toBeDefined(); await act(async () => reload().click()); await act(async () => old.abort()); expect(container.querySelector('[role="alert"]')).toBeNull(); expect(base.listBrowserExtensions).toHaveBeenCalledTimes(1);
  const next = source({ listBrowserProfiles: vi.fn().mockResolvedValue([]), listBrowserExtensions: vi.fn().mockResolvedValue([extension]), reloadBrowserExtension: vi.fn().mockRejectedValue(new Error("real reload failure")) });
  await act(async () => root.render(<SettingsControlsProvider controls={next}><BrowserSettingsSection /></SettingsControlsProvider>)); await act(async () => reload().click()); expect(container.textContent).toContain("real reload failure");
});
it("ignores proven engine-discovery retirement while preserving real authentication errors", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["listEngineAuthMethods"]>>>(); const old = scoped(source({ listEngineAuthMethods: vi.fn(() => pending.promise), cancelEngineAuth: vi.fn().mockResolvedValue(undefined) }));
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><EngineAuthentication engineID="codex" protocol="acp" onReuseLocal={vi.fn()} /></SettingsControlsProvider>);
  await act(async () => render(old.controls)); await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.click()); await act(async () => old.abort()); expect(container.textContent).not.toContain("scope is no longer active");
  const next = source({ listEngineAuthMethods: vi.fn().mockRejectedValue(new Error("real auth failure")) }); await act(async () => render(next)); await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="engine-auth-discover"]')!.click()); expect(container.textContent).toContain("real auth failure");
});

it("keeps same-target unsaved settings through authority rebind and saves only on a new explicit action", async () => {
  const make = () => source({
    getPluginSetting: vi.fn(async ({ id, key }) => ({ id, key, scope: "user" as const, value: key === "workspace" ? "stored" : "", configured: false })),
    setPluginSetting: vi.fn(async params => ({ ...params, scope: "user" as const })),
  });
  const old = make(), next = make();
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><PluginSettingsEditor plugin={plugin} /></SettingsControlsProvider>);
  await act(async () => render(old));
  const field = input("workspace"); const secret = input("token");
  await change(field, "unsaved user text"); await change(secret, "unsubmitted secret");
  await act(async () => render(next));
  expect(input("workspace")).toBe(field); expect(input("token")).toBe(secret);
  expect(field.value).toBe("unsaved user text"); expect(secret.value).toBe("unsubmitted secret");
  expect(old.setPluginSetting).not.toHaveBeenCalled(); expect(next.setPluginSetting).not.toHaveBeenCalled();
  await act(async () => field.dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  expect(next.setPluginSetting).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ value: "unsaved user text" }));
});

it("keeps a same-target ChatGPT connection name through authority rebind", async () => {
  const make = () => source({ listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [account("same")] }), listChatGPTModels: vi.fn().mockResolvedValue(models("same-model")) });
  const old = make(), next = make(); const save = vi.fn().mockResolvedValue(undefined);
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={save} onClose={vi.fn()} onConnected={vi.fn()} /></SettingsControlsProvider>);
  await act(async () => render(old)); const field = container.querySelector<HTMLInputElement>("input")!;
  await change(field, "my-unsaved-connection"); await act(async () => render(next));
  expect(container.querySelector("input")).toBe(field); expect(field.value).toBe("my-unsaved-connection"); expect(save).not.toHaveBeenCalled();
  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect"]')!.click());
  expect(save).toHaveBeenCalledWith("my-unsaved-connection", "same-model", undefined, expect.objectContaining({ chatgpt_account_id: "same" }));
});

it("retains same-target edits without accepting the old pending save or writing through it", async () => {
  const pending = deferred<Awaited<ReturnType<Controls["setPluginSetting"]>>>();
  const make = () => source({ getPluginSetting: vi.fn(async ({ id, key }) => ({ id, key, scope: "workspace" as const, value: "stored" })), setPluginSetting: vi.fn(async params => ({ ...params, scope: "workspace" as const })) });
  const old = { ...make(), setPluginSetting: vi.fn(() => pending.promise) }, next = make();
  const record = { ...plugin, contributions: { settings: plugin.contributions!.settings!.slice(0, 1) } };
  const render = (controls: Controls) => root.render(<SettingsControlsProvider controls={controls}><PluginSettingsEditor plugin={record} /></SettingsControlsProvider>);
  await act(async () => render(old)); await change(input("workspace"), "first draft");
  await act(async () => input("workspace").dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  await act(async () => render(next)); expect(input("workspace").value).toBe("first draft");
  await change(input("workspace"), "newer edit");
  await act(async () => pending.resolve({ id: plugin.id, key: "workspace", value: "old confirmation", scope: "workspace" }));
  expect(input("workspace").value).toBe("newer edit"); expect(next.setPluginSetting).not.toHaveBeenCalled();
  await act(async () => input("workspace").dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
  expect(next.setPluginSetting).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ value: "newer edit" })); expect(old.setPluginSetting).toHaveBeenCalledTimes(1);
});
