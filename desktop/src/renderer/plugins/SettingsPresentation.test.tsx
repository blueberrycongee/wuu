// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { InitializeResult } from "../../shared/protocol";
import { SETTINGS_ACTIONS, type PresentationHost, type SettingsSnapshotV1 } from "../../shared/workbench";
import { WorkbenchController } from "./Workbench";
import { desktopPluginHost } from "./DesktopPluginRuntime";
import {
  createSettingsSnapshot,
  SettingsPresentation,
  SETTINGS_UPDATE_KEYS,
} from "./SettingsPresentation";

const TEST_PLUGIN_IDS = ["test:settings-presenter", "test:settings-failure"] as const;
const PAGES = Object.freeze([
  Object.freeze({ id: "providers", label: "Providers" }),
  Object.freeze({ id: "general", label: "General" }),
]);

let container: HTMLDivElement | undefined;
let root: Root | undefined;
let workbench: WorkbenchController;

beforeEach(async () => {
  window.wuu = { ...window.wuu, reportPluginDesktopActivation: async () => {} };
  await desktopPluginHost.activateGeneration({ pluginId: "settings-presentation-fixture", generation: "one", graphGeneration: "test-graph",
    register: api => { workbench = new WorkbenchController(api.contributions); } });
});

afterEach(() => {
  act(() => root?.unmount());
  workbench.dispose();
  desktopPluginHost.unload("settings-presentation-fixture");
  container?.remove();
  root = undefined;
  container = undefined;
  for (const pluginId of TEST_PLUGIN_IDS) desktopPluginHost.unload(pluginId);
  vi.restoreAllMocks();
});

describe("SettingsPresentation", () => {
  it("creates an immutable settings snapshot without initialization secrets", () => {
    const initialized = initializedWithSecrets();
    const snapshot = createSettingsSnapshot({
      initialized,
      activePageId: "providers",
      availablePages: PAGES,
      runningProviderNames: ["private-provider"],
      busy: true,
      hasError: true,
    });

    expect(snapshot).toEqual(expect.objectContaining({
      contractVersion: 1,
      activePageId: "providers",
      busy: true,
      error: "A settings operation failed.",
      providers: [{ id: "private-provider", label: "private-provider", configured: true, status: "running" }],
      plugins: [{ id: "plugin.safe", name: "Safe Plugin", enabled: true, status: "active" }],
    }));
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot.availablePages)).toBe(true);
    expect(Object.isFrozen(snapshot.providers?.[0])).toBe(true);
    const serialized = JSON.stringify(snapshot);
    for (const secret of ["sk-private", "https://private.invalid", "/private/workspace", "private-fingerprint", "private-instance"]) {
      expect(serialized).not.toContain(secret);
    }
  });

  it("replaces the settings root, dispatches validated actions, updates live, and restores fallback on unload", async () => {
    let presenterHost: PresentationHost | undefined;
    let latestSnapshot: SettingsSnapshotV1 | undefined;
    await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph",
      pluginId: "test:settings-presenter",
      generation: "one",
      register(api) {
        api.registerPresenter({
          id: "settings-root",
          target: "settings",
          render: ({ host, snapshot }) => {
            presenterHost = host;
            latestSnapshot = snapshot as SettingsSnapshotV1;
            return <section data-settings-presenter>{latestSnapshot.activePageId}</section>;
          },
        });
      },
    });
    const onOpenPage = vi.fn();
    const onAdvancedSave = vi.fn(async () => undefined);
    const onGeneralSave = vi.fn(async () => undefined);
    const onRefresh = vi.fn(async () => undefined);

    renderPresentation({ activePageId: "providers", onOpenPage, onAdvancedSave, onGeneralSave, onRefresh });
    expect(container?.querySelector("[data-settings-presenter]")?.textContent).toBe("providers");
    expect(container?.querySelector("[data-native-settings]")).toBeNull();
    expect(presenterHost?.actions).toEqual([
      SETTINGS_ACTIONS.openPage,
      SETTINGS_ACTIONS.updateValue,
      SETTINGS_ACTIONS.refresh,
    ]);

    await act(async () => presenterHost?.invoke(SETTINGS_ACTIONS.openPage, { pageId: "general" }));
    expect(onOpenPage).toHaveBeenCalledWith("general");
    await expect(presenterHost?.invoke(SETTINGS_ACTIONS.openPage, { pageId: "private" })).rejects.toThrow("unavailable");
    await act(async () => presenterHost?.invoke(SETTINGS_ACTIONS.updateValue, { key: SETTINGS_UPDATE_KEYS.maxSteps, value: 12 }));
    expect(onAdvancedSave).toHaveBeenCalledWith({ max_steps: 12 });
	await act(async () => presenterHost?.invoke(SETTINGS_ACTIONS.updateValue, { key: SETTINGS_UPDATE_KEYS.gitAttributionEnabled, value: false }));
	expect(onGeneralSave).toHaveBeenCalledWith({ git_attribution_enabled: false });
    await expect(presenterHost?.invoke(SETTINGS_ACTIONS.updateValue, { key: SETTINGS_UPDATE_KEYS.temperature, value: "secret" })).rejects.toThrow("between 0 and 2");
    await act(async () => presenterHost?.invoke(SETTINGS_ACTIONS.refresh, { scope: "model-catalog" }));
    expect(onRefresh).toHaveBeenCalledOnce();

    renderPresentation({ activePageId: "general", onOpenPage, onAdvancedSave, onGeneralSave, onRefresh });
    expect(latestSnapshot?.activePageId).toBe("general");
    expect(container?.querySelector("[data-settings-presenter]")?.textContent).toBe("general");

    act(() => desktopPluginHost.unload("test:settings-presenter"));
    expect(container?.querySelector("[data-native-settings]")?.textContent).toBe("native");
  });

  it("uses the exact native fallback when a settings presenter fails", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    await desktopPluginHost.activateGeneration({ graphGeneration: "test-graph",
      pluginId: "test:settings-failure",
      generation: "one",
      register(api) {
        api.registerPresenter({ id: "broken", target: "settings", render: () => { throw new Error("broken"); } });
      },
    });
    renderPresentation({});
    expect(container?.querySelector("[data-native-settings]")?.textContent).toBe("native");
    expect(desktopPluginHost.getGenerationDiagnostics("test:settings-failure", "one")).toHaveLength(1);
  });
});

function renderPresentation(overrides: Partial<Parameters<typeof SettingsPresentation>[0]>): void {
  if (!container) {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  }
  const props: Parameters<typeof SettingsPresentation>[0] = {
    controller: workbench,
    initialized: initializedWithSecrets(),
    activePageId: "providers",
    availablePages: PAGES,
    busy: false,
    hasError: false,
    fallback: <main data-native-settings>native</main>,
    onOpenPage: () => undefined,
    onAdvancedSave: async () => undefined,
    onGeneralSave: async () => undefined,
    onRefresh: async () => undefined,
    ...overrides,
  };
  act(() => root?.render(<SettingsPresentation {...props} />));
}

function initializedWithSecrets(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    status: "ready",
    provider: "private-provider",
    model: "private-model",
    workspace_root: "/private/workspace",
    runtime_host: { kind: "local", instance_id: "private-instance" },
    providers: [{
      name: "private-provider",
      type: "openai-compatible",
      model: "private-model",
      base_url: "https://private.invalid",
      api_key_configured: true,
    }],
    extension_inventory: [{
      id: "plugin.safe",
      name: "Safe Plugin",
      kind: "plugin",
      provenance: { kind: "plugin", source: "private", scope: "user", path: "/private/plugin" },
      state: "active",
      enabled: true,
      fingerprint: "private-fingerprint",
    }],
    general_settings: {
      mcp_server_enabled: {},
    },
  };
}
