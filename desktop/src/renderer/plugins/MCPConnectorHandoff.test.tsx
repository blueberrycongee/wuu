import "../test-support/AbortSignal";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, WuuDesktopApi } from "../../shared/protocol";
import { DesktopPluginRuntime } from "./DesktopPluginRuntime";
import { PluginHost } from "./PluginHost";
import { ApplicationShellOutlet } from "./ApplicationShell";
import { activate } from "./DefaultApplicationProvider";
import * as RuntimeLoader from "../RuntimeLoadState";
import { MCPServices, MCPServicesProvider } from "../MCPServices";
import { SettingsControlsProvider } from "../SettingsControlsContext";
import { I18nProvider, translateCurrent } from "../i18n";
import { WuuUIRoot } from "../ui/layers/UILayerHost";

it.each(["config", "config-cancel", "consumed", "start", "start-cancel", "poll", "poll-success"])("carries a real default-provider connector login through replacement at %s", async stage => {
  const cancel = stage === "config-cancel";
  let replacementAfterConsume: Promise<unknown> | undefined;
  const original = window.wuu;
  const phases: Record<string, unknown>[] = [];
  const trace = (event: Event) => {
    phases.push((event as CustomEvent).detail);
    if (stage === "consumed" && (event as CustomEvent).detail.phase === "consumed" && !replacementAfterConsume) {
      graph = "gen-11-5578-12";
      replacementAfterConsume = runtime.sync(inventory());
    }
  };
  window.addEventListener("wuu:mcp-connector-handoff", trace);
  let graph = "gen-8-5578-9", enabled = false, release!: () => void;
  const receipt = new Promise<void>(resolve => { release = resolve; });
  let releaseStart!: () => void, releasePoll!: (value: { status: "pending" | "success" }) => void;
  const startReceipt = new Promise<void>(resolve => { releaseStart = resolve; });
  const pollReceipt = new Promise<{ status: "pending" | "success" }>(resolve => { releasePoll = resolve; });
  const context = { kind: "no_project" as const, cwd: "/fixture/project" };
  const plugin = (id: string, desktop = true): ExtensionInventoryRecord => ({
    id: `plugin:user:${id}`, name: id, kind: "plugin", graph_generation: graph, fingerprint: `${id}-source`,
    provenance: { kind: "plugin", source: "wuu", scope: "user", plugin_id: id, path: `/fixture/plugins/${id}/plugin.json` },
    state: "granted", approval_state: "granted", runtime_state: "active", enabled: true, enabled_override: true,
    package_source: "user", ...(desktop ? { desktop: { entry: "desktop.js" } } : {}),
  });
  const inventory = () => [plugin("business"), { ...plugin("shell"), requires: ["business"], resolved_required_dependencies: ["business"] }, plugin("mcp-journey", false)];
  const server = () => ({ name: "plugin.mcp-journey.account", plugin_id: "mcp-journey", plugin_active: true, configurable: true,
    connector: { title: "Fixture workspace" }, resource_url: "https://fixture.example.test/mcp", configuration_enabled: enabled,
    state: enabled ? "auth_required" : "disabled", error: enabled ? "HTTP 401 Unauthorized" : undefined, connected: false, tool_count: 0 });
  const host = new PluginHost({ react: React });
  const runtime = new DesktopPluginRuntime(host, async id => id.endsWith(":business") ? { activate } : {
    activatePresentation(api: import("../../../../packages/plugin-sdk/src/ui").ApplicationPresentationAPI) {
      api.registerApplicationShell({ id: "ui.shell", render: ({ application }) =>
        <SettingsControlsProvider controls={application.settingsControls}><MCPServicesProvider contextKey="fixture-source">
          <MCPServices />
        </MCPServicesProvider></SettingsControlsProvider> });
    },
  });
  let publication: Promise<unknown> | undefined;
  window.wuu = {
    listProjects: vi.fn(async () => ({ projects: [], active_context: context })), onServerEvent: () => () => {}, onRemoteControlEvent: () => () => {},
    listUserQuestions: vi.fn().mockResolvedValue({ requests: [] }),
    loadPluginDesktopModule: vi.fn(async ({ id, fingerprint }) => ({ id, fingerprint, digest: "a".repeat(64), url: id })),
    reportPluginDesktopActivation: vi.fn(async () => {}),
    listMCPServers: vi.fn(async () => ({ servers: [server()] })),
    getMCPAuthStatus: vi.fn(async () => ({ name: server().name, authenticated: false })),
    readMCPDefinition: vi.fn(async () => ({ config: { enabled }, revision: "original-revision" })),
    configureMCPServers: vi.fn(async () => {
      enabled = true; graph = "gen-9-5578-10";
      publication = runtime.sync(inventory());
      await publication; await receipt;
      return { servers: [server()] };
    }),
    startMCPLogin: vi.fn(async () => { if (stage.startsWith("start")) await startReceipt; return { login_id: "fixture-login" }; }),
    pollMCPLogin: vi.fn(async () => stage.startsWith("poll") ? pollReceipt : { status: "pending" }), cancelMCPLogin: vi.fn(async () => ({ ok: true })),
  } as unknown as WuuDesktopApi;
  vi.spyOn(RuntimeLoader, "loadRuntime").mockImplementation(async () => ({ activeContext: context, projects: [], threads: [],
    initialized: { provider: "fixture", model: "fixture", providers: [], extension_inventory: inventory() } as never, status: "ready" }));
  const element = document.createElement("div"), root = createRoot(element); document.body.append(element);
  try {
    await host.selectApplicationBinding({ pluginId: plugin("business").id, generation: plugin("business").fingerprint!, id: "ui.application" });
    expect(await runtime.sync(inventory())).toEqual([]);
    await act(async () => root.render(<I18nProvider preferenceStore={{ get: () => "en-US", set: async () => undefined, subscribe: () => () => {} }}>
      <WuuUIRoot><ApplicationShellOutlet host={host} binding={{ pluginId: plugin("shell").id, generation: plugin("shell").fingerprint!, id: "ui.shell" }} /></WuuUIRoot>
    </I18nProvider>));
    await act(async () => { element.querySelector<HTMLButtonElement>('[data-connector-action="connect"]')!.click(); });
    await act(async () => { await vi.waitFor(() => expect(publication).toBeDefined()); await publication; });
    // A second live graph replacement must observe, not repeat, the original write.
    graph = "gen-10-5578-11";
    await act(async () => { expect(await runtime.sync(inventory())).toEqual([]); });
    expect(window.wuu.startMCPLogin).not.toHaveBeenCalled();
    if (cancel) await act(async () => {
      [...element.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button => button.textContent === translateCurrent("mcp.cancelLogin"))!.click();
    });
    await act(async () => { release(); });
    await vi.waitFor(() => expect(window.wuu.configureMCPServers).toHaveBeenCalledOnce());
    expect(window.wuu.startMCPLogin).toHaveBeenCalledTimes(cancel ? 0 : 1);
    if (!["config", "config-cancel"].includes(stage)) {
      if (stage === "consumed") {
        await act(async () => { await replacementAfterConsume; });
      } else {
        if (stage.startsWith("poll")) await vi.waitFor(() => expect(window.wuu.pollMCPLogin).toHaveBeenCalledOnce());
        graph = "gen-11-5578-12";
        await act(async () => { expect(await runtime.sync(inventory())).toEqual([]); });
      }
      await act(async () => { await vi.waitFor(() => expect(element.querySelector('[role="dialog"]')).not.toBeNull()); });
      expect(window.wuu.startMCPLogin).toHaveBeenCalledOnce();
      expect(window.wuu.cancelMCPLogin).not.toHaveBeenCalled();
      if (stage === "start-cancel") await act(async () => {
        [...element.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button => button.textContent === translateCurrent("mcp.cancelLogin"))!.click();
      });
      await act(async () => { releaseStart(); releasePoll({ status: stage === "poll-success" ? "success" : "pending" }); });
      if (stage === "start-cancel") {
        await vi.waitFor(() => expect(window.wuu.cancelMCPLogin).toHaveBeenCalledExactlyOnceWith("fixture-login"));
        expect(window.wuu.pollMCPLogin).not.toHaveBeenCalled();
        expect(element.querySelector('[role="dialog"]')).toBeNull();
      } else if (stage === "poll-success") {
        await vi.waitFor(() => expect(element.querySelector('[role="dialog"]')).toBeNull());
        expect(window.wuu.pollMCPLogin).toHaveBeenCalledOnce();
      } else {
        await act(async () => { await vi.waitFor(() => expect(element.textContent).toContain(translateCurrent("mcp.browserWaiting"))); });
        if (stage === "poll") expect(window.wuu.pollMCPLogin).toHaveBeenCalledOnce();
        await act(async () => {
          [...element.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(button => button.textContent === translateCurrent("mcp.cancelLogin"))!.click();
        });
        await vi.waitFor(() => expect(window.wuu.cancelMCPLogin).toHaveBeenCalledExactlyOnceWith("fixture-login"));
      }
    }
    expect(phases.filter(value => value.phase === "consumed")).toHaveLength(cancel ? 0 : 1);
    expect(phases.filter(value => value.phase === "checkpoint" && value.pending === true).length).toBeGreaterThanOrEqual(2);
    expect(phases.every(value => Object.entries(value).every(([key, item]) => key === "phase" ? typeof item === "string" : typeof item === "boolean"))).toBe(true);
    expect(element.textContent).not.toContain(translateCurrent("mcp.connectionFailed"));
  } finally {
    release(); releaseStart(); releasePoll({ status: "pending" }); await act(async () => root.unmount()); element.remove(); await runtime.shutdown();
    window.wuu = original; vi.restoreAllMocks();
    window.removeEventListener("wuu:mcp-connector-handoff", trace);
  }
});
