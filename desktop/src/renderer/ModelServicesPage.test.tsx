import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { CatalogProviderSummary, EngineListResult, ProviderSummary, WuuDesktopApi } from "../shared/protocol";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { ModelServicesPage } from "./ModelServicesPage";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

const renewalCatalog: CatalogProviderSummary = { id: "fixture", name: "Fixture service", type: "openai", base_url: "https://fixture.invalid", model_count: 1, default_model: "default-model" };

function renewalControls(overrides: Partial<ApplicationSettingsControlsV1> = {}): ApplicationSettingsControlsV1 {
  const base = createApplicationSettingsControls();
  return { ...base, capabilities: { ...base.capabilities, catalog: true, engines: true },
    listEngines: vi.fn().mockResolvedValue({ engines: [] }),
    listCatalogProviders: vi.fn().mockResolvedValue({ providers: [] }),
    listChatGPTAccounts: vi.fn().mockResolvedValue({ accounts: [] }), ...overrides };
}

it("refreshes retained catalog and quota reads with new controls and ignores old results", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const oldCatalog = deferred<{ providers: CatalogProviderSummary[] }>();
  const oldQuota = deferred<EngineListResult>();
  const provider: ProviderSummary = { name: "api", type: "openai", model: "m", api_key_configured: true };
  const snapshot = (used: number): EngineListResult => ({ engines: [], subscription_providers: [{ ...provider,
    quota: { status: "available", checked_at: new Date().toISOString(), windows: [{ id: "weekly", used_percent: used }] } }] });
  const previous = renewalControls({ listCatalogProviders: vi.fn().mockReturnValue(oldCatalog.promise), listEngines: vi.fn().mockReturnValue(oldQuota.promise) });
  const next = renewalControls({ listCatalogProviders: vi.fn().mockResolvedValue({ providers: [renewalCatalog] }), listEngines: vi.fn().mockResolvedValue(snapshot(20)) });
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: provider.name, model: provider.model, providers: [provider] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn() };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    const page = container.querySelector('[data-testid="settings-providers"]');
    await render(next);
    expect(next.listEngines).toHaveBeenCalledExactlyOnceWith({ include_quota: true, include_activity: false });
    expect(next.listCatalogProviders).toHaveBeenCalledTimes(1);
    expect(container.querySelector('[data-testid="settings-providers"]')).toBe(page);
    expect(container.querySelector("meter")?.value).toBe(80);
    await act(async () => { oldCatalog.resolve({ providers: [{ ...renewalCatalog, id: "retired" }] }); oldQuota.resolve(snapshot(90)); });
    expect(container.querySelector("meter")?.value).toBe(80);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-connection-add"]')!.click());
    expect(container.querySelector('[data-catalog="fixture"]')).not.toBeNull();
    expect(container.querySelector('[data-catalog="retired"]')).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("rebinds retained account model discovery without publishing retired models", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const oldModels = deferred<Awaited<ReturnType<ApplicationSettingsControlsV1["listChatGPTModels"]>>>();
  const provider: ProviderSummary = { name: "account", type: "chatgpt", model: "current", chatgpt_account_id: "account-id", auth_status: "connected" };
  const changed = vi.fn();
  const previous = renewalControls({ listChatGPTModels: vi.fn().mockReturnValue(oldModels.promise) });
  const next = renewalControls({ listChatGPTModels: vi.fn().mockResolvedValue({ models: [{ id: "successor-model", name: "Successor model" }] }) });
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: provider.name, model: provider.model, providers: [provider], catalog_providers: [] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn(), onProviderModelsChanged: changed };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    const detail = container.querySelector('.connection-page');
    await render(next);
    expect(next.listChatGPTModels).toHaveBeenCalledExactlyOnceWith("account-id");
    expect(container.querySelector('.connection-page')).toBe(detail);
    expect(container.querySelector('[data-model="successor-model"]')).not.toBeNull();
    await act(async () => oldModels.resolve({ models: [{ id: "retired-model", name: "Retired model" }] }));
    expect(changed).toHaveBeenCalledTimes(1);
    expect(container.querySelector('[data-model="retired-model"]')).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("renews catalog model discovery while preserving an unfinished connection form", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const oldModels = deferred<{ providers: CatalogProviderSummary[] }>();
  const previous = renewalControls({ listCatalogProviders: vi.fn().mockReturnValue(oldModels.promise) });
  const next = renewalControls({ listCatalogProviders: vi.fn().mockResolvedValue({ providers: [{ ...renewalCatalog, models: [{ id: "successor-model" }] }] }) });
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: "", model: "", providers: [], catalog_providers: [renewalCatalog] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn() };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-connection-add"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-catalog="fixture"]')!.click());
    const key = container.querySelector<HTMLInputElement>('[data-testid="settings-provider-connect-key"]')!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(key, "unfinished-key"); key.dispatchEvent(new Event("input", { bubbles: true })); });
    await render(next);
    expect(next.listCatalogProviders).toHaveBeenCalledExactlyOnceWith("fixture");
    expect(container.querySelector('[data-testid="settings-provider-connect-key"]')).toBe(key);
    expect(key.value).toBe("unfinished-key");
    await act(async () => oldModels.resolve({ providers: [{ ...renewalCatalog, models: [{ id: "retired-model" }] }] }));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-connect-model"]')!.click());
    expect(document.querySelector('[role="menuitemradio"][data-value="successor-model"]')).not.toBeNull();
    expect(document.querySelector('[role="menuitemradio"][data-value="retired-model"]')).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("allows offline configured Grok model changes without promoting the connection to default", async () => {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const previous = window.wuu;
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [] }) } as unknown as WuuDesktopApi;
  const onSave = vi.fn().mockResolvedValue(undefined);
  try {
    await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<ModelServicesPage
      initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "local", model: "local-model", providers: [
        { name: "grok", type: "grok-build", model: "grok-4.5", api_key_configured: false, models: [{ id: "grok-4.5" }, { id: "grok-4.6" }] },
      ] }} running={false} runningProviderNames={new Set()} onSave={onSave}
      onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} />}</SettingsControlsProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    const model = container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-model"][data-model="grok-4.6"]')!;
    expect(model).not.toBeNull();
    expect(model.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-add-model-open"]')!.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-model-actions"]')!.disabled).toBe(false);
    expect(container.querySelector('[data-testid="settings-provider-make-default"]')).toBeNull();
    expect(container.querySelector('[data-testid="settings-provider-add-model-open"]')?.textContent?.trim()).toBeTruthy();
    await act(async () => model.click());
    expect(onSave).toHaveBeenCalledWith("grok", "grok-4.6", undefined, { keep_selection: true });
  } finally {
    act(() => root.unmount());
    container.remove();
    window.wuu = previous;
  }
});

it.each(["wuu-auth-store", "codex-cli", "codex-cli-readonly", "explicit", undefined])("enables local Codex reuse only explicitly for source %s and keeps failure retryable", async (source) => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); const previous = window.wuu;
  const provider = { name: "saved-codex", type: "openai-codex", model: "chosen-model", api_key_configured: Boolean(source), codex_credential_source: source, reuse_codex_credentials: source === "explicit", models: [{ id: "chosen-model" }] };
  const local = { ...provider, api_key_configured: true, reuse_codex_credentials: true, codex_credential_source: "codex-cli" };
  const reuse = vi.fn().mockRejectedValueOnce(new Error("Local session unavailable")).mockResolvedValue({ providers: [local] });
  const models = vi.fn().mockResolvedValue({ providers: [local], models: local.models });
  const changed = vi.fn(); const save = vi.fn(); const login = vi.fn();
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [] }), useCodexCredentials: reuse, loadCodexModels: models, startChatGPTLogin: login } as unknown as WuuDesktopApi;
  try {
    await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<ModelServicesPage initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "saved-codex", model: "chosen-model", providers: [provider] }} running={false} runningProviderNames={new Set()} onSave={save} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} onProvidersChanged={changed} />}</SettingsControlsProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    const action = container.querySelector<HTMLButtonElement>('[data-testid="settings-auth-method-local"]')!;
    expect(action.getAttribute("aria-pressed")).toBe("false");
    expect(container.querySelector("[data-auth-source]")?.getAttribute("data-auth-source")).toBe(!source ? undefined : source === "explicit" ? "codex-explicit" : source === "wuu-auth-store" ? "codex-saved" : "codex-local-available");
    expect(reuse).not.toHaveBeenCalled();
    await act(async () => action.click());
    expect(reuse).toHaveBeenCalledExactlyOnceWith("saved-codex");
    expect(models).not.toHaveBeenCalled();
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Local session unavailable");
    expect(action.disabled).toBe(false);
    await act(async () => action.click());
    expect(reuse).toHaveBeenCalledTimes(2);
    expect(models).toHaveBeenCalledExactlyOnceWith("saved-codex");
    expect(changed).toHaveBeenLastCalledWith([local]);
    expect(login).not.toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
    expect(container.querySelector('[data-model="chosen-model"]')?.getAttribute("aria-pressed")).toBe("true");
  } finally { act(() => root.unmount()); container.remove(); window.wuu = previous; }
});

it("keeps alias and display ordering separate from configuration identity and selection", async () => {
  localStorage.removeItem("wuu.connectionDisplay.v1");
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); const previous = window.wuu;
  const save = vi.fn();
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [] }) } as unknown as WuuDesktopApi;
  try {
    await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<ModelServicesPage initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "api-stable-id", model: "model-a", providers: [{ name: "api-stable-id", type: "openai", model: "model-a", api_key_configured: true, models: [{ id: "model-a", display_name: "A" }, { id: "model-b", display_name: "B" }] }] }} running={false} runningProviderNames={new Set()} onSave={save} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} />}</SettingsControlsProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('.connection-title-edit button')!.click());
    const input = container.querySelector<HTMLInputElement>('.connection-title-form input')!;
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Personal account"); input.dispatchEvent(new Event("input", { bubbles: true })); });
    await act(async () => container.querySelector<HTMLButtonElement>('.connection-title-form button')!.click());
    expect(container.querySelector('.connection-page-title')?.textContent).toContain("Personal account");
    const rows = () => [...container.querySelectorAll<HTMLButtonElement>('[data-testid="settings-provider-model"]')].map(button => button.dataset.model);
    await act(async () => container.querySelector<HTMLButtonElement>('.connection-model-row:last-child .connection-model-order button')!.click());
    expect(rows()).toEqual(["model-b", "model-a"]);
    expect(save).not.toHaveBeenCalled();
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-model"][data-model="model-b"]')!.click());
    expect(save.mock.calls[0].slice(0, 2)).toEqual(["api-stable-id", "model-b"]);
  } finally { act(() => root.unmount()); container.remove(); window.wuu = previous; localStorage.removeItem("wuu.connectionDisplay.v1"); }
});

it.each(["starting", "polling"])("stops xAI authorization when selected settings unmount during %s", async phase => {
  vi.useFakeTimers();
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); const previous = window.wuu;
  let started!: (value: Awaited<ReturnType<WuuDesktopApi["startXAILogin"]>>) => void;
  let polled!: (value: Awaited<ReturnType<WuuDesktopApi["pollXAILogin"]>>) => void;
  const start = { login_id:"owned-login", user_code:"CODE", verification_uri:"https://auth.example", expires_in:300, interval_ms:1000 };
  const cancel=vi.fn().mockResolvedValue({ok:true}); const external=vi.fn().mockResolvedValue(undefined); const save=vi.fn();
  const poll=vi.fn(()=>new Promise<Awaited<ReturnType<WuuDesktopApi["pollXAILogin"]>>>(resolve=>{polled=resolve;}));
  window.wuu = {listEngines:vi.fn().mockResolvedValue({engines:[]}),startXAILogin:vi.fn(()=>new Promise(resolve=>{started=resolve;})),pollXAILogin:poll,cancelXAILogin:cancel,openExternal:external} as unknown as WuuDesktopApi;
  let mounted=true;
  try {
    await act(async()=>root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}><ModelServicesPage
      initialized={{protocol_version:"test",workspace_root:"/tmp",provider:"xai",model:"grok",providers:[{name:"xai",type:"xai-subscription",model:"grok",api_key_configured:false}]}}
      running={false} runningProviderNames={new Set()} onSave={save} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()}/></SettingsControlsProvider>));
    await act(async()=>container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    await act(async()=>container.querySelector<HTMLButtonElement>('[data-testid="settings-xai-login"]')!.click());
    if(phase==="polling") {
      await act(async()=>started(start));
      await act(async()=>vi.advanceTimersByTimeAsync(1000));
      expect(poll).toHaveBeenCalledTimes(1);
    }
    await act(async()=>root.unmount()); mounted=false;
    if(phase==="starting") await act(async()=>started(start));
    else await act(async()=>polled({status:"pending",interval_ms:1000}));
    await act(async()=>vi.advanceTimersByTimeAsync(5000));
    expect(cancel).toHaveBeenCalledExactlyOnceWith("owned-login");
    expect(poll).toHaveBeenCalledTimes(phase==="polling"?1:0);
    if(phase==="starting") expect(external).not.toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
  } finally {if(mounted)act(()=>root.unmount());container.remove();window.wuu=previous;vi.useRealTimers();}
});

it("does not load Codex models after credential admission outlives settings", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); const previous = window.wuu;
  const provider = { name: "saved-codex", type: "openai-codex", model: "chosen-model" };
  let reused!: (value: unknown) => void;
  const models = vi.fn(); const changed = vi.fn();
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [] }), useCodexCredentials: vi.fn().mockReturnValue(new Promise(resolve => { reused = resolve; })), loadCodexModels: models } as unknown as WuuDesktopApi;
  try {
    await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}><ModelServicesPage initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/fixture", provider: provider.name, model: provider.model, providers: [provider] }} running={false} runningProviderNames={new Set()} onSave={vi.fn()} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} onProvidersChanged={changed} /></SettingsControlsProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-auth-method-local"]')!.click());
    act(() => root.render(null));
    await act(async () => reused({ providers: [provider] }));
    expect(models).not.toHaveBeenCalled();
    expect(changed).not.toHaveBeenCalled();
  } finally { act(() => root.unmount()); container.remove(); window.wuu = previous; }
});

it("retires in-flight credential admission when controls renew without clearing a successor action", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const provider: ProviderSummary = { name: "saved-codex", type: "openai-codex", model: "chosen-model" };
  const oldAdmission = deferred<{ providers: ProviderSummary[] }>();
  const nextAdmission = deferred<{ providers: ProviderSummary[] }>();
  const previous = renewalControls({ useCodexCredentials: vi.fn().mockReturnValue(oldAdmission.promise), loadCodexModels: vi.fn() });
  const next = renewalControls({ useCodexCredentials: vi.fn().mockReturnValue(nextAdmission.promise), loadCodexModels: vi.fn().mockResolvedValue({ providers: [provider], models: [] }) });
  const changed = vi.fn();
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: provider.name, model: provider.model, providers: [provider] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn(), onProvidersChanged: changed };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    const action = container.querySelector<HTMLButtonElement>('[data-testid="settings-auth-method-local"]')!;
    await act(async () => action.click());
    await render(next);
    expect(action.disabled).toBe(false);
    await act(async () => action.click());
    await act(async () => oldAdmission.resolve({ providers: [provider] }));
    expect(previous.loadCodexModels).not.toHaveBeenCalled();
    expect(next.loadCodexModels).not.toHaveBeenCalled();
    expect(changed).not.toHaveBeenCalled();
    expect(action.disabled).toBe(true);
    await act(async () => nextAdmission.resolve({ providers: [provider] }));
    expect(next.loadCodexModels).toHaveBeenCalledExactlyOnceWith(provider.name);
    expect(action.disabled).toBe(false);
  } finally { act(() => root.unmount()); container.remove(); }
});

it("cancels only the old xAI authorization after retained controls renewal", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const oldAdmission = deferred<Awaited<ReturnType<ApplicationSettingsControlsV1["startXAILogin"]>>>();
  const previous = renewalControls({ startXAILogin: vi.fn().mockReturnValue(oldAdmission.promise), cancelXAILogin: vi.fn().mockResolvedValue({ ok: true }), openExternal: vi.fn() });
  const next = renewalControls({ startXAILogin: vi.fn().mockReturnValue(new Promise(() => {})), cancelXAILogin: vi.fn() });
  const save = vi.fn();
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: "xai", model: "grok", providers: [{ name: "xai", type: "xai-subscription", model: "grok", api_key_configured: false }] },
    running: false, runningProviderNames: new Set<string>(), onSave: save, onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn() };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    const login = container.querySelector<HTMLButtonElement>('[data-testid="settings-xai-login"]')!;
    await act(async () => login.click());
    await render(next);
    expect(login.disabled).toBe(false);
    await act(async () => login.click());
    await act(async () => oldAdmission.resolve({ login_id: "retired-login", user_code: "OLD", verification_uri: "https://fixture.invalid", expires_in: 300, interval_ms: 1000 }));
    expect(previous.cancelXAILogin).toHaveBeenCalledExactlyOnceWith("retired-login");
    expect(previous.openExternal).not.toHaveBeenCalled();
    expect(next.cancelXAILogin).not.toHaveBeenCalled();
    expect(next.startXAILogin).toHaveBeenCalledTimes(1);
    expect(login.disabled).toBe(true);
    expect(save).not.toHaveBeenCalled();
  } finally { act(() => root.unmount()); container.remove(); }
});

it.each([true, false])("keeps retired credential cancellation silent but exposes genuine errors (retired: %s)", async retired => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const cause = retired ? new PluginLifecycleCancellationError() : new Error("Credential transport failed");
  const controls = renewalControls({ useCodexCredentials: vi.fn().mockRejectedValue(cause), loadCodexModels: vi.fn() });
  try {
    await act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage
      initialized={{ protocol_version: "test", workspace_root: "/fixture", provider: "codex", model: "m", providers: [{ name: "codex", type: "openai-codex", model: "m" }] }}
      running={false} runningProviderNames={new Set()} onSave={vi.fn()} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} /></SettingsControlsProvider>));
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-auth-method-local"]')!.click());
    expect(Boolean(container.querySelector('[role="alert"]'))).toBe(!retired);
    expect(controls.loadCodexModels).not.toHaveBeenCalled();
    expect(controls.useCodexCredentials).toHaveBeenCalledTimes(1);
  } finally { act(() => root.unmount()); container.remove(); }
});

it("does not publish a connection refresh after its settings authority is replaced", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const refresh = deferred<Awaited<ReturnType<ApplicationSettingsControlsV1["refreshModelCatalog"]>>>();
  const previous = renewalControls({ refreshModelCatalog: vi.fn().mockReturnValue(refresh.promise) });
  const next = renewalControls();
  const provider: ProviderSummary = { name: "grok", type: "grok-build", model: "grok-4.5", api_key_configured: false };
  const changed = vi.fn();
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: provider.name, model: provider.model, providers: [provider] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn(), onProvidersChanged: changed };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="grok-login-local"]')!.click());
    expect(previous.refreshModelCatalog).toHaveBeenCalledTimes(1);
    await render(next);
    await act(async () => refresh.resolve({ provider_count: 1, model_count: 1, providers: [{ ...provider, api_key_configured: true }] }));
    expect(changed).not.toHaveBeenCalled();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("does not close retained successor details when old provider removal settles", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const removal = deferred<void>();
  const previous = renewalControls(); const next = renewalControls();
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: "default", model: "m", providers: [
    { name: "default", type: "openai", model: "m", api_key_configured: true }, { name: "remove-me", type: "openai", model: "m", api_key_configured: true }] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn().mockReturnValue(removal.promise), onRefreshModelCatalog: vi.fn() };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-card"][data-provider="remove-me"]')!.click());
    await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove"]')!.click());
    await act(async () => document.querySelector<HTMLButtonElement>('[data-testid="settings-provider-remove-confirm"]')!.click());
    expect(props.onRemoveProvider).toHaveBeenCalledExactlyOnceWith("remove-me", undefined);
    await render(next);
    const detail = container.querySelector('.connection-page');
    await act(async () => removal.resolve());
    expect(container.querySelector('.connection-page')).toBe(detail);
  } finally { act(() => root.unmount()); container.remove(); }
});

it("stops quota loading when successor controls no longer provide engine inventory", async () => {
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container);
  const previous = renewalControls({ listEngines: vi.fn().mockReturnValue(new Promise(() => {})) });
  const baseNext = renewalControls();
  const next = { ...baseNext, capabilities: { ...baseNext.capabilities, engines: false } };
  const props = { initialized: { protocol_version: "test", workspace_root: "/fixture", provider: "xai", model: "m", providers: [{ name: "xai", type: "xai-subscription", model: "m", api_key_configured: true }] },
    running: false, runningProviderNames: new Set<string>(), onSave: vi.fn(), onRemoveProvider: vi.fn(), onRefreshModelCatalog: vi.fn() };
  const render = (controls: ApplicationSettingsControlsV1) => act(async () => root.render(<SettingsControlsProvider controls={controls}><ModelServicesPage {...props} /></SettingsControlsProvider>));
  try {
    await render(previous);
    expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
    await render(next);
    expect(next.listEngines).not.toHaveBeenCalled();
    expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});
