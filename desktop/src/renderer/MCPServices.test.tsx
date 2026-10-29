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
import { ConfirmDialogHost } from "./ConfirmDialog";

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
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: "docs", authenticated: false }),
    cancelMCPLogin: vi.fn().mockResolvedValue(undefined),
    cancelMCPAuth: vi.fn().mockResolvedValue(undefined),
    openExternal: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

async function render(source: Controls, props: { pluginID?: string; enabled?: boolean; filter?: string } = {}): Promise<void> {
  await act(async () => root.render(
    <I18nProvider preferenceStore={{ get: () => "en-US", set: async () => undefined, subscribe: () => () => {} }}>
      <WuuUIRoot><SettingsControlsProvider controls={source}><MCPServicesProvider><MCPServices {...props} /></MCPServicesProvider></SettingsControlsProvider><ConfirmDialogHost /></WuuUIRoot>
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
  expect(row().querySelector('button[aria-expanded]:not([aria-haspopup])')).toBeNull();
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
  expect(container.querySelector('button[aria-expanded]:not([aria-haspopup])')).toBeNull();
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
  expect(row().querySelector('button[aria-expanded]:not([aria-haspopup])')).toBeNull();
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
  expect(row().querySelector('button[aria-expanded]:not([aria-haspopup])')).toBeNull();
});

it("reads the current definition when Edit is selected and preserves a rejected stale draft", async () => {
  let revision = "before-menu";
  const source = controls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connected, editable: true }] }),
    readMCPDefinition: vi.fn(async () => ({ config: { url: "https://fixture.example.test/mcp", tool_timeout_ms: revision === "after-menu" ? 35000 : 10000 }, revision })),
    configureMCPServers: vi.fn().mockRejectedValue(new Error("configuration changed")),
  });
  await render(source);
  await click(row().querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!);
  expect(source.readMCPDefinition).not.toHaveBeenCalled();
  revision = "after-menu";
  await click(button(document, "common.edit"));
  expect(source.readMCPDefinition).toHaveBeenCalledOnce();
  const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
  const advanced = dialog.querySelector<HTMLTextAreaElement>("textarea")!;
  expect(JSON.parse(advanced.value).tool_timeout_ms).toBe(35000);
  revision = "after-edit";
  await click(button(dialog, "common.save"));
  expect(source.configureMCPServers).toHaveBeenCalledOnce();
  expect(source.configureMCPServers).toHaveBeenCalledWith(expect.objectContaining({ revision: "after-menu" }));
  expect(container.querySelector('[role="dialog"]')).toBe(dialog);
  expect(JSON.parse(advanced.value).tool_timeout_ms).toBe(35000);
  expect(dialog.querySelector('[role="alert"]')).not.toBeNull();
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
  expect(row().querySelector('button[aria-expanded]:not([aria-haspopup])')).toBeNull();
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
  await click(row().querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!);
  await click(button(document, "mcp.refresh"));
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


const connector: MCPServerStatus = {
  ...stopped, name: "library-service", plugin_id: "example-library", state: "disabled",
  connector: { title: "Research library", description: "Search your research documents.", documentation_url: "https://docs.example.test/connect" },
  plugin_active: true, configurable: true, configuration_enabled: false,
  resource_url: "https://library.example.test/mcp",
};

function connectorRow(): HTMLElement {
  const value = container.querySelector<HTMLElement>('[data-connector="library-service"]');
  expect(value).not.toBeNull();
  return value!;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function connectorControls(overrides: Partial<Controls> = {}): Controls {
  return controls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [connector] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: { enabled: false }, revision: "connector-revision" }),
    configureMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true, state: "stopped" }] }),
    connectMCPServer: vi.fn(),
    disconnectMCPServer: vi.fn().mockResolvedValue({ status: connector }),
    removeMCPAuth: vi.fn().mockResolvedValue({ auth: { name: connector.name, authenticated: false }, server: connector }),
    startMCPLogin: vi.fn().mockResolvedValue({ login_id: "connector-login" }),
    pollMCPLogin: vi.fn().mockResolvedValue({ status: "pending" }),
    ...overrides,
  });
}

it("surfaces plugin-declared lazy connectors separately from generic MCP and in their plugin detail", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [connector, stopped] }) });
  await render(source);
  expect(connectorRow().textContent).toContain(connector.connector!.title);
  expect(container.querySelector('[data-testid="mcp-connectors"]')?.contains(connectorRow())).toBe(true);
  expect(container.querySelector('[data-testid="mcp-services"]')?.contains(row())).toBe(true);
  expect(container.querySelector('[data-testid="mcp-services"]')?.contains(connectorRow())).toBe(false);
  await render(source, { pluginID: connector.plugin_id });
  expect(connectorRow()).not.toBeNull();
  expect(container.querySelector('[data-mcp="docs"]')).toBeNull();
});

it("keeps setup-required connectors informational and opens their documentation through controls", async () => {
  const setup = { ...connector, connector: { ...connector.connector!, setup_required: "Provider admission is required before browser authorization is available." } };
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [setup] }) });
  await render(source);
  expect(connectorRow().textContent).not.toContain(setup.connector.setup_required);
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  expect(connectorRow().textContent).toContain(setup.connector.setup_required);
  expect(connectorRow().querySelector('[data-connector-action="connect"]')).toBeNull();
  expect(connectorRow().querySelector('[data-connector-action="reconnect"]')).toBeNull();
  await click(button(connectorRow(), "connector.documentation"));
  expect(source.openExternal).toHaveBeenCalledWith(setup.connector.documentation_url);
  expect(source.configureMCPServers).not.toHaveBeenCalled();
  expect(source.startMCPLogin).not.toHaveBeenCalled();
});

it("does not activate an inactive owner plugin or allow an unconfigurable connector", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, plugin_active: false, configurable: false }] }) });
  await render(source);
  expect(connectorRow().querySelector('[data-connector-action="connect"]')).toBeNull();
  expect(source.configureMCPServers).not.toHaveBeenCalled();
});

it("saves only the revisioned enable preference before browser authorization and ignores repeated clicks", async () => {
  const saving = deferred<Awaited<ReturnType<Controls["configureMCPServers"]>>>();
  const source = connectorControls({ configureMCPServers: vi.fn(() => saving.promise) });
  await render(source);
  const connect = button(connectorRow(), "mcp.connect");
  await act(async () => { connect.click(); connect.click(); });
  expect(source.readMCPDefinition).toHaveBeenCalledOnce();
  expect(source.configureMCPServers).toHaveBeenCalledExactlyOnceWith({ servers: { [connector.name]: { enabled: true } }, revision: "connector-revision" });
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(connect.disabled).toBe(true);
  await act(async () => saving.resolve({ servers: [{ ...connector, configuration_enabled: true }] }));
  expect(source.startMCPLogin).toHaveBeenCalledExactlyOnceWith(connector.name);
  expect(source.connectMCPServer).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).not.toBeNull();
});

it("starts a fresh login on stable current controls when the connector is already enabled", async () => {
  let pending: string | undefined;
  const source = connectorControls({
    requestMCPConnectorLogin: vi.fn(async () => { pending = connector.name; }),
    getPendingMCPConnectorLogin: () => pending,
    takeMCPConnectorLogin: vi.fn(async () => { pending = undefined; return { ...connector, configuration_enabled: true }; }),
    cancelMCPConnectorLogin: vi.fn(),
  });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  expect(source.startMCPLogin).toHaveBeenCalledExactlyOnceWith(connector.name);
  expect(source.configureMCPServers).not.toHaveBeenCalled();
});

it.each(["commit", "cancel", "replace"])("adopts a prepared connector login only on current controls (%s)", async outcome => {
  const saving = deferred<void>();
  const claim = deferred<MCPServerStatus | undefined>();
  let pending: string | undefined;
  const source = connectorControls({
    requestMCPConnectorLogin: vi.fn(() => { pending = connector.name; return saving.promise; }),
    getPendingMCPConnectorLogin: () => pending,
    takeMCPConnectorLogin: vi.fn().mockResolvedValue(undefined),
    cancelMCPConnectorLogin: vi.fn(() => { pending = undefined; }),
  });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  expect(source.requestMCPConnectorLogin).toHaveBeenCalledExactlyOnceWith(connector.name);
  expect(source.configureMCPServers).not.toHaveBeenCalled();
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).not.toBeNull();
  const next = connectorControls({
    requestMCPConnectorLogin: vi.fn(), getPendingMCPConnectorLogin: () => pending,
    takeMCPConnectorLogin: vi.fn(() => claim.promise),
    cancelMCPConnectorLogin: vi.fn(() => { pending = undefined; }),
  });
  await render(next);
  expect(next.startMCPLogin).not.toHaveBeenCalled();
  if (outcome === "cancel") await click(button(container.querySelector('[role="dialog"]')!, "mcp.cancelLogin"));
  if (outcome === "replace") await render(connectorControls());
  await act(async () => { saving.resolve(); claim.resolve({ ...connector, configuration_enabled: true }); });
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(next.startMCPLogin).toHaveBeenCalledTimes(outcome === "commit" ? 1 : 0);
  if (outcome === "cancel") expect(next.cancelMCPConnectorLogin).toHaveBeenCalledOnce();
});

it.each(["configuration changed", "cannot activate while a turn is running", "cannot activate while a turn is pending"])("does not open OAuth or claim connection after enable fails: %s", async diagnostic => {
  const source = connectorControls({ configureMCPServers: vi.fn().mockRejectedValue(new Error(diagnostic)) });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(connectorRow().getAttribute("data-connected")).toBe("false");
  expect(button(connectorRow(), "mcp.connect").disabled).toBe(false);
  expect((await revealDiagnostic(connectorRow())).textContent).toContain(diagnostic);
});

it("cancels a late browser login start after dismissal without showing a connected state", async () => {
  const starting = deferred<Awaited<ReturnType<Controls["startMCPLogin"]>>>();
  const source = connectorControls({ startMCPLogin: vi.fn(() => starting.promise) });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  await click(button(container.querySelector('[role="dialog"]')!, "mcp.cancelLogin"));
  await act(async () => starting.resolve({ login_id: "late-login" }));
  expect(source.cancelMCPLogin).toHaveBeenCalledWith("late-login");
  expect(source.pollMCPLogin).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  expect(connectorRow().getAttribute("data-connected")).toBe("false");
});

it("does not start OAuth after a pending enable completes in an unmounted catalog", async () => {
  const saving = deferred<Awaited<ReturnType<Controls["configureMCPServers"]>>>();
  const source = connectorControls({ configureMCPServers: vi.fn(() => saving.promise) });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  await act(async () => root.render(<div />));
  await act(async () => saving.resolve({ servers: [connector] }));
  expect(source.startMCPLogin).not.toHaveBeenCalled();
});

it("discards pending connector actions when controls are replaced", async () => {
  const saving = deferred<Awaited<ReturnType<Controls["configureMCPServers"]>>>();
  const previous = connectorControls({ configureMCPServers: vi.fn(() => saving.promise) });
  const next = connectorControls();
  await render(previous);
  await click(button(connectorRow(), "mcp.connect"));
  await render(next);
  await act(async () => saving.resolve({ servers: [connector] }));
  expect(previous.startMCPLogin).not.toHaveBeenCalled();
  expect(next.startMCPLogin).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).toBeNull();
  expect(button(connectorRow(), "mcp.connect").disabled).toBe(false);
});

it("cancels browser authorization instead of restarting it in replacement controls", async () => {
  const previous = connectorControls();
  const next = connectorControls();
  await render(previous);
  await click(button(connectorRow(), "mcp.connect"));
  await render(next);
  expect(previous.cancelMCPLogin).toHaveBeenCalledWith("connector-login");
  expect(next.startMCPLogin).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).toBeNull();
});

it("invalidates pending connector actions on a remote context change even when controls are stable", async () => {
  let changeRemote!: () => void;
  const saving = deferred<Awaited<ReturnType<Controls["configureMCPServers"]>>>();
  const source = connectorControls({
    configureMCPServers: vi.fn(() => saving.promise),
    subscribeRemote: vi.fn(listener => { changeRemote = listener; return vi.fn(); }),
  });
  await render(source);
  await click(button(connectorRow(), "mcp.connect"));
  await act(async () => changeRemote());
  await act(async () => saving.resolve({ servers: [connector] }));
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(button(connectorRow(), "mcp.connect").disabled).toBe(false);
});

it("shows the resource and saved scopes without presenting the authorized service as a verified account", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, auth_status: "oauth", configuration_enabled: true }] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: true, scopes: ["documents:read", "comments:write"] }),
  });
  await render(source);
  expect(source.getMCPAuthStatus).toHaveBeenCalledWith(connector.name);
  expect(connectorRow().querySelector('[data-connector-detail="endpoint"]')).toBeNull();
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  expect(connectorRow().querySelector('[data-connector-detail="endpoint"]')?.textContent).toBe(connector.resource_url);
  expect(connectorRow().querySelector('[data-connector-detail="scopes"]')?.textContent).toContain("documents:read");
  expect(connectorRow().querySelector('[data-connector-detail="scopes"]')?.textContent).toContain("comments:write");
  expect(connectorRow().querySelector('[data-connector-detail="account"]')?.getAttribute("data-verified")).toBe("false");
  expect(connectorRow().getAttribute("data-connected")).toBe("false");
  expect(connectorRow().getAttribute("data-authorized")).toBe("true");
});

const identifiedConnector: MCPServerStatus = {
  ...connector, ...connected, name: connector.name, configuration_enabled: true,
  connector: { ...connector.connector!, identity_probe: {
    tool: "account-self", arguments: {}, result: "single_text_json",
    account: { id: "/user/id", label: "/user/name", email: "/user/email", kind: "/user/type" },
    workspace: { id: "/workspace/id", label: "/workspace/name" },
  } },
};
const verifiedIdentity = {
  state: "verified" as const, verified_at: "2026-10-29T10:00:00Z",
  account: { id: "person-123", label: "Ada <script>safe</script>", email: "ada@example.test", kind: "person" },
  workspace: { id: "workspace-123", label: "Research workspace" },
};

it("shows only provider-verified account and workspace data, as text, without refreshing identity on render", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: true, identity: verifiedIdentity }),
    refreshMCPIdentity: vi.fn(),
  });
  await render(source);
  expect(connectorRow().textContent).toContain(verifiedIdentity.account.label);
  expect(connectorRow().textContent).toContain(verifiedIdentity.workspace.label);
  expect(connectorRow().querySelector("script")).toBeNull();
  expect(source.refreshMCPIdentity).not.toHaveBeenCalled();
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  expect(connectorRow().querySelector('[data-connector-detail="account"]')?.getAttribute("data-verified")).toBe("true");
  expect(connectorRow().querySelector('[data-connector-detail="account-id"]')?.textContent).toBe("person-123");
  expect(connectorRow().querySelector('[data-connector-detail="workspace-id"]')?.textContent).toBe("workspace-123");
  expect(connectorRow().querySelector('[data-connector-detail="email"]')?.textContent).toBe("ada@example.test");
  expect(connectorRow().querySelector('[data-connector-detail="verified-at"]')).not.toBeNull();
  expect(source.startMCPLogin).not.toHaveBeenCalled();
});

it("retries identity without replacing OAuth and keeps a failed identity separate from transport health", async () => {
  const retry = deferred<Awaited<ReturnType<Controls["refreshMCPIdentity"]>>>();
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: true, identity: { state: "unavailable", error_code: "identity_invalid_response" } }),
    refreshMCPIdentity: vi.fn(() => retry.promise),
  });
  await render(source);
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  const button = connectorRow().querySelector<HTMLButtonElement>('[data-connector-action="identity"]')!;
  expect(button).not.toBeNull();
  await act(async () => { button.click(); button.click(); });
  expect(source.refreshMCPIdentity).toHaveBeenCalledExactlyOnceWith(connector.name);
  expect(button.disabled).toBe(true);
  vi.mocked(source.getMCPAuthStatus).mockResolvedValue({ name: connector.name, authenticated: true });
  await act(async () => retry.resolve({ name: connector.name, authenticated: true, identity: { state: "unavailable", error_code: "identity_credentials_unavailable" } }));
  expect(connectorRow().getAttribute("data-connected")).toBe("true");
  expect(connectorRow().querySelector('[data-connector-detail="account"]')?.getAttribute("data-verified")).toBe("false");
  expect(connectorRow().querySelector(".settings-mcp-diagnostic")).not.toBeNull();
  expect(connectorRow().textContent).not.toContain(translateCurrent("mcp.connectionFailed"));
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  expect(source.configureMCPServers).not.toHaveBeenCalled();
  expect(source.removeMCPAuth).not.toHaveBeenCalled();
});

it("discards a late identity result after the connection context changes", async () => {
  const retry = deferred<Awaited<ReturnType<Controls["refreshMCPIdentity"]>>>();
  const previous = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: true }),
    refreshMCPIdentity: vi.fn(() => retry.promise),
  });
  await render(previous);
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  await click(connectorRow().querySelector('[data-connector-action="identity"]')!);
  const next = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }) });
  await render(next);
  await act(async () => retry.resolve({ name: connector.name, authenticated: true, identity: verifiedIdentity }));
  expect(connectorRow().textContent).not.toContain(verifiedIdentity.account.label);
  expect(next.startMCPLogin).not.toHaveBeenCalled();
});

it("does not present a bot as the authorizing person or stale identity as authenticated", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: true, identity: { ...verifiedIdentity, account: { id: "bot-1", kind: "bot" } } }),
  });
  await render(source);
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  expect(connectorRow().querySelector('[data-connector-detail="account"]')?.textContent).toContain("bot");
  expect(connectorRow().querySelector('[data-connector-detail="email"]')).toBeNull();
  await render(connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [identifiedConnector] }),
    getMCPAuthStatus: vi.fn().mockResolvedValue({ name: connector.name, authenticated: false, identity: verifiedIdentity }),
  }));
  expect(connectorRow().textContent).not.toContain(verifiedIdentity.account.label);
  expect(connectorRow().querySelector('[data-connector-action="identity"]')).toBeNull();
});

it("confirms connector disconnect, then stops runtime, removes local credentials and saves disabled", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, ...connected, name: connector.name, configuration_enabled: true }] }) });
  await render(source);
  await click(button(connectorRow(), "mcp.disconnect"));
  expect(source.disconnectMCPServer).not.toHaveBeenCalled();
  await click(document.querySelector('[data-confirm-action="cancel"]')!);
  expect(source.removeMCPAuth).not.toHaveBeenCalled();
  expect(source.configureMCPServers).not.toHaveBeenCalled();
  await click(button(connectorRow(), "mcp.disconnect"));
  await click(document.querySelector('[data-confirm-action="confirm"]')!);
  expect(source.disconnectMCPServer).toHaveBeenCalledWith(connector.name);
  expect(source.removeMCPAuth).toHaveBeenCalledWith(connector.name);
  expect(source.configureMCPServers).toHaveBeenCalledExactlyOnceWith({ servers: { [connector.name]: { enabled: false } }, revision: "connector-revision" });
});

it("reports a partial disconnect failure and still attempts credential cleanup and disabling", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true }] }),
    disconnectMCPServer: vi.fn().mockRejectedValue(new Error("runtime stop failed")),
  });
  await render(source);
  await click(button(connectorRow(), "mcp.disconnect"));
  await click(document.querySelector('[data-confirm-action="confirm"]')!);
  expect(source.removeMCPAuth).toHaveBeenCalledWith(connector.name);
  expect(source.configureMCPServers).toHaveBeenCalledWith({ servers: { [connector.name]: { enabled: false } }, revision: "connector-revision" });
  expect((await revealDiagnostic(connectorRow())).textContent).toContain("runtime stop failed");
});

it("confirms reconnect without removing the existing authorization before replacement succeeds", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true, auth_status: "oauth" }] }) });
  await render(source);
  await click(button(connectorRow(), "connector.reconnect"));
  expect(source.startMCPLogin).not.toHaveBeenCalled();
  await click(document.querySelector('[data-confirm-action="confirm"]')!);
  expect(source.startMCPLogin).toHaveBeenCalledWith(connector.name);
  expect(source.removeMCPAuth).not.toHaveBeenCalled();
});

it("preserves generic MCP disconnect semantics without deleting credentials or saved configuration", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [connected] }) });
  await render(source);
  await click(button(row(), "mcp.disconnect"));
  expect(source.disconnectMCPServer).toHaveBeenCalledWith("docs");
  expect(source.removeMCPAuth).not.toHaveBeenCalled();
  expect(source.configureMCPServers).not.toHaveBeenCalled();
});


it("dismisses an owned confirmation on controls replacement without mutating either context", async () => {
  const previous = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true }] }) });
  const next = connectorControls();
  await render(previous);
  await click(button(connectorRow(), "mcp.disconnect"));
  expect(document.querySelector('[data-confirm-action="confirm"]')).not.toBeNull();
  await render(next);
  expect(document.querySelector('[data-confirm-action="confirm"]')).toBeNull();
  expect(previous.disconnectMCPServer).not.toHaveBeenCalled();
  expect(next.disconnectMCPServer).not.toHaveBeenCalled();
});

it("does not revive a pending action after navigating away and back to the same controls object", async () => {
  const saving = deferred<Awaited<ReturnType<Controls["configureMCPServers"]>>>();
  const original = connectorControls({ configureMCPServers: vi.fn(() => saving.promise) });
  await render(original);
  await click(button(connectorRow(), "mcp.connect"));
  await render(connectorControls());
  await render(original);
  await act(async () => saving.resolve({ servers: [connector] }));
  expect(original.startMCPLogin).not.toHaveBeenCalled();
  expect(container.querySelector('[role="dialog"]')).toBeNull();
});

it("does not treat unreadable credentials or runtime startup as verified authorization or connection", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true, state: "starting" }] }),
    getMCPAuthStatus: vi.fn().mockRejectedValue(new Error("credential store unavailable")),
  });
  await render(source);
  expect(connectorRow().getAttribute("data-connected")).toBe("false");
  expect(connectorRow().getAttribute("data-authorized")).toBe("unknown");
  expect(connectorRow().querySelector('[data-connector-detail="scopes"]')).toBeNull();
  await click(connectorRow().querySelector("[data-connector-details-toggle]")!);
  expect((await revealDiagnostic(connectorRow())).textContent).toContain("credential store unavailable");
});

it.each(["javascript:alert(1)", "file:///private/setup", "http://docs.example.test/setup"])("does not launch unsafe connector documentation URL: %s", async url => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, connector: { ...connector.connector!, documentation_url: url, setup_required: "Registration unavailable" } }] }) });
  await render(source);
  expect(connectorRow().querySelector('[data-connector-action="documentation"]')).toBeNull();
  expect(source.openExternal).not.toHaveBeenCalled();
});

it("preserves a failed disabled-preference save as an incomplete disconnect after runtime and credential cleanup", async () => {
  const source = connectorControls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configuration_enabled: true }] }),
    configureMCPServers: vi.fn().mockRejectedValue(new Error("configuration changed during disconnect")),
  });
  await render(source);
  await click(button(connectorRow(), "mcp.disconnect"));
  await click(document.querySelector('[data-confirm-action="confirm"]')!);
  expect(source.disconnectMCPServer).toHaveBeenCalledWith(connector.name);
  expect(source.removeMCPAuth).toHaveBeenCalledWith(connector.name);
  expect((await revealDiagnostic(connectorRow())).textContent).toContain("configuration changed during disconnect");
  expect(button(connectorRow(), "mcp.disconnect").disabled).toBe(false);
});


it("does not expose a retired connector while replacement controls are still loading", async () => {
  const loading = deferred<Awaited<ReturnType<Controls["listMCPServers"]>>>();
  const previous = connectorControls();
  const next = connectorControls({ listMCPServers: vi.fn(() => loading.promise) });
  await render(previous);
  await render(next);
  expect(container.querySelector('[data-connector="library-service"]')).toBeNull();
  await act(async () => loading.resolve({ servers: [] }));
  expect(next.configureMCPServers).not.toHaveBeenCalled();
});

it("treats whitespace-only setup metadata as no setup restriction", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, connector: { ...connector.connector!, setup_required: "  \n  " } }] }) });
  await render(source);
  expect(button(connectorRow(), "mcp.connect").disabled).toBe(false);
});

it("keeps standalone metadata-bearing MCP editable and preserves its descriptor when saving", async () => {
  const descriptor = { title: "Standalone service", description: "User-managed server" };
  const definition = { url: "https://standalone.example.test/mcp", enabled: true, connector: descriptor };
  const source = controls({
    listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connected, connector: descriptor, editable: true }] }),
    readMCPDefinition: vi.fn().mockResolvedValue({ config: definition, revision: "standalone-revision" }),
    configureMCPServers: vi.fn().mockResolvedValue({ servers: [connected] }),
  });
  await render(source);
  expect(container.querySelector('[data-connector]')).toBeNull();
  await click(row().querySelector('[aria-haspopup="menu"]')!);
  await click(button(document, "common.edit"));
  await click(button(container.querySelector('[role="dialog"]')!, "common.save"));
  expect(source.configureMCPServers).toHaveBeenCalledWith({ servers: { docs: definition }, revision: "standalone-revision" });
  expect(container.querySelector('[role="dialog"]')).toBeNull();
});


it("explains a context-unavailable connector in details without offering activation", async () => {
  const reason = "This host does not own project MCP configuration.";
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, configurable: false, configuration_unavailable_reason: reason }] }) });
  await render(source);
  expect(connectorRow().querySelector('[data-connector-action="connect"]')).toBeNull();
  expect(source.getMCPAuthStatus).not.toHaveBeenCalled();
  expect(connectorRow().textContent).not.toContain(reason);
  await click(connectorRow().querySelector('[data-connector-details-toggle]')!);
  expect(connectorRow().textContent).toContain(reason);
});

it("does not read credentials for a connector excluded from the active plugin composition", async () => {
  const source = connectorControls({ listMCPServers: vi.fn().mockResolvedValue({ servers: [{ ...connector, plugin_active: false, configurable: false }] }) });
  await render(source);
  expect(connectorRow().querySelector('[data-connector-action="connect"]')).toBeNull();
  expect(source.getMCPAuthStatus).not.toHaveBeenCalled();
  await click(connectorRow().querySelector('[data-connector-details-toggle]')!);
  expect(connectorRow().getAttribute("data-authorized")).toBe("unknown");
});
