import { act, createRef, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { connectionDisplayPreferences } from "./ConnectionDisplayPreferences";
import { writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { permissionModeOption, RuntimePicker } from "./ComposerRuntimeMenus";
import type { CodexRuntimeMenu } from "./ComposerTypes";
import { setActiveLocale, translateCurrent } from "./i18n";
import { variantLabel } from "./RuntimeHelpers";
import { clearToasts, ToastViewport } from "./Toast";

describe("RuntimePicker", () => {
  let container: HTMLDivElement;
  let root: Root | null = null;

  beforeEach(() => {
    window.localStorage.clear();
    container = document.createElement("div");
    document.body.appendChild(container);
  });

  afterEach(() => {
    act(() => root?.unmount());
    root = null;
    window.localStorage.clear();
    setActiveLocale("zh-CN");
    document
      .querySelectorAll('[data-floating-menu-owner="codex-runtime"]')
      .forEach((element) => element.remove());
    container.remove();
    vi.unstubAllGlobals();
    act(() => clearToasts());
  });

  function renderPicker(
    openMenu: CodexRuntimeMenu,
    initialized: InitializeResult,
    onToggleMenu = vi.fn(),
    onSelectEffort = vi.fn(),
    onSelectModel = vi.fn(),
    anchorRef = createRef<HTMLDivElement>(),
    engineProps: Partial<Pick<
      ComponentProps<typeof RuntimePicker>,
      | "inToolRail"
      | "engines"
      | "activeEngine"
      | "engineLocked"
      | "engineModel"
      | "engineEffort"
      | "engineSpeed"
      | "onSelectSpeed"
      | "onSelectEngine"
      | "onSelectEngineModel"
      | "onSelectEngineEffort"
      | "state"
    >> = {}
  ): void {
    act(() => {
      root ??= createRoot(container);
      root.render(
        <>
          <RuntimePicker
            initialized={initialized}
            state={{ loading: false, error: "", models: [] }}
            openMenu={openMenu}
            anchorRef={anchorRef}
            running={false}
            onToggleMenu={onToggleMenu}
            onSelectModel={onSelectModel}
            onSelectEffort={onSelectEffort}
            {...engineProps}
          />
          <ToastViewport />
        </>
      );
    });
  }

  function runtimeWithEffort(): InitializeResult {
    return {
      protocol_version: "wuu-app-server/v0.1",
      provider: "work",
      model: "claude-sonnet",
      variant: "medium",
      workspace_root: "/tmp/project",
      providers: [
        {
          name: "work",
          type: "anthropic",
          model: "claude-sonnet",
          models: [
            {
              id: "claude-sonnet",
              display_name: "Claude Sonnet",
              supported_efforts: ["low", "medium", "high"]
            }
          ]
        }
      ]
    };
  }

  it.each([["wuu", false], ["external", true]] as const)("selects the declared model source for %s (requires host model=%s)", async (engine, requiresHostModel) => {
    const onSelectModel = vi.fn(), onSelectEngineModel = vi.fn(), onSelectEngine = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), onSelectModel, createRef<HTMLDivElement>(), {
      activeEngine: engine, engineModel: "engine-model", onSelectEngineModel, onSelectEngine,
      engines: [{ id: engine, enabled: true, binary_ok: true, requires_host_model: requiresHostModel, models: [{ id: "engine-model", display_name: "Engine owned model" }] }],
    });
    const rows = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"));
    const chosen = rows.find(row => row.textContent?.includes(requiresHostModel ? "Claude Sonnet" : "Engine owned model"));
    expect(chosen).toBeDefined();
    await act(async () => chosen!.click());
    expect(onSelectEngine).not.toHaveBeenCalled();
    if (requiresHostModel) { expect(onSelectModel).toHaveBeenCalled(); expect(onSelectEngineModel).not.toHaveBeenCalled(); }
    else { expect(onSelectEngineModel).toHaveBeenCalled(); expect(onSelectModel).not.toHaveBeenCalled(); }
  });

  it.each([true, false])("keeps external effort keyboard focus and rolls back rejected saves (saved=%s)", async (saved) => {
    let finish!: (result: boolean) => void;
    const onSelectEngineEffort = vi.fn(() => new Promise<boolean>((resolve) => { finish = resolve; }));
    const initialized = runtimeWithEffort();
    const anchor = createRef<HTMLDivElement>();
    const engineProps = {
      activeEngine: "codex", engineLocked: true, engineModel: "gpt-test", engineEffort: "low",
      engines: [{ id: "codex", enabled: true, binary_ok: true, models: [{ id: "gpt-test", supported_efforts: ["low", "high"] }] }],
      onSelectEngineEffort,
    };
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchor, engineProps);
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    const effortOption = (name: string): HTMLButtonElement =>
      Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-effort-option"))
        .find((button) => button.querySelector(".runtime-effort-option-name")?.textContent === name)!;
    const high = effortOption(variantLabel("high"));
    act(() => high.focus());
    await act(async () => high.click());
    expect(onSelectEngineEffort).toHaveBeenCalledExactlyOnceWith("high");
    expect(document.activeElement).toBe(high);
    expect(high.disabled).toBe(false);
    expect(high.getAttribute("aria-busy")).toBe("true");
    await act(async () => high.click());
    expect(onSelectEngineEffort).toHaveBeenCalledTimes(1);
    await act(async () => {
      if (saved) {
        renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchor, { ...engineProps, engineEffort: "high" });
      }
      finish(saved);
    });
    expect(document.activeElement).toBe(high);
    expect(high.getAttribute("aria-busy")).toBeNull();
    // A rejected save leaves the prop-driven checked state untouched.
    expect(high.getAttribute("aria-checked")).toBe(String(saved));
    expect(effortOption(variantLabel("low")).getAttribute("aria-checked")).toBe(String(!saved));

    const low = effortOption(variantLabel("low"));
    act(() => low.focus());
    await act(async () => low.click());
    expect(onSelectEngineEffort).toHaveBeenCalledTimes(2);
    expect(onSelectEngineEffort).toHaveBeenLastCalledWith("low");
    await act(async () => finish(false));
    expect(document.activeElement).toBe(low);
  });

  it("toggles speed independently on a bound engine conversation", async () => {
    const speed = vi.fn().mockResolvedValue(true);
    const effort = vi.fn();
    const model = vi.fn();
    const toggleMenu = vi.fn();
    renderPicker("model", runtimeWithEffort(), toggleMenu, effort, model, createRef(), {
      activeEngine: "codex", engineLocked: true, engineModel: "gpt-6-astra", engineEffort: "high", engineSpeed: "standard",
      onSelectSpeed: speed,
      engines: [{ id: "codex", enabled: true, binary_ok: true, models: [{ id: "gpt-6-astra", fast_mode: true, supported_efforts: ["low", "high"] }] }],
    });
    const segmented = document.querySelector<HTMLElement>('.runtime-speed-segmented[role="group"]');
    expect(segmented?.getAttribute("aria-label")).toBe("速度");
    const options = Array.from(segmented?.querySelectorAll<HTMLButtonElement>("button") ?? []);
    expect(options).toHaveLength(2);
    expect(options.every(option => option.disabled === false)).toBe(true);
    await act(async () => options[1]?.click());
    expect(speed).toHaveBeenCalledWith("fast");
    expect(effort).not.toHaveBeenCalled();
    expect(model).not.toHaveBeenCalled();
    expect(toggleMenu).not.toHaveBeenCalled();
    expect(document.querySelector(".runtime-flat-panel")).not.toBeNull();
    expect(segmented?.parentElement?.closest("button")).toBeNull();
  });

  it.each([
    { speed: "", defaultSpeed: "fast", enabled: true, next: "standard" },
    { speed: "", defaultSpeed: "standard", enabled: false, next: "fast" },
    { speed: "standard", defaultSpeed: "fast", enabled: false, next: "fast" },
  ])("toggles the effective engine speed for $speed / $defaultSpeed", async ({ speed, defaultSpeed, enabled, next }) => {
    const onSelectSpeed = vi.fn().mockResolvedValue(true);
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef(), {
      activeEngine: "codex", engineLocked: true, engineModel: "gpt-6-astra", engineSpeed: speed,
      onSelectSpeed,
      engines: [{ id: "codex", enabled: true, binary_ok: true, models: [{ id: "gpt-6-astra", fast_mode: true, default_speed: defaultSpeed }] }],
    });
    // Wait for the opening frame's automatic focus before simulating input.
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    const options = document.querySelectorAll<HTMLButtonElement>(".runtime-speed-option");
    const target = next === "fast" ? options[1] : options[0];
    expect(options[1]?.getAttribute("aria-pressed")).toBe(String(enabled));
    act(() => target?.focus());
    await act(async () => target?.click());
    expect(onSelectSpeed).toHaveBeenCalledWith(next);
    expect(target?.getAttribute("aria-pressed")).toBe("true");
    // A second click on the now-active option is a no-op; the other side saves again.
    await act(async () => target?.click());
    expect(onSelectSpeed).toHaveBeenCalledTimes(1);
    const other = next === "fast" ? options[0] : options[1];
    act(() => other?.focus());
    await act(async () => other?.click());
    expect(onSelectSpeed).toHaveBeenLastCalledWith(enabled ? "fast" : "standard");
    expect(document.activeElement).toBe(other);
    expect(other?.getAttribute("aria-pressed")).toBe("true");
    expect(target?.getAttribute("aria-pressed")).toBe("false");
  });

  it("keeps a speed save focused while preventing duplicate requests", async () => {
    let finish!: (saved: boolean) => void;
    const onSelectSpeed = vi.fn(() => new Promise<boolean>((resolve) => { finish = resolve; }));
    const initialized = runtimeWithEffort();
    initialized.speed = "standard";
    initialized.providers![0].models![0].fast_mode = true;
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed });
    // Wait for the opening frame's automatic focus before simulating input.
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    const button = document.querySelectorAll<HTMLButtonElement>(".runtime-speed-option")[1]!;
    act(() => { button.focus(); button.click(); });
    expect(button.disabled).toBe(false);
    expect(button.getAttribute("aria-disabled")).toBe("true");
    expect(button.getAttribute("aria-busy")).toBe("true");
    act(() => button.click());
    expect(onSelectSpeed).toHaveBeenCalledTimes(1);
    await act(async () => finish(true));
    expect(document.activeElement).toBe(button);
    expect(button.getAttribute("aria-disabled")).toBeNull();
  });

  it("omits speed controls when the model does not support fast mode", () => {
    const initialized = runtimeWithEffort();
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed: vi.fn() });
    expect(document.querySelector(".runtime-speed-segmented")).toBeNull();
    expect(document.querySelector(".runtime-flat-footer")).toBeNull();
  });

  it("restores the speed toggle after a rejected update", async () => {
    const initialized = runtimeWithEffort();
    initialized.speed = "standard";
    initialized.providers![0].models![0].fast_mode = true;
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed: vi.fn().mockResolvedValue(false) });
    const button = document.querySelectorAll<HTMLButtonElement>(".runtime-speed-option")[1];
    await act(async () => button?.click());
    expect(button?.getAttribute("aria-pressed")).toBe("false");
    expect(document.querySelectorAll<HTMLButtonElement>(".runtime-speed-option")[0]?.getAttribute("aria-pressed")).toBe("true");
  });

  it("updates model display order without selecting or moving the configured model to the front", () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].models!.push({ id: "other", display_name: "Other" });
    const onSelectModel = vi.fn();
    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    act(() => connectionDisplayPreferences.setModelOrder("provider:work", ["other", "claude-sonnet"]));
    expect(Array.from(document.querySelectorAll(".codex-model-item-name")).map(item => item.textContent)).toEqual(["Other", "Claude Sonnet"]);
    expect(document.querySelector('.codex-model-item[aria-checked="true"]')?.textContent).toContain("Claude Sonnet");
    expect(onSelectModel).not.toHaveBeenCalled();
  });

  it("opens the model panel from the trigger with a single click", () => {
    const onToggleMenu = vi.fn();
    renderPicker(null, runtimeWithEffort(), onToggleMenu);

    const trigger = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(trigger?.getAttribute("aria-expanded")).toBe("false");
    expect(trigger?.textContent).toContain("Claude Sonnet");
    expect(trigger?.textContent).toContain(variantLabel("medium"));
    expect(trigger?.textContent).not.toContain("Wuu");
    expect(trigger?.getAttribute("aria-label")).toContain("Wuu");

    act(() => trigger?.click());

    expect(onToggleMenu).toHaveBeenCalledWith("model");
  });

  it("names no level for a model without levels", () => {
    const initialized = runtimeWithEffort();
    delete initialized.providers![0].models![0].supported_efforts;
    initialized.variant = "";
    renderPicker(null, initialized);

    const trigger = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(trigger?.querySelector(".codex-runtime-effort")).toBeNull();
    expect(trigger?.getAttribute("aria-label")).not.toContain(variantLabel(""));
  });

  it("shows the level stored in effort when the variant column is empty", () => {
    const initialized = runtimeWithEffort();
    initialized.variant = "";
    initialized.effort = "max";
    renderPicker(null, initialized);

    const trigger = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(trigger?.textContent).toContain(variantLabel("max"));
  });

  it("uses the configured inventory even when stale discovery contains a removed model", () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].type = "openai-codex";
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), {
      state: { provider: "work", loading: false, error: "", models: [
        { slug: "removed-live-model", supported_in_api: true },
      ] },
    });
    const rows = Array.from(document.querySelectorAll<HTMLElement>(".codex-model-item-name"));
    expect(rows.map((row) => row.textContent?.trim())).toEqual(["Claude Sonnet"]);
  });

  it("opens as a flat panel with engines, models and effort visible together", () => {
    renderPicker("model", runtimeWithEffort());

    const menu = document.querySelector<HTMLElement>(".codex-model-menu");
    expect(menu).not.toBeNull();
    // The intermediate main menu is gone.
    expect(document.querySelector(".codex-main-menu")).toBeNull();
    expect(menu?.classList.contains("runtime-flat-panel")).toBe(true);

    // Search, the provider group header, the model row and the effort list
    // share one panel instead of drilling between pages.
    const search = menu?.querySelector<HTMLInputElement>(".select-menu-search input");
    expect(search?.placeholder).toBe("搜索模型");
    expect(menu?.querySelector(".runtime-provider-head")?.textContent).toContain("work");
    const modelItems = Array.from(menu?.querySelectorAll<HTMLButtonElement>(".codex-model-item") ?? []);
    expect(modelItems.map((item) => item.querySelector(".codex-model-item-name")?.textContent)).toEqual(["Claude Sonnet"]);
    expect(modelItems[0]?.getAttribute("aria-checked")).toBe("true");
    const effortOptions = Array.from(menu?.querySelectorAll<HTMLButtonElement>(".runtime-effort-option") ?? []);
    expect(effortOptions.map((item) => item.querySelector(".runtime-effort-option-name")?.textContent)).toEqual([
      variantLabel(""), variantLabel("low"), variantLabel("medium"), variantLabel("high"),
    ]);
    expect(effortOptions[2]?.getAttribute("aria-checked")).toBe("true");
  });

  it("reaches model rows, effort options and the speed controls with the arrow keys", async () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].models![0].fast_mode = true;
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed: vi.fn().mockResolvedValue(true) });
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    const press = (key: string): void => {
      act(() => { document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })); });
    };

    expect(document.activeElement).toBe(document.querySelector(".select-menu-search input"));
    press("ArrowDown");
    expect(document.activeElement).toBe(document.querySelector(".codex-model-item"));
    press("ArrowDown");
    expect(document.activeElement).toBe(document.querySelector(".runtime-effort-option"));
    const effort = document.activeElement as HTMLElement;
    // Up walks back to the model row without leaving the panel.
    press("ArrowUp");
    expect(document.activeElement).toBe(document.querySelector(".codex-model-item"));
    // End lands on the last control, the fast segment.
    press("End");
    expect(document.activeElement).toBe(document.querySelectorAll(".runtime-speed-option")[1]);
    press("Home");
    expect(document.activeElement).toBe(document.querySelector(".select-menu-search input"));
    expect(effort.isConnected).toBe(true);
  });

  it("closes the panel with Escape", () => {
    const onToggleMenu = vi.fn();
    renderPicker("model", runtimeWithEffort(), onToggleMenu);
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;

    act(() => { search.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })); });
    expect(onToggleMenu).toHaveBeenCalledWith("model");
  });

  it("picks the first matching model when Enter is pressed in search", () => {
    const onSelectModel = vi.fn();
    const initialized = runtimeWithEffort();
    initialized.providers![0].models!.push({ id: "claude-opus", display_name: "Claude Opus", supported_efforts: ["low", "high"] });
    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(search, "opus");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    act(() => { search.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });

    expect(onSelectModel).toHaveBeenCalledWith("work", "claude-opus", "");
    // The flat panel stays open and reflects the new selection immediately.
    expect(document.querySelector('.codex-model-item[aria-checked="true"]')?.textContent).toContain("Claude Opus");
  });

  it("presents engines as an icon column beside the selected engine's models", () => {
    const onSelectEngine = vi.fn();
    renderPicker(
      "model",
      runtimeWithEffort(),
      vi.fn(),
      vi.fn(),
      vi.fn(),
      createRef<HTMLDivElement>(),
      {
        engines: [
          { id: "wuu", enabled: true, binary_ok: true },
          {
            id: "codex",
            enabled: true,
            binary_ok: true,
            models: [
              {
                id: "gpt-5.6-sol",
                display_name: "GPT-5.6-Sol",
                supported_efforts: ["low", "medium", "high"]
              }
            ]
          }
        ],
        activeEngine: "codex",
        engineModel: "gpt-5.6-sol",
        engineEffort: "low",
        onSelectEngine,
        onSelectEngineModel: vi.fn(),
        onSelectEngineEffort: vi.fn()
      }
    );

    const engineRail = document.querySelector<HTMLElement>('.runtime-engine-rail[role="group"]');
    expect(engineRail?.getAttribute("aria-label")).toBe("运行引擎");
    const engineChoices = Array.from(
      engineRail?.querySelectorAll<HTMLButtonElement>('.runtime-engine-icon') ?? []
    );
    expect(engineChoices.map((choice) => choice.getAttribute("aria-label"))).toEqual([
      "Wuu",
      "Codex"
    ]);
    expect(engineChoices.every((choice) => choice.querySelector("svg.engine-icon"))).toBe(true);
    expect(engineChoices[1]?.getAttribute("aria-checked")).toBe("true");
    act(() => engineChoices[0]?.click());
    expect(onSelectEngine).toHaveBeenCalledWith("wuu");
  });

  it.each(["wuu", "codex"])("opens the selected %s engine without resetting its runtime", (activeEngine) => {
    const onSelectEngine = vi.fn();
    const onSelectModel = vi.fn();
    const onSelectEffort = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), onSelectEffort, onSelectModel, createRef(), {
      activeEngine,
      engineModel: "gpt-6-astra",
      engineEffort: "high",
      engines: [{
        id: "codex", enabled: true, binary_ok: true,
        models: [{ id: "gpt-6-astra", supported_efforts: ["low", "high"] }],
      }],
      onSelectEngine,
      onSelectEngineModel: onSelectModel,
      onSelectEngineEffort: onSelectEffort,
    });
    const checkedModel = document.querySelector('.codex-model-item[aria-checked="true"] .codex-model-item-name')?.textContent;
    const checkedEffort = document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent;
    act(() => document.querySelector<HTMLButtonElement>('.runtime-engine-icon[aria-checked="true"]')!.click());

    expect(document.querySelector(".runtime-flat-panel")).not.toBeNull();
    expect(document.querySelector('.codex-model-item[aria-checked="true"] .codex-model-item-name')?.textContent).toBe(checkedModel);
    expect(document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent).toBe(checkedEffort);
    expect(onSelectEngine).not.toHaveBeenCalled();
    expect(onSelectModel).not.toHaveBeenCalled();
    expect(onSelectEffort).not.toHaveBeenCalled();
  });

  it("offers installed protocol engines and retains an unavailable active binding without leaking Wuu models", () => {
    const onSelectEngine = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef(), {
      activeEngine: "devin",
      engines: [
        { id: "devin", display_name: "Devin", enabled: false, binary_ok: false },
        { id: "hermes", display_name: "Hermes", enabled: true, binary_ok: true },
        { id: "grok", enabled: false, binary_ok: false },
      ],
      onSelectEngine,
    });
    expect(document.querySelector('.codex-runtime-menu')!.textContent).not.toContain("Claude Sonnet");
    const choices = Array.from(document.querySelectorAll<HTMLButtonElement>('.runtime-engine-icon'));
    expect(choices.find((choice) => choice.getAttribute("aria-checked") === "true")!.getAttribute("aria-label")).toContain("Devin");
    expect(choices.some((choice) => choice.getAttribute("aria-label")?.includes("grok"))).toBe(false);
    act(() => choices.find((choice) => choice.getAttribute("aria-label")?.includes("Hermes"))!.click());
    expect(onSelectEngine).toHaveBeenCalledExactlyOnceWith("hermes");
  });

  it("lists advertised Grok models with a native default reset", () => {
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef(), {
      activeEngine: "grok",
      engineModel: "grok-4.6",
      engineEffort: "high",
      engines: [
        {
          id: "grok",
          display_name: "Grok",
          enabled: true,
          binary_ok: true,
          models: [
            {
              id: "grok-4.6",
              display_name: "Grok 4.6",
              supported_efforts: ["low", "medium", "high"],
              is_default: true
            },
            {
              id: "grok-4.5",
              display_name: "Grok 4.5",
              supported_efforts: ["low", "medium", "high"]
            }
          ]
        }
      ],
      onSelectEngineModel: vi.fn()
    });
    const triggerButton = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    const trigger = triggerButton?.textContent ?? "";
    expect(trigger).toContain("Grok 4.6");
    expect(trigger).not.toMatch(/Grok\s·/);
    expect(trigger).not.toContain("Agent 默认模型");
    expect(triggerButton?.querySelector("svg.engine-icon")).not.toBeNull();
    expect(triggerButton?.getAttribute("aria-label")).toContain("Grok");
    const items = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item")).map((item) =>
      item.querySelector(".codex-model-item-name")?.textContent
    );
    expect(items).toEqual([translateCurrent("runtime.engineDefaultModel"), "Grok 4.6", "Grok 4.5"]);
  });

  it("reports a failed engine catalog through the notice queue instead of the model list", () => {
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      activeEngine: "grok",
      engineModel: "grok-4.5",
      engines: [{
        id: "grok",
        enabled: true,
        binary_ok: true,
        models_status: "partial",
        models_error: 'discover model "grok-4.6": context deadline exceeded\n[WARNING] repeated agent stderr',
        models: [{ id: "grok-4.5", display_name: "Grok 4.5" }],
      }],
    });

    const toast = document.querySelector(".archive-tip-message");
    expect(toast?.textContent).toContain(translateCurrent("runtime.modelsPartial"));
    expect(toast?.textContent).toContain('discover model "grok-4.6": context deadline exceeded');
    expect(toast?.textContent).not.toContain("stderr");

    expect(document.querySelector(".codex-model-menu")?.textContent).not.toContain("context deadline exceeded");
    expect(Array.from(document.querySelectorAll(".codex-model-item-name")).map(item => item.textContent))
      .toEqual([translateCurrent("runtime.engineDefaultModel"), "Grok 4.5"]);
  });

  it("reports an unreadable Codex login through the notice queue instead of the model list", () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].type = "openai-codex";
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      state: { provider: "work", loading: false, error: "chatgpt account token expired\nstack detail", models: [] },
    });

    const toast = document.querySelector(".archive-tip-message");
    expect(toast?.textContent).toContain(translateCurrent("runtime.codexLoginUnavailable"));
    expect(toast?.textContent).toContain("chatgpt account token expired");
    expect(toast?.textContent).not.toContain("stack detail");

    expect(document.querySelector(".codex-model-menu")?.textContent).not.toContain("chatgpt account token expired");
  });

  it("shows the engine column only when another engine can be chosen", () => {
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      engines: [{ id: "wuu", enabled: true, binary_ok: true }],
    });
    expect(document.querySelector(".runtime-engine-rail")).toBeNull();

    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      engines: [{ id: "wuu", enabled: true, binary_ok: true }, { id: "codex", enabled: true, binary_ok: true }],
      onSelectEngine: vi.fn(),
    });
    expect(document.querySelectorAll(".runtime-engine-icon")).toHaveLength(2);
  });

  it("locks the engine column on a bound engine instead of offering choices it cannot take", () => {
    renderPicker(
      "model",
      runtimeWithEffort(),
      vi.fn(),
      vi.fn(),
      vi.fn(),
      createRef<HTMLDivElement>(),
      {
        engines: [
          { id: "wuu", enabled: true, binary_ok: true },
          { id: "codex", enabled: true, binary_ok: true }
        ],
        activeEngine: "codex",
        engineLocked: true
      }
    );

    const rail = document.querySelector<HTMLElement>(".runtime-engine-rail")!;
    expect(rail.classList.contains("is-locked")).toBe(true);
    expect(rail.getAttribute("aria-label")).toBe(translateCurrent("runtime.engineLockedDescription"));
    const choices = Array.from(rail.querySelectorAll<HTMLButtonElement>(".runtime-engine-icon"));
    expect(choices.every((choice) => choice.disabled)).toBe(true);
    // The bound engine carries the lock badge.
    expect(rail.querySelector('.runtime-engine-icon[aria-checked="true"] svg:not(.engine-icon)')).not.toBeNull();
  });

  it("builds permission labels in the active language", () => {
    setActiveLocale("en-US");

    expect(permissionModeOption("standard")).toMatchObject({
      label: translateCurrent("runtime.permission.standard"),
      hint: translateCurrent("runtime.permission.standardHint"),
    });
  });

  it.each(["codex", "claude", "cursor", "devin"])(
    "names %s modes with the shared words and keeps the engine's own mode as the hint",
    (engine) => {
      for (const mode of ["standard", "unconfined"] as const) {
        const option = permissionModeOption(mode, engine);
        expect(option.label).toBe(permissionModeOption(mode).label);
        expect(option.hint).not.toBe("");
        expect(option.hint).not.toBe(option.label);
      }
    },
  );

  it("shows advertised ACP permission labels as the hint under the shared name", () => {
    expect(permissionModeOption("unconfined", "devin", [
      { mode: "standard", id: "ask", label: "Ask" },
      { mode: "unconfined", id: "bypass", label: "Bypass Permissions" },
    ])).toMatchObject({
      label: permissionModeOption("unconfined").label,
      hint: "Bypass Permissions",
      tone: "danger",
    });
  });

  it.each([undefined, "codex", "claude"])(
    "reserves the danger tone for unconfined access regardless of engine (%s)",
    (engine) => {
      expect(permissionModeOption("standard", engine)).toMatchObject({
        tone: "neutral",
      });
      expect(permissionModeOption("read_only", engine)).toMatchObject({
        tone: "neutral",
      });
      expect(permissionModeOption("unconfined", engine)).toMatchObject({
        tone: "danger",
      });
    },
  );

  it("hides effort choices when the model does not expose reasoning levels", () => {
    const initialized = runtimeWithEffort();
    initialized.variant = "";
    initialized.providers![0].models = [{ id: "claude-sonnet", display_name: "Claude Sonnet" }];

    renderPicker("model", initialized);

    expect(document.querySelector(".runtime-effort-column")).toBeNull();
    expect(document.querySelector(".runtime-flat-side")).toBeNull();
    const menu = document.querySelector<HTMLElement>(".codex-model-menu");
    expect(menu?.querySelectorAll(".codex-model-item")).toHaveLength(1);
  });

  it("filters models by name or provider through the search box", () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].models = [
      { id: "claude-sonnet", display_name: "Claude Sonnet", supported_efforts: ["low", "medium", "high"] },
      { id: "claude-opus", display_name: "Claude Opus", supported_efforts: ["low", "medium", "high"] }
    ];
    renderPicker("model", initialized);

    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    const setSearchValue = (value: string): void => {
      const valueSetter = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        "value",
      )?.set;
      valueSetter?.call(search, value);
      search.dispatchEvent(new Event("input", { bubbles: true }));
    };
    act(() => setSearchValue("opus"));

    const items = Array.from(document.querySelectorAll<HTMLElement>(".codex-model-item-name"));
    expect(items.map((item) => item.textContent?.trim())).toEqual(["Claude Opus"]);

    act(() => setSearchValue("no-such-model"));
    expect(document.querySelector(".composer-menu-empty")?.textContent).toBe("没有匹配的模型");
  });

  it("makes every Wuu provider directly selectable without burying its models in one long list", () => {
    const initialized = runtimeWithEffort();
    initialized.provider = "tokenhub";
    initialized.model = "gpt-5.6-sol";
    initialized.providers = [
      {
        name: "deepseek",
        type: "openai-compatible",
        model: "deepseek-chat",
        models: [{ id: "deepseek-chat", display_name: "DeepSeek Chat" }]
      },
      {
        name: "tokenhub",
        type: "openai-compatible",
        model: "gpt-5.6-sol",
        models: [
          { id: "gpt-5.6-sol", display_name: "GPT-5.6 Sol" },
          { id: "gpt-5.6-terra", display_name: "GPT-5.6 Terra" },
          { id: "gpt-5.6-luna", display_name: "GPT-5.6 Luna" }
        ]
      }
    ];

    const onSelectModel = vi.fn();
    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);

    // Every provider is a group header in one column, not a page to drill
    // through; its models sit directly beneath it.
    const providerHeads = Array.from(document.querySelectorAll<HTMLElement>(".runtime-provider-head"));
    expect(providerHeads.map((head) => head.lastElementChild?.textContent?.trim())).toEqual(["deepseek", "tokenhub"]);
    const modelItems = Array.from(document.querySelectorAll<HTMLElement>(".codex-model-item-name"));
    expect(modelItems.map((item) => item.textContent?.trim())).toEqual([
      "DeepSeek Chat", "GPT-5.6 Sol", "GPT-5.6 Terra", "GPT-5.6 Luna",
    ]);
    expect(document.querySelector('.codex-model-item[aria-checked="true"]')?.textContent).toContain("GPT-5.6 Sol");

    const deepseekChat = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
      .find((item) => item.textContent?.includes("DeepSeek Chat"))!;
    act(() => deepseekChat.click());
    expect(onSelectModel).toHaveBeenCalledWith("deepseek", "deepseek-chat", "");
  });

  it("reuses the model and effort a provider was last used with", () => {
    writeDraftRuntimeMemory({
      provider: "deepseek",
      model: "deepseek-reasoner",
      effort: "high",
    });
    const initialized = runtimeWithEffort();
    initialized.provider = "tokenhub";
    initialized.model = "gpt-5.6-sol";
    initialized.providers = [
      {
        name: "deepseek",
        type: "openai-compatible",
        model: "deepseek-chat",
        models: [
          { id: "deepseek-chat", display_name: "DeepSeek Chat" },
          {
            id: "deepseek-reasoner",
            display_name: "DeepSeek Reasoner",
            supported_efforts: ["low", "high"],
            default_effort: "low",
          },
        ],
      },
      {
        name: "tokenhub",
        type: "openai-compatible",
        model: "gpt-5.6-sol",
        models: [{ id: "gpt-5.6-sol", display_name: "GPT-5.6 Sol" }],
      },
    ];
    const onSelectModel = vi.fn();

    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    // Selecting a model in another provider restores the effort that model
    // was last used with instead of resetting to the catalog default.
    const reasoner = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
      .find((item) => item.textContent?.includes("DeepSeek Reasoner"))!;
    act(() => reasoner.click());

    expect(onSelectModel).toHaveBeenCalledWith("deepseek", "deepseek-reasoner", "high");
  });

  it("orders effort weakest to strongest even when efforts arrive descending", () => {
    const initialized = runtimeWithEffort();
    initialized.provider = "Grok";
    initialized.model = "grok-4.6";
    initialized.variant = "xhigh";
    initialized.providers![0] = {
      name: "Grok",
      type: "grok-build",
      model: "grok-4.6",
      models: [{
        id: "grok-4.6",
        display_name: "Grok 4.6",
        variants: [{ id: "xhigh" }, { id: "high" }, { id: "medium" }, { id: "low" }],
        supported_efforts: ["xhigh", "high", "medium", "low"],
      }],
    };

    renderPicker("model", initialized);

    const options = Array.from(document.querySelectorAll<HTMLElement>(".runtime-effort-option-name"));
    expect(options.map((option) => option.textContent)).toEqual([
      variantLabel(""), variantLabel("low"), variantLabel("medium"), variantLabel("high"), variantLabel("xhigh"),
    ]);
    expect(document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent)
      .toBe(variantLabel("xhigh"));
  });

  it("selects a discrete effort from the level list and resolves the model's own default beside Default", () => {
    const onSelectEffort = vi.fn();
    const initialized = runtimeWithEffort();
    initialized.providers![0].models![0].default_effort = "medium";
    renderPicker("model", initialized, vi.fn(), onSelectEffort);

    const option = (name: string): HTMLButtonElement =>
      Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-effort-option"))
        .find((button) => button.querySelector(".runtime-effort-option-name")?.textContent === name)!;
    expect(option(variantLabel("")).querySelector(".runtime-effort-option-hint")?.textContent)
      .toBe(variantLabel("medium"));
    expect(option(variantLabel("medium")).querySelector(".runtime-effort-option-hint")).toBeNull();
    expect(option(variantLabel("low")).querySelector(".runtime-effort-option-hint")).toBeNull();

    act(() => option(variantLabel("high")).click());
    expect(onSelectEffort).toHaveBeenCalledExactlyOnceWith("high");
    // The pending choice highlights immediately while the save round-trips.
    expect(option(variantLabel("high")).getAttribute("aria-checked")).toBe("true");
  });

  it("flips the model menu below the trigger when the window top has too little room", () => {
    const initialized = runtimeWithEffort();
    const anchorRef = createRef<HTMLDivElement>();
    renderPicker(null, initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef);
    vi.spyOn(anchorRef.current as HTMLDivElement, "getBoundingClientRect").mockReturnValue({
      x: 700,
      y: 40,
      top: 40,
      left: 700,
      right: 880,
      bottom: 70,
      width: 180,
      height: 30,
      toJSON: () => ({}),
    });

    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef);

    const layer = document.querySelector<HTMLElement>(
      '[data-floating-menu-owner="codex-runtime"]'
    );
    expect(layer?.classList.contains("floating-menu-below")).toBe(true);
    expect(Number.parseFloat(layer?.style.left ?? "")).toBeGreaterThan(0);
    expect(layer?.style.top).toBe("78px");
    expect(layer?.style.bottom).toBe("");
    expect(layer?.style.getPropertyValue("--floating-menu-available-height")).toBe(
      `${window.innerHeight - 86}px`
    );
  });

  it("centers a tool-rail model panel on its button instead of dropping below it", () => {
    const initialized = runtimeWithEffort();
    const anchorRef = createRef<HTMLDivElement>();
    renderPicker(null, initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef, { inToolRail: true });
    vi.spyOn(anchorRef.current as HTMLDivElement, "getBoundingClientRect").mockReturnValue({
      x: 940,
      y: 400,
      top: 400,
      left: 940,
      right: 968,
      bottom: 428,
      width: 28,
      height: 28,
      toJSON: () => ({}),
    });

    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef, { inToolRail: true });

    const layer = document.querySelector<HTMLElement>(
      '[data-floating-menu-owner="codex-runtime"]'
    );
    expect(layer?.classList.contains("floating-menu-middle")).toBe(true);
    expect(layer?.style.top).toBe("414px");
    expect(layer?.style.transform).toBe("translateY(-50%)");
  });

  it("pulls a measured tool-rail panel back inside the viewport", async () => {
    const initialized = runtimeWithEffort();
    const anchorRef = createRef<HTMLDivElement>();
    renderPicker(null, initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef, { inToolRail: true });
    vi.spyOn(anchorRef.current as HTMLDivElement, "getBoundingClientRect").mockReturnValue({
      x: 940,
      y: 700,
      top: 700,
      left: 940,
      right: 968,
      bottom: 728,
      width: 28,
      height: 28,
      toJSON: () => ({}),
    });

    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef, { inToolRail: true });

    const layer = document.querySelector<HTMLElement>(
      '[data-floating-menu-owner="codex-runtime"]'
    )!;
    const panel = layer.firstElementChild as HTMLElement;
    expect(panel).not.toBeNull();
    Object.defineProperty(panel, "offsetHeight", { configurable: true, value: 300 });

    await act(async () => {
      await new Promise(requestAnimationFrame);
    });

    expect(layer.style.top).toBe(`${window.innerHeight - 8 - 150}px`);
  });

  it("keeps the dock model card above the trigger after a software keyboard lifts the composer", async () => {
    const initialized = runtimeWithEffort();
    const anchorRef = createRef<HTMLDivElement>();
    const viewport = Object.assign(new EventTarget(), {
      offsetLeft: 0,
      offsetTop: 0,
      width: window.innerWidth,
      height: window.innerHeight,
    });
    vi.stubGlobal("visualViewport", viewport);
    renderPicker(null, initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef);
    vi.spyOn(anchorRef.current as HTMLDivElement, "getBoundingClientRect").mockReturnValue({
      x: 180,
      y: 620,
      top: 620,
      left: 180,
      right: 360,
      bottom: 652,
      width: 180,
      height: 32,
      toJSON: () => ({}),
    });

    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchorRef);

    const layer = document.querySelector<HTMLElement>(
      '[data-floating-menu-owner="codex-runtime"]'
    );
    expect(layer?.classList.contains("floating-menu-above")).toBe(true);
    expect(layer?.style.bottom).toBe(`${window.innerHeight - 612}px`);
    expect(layer?.style.top).toBe("");

    viewport.height = 420;
    await act(async () => {
      viewport.dispatchEvent(new Event("resize"));
      await new Promise(requestAnimationFrame);
    });
    expect(layer?.classList.contains("floating-menu-above")).toBe(true);
    expect(layer?.style.bottom).toBe(`${window.innerHeight - 420 + 8}px`);
    expect(layer?.style.getPropertyValue("--floating-menu-available-height")).toBe("404px");
  });

  it("uses the target model default instead of carrying effort across models, with optimistic highlighting", () => {
    const initialized = runtimeWithEffort();
    initialized.model = "model-a";
    initialized.variant = "max";
    initialized.providers![0].model = "model-a";
    initialized.providers![0].models = [
      {
        id: "model-a",
        display_name: "Model A",
        default_effort: "medium",
        supported_efforts: ["medium", "max"]
      },
      {
        id: "model-b",
        display_name: "Model B",
        default_effort: "medium",
        supported_efforts: ["medium", "max"]
      }
    ];
    const onSelectModel = vi.fn();

    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);

    const choices = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".codex-model-menu .codex-model-item")
    );
    const modelB = choices.find((choice) => choice.textContent?.includes("Model B"))!;
    act(() => modelB?.click());

    expect(onSelectModel).toHaveBeenCalledWith("work", "model-b", "medium");
    // The flat panel previews the target model's own default effort before
    // the stream round-trip.
    expect(document.querySelector('.codex-model-item[aria-checked="true"]')?.textContent).toContain("Model B");
    expect(document.querySelector('.runtime-effort-option[aria-checked="true"] .runtime-effort-option-name')?.textContent)
      .toBe(variantLabel("medium"));
  });

  it("restores the last effort when selecting a previously used model", () => {
    writeDraftRuntimeMemory({
      provider: "work",
      model: "model-b",
      effort: "max",
    });
    const initialized = runtimeWithEffort();
    initialized.model = "model-a";
    initialized.variant = "medium";
    initialized.providers![0].model = "model-a";
    initialized.providers![0].models = [
      {
        id: "model-a",
        display_name: "Model A",
        default_effort: "medium",
        supported_efforts: ["medium", "max"],
      },
      {
        id: "model-b",
        display_name: "Model B",
        default_effort: "medium",
        supported_efforts: ["medium", "max"],
      },
    ];
    const onSelectModel = vi.fn();

    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    const modelB = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
      .find((choice) => choice.textContent?.includes("Model B"));
    act(() => modelB?.click());

    expect(onSelectModel).toHaveBeenCalledWith("work", "model-b", "max");
  });

  it("mirrors the panel columns when it opens from the workspace tool rail", () => {
    const initialized = runtimeWithEffort();
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { inToolRail: true });

    expect(document.querySelector(".runtime-flat-panel.from-rail")).not.toBeNull();
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef());
    expect(document.querySelector(".runtime-flat-panel.from-rail")).toBeNull();
    expect(document.querySelector(".runtime-flat-panel")).not.toBeNull();
  });

  it("selects a visible model row by its numbered shortcut", () => {
    const onSelectModel = vi.fn();
    const initialized = runtimeWithEffort();
    initialized.providers![0].models!.push({ id: "claude-opus", display_name: "Claude Opus" });
    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    const panel = document.querySelector<HTMLElement>(".runtime-flat-panel")!;

    // Rows carry a kbd hint for the first nine; ⌘2 picks the second row.
    expect(panel.querySelectorAll(".runtime-model-kbd")).toHaveLength(2);
    act(() => {
      panel.dispatchEvent(new KeyboardEvent("keydown", { key: "2", metaKey: true, bubbles: true, cancelable: true }));
    });
    expect(onSelectModel).toHaveBeenCalledWith("work", "claude-opus", "");
    expect(document.querySelector('.codex-model-item[aria-checked="true"]')?.textContent).toContain("Claude Opus");
  });
});
