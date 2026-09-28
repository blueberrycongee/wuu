import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ExtensionInventoryRecord,
  PluginSettingResult,
  WuuDesktopApi,
} from "../shared/protocol";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
  delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  delete (window as { wuu?: WuuDesktopApi }).wuu;
  vi.restoreAllMocks();
  desktopPluginHost.unload("alpha-conflict");
  desktopPluginHost.unload("zeta-conflict");
});

describe("PluginSettingsEditor", () => {
  it("loads and renders boolean, string, number, and enum declarations", async () => {
    const getPluginSetting = vi.fn(({ id, key }: { id: string; key: string }) =>
      Promise.resolve(result(id, key, values[key])),
    );
    installAPI({ getPluginSetting });

    await renderEditor(pluginRecord());

    expect(getPluginSetting).toHaveBeenCalledTimes(4);
    expect(field("feature.enabled").querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(false);
    expect(field("display.name").querySelector<HTMLInputElement>('input[type="text"]')?.value).toBe("Custom name");
    expect(field("retry.count").querySelector<HTMLInputElement>('input[type="number"]')?.value).toBe("5");
    expect(field("display.mode").querySelector(".select-menu-value")?.textContent).toBe("roomy");
    await act(async () => field("display.mode").querySelector<HTMLButtonElement>(".select-menu-trigger")?.click());
    expect(Array.from(document.querySelectorAll(".select-menu-item-label")).map((option) => option.textContent)).toEqual(["compact", "roomy"]);
    expect(field("display.name").textContent).toContain("Shown in the header");
    // Only a setting narrower than the user's own scope says where it applies.
    expect(field("retry.count").textContent).toContain("工作区范围");
  });

  it("writes typed values and distinguishes live from restart application", async () => {
    const setPluginSetting = vi.fn(({ id, key, value }: { id: string; key: string; value: boolean | string | number }) =>
      Promise.resolve(result(id, key, value)),
    );
    installAPI({ setPluginSetting });
    await renderEditor(pluginRecord());

    const checkbox = field("feature.enabled").querySelector<HTMLInputElement>("input")!;
    await act(async () => checkbox.click());
    expect(setPluginSetting).toHaveBeenCalledWith({
      id: "demo.settings",
      fingerprint: "sha256:generation-1",
      key: "feature.enabled",
      value: true,
    });
    expect(field("feature.enabled").textContent).toContain("已保存并立即生效");

    const numberInput = field("retry.count").querySelector<HTMLInputElement>("input")!;
    await changeInput(numberInput, "8");
    await act(async () => numberInput.dispatchEvent(new FocusEvent("focusout", { bubbles: true })));
    expect(setPluginSetting).toHaveBeenCalledWith({
      id: "demo.settings",
      fingerprint: "sha256:generation-1",
      key: "retry.count",
      value: 8,
    });
    expect(field("retry.count").textContent).toContain("已保存；请重启或重载 Wuu 以应用更改");
    expect(field("retry.count").textContent).not.toContain("立即生效");
  });

  it("shows stale generation errors, preserves the draft, and retries the same write", async () => {
    const setPluginSetting = vi
      .fn()
      .mockRejectedValueOnce(new Error("stale plugin generation: sha256:generation-1"))
      .mockImplementation(({ id, key, value }) => Promise.resolve(result(id, key, value)));
    installAPI({ setPluginSetting });
    await renderEditor(pluginRecord());

    const input = field("display.name").querySelector<HTMLInputElement>("input")!;
    await changeInput(input, "Unsaved draft");
    await act(async () => input.dispatchEvent(new FocusEvent("focusout", { bubbles: true })));

    expect(input.value).toBe("Unsaved draft");
    expect(field("display.name").textContent).toContain("stale plugin generation");
    expect(buttonByText("重试")).not.toBeNull();

    await act(async () => buttonByText("重试")?.click());
    expect(setPluginSetting).toHaveBeenCalledTimes(2);
    expect(setPluginSetting).toHaveBeenLastCalledWith(expect.objectContaining({
      key: "display.name",
      value: "Unsaved draft",
    }));
    expect(input.value).toBe("Unsaved draft");
    expect(field("display.name").textContent).toContain("已保存并立即生效");
  });

  it("shows read API errors with a retry action", async () => {
    const getPluginSetting = vi
      .fn()
      .mockRejectedValueOnce(new Error("Settings API unavailable"))
      .mockImplementation(({ id, key }) => Promise.resolve(result(id, key, values[key])));
    installAPI({ getPluginSetting });
    await renderEditor(singleSettingPlugin());

    expect(container.textContent).toContain("Settings API unavailable");
    await act(async () => buttonByText("重试")?.click());
    expect(getPluginSetting).toHaveBeenCalledTimes(2);
    expect(field("display.name").querySelector<HTMLInputElement>("input")?.value).toBe("Custom name");
  });

  it("does not expose settings for unapproved or disabled plugins", async () => {
    const getPluginDiagnostics = vi.fn();
    installAPI({ getPluginDiagnostics });
    await renderEditor({ ...pluginRecord(), approval_state: "pending" });
    expect(container.querySelector(".plugin-settings-editor")).toBeNull();
    expect(getPluginDiagnostics).not.toHaveBeenCalled();

    await act(async () => root?.render(<PluginSettingsEditor plugin={{ ...pluginRecord(), enabled: false }} />));
    expect(container.querySelector(".plugin-settings-editor")).toBeNull();
    expect(getPluginDiagnostics).not.toHaveBeenCalled();
  });

  it("loads diagnostics after a plugin becomes approved and enabled", async () => {
    const getPluginDiagnostics = vi.fn(async ({ id }) => ({ id, diagnostics: [] }));
    installAPI({ getPluginDiagnostics });

    await renderEditor({ ...pluginRecord(), approval_state: "pending", enabled: false });
    expect(getPluginDiagnostics).not.toHaveBeenCalled();

    await act(async () => root?.render(<PluginSettingsEditor plugin={pluginRecord()} />));
    expect(getPluginDiagnostics).toHaveBeenCalledOnce();
    expect(container.textContent).not.toContain("desktop plugin is not approved and enabled");
  });

  it("shows replace conflicts and persists a different winner", async () => {
    const setPluginConflictPreference = vi.fn(async (key: string, pluginId: string) => ({ [key]: pluginId }));
    installAPI({ setPluginConflictPreference });
    for (const pluginId of ["alpha-conflict", "zeta-conflict"]) {
      await desktopPluginHost.activateGeneration({
        pluginId,
        generation: "sha256:generation-1",
        register(api) {
          api.registerSurface("conversation.timeline", {
            id: `${pluginId}-main`, mode: "replace", render: (_context, fallback) => fallback,
          });
        },
      });
    }

    await renderEditor({ ...pluginRecord(), id: "alpha-conflict", contributions: {} });
    const select = container.querySelector<HTMLSelectElement>("[data-conflict-key='surface:conversation.timeline'] select")!;
    expect(select.value).toBe("zeta-conflict");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
      setter?.call(select, "alpha-conflict");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(setPluginConflictPreference).toHaveBeenCalledWith("surface:conversation.timeline", "alpha-conflict");
    expect(desktopPluginHost.getSurfaceSnapshot("conversation.timeline").at(-1)?.pluginId).toBe("alpha-conflict");
  });

  it("shows isolated contribution diagnostics", async () => {
    installAPI();
    await desktopPluginHost.activateGeneration({
      pluginId: "alpha-conflict",
      generation: "sha256:generation-1",
      register(api) {
        api.registerSurface("conversation.timeline", {
          id: "broken-main", mode: "wrap", render: (_context, fallback) => fallback,
        });
      },
    });
    const contribution = desktopPluginHost.getSurfaceSnapshot("conversation.timeline")[0];
    desktopPluginHost.recordRenderFailure(contribution, { surfaceId: "conversation.timeline" }, new Error("render boom"));

    await renderEditor({ ...pluginRecord(), id: "alpha-conflict", contributions: {} });
    expect(container.textContent).toContain("插件贡献已被隔离");
    expect(container.textContent).toContain("render boom");
  });

  it("loads isolated runtime capability diagnostics", async () => {
    installAPI({
      getPluginDiagnostics: vi.fn(async ({ id }) => ({
        id,
        diagnostics: [{ contribution: "agent.request.transform", message: "transform boom" }],
      })),
    });
    await renderEditor({ ...pluginRecord(), contributions: {} });
    expect(container.textContent).toContain("agent.request.transform");
    expect(container.textContent).toContain("transform boom");
  });
});

const values: Record<string, boolean | string | number> = {
  "feature.enabled": false,
  "display.name": "Custom name",
  "retry.count": 5,
  "display.mode": "roomy",
};

function pluginRecord(): ExtensionInventoryRecord {
  return {
    id: "demo.settings",
    name: "Settings demo",
    kind: "plugin",
    provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "demo.settings" },
    state: "granted",
    approval_state: "granted",
    enabled: true,
    fingerprint: "sha256:generation-1",
    contributions: {
      settings: [
        { id: "feature.enabled", type: "boolean", title: "Enabled", description: "Enable the feature", default: true, scope: "user", apply: "live" },
        { id: "display.name", type: "string", title: "Display name", description: "Shown in the header", default: "Default name", scope: "user", apply: "live" },
        { id: "retry.count", type: "number", title: "Retry count", default: 3, scope: "workspace", apply: "restart" },
        { id: "display.mode", type: "enum", title: "Display mode", default: "compact", enum: ["compact", "roomy"], scope: "user", apply: "live" },
      ],
    },
  };
}

function singleSettingPlugin(): ExtensionInventoryRecord {
  const plugin = pluginRecord();
  return {
    ...plugin,
    contributions: { settings: plugin.contributions?.settings?.filter((setting) => setting.id === "display.name") },
  };
}

function result(id: string, key: string, value: boolean | string | number): PluginSettingResult {
  return { id, key, scope: key === "retry.count" ? "workspace" : "user", value };
}

function installAPI(overrides: Partial<WuuDesktopApi> = {}): void {
  const api: Partial<WuuDesktopApi> = {
    getPluginSetting: vi.fn(({ id, key }) => Promise.resolve(result(id, key, values[key]))),
    setPluginSetting: vi.fn(({ id, key, value }) => Promise.resolve(result(id, key, value))),
    ...overrides,
  };
  (globalThis as { wuu?: Partial<WuuDesktopApi> }).wuu = api;
  (window as { wuu?: Partial<WuuDesktopApi> }).wuu = api;
}

async function renderEditor(plugin: ExtensionInventoryRecord): Promise<void> {
  await act(async () => {
    root = createRoot(container);
    root.render(<PluginSettingsEditor plugin={plugin} />);
  });
}

function field(key: string): HTMLElement {
  const element = container.querySelector<HTMLElement>(`[data-setting-key="${key}"]`);
  if (!element) throw new Error(`Missing setting field ${key}`);
  return element;
}

function buttonByText(text: string): HTMLButtonElement | null {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button"))
    .find((button) => button.textContent?.trim() === text) ?? null;
}

async function changeInput(input: HTMLInputElement, value: string): Promise<void> {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
    setter?.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
}
