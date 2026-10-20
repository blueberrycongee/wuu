import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";
import { ModelServicesPage } from "./ModelServicesPage";

it("allows offline configured Grok model changes without promoting the connection to default", async () => {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const previous = window.wuu;
  window.wuu = { listEngines: vi.fn().mockResolvedValue({ engines: [] }) } as unknown as WuuDesktopApi;
  const onSave = vi.fn().mockResolvedValue(undefined);
  try {
    await act(async () => root.render(<ModelServicesPage
      initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "local", model: "local-model", providers: [
        { name: "grok", type: "grok-build", model: "grok-4.5", api_key_configured: false, models: [{ id: "grok-4.5" }, { id: "grok-4.6" }] },
      ] }} running={false} runningProviderNames={new Set()} onSave={onSave}
      onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} />));
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
    await act(async () => root.render(<ModelServicesPage initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "saved-codex", model: "chosen-model", providers: [provider] }} running={false} runningProviderNames={new Set()} onSave={save} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} onProvidersChanged={changed} />));
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
    await act(async () => root.render(<ModelServicesPage initialized={{ protocol_version: "wuu-app-server/v0.1", workspace_root: "/tmp/project", provider: "api-stable-id", model: "model-a", providers: [{ name: "api-stable-id", type: "openai", model: "model-a", api_key_configured: true, models: [{ id: "model-a", display_name: "A" }, { id: "model-b", display_name: "B" }] }] }} running={false} runningProviderNames={new Set()} onSave={save} onRemoveProvider={vi.fn()} onRefreshModelCatalog={vi.fn()} />));
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
