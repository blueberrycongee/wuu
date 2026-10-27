import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ApplicationSettingsControlsV1 as Controls } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import type { MCPServerStatus } from "../shared/protocol";
import { MCPServices, MCPServicesProvider } from "./MCPServices";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { createApplicationSettingsControls } from "./plugins/hostApplicationSettingsControls";
import { I18nProvider, translateCurrent } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

let container: HTMLDivElement;
let root: Root;
const stopped: MCPServerStatus = { name: "docs", state: "stopped", connected: false, tool_count: 0 };
const authFailure = "mcp initialize: HTTP 401 Unauthorized (explicit HTTP transport; no SSE fallback)";
const authRequired: MCPServerStatus = { ...stopped, state: "auth_required", auth_status: "not_logged_in", error: authFailure };
const connected: MCPServerStatus = { ...stopped, state: "ready", connected: true, auth_status: "oauth", tool_count: 2 };

beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.restoreAllMocks();
});

function controls(overrides: Partial<Controls> = {}): Controls {
  const base = createApplicationSettingsControls();
  return {
    ...base,
    capabilities: { ...base.capabilities, mcpBrowserLogin: true },
    listMCPServers: vi.fn().mockResolvedValue({ servers: [] }),
    subscribe: vi.fn(() => vi.fn()),
    subscribeRemote: vi.fn(() => vi.fn()),
    cancelMCPLogin: vi.fn().mockResolvedValue(undefined),
    cancelMCPAuth: vi.fn().mockResolvedValue(undefined),
    openExternal: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

async function render(source: Controls): Promise<void> {
  await act(async () => root.render(
    <I18nProvider preferenceStore={{ get: () => "en-US", set: async () => undefined, subscribe: () => () => {} }}>
      <WuuUIRoot><SettingsControlsProvider controls={source}><MCPServicesProvider><MCPServices /></MCPServicesProvider></SettingsControlsProvider></WuuUIRoot>
    </I18nProvider>,
  ));
}

function row(): HTMLElement {
  return container.querySelector<HTMLElement>('[data-mcp="docs"]')!;
}

function button(scope: ParentNode, key: Parameters<typeof translateCurrent>[0]): HTMLButtonElement {
  const value = [...scope.querySelectorAll<HTMLButtonElement>("button")].find(candidate => candidate.textContent?.trim() === translateCurrent(key));
  expect(value).toBeDefined();
  return value!;
}

async function click(target: HTMLElement): Promise<void> {
  await act(async () => target.click());
}

async function revealDiagnostic(scope: ParentNode): Promise<HTMLPreElement> {
  const disclosure = [...scope.querySelectorAll<HTMLButtonElement>('button[aria-expanded="false"]')]
    .find(candidate => candidate.textContent?.trim() === translateCurrent("mcp.technicalDetails"));
  expect(disclosure).toBeDefined();
  await click(disclosure!);
  const diagnostic = scope.querySelector<HTMLPreElement>("pre");
  expect(diagnostic).not.toBeNull();
  expect(diagnostic!.id).toBe(disclosure!.getAttribute("aria-controls"));
  return diagnostic!;
}

it("keeps catalog authentication diagnostics opt-in and beside their owning service", async () => {
  const source = controls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [authRequired, { ...connected, name: "healthy" }] }) });
  await render(source);
  expect(container.textContent).not.toContain(authFailure);
  expect(button(row(), "mcp.oauthLogin").disabled).toBe(false);
  const diagnostic = await revealDiagnostic(row());
  expect(diagnostic.textContent).toBe(authFailure);
  expect(container.querySelectorAll("pre")).toHaveLength(1);
  expect(container.querySelector('[data-mcp="healthy"] pre')).toBeNull();
  expect(container.querySelector('[role="alert"]')?.textContent ?? "").not.toContain(authFailure);
});

it("deduplicates a rejected connection against refreshed authoritative auth state", async () => {
  const source = controls({
    listMCPServers: vi.fn().mockResolvedValueOnce({ servers: [stopped] }).mockResolvedValue({ servers: [authRequired] }),
    connectMCPServer: vi.fn().mockRejectedValue(new Error(`Error invoking remote method 'wuu:mcp-connect': ${authFailure}`)),
  });
  await render(source);
  await click(button(row(), "mcp.connect"));
  expect(source.connectMCPServer).toHaveBeenCalledWith("docs");
  expect(source.listMCPServers).toHaveBeenCalledTimes(2);
  expect(button(row(), "mcp.oauthLogin").disabled).toBe(false);
  expect(container.textContent).not.toContain(authFailure);
  await revealDiagnostic(row());
  expect(container.querySelectorAll("pre")).toHaveLength(1);
  expect(row().querySelector("pre")?.textContent).toContain(authFailure);
  expect([...container.querySelectorAll('[role="alert"]')].filter(alert => !row().contains(alert))).toHaveLength(0);
});

it("clears a failed connection on retry success without changing another service", async () => {
  let servers = [stopped, { ...connected, name: "healthy" }];
  let resolveRetry!: (result: { status: MCPServerStatus }) => void;
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers })),
    connectMCPServer: vi.fn().mockRejectedValueOnce(new Error("connect ECONNREFUSED"))
      .mockImplementationOnce(() => new Promise(resolve => { resolveRetry = resolve; })),
  });
  await render(source);
  await click(button(row(), "mcp.connect"));
  expect(container.textContent).not.toContain("ECONNREFUSED");
  expect((await revealDiagnostic(row())).textContent).toContain("ECONNREFUSED");
  await click(button(row(), "mcp.connect"));
  expect(button(row(), "mcp.connect").disabled).toBe(true);
  expect(row().querySelector("pre")).toBeNull();
  servers = [connected, servers[1]];
  await act(async () => resolveRetry({ status: connected }));
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('button[aria-expanded]')).toBeNull();
  expect(container.textContent).not.toContain("ECONNREFUSED");
  expect(container.querySelector('[data-mcp="healthy"]')).not.toBeNull();
});

it("keeps list failures concise and recoverable rather than presenting an empty inventory", async () => {
  const source = controls({ listMCPServers: vi.fn().mockRejectedValueOnce(new Error("raw catalog transport unavailable")).mockResolvedValue({ servers: [connected] }) });
  await render(source);
  expect(container.textContent).not.toContain("raw catalog transport unavailable");
  expect(container.querySelector('[role="alert"], [role="status"]')).not.toBeNull();
  const retry = container.querySelector<HTMLButtonElement>('[data-testid="mcp-list-retry"]');
  expect(retry).not.toBeNull();
  await click(retry!);
  expect(source.listMCPServers).toHaveBeenCalledTimes(2);
  expect(row()).not.toBeNull();
  expect(container.querySelector('[data-testid="mcp-list-retry"]')).toBeNull();
  expect(container.querySelector('button[aria-expanded]')).toBeNull();
});

it("retries a failed browser login and cancels the new pending attempt on dismissal", async () => {
  const source = controls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [authRequired] }),
    startMCPLogin: vi.fn().mockResolvedValueOnce({ login_id: "failed-login" }).mockResolvedValueOnce({ login_id: "retry-login" }),
    pollMCPLogin: vi.fn().mockResolvedValueOnce({ status: "error", error: "OAuth poll HTTP 503 transport diagnostic" }).mockResolvedValue({ status: "pending" }),
  });
  await render(source);
  await click(button(row(), "mcp.oauthLogin"));
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  expect(dialog).not.toBeNull();
  expect(dialog.querySelector('[role="alert"]')).not.toBeNull();
  expect(dialog.textContent).not.toContain("OAuth poll HTTP 503");
  expect((await revealDiagnostic(dialog)).textContent).toContain("OAuth poll HTTP 503");
  const retry = dialog.querySelector<HTMLButtonElement>('[data-testid="mcp-login-retry"]');
  expect(retry).not.toBeNull();
  await click(retry!);
  expect(source.startMCPLogin).toHaveBeenCalledTimes(2);
  expect(source.cancelMCPLogin).toHaveBeenCalledWith("failed-login");
  expect(dialog.querySelector("pre")).toBeNull();
  expect(dialog.querySelector('[role="alert"]')).toBeNull();
  await click(button(dialog, "mcp.cancelLogin"));
  expect(source.cancelMCPLogin).toHaveBeenCalledWith("retry-login");
  expect(container.querySelector('[role="dialog"]')).toBeNull();
});

it("clears an earlier row action failure only after browser login succeeds", async () => {
  let servers = [stopped];
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers })),
    connectMCPServer: vi.fn(async () => { servers = [authRequired]; throw new Error(authFailure); }),
    startMCPLogin: vi.fn().mockResolvedValue({ login_id: "successful-login" }),
    pollMCPLogin: vi.fn(async () => { servers = [connected]; return { status: "success" as const }; }),
  });
  await render(source);
  await click(button(row(), "mcp.connect"));
  await revealDiagnostic(row());
  await click(button(row(), "mcp.oauthLogin"));
  expect(source.pollMCPLogin).toHaveBeenCalledWith("successful-login");
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('button[aria-expanded]')).toBeNull();
  expect(container.textContent).not.toContain(authFailure);
});

it("retries connection after manual authorization without exchanging a consumed code twice", async () => {
  const manualServer = { ...authRequired, oauth_redirect_uri: "https://callback.example.test/mcp" };
  let servers = [manualServer as MCPServerStatus];
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers })),
    startMCPAuth: vi.fn().mockResolvedValue({ authorization_url: "https://auth.example.test/authorize", state: "manual-state" }),
    finishMCPAuth: vi.fn().mockResolvedValue({ auth: { name: "docs", authenticated: true }, server: { ...stopped, auth_status: "oauth" } }),
    connectMCPServer: vi.fn().mockRejectedValueOnce(new Error("post-authorization connection HTTP 503 diagnostic"))
      .mockImplementationOnce(async () => { servers = [connected]; return { status: connected }; }),
  });
  await render(source);
  await click(button(row(), "mcp.oauthLogin"));
  expect(source.openExternal).toHaveBeenCalledWith("https://auth.example.test/authorize");
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const field = dialog.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "https://callback.example.test/mcp?state=manual-state&code=one-use-code");
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(button(dialog, "mcp.finishLogin"));
  expect(source.finishMCPAuth).toHaveBeenCalledWith("docs", "manual-state", "one-use-code");
  expect(dialog.querySelector('[role="alert"]')).not.toBeNull();
  expect(dialog.textContent).not.toContain("post-authorization connection HTTP 503");
  expect((await revealDiagnostic(dialog)).textContent).toContain("post-authorization connection HTTP 503");
  await click(button(dialog, "mcp.connect"));
  expect(source.finishMCPAuth).toHaveBeenCalledOnce();
  expect(source.connectMCPServer).toHaveBeenCalledTimes(2);
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('button[aria-expanded]')).toBeNull();
});

it("preserves an MCP editor draft after a save rejection and clears diagnostics when retry succeeds", async () => {
  const diagnostic = "persistence HTTP 503 detail";
  const source = controls({
    configureMCPServers: vi.fn().mockRejectedValueOnce(new Error(`Error invoking remote method 'wuu:mcp-configure': ${diagnostic}`)).mockResolvedValueOnce({ servers: [stopped] }),
  });
  await render(source);
  await click(button(container, "mcp.add"));
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const name = dialog.querySelector<HTMLInputElement>('input:not([type])')!;
  const url = dialog.querySelector<HTMLInputElement>('input[type="url"]')!;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    setter.call(name, "docs");
    name.dispatchEvent(new Event("input", { bubbles: true }));
    setter.call(url, "https://mcp.example.test/service");
    url.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(button(dialog, "common.save"));
  expect(dialog.querySelector('[role="alert"]')).not.toBeNull();
  expect(dialog.textContent).not.toContain(diagnostic);
  expect((await revealDiagnostic(dialog)).textContent).toContain("persistence HTTP 503 detail");
  expect(name.value).toBe("docs");
  expect(url.value).toBe("https://mcp.example.test/service");
  await click(button(dialog, "common.save"));
  expect(source.configureMCPServers).toHaveBeenCalledTimes(2);
  expect(source.configureMCPServers).toHaveBeenLastCalledWith({ servers: { docs: { enabled: true, url: "https://mcp.example.test/service" } }, revision: undefined });
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  expect(container.textContent).not.toContain(diagnostic);
});

it("returns the latest post-authorization connection failure to the service row when login is dismissed", async () => {
  const latestDiagnostic = "new connection HTTP 503 diagnostic";
  const manualServer = { ...authRequired, oauth_redirect_uri: "https://callback.example.test/mcp" };
  let servers = [stopped];
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers })),
    startMCPAuth: vi.fn().mockResolvedValue({ authorization_url: "https://auth.example.test/authorize", state: "manual-state" }),
    finishMCPAuth: vi.fn().mockResolvedValue({ auth: { name: "docs", authenticated: true }, server: { ...stopped, auth_status: "oauth" } }),
    connectMCPServer: vi.fn()
      .mockImplementationOnce(async () => { servers = [manualServer]; throw new Error(authFailure); })
      .mockImplementationOnce(async () => { servers = [{ ...stopped, state: "error", auth_status: "oauth", error: latestDiagnostic }]; throw new Error(latestDiagnostic); }),
  });
  await render(source);
  await click(button(row(), "mcp.connect"));
  await click(button(row(), "mcp.oauthLogin"));
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const field = dialog.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "new-auth-code");
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(button(dialog, "mcp.finishLogin"));
  expect(source.finishMCPAuth).toHaveBeenCalledOnce();
  expect(source.connectMCPServer).toHaveBeenCalledTimes(2);
  await click(button(dialog, "mcp.cancelLogin"));
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  const diagnostic = await revealDiagnostic(row());
  expect(diagnostic.textContent).toContain(latestDiagnostic);
  expect(diagnostic.textContent).not.toContain(authFailure);
  expect(container.querySelectorAll("pre")).toHaveLength(1);
});

it("does not let a retired editor save close or refresh the replacement controls scope", async () => {
  let resolveSave!: (result: Awaited<ReturnType<Controls["configureMCPServers"]>>) => void;
  const previous = controls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [stopped] }),
    configureMCPServers: vi.fn(() => new Promise<Awaited<ReturnType<Controls["configureMCPServers"]>>>(resolve => { resolveSave = resolve; })),
  });
  const next = controls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connected, name: "new-scope" }] }) });
  await render(previous);
  await click(button(container, "mcp.add"));
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const name = dialog.querySelector<HTMLInputElement>('input:not([type])')!;
  const url = dialog.querySelector<HTMLInputElement>('input[type="url"]')!;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    setter.call(name, "draft");
    name.dispatchEvent(new Event("input", { bubbles: true }));
    setter.call(url, "https://mcp.example.test/draft");
    url.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(button(dialog, "common.save"));
  expect(previous.configureMCPServers).toHaveBeenCalledOnce();
  await render(next);
  await act(async () => resolveSave({ servers: [{ ...stopped, name: "draft" }] }));
  expect(container.querySelector('[role="dialog"]')).toBe(dialog);
  expect(name.value).toBe("draft");
  expect(container.querySelector('[data-mcp="new-scope"]')).not.toBeNull();
  expect(container.querySelector('[data-mcp="docs"]')).toBeNull();
  expect(previous.listMCPServers).toHaveBeenCalledOnce();
  expect(next.listMCPServers).toHaveBeenCalledOnce();
});

it("clears a retained auth failure when a dismissed pending login connection later becomes ready", async () => {
  const manualServer = { ...authRequired, oauth_redirect_uri: "https://callback.example.test/mcp" };
  let servers = [stopped];
  let notifyStatus!: () => void;
  let resolveConnection!: (result: Awaited<ReturnType<Controls["connectMCPServer"]>>) => void;
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers })),
    subscribe: vi.fn((listener: () => void) => { notifyStatus = listener; return vi.fn(); }),
    startMCPAuth: vi.fn().mockResolvedValue({ authorization_url: "https://auth.example.test/authorize", state: "manual-state" }),
    finishMCPAuth: vi.fn().mockResolvedValue({ auth: { name: "docs", authenticated: true }, server: { ...stopped, auth_status: "oauth" } }),
    connectMCPServer: vi.fn()
      .mockImplementationOnce(async () => { servers = [manualServer]; throw new Error(authFailure); })
      .mockImplementationOnce(() => {
        servers = [{ ...stopped, state: "starting", auth_status: "oauth" }];
        return new Promise<Awaited<ReturnType<Controls["connectMCPServer"]>>>(resolve => { resolveConnection = resolve; });
      }),
  });
  await render(source);
  await click(button(row(), "mcp.connect"));
  await click(button(row(), "mcp.oauthLogin"));
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const field = dialog.querySelector<HTMLInputElement>("input")!;
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "new-auth-code");
    field.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await click(button(dialog, "mcp.finishLogin"));
  expect(source.finishMCPAuth).toHaveBeenCalledOnce();
  expect(source.connectMCPServer).toHaveBeenCalledTimes(2);
  await click(button(dialog, "mcp.cancelLogin"));
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  servers = [connected];
  await act(async () => resolveConnection({ status: connected }));
  await act(async () => notifyStatus());
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('button[aria-expanded]')).toBeNull();
  expect(row().querySelector('[role="status"]')).toBeNull();
  expect(container.textContent).not.toContain(authFailure);
});

it("retains a failed refresh operation when authoritative status still reports a ready connection", async () => {
  const diagnostic = "tool refresh HTTP 503 diagnostic";
  let notifyStatus!: () => void;
  const source = controls({
    listMCPServers: vi.fn(async () => ({ servers: [{ ...connected }] })),
    subscribe: vi.fn((listener: () => void) => { notifyStatus = listener; return vi.fn(); }),
    refreshMCPServer: vi.fn().mockRejectedValue(new Error(diagnostic)),
  });
  await render(source);
  await click(button(row(), "mcp.refresh"));
  expect(source.refreshMCPServer).toHaveBeenCalledWith("docs");
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('[role="status"]')).not.toBeNull();
  expect(row().textContent).not.toContain(diagnostic);
  expect((await revealDiagnostic(row())).textContent).toContain(diagnostic);
  await act(async () => notifyStatus());
  expect(button(row(), "mcp.disconnect").disabled).toBe(false);
  expect(row().querySelector('[role="status"]')).not.toBeNull();
  expect(row().querySelector("pre")?.textContent).toContain(diagnostic);
});
