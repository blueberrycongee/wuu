import { act, createRef, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { writeDraftRuntimeMemory } from "./DraftRuntimeMemory";
import { permissionModeOption, RuntimePicker } from "./ComposerRuntimeMenus";
import type { CodexRuntimeMenu } from "./ComposerTypes";
import { setActiveLocale } from "./i18n";

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

  it("opens the model panel from the trigger with a single click", () => {
    const onToggleMenu = vi.fn();
    renderPicker(null, runtimeWithEffort(), onToggleMenu);

    const trigger = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(trigger?.getAttribute("aria-expanded")).toBe("false");
    expect(trigger?.textContent).toContain("Claude Sonnet");
    expect(trigger?.textContent).toContain("Medium");

    act(() => trigger?.click());

    expect(onToggleMenu).toHaveBeenCalledWith("model");
  });

  it("shows the level stored in effort when the variant column is empty", () => {
    const initialized = runtimeWithEffort();
    initialized.variant = "";
    initialized.effort = "max";
    renderPicker(null, initialized);

    const trigger = document.querySelector<HTMLButtonElement>(".codex-runtime-trigger");
    expect(trigger?.textContent).toContain("Max");
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
    expect(menu?.textContent).toContain("Wuu");
    expect(menu?.textContent).toContain("work");
    expect(menu?.textContent).toContain("Claude Sonnet");
    expect(menu?.textContent).toContain("Medium");
    expect(menu?.querySelector(".select-menu-search input")).toBeNull();
    const effortSlider = menu?.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]');
    expect(effortSlider?.value).toBe("2");
    expect(effortSlider?.getAttribute("aria-valuetext")).toBe("Medium");
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

  it("fits the model page to the visible rows instead of stretching them", () => {
    const initialized = runtimeWithEffort();
    initialized.providers![0].models = [
      { id: "grok-4.6", display_name: "Grok 4.6", supported_efforts: ["low", "medium", "high"] },
      { id: "grok-4.5", display_name: "Grok 4.5", supported_efforts: ["low", "medium", "high"] }
    ];
    renderPicker("model", initialized);
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const two = Number.parseInt(
      document.querySelector<HTMLElement>(".codex-model-menu")?.style.getPropertyValue("--runtime-models-height") ?? "",
      10
    );

    initialized.providers![0].models = [
      { id: "grok-4.6", display_name: "Grok 4.6", supported_efforts: ["low", "medium", "high"] }
    ];
    renderPicker("model", initialized);
    act(() => document.querySelector<HTMLButtonElement>(".runtime-panel-model")?.click());
    const one = Number.parseInt(
      document.querySelector<HTMLElement>(".codex-model-menu")?.style.getPropertyValue("--runtime-models-height") ?? "",
      10
    );

    expect(two).toBeGreaterThan(one);
    expect(two - one).toBe(37);
    expect(two).toBeLessThan(320);
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
    expect(engineChoices[1]?.getAttribute("aria-checked")).toBe("true");
    act(() => engineChoices[0]?.click());
    expect(onSelectEngine).toHaveBeenCalledWith("wuu");
  });

  it("keeps every engine visible when the current conversation locks engine switching", () => {
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

    const engineContext = document.querySelector<HTMLButtonElement>(".runtime-panel-context button");
    act(() => engineContext?.click());
    const choices = Array.from(
      document.querySelectorAll<HTMLButtonElement>(".runtime-engine-option")
    );
    expect(choices.map((choice) => choice.querySelector(".runtime-engine-option-name")?.textContent)).toEqual([
      "Wuu",
      "Codex"
    ]);
    expect(choices.every((choice) => choice.disabled)).toBe(true);
  });

  it("builds permission labels in the active language", () => {
    setActiveLocale("en-US");

    expect(permissionModeOption("standard")).toMatchObject({
      label: "Full trust within workspace",
      chipLabel: "Standard",
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

  it("selects a discrete effort by dragging the unlabeled slider", () => {
    const onSelectEffort = vi.fn();
    renderPicker("model", runtimeWithEffort(), vi.fn(), onSelectEffort);

    const slider = document.querySelector<HTMLInputElement>('.codex-effort-slider input[type="range"]')!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(slider, "3");
      slider.dispatchEvent(new Event("input", { bubbles: true }));
    });

    expect(document.querySelector(".runtime-panel-effort-value")?.textContent).toBe("High");
    expect(onSelectEffort).not.toHaveBeenCalled();

    act(() => slider.dispatchEvent(new Event("pointerup", { bubbles: true })));

    expect(onSelectEffort).toHaveBeenCalledTimes(1);
    expect(onSelectEffort).toHaveBeenCalledWith("high");
    expect(slider.value).toBe("3");
    expect(slider.getAttribute("aria-valuetext")).toBe("High");
  });

  it("maps pointer spans and cancels a drag without saving", () => {
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
    expect(selectedEffort?.getAttribute("aria-valuetext")).toBe("Medium");
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
