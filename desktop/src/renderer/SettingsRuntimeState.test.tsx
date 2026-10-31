import { clearToasts, ToastViewport } from "./Toast";
import { createScopedTestApplication } from "./test-support/ScopedApplication";
import { ApplicationController } from "./plugins/ApplicationController.test-support";
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  CodexPetsSnapshot,
  SettingsUsageResponse,
  WuuDesktopApi,
} from "../shared/protocol";
import {
  useSettingsRuntimeState,
  type SettingsRuntimeState,
} from "./SettingsRuntimeState";
import { translateCurrent } from "./i18n";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationSettingsLayoutV1 } from "../../../packages/plugin-sdk/src/application-settings";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";
import { DefaultSettingsSurface } from "./modules/DefaultSettingsSurface";
import { SettingsShellRenderer } from "./ConversationShellRenderers";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

vi.mock("./ConversationShellRenderers", () => ({ SettingsShellRenderer: vi.fn(() => null) }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function renewableRuntime(application: Pick<ApplicationServiceV1, "invoke">) {
  let latest!: SettingsRuntimeState;
  function Probe({ application }: { application: Pick<ApplicationServiceV1, "invoke"> }) {
    latest = useSettingsRuntimeState({ application, settingsOpen: false });
    return null;
  }
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  const render = (application: Pick<ApplicationServiceV1, "invoke">) => act(async () => root.render(<Probe application={application} />));
  await render(application);
  return { render, get current() { return latest; } };
}

let mountedRoots: Root[] = [];
const generationCleanups: Array<() => void> = [];

afterEach(() => { act(() => clearToasts());
  act(() => {
    for (const root of mountedRoots) root.unmount();
  });
  mountedRoots = [];
  generationCleanups.splice(0).forEach(dispose => dispose());
  document.body.innerHTML = "";
  delete (window as unknown as { wuu?: unknown }).wuu;
});

function settingsLayout(onNoticeVisibilityChange: (visible: boolean) => void): ApplicationSettingsLayoutV1 {
  return {
    embedded: true, onNoticeVisibilityChange, browserSettingsAllowed: true, embeddedDrawerPhase: "open",
    onSidebarPointerEnter: () => {}, onSidebarPointerLeave: () => {}, sidebarWidth: 320, resizingSidebar: false,
    shellRef: { current: null }, sidebarCollapsed: false, sidebarAnimating: false, onToggleSidebar: () => {},
    onSidebarResizeStart: () => {}, onSidebarResizerPointer: () => {}, onSidebarSeparatorKey: () => {},
  };
}

function snapshot(enabled: boolean): CodexPetsSnapshot {
  return {
    home: "/tmp/pets",
    enabled,
    selected_id: enabled ? "alpha" : "",
    errors: [],
    pets: enabled
      ? [
          {
            id: "alpha",
            display_name: "Alpha Pet",
            description: "",
            manifest_path: "/tmp/pets/alpha/pet.json",
            spritesheet_path: "/tmp/pets/alpha/spritesheet.webp",
            spritesheet_url: "wuu-file://local/alpha",
          },
        ]
      : [],
  };
}

function usage(): SettingsUsageResponse {
  return {
    total_sessions: 12,
    generated_at: "2026-07-09T00:00:00Z",
    metrics: {
      prompt_tokens: 0,
      context_tokens: 0,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_creation_tokens: 0,
      cache_hit_rate: 0,
      turns: 0,
      agents: 0,
      date_range: ["2026-07-01", "2026-07-09"],
      active_days: 0,
    },
    model_breakdowns: [],
    skill_usage: [],
    days: [],
  };
}

function installWuuStub(overrides: Partial<WuuDesktopApi>): void {
  (window as unknown as { wuu: WuuDesktopApi }).wuu = {
    ...overrides,
  } as WuuDesktopApi;
}

async function flushEffects(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

async function renderSettingsRuntimeState(settingsOpen: boolean): Promise<{
  get: () => SettingsRuntimeState;
  rerender: (nextSettingsOpen: boolean) => Promise<void>;
}> {
  let latest: SettingsRuntimeState | undefined;
  const scoped = await createScopedTestApplication(new ApplicationController());
  const application = scoped.application;
  generationCleanups.push(scoped.dispose);

  function Probe({ open }: { open: boolean }) {
    latest = useSettingsRuntimeState({ settingsOpen: open, application });
    return null;
  }

  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  mountedRoots.push(root);

  async function rerender(open: boolean): Promise<void> {
    await act(async () => {
      root.render(createElement(Probe, { open }));
      await flushEffects();
    });
  }

  await rerender(settingsOpen);

  return {
    get: () => {
      if (!latest) {
        throw new Error("settings runtime state was not rendered");
      }
      return latest;
    },
    rerender,
  };
}

describe("useSettingsRuntimeState", () => {
  it.each(["refresh", "update"] as const)("retires old pets %s outcomes without replacing successor data or clearing its pending operation", async action => {
    const oldResult = deferred<CodexPetsSnapshot>();
    const nextResult = deferred<CodexPetsSnapshot>();
    const oldInvoke = vi.fn().mockResolvedValueOnce(snapshot(false)).mockReturnValue(oldResult.promise);
    const nextInvoke = vi.fn().mockResolvedValueOnce(snapshot(true)).mockReturnValue(nextResult.promise);
    const previous = { invoke: oldInvoke as ApplicationServiceV1["invoke"] };
    const next = { invoke: nextInvoke as ApplicationServiceV1["invoke"] };
    const hook = await renewableRuntime(previous);
    let oldWork!: Promise<unknown>; let nextWork!: Promise<unknown>;
    const start = () => action === "refresh" ? hook.current.refreshCodexPets() : hook.current.updateCodexPets({ enabled: true });
    await act(async () => { oldWork = start().catch(error => error); });
    await hook.render(next);
    await act(async () => { nextWork = start(); });
    await act(async () => { oldResult.resolve(snapshot(false)); await oldWork; });
    expect(hook.current.codexPets).toEqual(snapshot(true));
    expect(hook.current.codexPetsLoading).toBe(true);
    expect(hook.current.codexPetsError).toBe("");
    await act(async () => { nextResult.resolve(snapshot(true)); await nextWork; });
    expect(hook.current.codexPetsLoading).toBe(false);
  });

  it("ignores late old pets rejection while a successor update remains pending", async () => {
    const oldResult = deferred<CodexPetsSnapshot>(); const nextResult = deferred<CodexPetsSnapshot>();
    const hook = await renewableRuntime({ invoke: vi.fn().mockResolvedValueOnce(snapshot(false)).mockReturnValue(oldResult.promise) as ApplicationServiceV1["invoke"] });
    let oldWork!: Promise<unknown>; let nextWork!: Promise<unknown>;
    await act(async () => { oldWork = hook.current.updateCodexPets({ enabled: true }).catch(error => error); });
    await hook.render({ invoke: vi.fn().mockResolvedValueOnce(snapshot(true)).mockReturnValue(nextResult.promise) as ApplicationServiceV1["invoke"] });
    await act(async () => { nextWork = hook.current.updateCodexPets({ enabled: true }); });
    await act(async () => { oldResult.reject(new Error("retired write failed")); await oldWork; });
    expect(hook.current.codexPetsLoading).toBe(true);
    expect(hook.current.codexPetsError).toBe("");
    await act(async () => { nextResult.resolve(snapshot(true)); await nextWork; });
  });

  it.each([true, false])("keeps pets scope cancellation quiet while reporting genuine failures (retired: %s)", async retired => {
    const cause = retired ? new PluginLifecycleCancellationError() : new Error("pets read failed");
    const invoke = vi.fn().mockResolvedValueOnce(snapshot(false)).mockRejectedValue(cause);
    const hook = await renewableRuntime({ invoke: invoke as ApplicationServiceV1["invoke"] });
    await act(async () => { await expect(hook.current.refreshCodexPets()).rejects.toBe(cause); });
    expect(hook.current.codexPetsError).toBe(retired ? "" : cause.message);
    expect(invoke).toHaveBeenCalledTimes(2);
  });
  it("loads Codex Pets on mount and stores updated settings snapshots", async () => {
    const initialSnapshot = snapshot(false);
    const updatedSnapshot = snapshot(true);
    const updateCodexPetSettings = vi.fn().mockResolvedValue(updatedSnapshot);
    installWuuStub({
      listCodexPets: vi.fn().mockResolvedValue(initialSnapshot),
      updateCodexPetSettings,
    });

    const hook = await renderSettingsRuntimeState(false);

    expect(hook.get().codexPets).toEqual(initialSnapshot);
    expect(hook.get().codexPetsLoading).toBe(false);
    expect(hook.get().codexPetsError).toBe("");

    await act(async () => {
      await hook.get().updateCodexPets({ enabled: true });
      await flushEffects();
    });

    expect(updateCodexPetSettings).toHaveBeenCalledWith({ enabled: true });
    expect(hook.get().codexPets).toEqual(updatedSnapshot);
  });

  it("loads usage only while settings are open and clears it on close", async () => {
    const getSettingsUsage = vi.fn().mockImplementation(async () => usage());
    installWuuStub({
      listCodexPets: vi.fn().mockResolvedValue(snapshot(false)),
      updateCodexPetSettings: vi.fn().mockResolvedValue(snapshot(false)),
      getSettingsUsage,
    });

    const hook = await renderSettingsRuntimeState(true);

    expect(getSettingsUsage).toHaveBeenCalledTimes(1);
    expect(hook.get().settingsUsage?.total_sessions).toBe(12);
    expect(hook.get().settingsUsageLoading).toBe(false);
    expect(hook.get().settingsUsageError).toBe("");

    await hook.rerender(false);

    expect(hook.get().settingsUsage).toBeUndefined();
    expect(hook.get().settingsUsageLoading).toBe(false);
    expect(hook.get().settingsUsageError).toBe("");
  });

  it("keeps the app usable when a stale preload does not expose settings usage", async () => {
    installWuuStub({
      listCodexPets: vi.fn().mockResolvedValue(snapshot(false)),
      updateCodexPetSettings: vi.fn().mockResolvedValue(snapshot(false)),
    });

    const hook = await renderSettingsRuntimeState(true);

    expect(hook.get().settingsUsage).toBeUndefined();
    expect(hook.get().settingsUsageLoading).toBe(false);
    expect(hook.get().settingsUsageError).toBe(translateCurrent("settings.usageUnsupported"));
    expect(hook.get().codexPetsLoading).toBe(false);
  });

  it("surfaces the existing stale-preload error when Codex Pets APIs are missing", async () => {
    installWuuStub({});

    const hook = await renderSettingsRuntimeState(false);

    expect(hook.get().codexPetsLoading).toBe(false);
    expect(hook.get().codexPetsError).toBe(translateCurrent("settings.pets.unsupported"));

    let refreshError: unknown;
    await act(async () => {
      try {
        await hook.get().refreshCodexPets();
      } catch (error) {
        refreshError = error;
      }
    });
    expect(refreshError).toBeInstanceOf(Error);
    expect((refreshError as Error).message).toBe(translateCurrent("settings.pets.unsupported"));
  });
});

it.each(["success", "failure", "retired"] as const)("does not publish an old model catalog %s notice after settings authority renewal", async outcome => {
  const oldResult = deferred<{ modelCount: number }>(); const nextResult = deferred<{ modelCount: number }>();
  const state = { settings: { open: true, engineInventoryError: "", runningProviderNames: [], archivedThreads: [], runtimeSwitchPending: false } };
  const make = (catalog: Promise<{ modelCount: number }>) => ({
    subscribe: () => () => {}, getSnapshot: () => state,
    invoke: vi.fn((action: string) => action === "settings.refresh-model-catalog" ? catalog : Promise.resolve(action === "settings.read-pets" ? snapshot(false) : action === "settings.read-usage" ? usage() : undefined)),
  } as unknown as ApplicationServiceV1);
  const previous = make(oldResult.promise); const next = make(nextResult.promise);
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  const visibility = vi.fn();
  const layout = settingsLayout(visibility);
  const render = (application: ApplicationServiceV1) => act(async () => root.render(<WuuUIRoot><ToastViewport /><DefaultSettingsSurface {...layout} application={application} /></WuuUIRoot>));
  const current = () => vi.mocked(SettingsShellRenderer).mock.calls.at(-1)![0];
  await render(previous);
  let oldWork!: Promise<void>; let nextWork!: Promise<void>;
  await act(async () => { oldWork = current().onRefreshModelCatalog(); });
  await render(next);
  await act(async () => { nextWork = current().onRefreshModelCatalog(); });
  visibility.mockClear();
  await act(async () => {
    if (outcome === "success") oldResult.resolve({ modelCount: 10 });
    else oldResult.reject(outcome === "retired" ? new PluginLifecycleCancellationError() : new Error("old catalog failed"));
    await oldWork;
  });
  expect(container.querySelector('[data-wuu-component="notice"]')).toBeNull();
  await act(async () => { nextResult.resolve({ modelCount: 20 }); await nextWork; });
  expect(container.querySelector('[data-wuu-component="notice"]')).not.toBeNull();
});

it.each([true, false])("keeps catalog retirement quiet without hiding genuine errors before successor props (retired: %s)", async retired => {
  const cause = retired ? new PluginLifecycleCancellationError() : new Error("catalog failed");
  const state = { settings: { open: true, engineInventoryError: "", runningProviderNames: [], archivedThreads: [], runtimeSwitchPending: false } };
  const application = { subscribe: () => () => {}, getSnapshot: () => state,
    invoke: vi.fn((action: string) => action === "settings.refresh-model-catalog" ? Promise.reject(cause) : Promise.resolve(action === "settings.read-pets" ? snapshot(false) : action === "settings.read-usage" ? usage() : undefined)),
  } as unknown as ApplicationServiceV1;
  const container = document.createElement("div"); document.body.appendChild(container);
  const root = createRoot(container); mountedRoots.push(root);
  const visibility = vi.fn();
  const layout = settingsLayout(visibility);
  await act(async () => root.render(<WuuUIRoot><ToastViewport /><DefaultSettingsSurface {...layout} application={application} /></WuuUIRoot>));
  visibility.mockClear();
  await act(async () => { await vi.mocked(SettingsShellRenderer).mock.calls.at(-1)![0].onRefreshModelCatalog(); });
  expect(Boolean(container.querySelector('[data-wuu-component="notice"]'))).toBe(!retired);
});
