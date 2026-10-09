import { act, createRef, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { permissionModeOption, RuntimePicker } from "./ComposerRuntimeMenus";
import type { CodexRuntimeMenu } from "./ComposerTypes";
import { setActiveLocale, translateCurrent } from "./i18n";
import { variantLabel } from "./RuntimeHelpers";

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
    const slider = document.querySelector<HTMLInputElement>('input[type="range"]')!;
    const step = (value: string): void => {
      act(() => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(slider, value);
        slider.dispatchEvent(new Event("input", { bubbles: true }));
        slider.dispatchEvent(new KeyboardEvent("keyup", { key: "ArrowRight", bubbles: true }));
      });
    };
    act(() => slider.focus());
    step("2");
    expect(onSelectEngineEffort).toHaveBeenCalledExactlyOnceWith("high");
    expect(document.activeElement).toBe(slider);
    expect(slider.disabled).toBe(false);
    expect(slider.getAttribute("aria-busy")).toBe("true");
    step("1");
    expect(onSelectEngineEffort).toHaveBeenCalledTimes(1);
    await act(async () => {
      if (saved) {
        renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), anchor, { ...engineProps, engineEffort: "high" });
      }
      finish(saved);
    });
    expect(document.activeElement).toBe(slider);
    expect(slider.getAttribute("aria-busy")).toBeNull();
    expect(slider.value).toBe(saved ? "2" : "1");
    expect(document.querySelector(".runtime-panel-effort-value")?.textContent).toBe(variantLabel(saved ? "high" : "low"));

    step(saved ? "1" : "2");
    expect(onSelectEngineEffort).toHaveBeenCalledTimes(2);
    expect(onSelectEngineEffort).toHaveBeenLastCalledWith(saved ? "low" : "high");
    await act(async () => finish(false));
    expect(document.activeElement).toBe(slider);
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
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="Fast mode"]');
    expect(button?.disabled).toBe(false);
    await act(async () => button?.click());
    expect(speed).toHaveBeenCalledWith("fast");
    expect(effort).not.toHaveBeenCalled();
    expect(model).not.toHaveBeenCalled();
    expect(toggleMenu).not.toHaveBeenCalled();
    expect(document.querySelector(".runtime-panel.is-summary")).not.toBeNull();
    expect(button?.getAttribute("aria-description")).toBeTruthy();
    expect(button?.parentElement?.closest("button")).toBeNull();
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
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="Fast mode"]');
    expect(button?.getAttribute("aria-pressed")).toBe(String(enabled));
    await act(async () => button?.click());
    expect(onSelectSpeed).toHaveBeenCalledWith(next);
    expect(button?.getAttribute("aria-pressed")).toBe(String(!enabled));
    act(() => button?.focus());
    await act(async () => button?.click());
    expect(onSelectSpeed).toHaveBeenLastCalledWith(enabled ? "fast" : "standard");
    expect(document.activeElement).toBe(button);
    expect(button?.getAttribute("aria-pressed")).toBe(String(enabled));
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
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="Fast mode"]')!;
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
    expect(document.querySelector('button[aria-label="Fast mode"]')).toBeNull();
  });

  it("restores the speed toggle after a rejected update", async () => {
    const initialized = runtimeWithEffort();
    initialized.speed = "standard";
    initialized.providers![0].models![0].fast_mode = true;
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed: vi.fn().mockResolvedValue(false) });
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="Fast mode"]');
    await act(async () => button?.click());
    expect(button?.getAttribute("aria-pressed")).toBe("false");
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
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const rows = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"));
    expect(rows.map((row) => row.textContent?.trim())).toEqual(["Claude Sonnet"]);
  });

  it("opens as a compact summary and drills into the model list", () => {
    renderPicker("model", runtimeWithEffort());

    const menu = document.querySelector<HTMLElement>(".codex-model-menu");
    expect(menu).not.toBeNull();
    // The intermediate main menu is gone.
    expect(document.querySelector(".codex-main-menu")).toBeNull();

    expect(menu?.classList.contains("is-summary")).toBe(true);
    expect(menu?.textContent).toContain("work");
    expect(menu?.textContent).toContain("Claude Sonnet");
    expect(menu?.textContent).toContain(variantLabel("medium"));
    expect(menu?.querySelector(".select-menu-search input")).toBeNull();
    const effortSlider = menu?.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]');
    expect(effortSlider?.value).toBe("2");
    expect(effortSlider?.getAttribute("aria-valuetext")).toBe(variantLabel("medium"));
    expect(menu?.querySelector(".codex-effort-slider")?.textContent).toBe("");

    act(() => menu?.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());

    const search = menu?.querySelector<HTMLInputElement>(".select-menu-search input");
    expect(search?.placeholder).toBe("搜索模型");
    expect(menu?.querySelector(".runtime-panel-header")).toBeNull();
    expect(menu?.querySelector(".runtime-panel-back")).toBeNull();
    const modelItems = Array.from(menu?.querySelectorAll<HTMLButtonElement>(".codex-model-item") ?? []);
    expect(modelItems.map((item) => item.textContent?.trim())).toEqual(["Claude Sonnet"]);
    expect(modelItems[0]?.getAttribute("aria-checked")).toBe("true");
  });

  it("sizes each page to the rows it shows", () => {
    const rows = (): string =>
      document.querySelector<HTMLElement>(".codex-model-menu")?.style.getPropertyValue("--runtime-rows") ?? "";
    const initialized = runtimeWithEffort();
    initialized.providers![0].models = [
      { id: "grok-4.6", display_name: "Grok 4.6", supported_efforts: ["low", "medium", "high"] },
      { id: "grok-4.5", display_name: "Grok 4.5", supported_efforts: ["low", "medium", "high"] }
    ];
    initialized.model = "grok-4.6";
    initialized.providers![0].model = "grok-4.6";
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      engines: Array.from({ length: 7 }, (_, index) => ({
        id: index === 0 ? "wuu" : `engine-${index}`,
        enabled: true,
        binary_ok: true
      })),
      onSelectEngine: vi.fn()
    });

    // The shell height morphs between pages instead of following its
    // content, so a count that misses a row clips the last one.
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-context button")?.click());
    expect(rows()).toBe("7");

    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-back")?.click());
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    expect(rows()).toBe("2");

    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(search, "4.6");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(rows()).toBe("1");
  });

  it("reaches inline speed controls above the model and the effort slider with the arrow keys", async () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].models![0].fast_mode = true;
    renderPicker("model", initialized, vi.fn(), vi.fn(), vi.fn(), createRef(), { onSelectSpeed: vi.fn().mockResolvedValue(true) });
    await act(async () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
    const press = (key: string): void => {
      act(() => { document.activeElement?.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })); });
    };

    expect(document.activeElement).toBe(document.querySelector(".runtime-panel-model"));
    press("ArrowUp");
    expect(document.activeElement).toBe(document.querySelector(".runtime-panel-fast"));
    press("ArrowDown");
    expect(document.activeElement).toBe(document.querySelector(".runtime-panel-model"));
    press("ArrowDown");
    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(document.activeElement).toBe(slider);
    const level = slider.value;
    // Up and Down leave the slider; Left and Right stay its own steps.
    press("ArrowDown");
    expect(document.activeElement).toBe(document.querySelector(".runtime-panel-fast"));
    expect(slider.value).toBe(level);
  });

  it("steps back with Escape before closing the panel", () => {
    const onToggleMenu = vi.fn();
    renderPicker("model", runtimeWithEffort(), onToggleMenu);
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;

    act(() => { search.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })); });
    expect(document.querySelector(".runtime-panel.is-summary")).not.toBeNull();
    expect(onToggleMenu).not.toHaveBeenCalled();

    const model = document.querySelector<HTMLButtonElement>(".runtime-panel-model")!;
    act(() => { model.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })); });
    expect(onToggleMenu).toHaveBeenCalledWith("model");
  });

  it("picks the first matching model when Enter is pressed in search", () => {
    const onSelectModel = vi.fn();
    const initialized = runtimeWithEffort();
    initialized.providers![0].models!.push({ id: "claude-opus", display_name: "Claude Opus", supported_efforts: ["low", "high"] });
    renderPicker("model", initialized, vi.fn(), vi.fn(), onSelectModel);
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const search = document.querySelector<HTMLInputElement>(".select-menu-search input")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(search, "opus");
      search.dispatchEvent(new Event("input", { bubbles: true }));
    });
    act(() => { search.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });

    expect(onSelectModel).toHaveBeenCalledWith("work", "claude-opus", "");
    expect(document.querySelector(".runtime-panel.is-summary")).not.toBeNull();
  });

  it("presents engines as the parent navigation for the selected engine's models", () => {
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

    const engineContext = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-panel-context button"))
      .find((button) => button.textContent?.includes("Codex"));
    act(() => engineContext?.click());
    const engineRail = document.querySelector<HTMLElement>('.runtime-engine-options[role="group"]');
    expect(engineRail?.getAttribute("aria-label")).toBe("运行引擎");
    expect(engineRail?.textContent).not.toContain("Agent");
    const engineChoices = Array.from(
      engineRail?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []
    );
    expect(engineChoices.map((choice) => choice.querySelector(".runtime-engine-option-name")?.textContent)).toEqual([
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
    const model = document.querySelector(".runtime-panel-model-name")?.textContent;
    const effort = document.querySelector(".runtime-panel-effort-value")?.textContent;
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-context button:has(.engine-icon)")!.click());
    act(() => document.querySelector<HTMLButtonElement>('.runtime-engine-option[aria-checked="true"]')!.click());

    expect(document.querySelector(".runtime-panel.is-summary")).not.toBeNull();
    expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe(model);
    expect(document.querySelector(".runtime-panel-effort-value")?.textContent).toBe(effort);
    expect(onSelectEngine).not.toHaveBeenCalled();
    expect(onSelectModel).not.toHaveBeenCalled();
    expect(onSelectEffort).not.toHaveBeenCalled();
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")!.click());
    expect(document.querySelector(".runtime-panel.is-models")).not.toBeNull();
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
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-context button")!.click());
    const choices = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'));
    expect(choices.find((choice) => choice.getAttribute("aria-checked") === "true")!.textContent).toContain("Devin");
    expect(choices.some((choice) => choice.textContent?.includes("grok"))).toBe(false);
    act(() => choices.find((choice) => choice.textContent?.includes("Hermes"))!.click());
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
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const items = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item")).map((item) =>
      item.querySelector(".codex-model-item-name")?.textContent
    );
    expect(items).toEqual([translateCurrent("runtime.engineDefaultModel"), "Grok 4.6", "Grok 4.5"]);
  });

  it("names the engine only when another one can be chosen", () => {
    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      engines: [{ id: "wuu", enabled: true, binary_ok: true }],
    });
    expect(document.querySelector('.runtime-panel-context [aria-label*="Wuu"]')).toBeNull();

    renderPicker("model", runtimeWithEffort(), vi.fn(), vi.fn(), vi.fn(), createRef<HTMLDivElement>(), {
      engines: [{ id: "wuu", enabled: true, binary_ok: true }, { id: "codex", enabled: true, binary_ok: true }],
      onSelectEngine: vi.fn(),
    });
    act(() => document.querySelector<HTMLButtonElement>('.runtime-panel-context button[aria-label*="Wuu"]')?.click());
    expect(document.querySelectorAll(".runtime-engine-option")).toHaveLength(2);
  });

  it("explains a bound engine instead of opening choices it cannot take", () => {
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

    const context = document.querySelector<HTMLElement>(".runtime-panel-context")!;
    expect(context.querySelector("button")).toBeNull();
    expect(context.querySelector("[aria-label]")?.getAttribute("aria-label"))
      .toContain(translateCurrent("runtime.engineLockedDescription"));
    expect(document.querySelector(".runtime-engine-option")).toBeNull();
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

    expect(document.querySelector(".codex-effort-slider")).toBeNull();
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
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

    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());

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

    const items = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"));
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

    renderPicker("model", initialized);

    const providerContext = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-panel-context button"))
      .find((button) => button.textContent?.includes("tokenhub"));
    act(() => providerContext?.click());

    const providerOptions = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-provider-option"));
    expect(providerOptions.map((option) => option.textContent?.trim())).toEqual(["deepseek", "tokenhub"]);
    expect(providerOptions[1]?.getAttribute("aria-checked")).toBe("true");
    expect(document.querySelector(".runtime-panel-header")).toBeNull();
    expect(document.querySelector(".runtime-panel-back")).toBeNull();
    expect(document.querySelector(".codex-model-menu")?.textContent).not.toContain("模型服务");

    act(() => providerOptions[0]?.click());
    expect(document.querySelector(".runtime-panel-context")?.textContent).toContain("deepseek");
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    expect(document.querySelector(".codex-model-item-name")?.textContent).toBe("DeepSeek Chat");
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
    const providerContext = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-panel-context button"))
      .find((button) => button.textContent?.includes("tokenhub"));
    act(() => providerContext?.click());
    // The configured model stays the fallback, but the provider's own last pick
    // wins so switching back does not reset the model or its effort.
    const deepseek = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-provider-option"))
      .find((button) => button.textContent?.includes("deepseek"));
    act(() => deepseek?.click());

    expect(onSelectModel).toHaveBeenCalledWith("deepseek", "deepseek-reasoner", "high");
  });

  it("falls back to the provider's configured model when the remembered one is gone", () => {
    writeDraftRuntimeMemory({
      provider: "deepseek",
      model: "retired-model",
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
        models: [{ id: "deepseek-chat", display_name: "DeepSeek Chat" }],
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
    const providerContext = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-panel-context button"))
      .find((button) => button.textContent?.includes("tokenhub"));
    act(() => providerContext?.click());
    const deepseek = Array.from(document.querySelectorAll<HTMLButtonElement>(".runtime-provider-option"))
      .find((button) => button.textContent?.includes("deepseek"));
    act(() => deepseek?.click());

    expect(onSelectModel).toHaveBeenCalledWith("deepseek", "deepseek-chat", "");
  });

  it("places extra high at the rightmost stop even when efforts arrive descending", () => {
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

    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    expect(slider.max).toBe("4");
    expect(slider.value).toBe("4");
    expect(slider.getAttribute("aria-valuetext")).toBe(variantLabel("xhigh"));
    expect(document.querySelector(".runtime-panel-effort-value")?.textContent).toBe(variantLabel("xhigh"));
  });

  it("selects a discrete effort by dragging the unlabeled slider", () => {
    const onSelectEffort = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), onSelectEffort);

    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(slider, "3");
      slider.dispatchEvent(new Event("input", { bubbles: true }));
    });

    expect(document.querySelector(".runtime-panel-effort-value")?.textContent).toBe(variantLabel("high"));
    expect(onSelectEffort).not.toHaveBeenCalled();

    act(() => slider.dispatchEvent(new Event("pointerup", { bubbles: true })));

    expect(onSelectEffort).toHaveBeenCalledTimes(1);
    expect(onSelectEffort).toHaveBeenCalledWith("high");
    expect(slider.value).toBe("3");
    expect(slider.getAttribute("aria-valuetext")).toBe(variantLabel("high"));
  });

  it("snaps the pointer to the nearest level and cancels a drag without saving", () => {
    const onSelectEffort = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), onSelectEffort);
    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    slider.setPointerCapture = vi.fn();
    vi.spyOn(slider.parentElement!, "getBoundingClientRect").mockReturnValue({
      left: 100, width: 400, top: 0, right: 500, bottom: 30, height: 30, x: 100, y: 0, toJSON: () => ({})
    });
    const pointer = (type: string, clientX: number): void => {
      const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX });
      Object.defineProperty(event, "pointerId", { value: 1 });
      act(() => slider.dispatchEvent(event));
    };
    pointer("pointerdown", 110);
    expect(slider.value).toBe("0");
    pointer("pointermove", 490);
    expect(slider.value).toBe("3");
    expect(onSelectEffort).not.toHaveBeenCalled();
    pointer("pointercancel", 490);
    expect(slider.value).toBe("2");
    expect(onSelectEffort).not.toHaveBeenCalled();
    pointer("pointerdown", 210);
    pointer("pointerup", 210);
    expect(onSelectEffort).toHaveBeenCalledExactlyOnceWith("low");
  });

  it("commits keyboard changes once and ignores unrelated key releases", () => {
    const onSelectEffort = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), onSelectEffort);
    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(slider, "3");
      slider.dispatchEvent(new Event("input", { bubbles: true }));
      slider.dispatchEvent(new KeyboardEvent("keyup", { key: "ArrowRight", bubbles: true }));
      slider.dispatchEvent(new KeyboardEvent("keyup", { key: "Tab", bubbles: true }));
    });
    expect(onSelectEffort).toHaveBeenCalledExactlyOnceWith("high");
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

    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());

    const choices = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".codex-model-menu .codex-model-item")
    );
    const modelB = choices.find((choice) => choice.textContent?.includes("Model B"))!;
    act(() => modelB?.click());

    expect(onSelectModel).toHaveBeenCalledWith("work", "model-b", "medium");
    // The picker returns to the compact summary and previews the target
    // model's own default before the stream round-trip.
    expect(document.querySelector(".runtime-panel-model-name")?.textContent).toBe("Model B");
    const selectedEffort = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]');
    expect(selectedEffort?.getAttribute("aria-valuetext")).toBe(variantLabel("medium"));
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
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const modelB = Array.from(document.querySelectorAll<HTMLButtonElement>(".codex-model-item"))
      .find((choice) => choice.textContent?.includes("Model B"));
    act(() => modelB?.click());

    expect(onSelectModel).toHaveBeenCalledWith("work", "model-b", "max");
  });
});
