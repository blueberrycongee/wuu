import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineListResult, ExtensionInventoryRecord, WuuDesktopApi } from "../shared/protocol";
import {
  FirstRunOnboarding,
  bundledOnboardingPlugins,
  discoveredCodexCredential,
  hasOnboardingProvider,
  recommendedOnboardingEngine,
} from "./FirstRunOnboarding";
import { I18nProvider } from "./i18n";
import { ONBOARDING_PLUGIN_ORDER } from "./onboardingCatalog";

function plugin(
  id: string,
  enabled: boolean,
  source: "bundled" | "user" = "bundled",
): ExtensionInventoryRecord {
  return {
    id: `plugin:${source}:${id}`,
    name: id,
    description: `${id} description`,
    kind: "plugin",
    state: "active",
    enabled,
    fingerprint: `${id}-fingerprint`,
    icon: { name: "plug" },
    package_source: source,
    provenance: {
      kind: "plugin",
      source,
      scope: source,
      official: source === "bundled",
      plugin_id: id,
    },
  };
}

describe("FirstRunOnboarding", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    window.wuu = {
      initialLanguagePreference: "zh-CN",
      initialSystemLocale: "zh-CN",
      initialThemePreference: "dark",
    } as unknown as WuuDesktopApi;
    document.documentElement.dataset.theme = "dark";
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
    vi.restoreAllMocks();
  });

  it("offers only known official plugins distributed with Wuu", () => {
    const inventory = [
      plugin("todo", true),
      plugin("ask-user", false),
      plugin("user-theme", true, "user"),
      plugin("peers", false),
      plugin("note-compaction", false),
    ];

    expect(bundledOnboardingPlugins(inventory).map((item) => item.id)).toEqual([
      "plugin:bundled:ask-user",
      "plugin:bundled:todo",
      "plugin:bundled:peers",
    ]);
  });

  it.each(["goal", "peers"])("allows enabling %s from the bundled onboarding choices", async (id) => {
    const update = vi.fn(async () => undefined);
    await act(async () => root.render(
      <I18nProvider><FirstRunOnboarding
        inventory={[plugin(id, false)]} providers={[]}
        onUpdateExtensionPackage={update}
        onSaveProvider={vi.fn(async () => undefined)}
        onComplete={vi.fn(async () => undefined)}
      /></I18nProvider>,
    ));
    await clickButton("开始设置");
    await clickPlugin(id);
    await clickButton("继续");
    expect(update).toHaveBeenCalledWith({ id: `plugin:bundled:${id}`, action: "enable" });
  });

  it("recognizes configured and locked model providers", () => {
    expect(hasOnboardingProvider([{ name: "a", type: "x", model: "m" }])).toBe(false);
    expect(hasOnboardingProvider([{ name: "a", type: "x", model: "m", api_key_configured: true }])).toBe(true);
    expect(hasOnboardingProvider([{ name: "a", type: "x", model: "m", connection_locked: true }])).toBe(true);
    expect(hasOnboardingProvider([{ name: "openai-codex", type: "openai-codex", model: "gpt-6-astra", connection_locked: true }])).toBe(false);
    expect(hasOnboardingProvider([{
      name: "openai-codex",
      type: "openai-codex",
      model: "gpt-6-astra",
      connection_locked: true,
      reuse_codex_credentials: true,
      codex_credential_source: "codex-cli",
    }])).toBe(true);
  });

  it("recommends Wuu even when an external engine is already installed", () => {
    expect(recommendedOnboardingEngine([
      { id: "wuu", enabled: true, binary_ok: true },
      { id: "codex", enabled: true, binary_ok: true },
    ])).toBe("wuu");
  });

  it("decorates the runtime mascot with the selected engine mark", async () => {
    const engines: EngineListResult = {
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: true, binary_ok: true },
        { id: "claude", enabled: true, binary_ok: true },
      ],
      settings: { default_engine: "wuu" },
    };
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("todo", true)]}
            providers={[]}
            engines={engines}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={vi.fn(async () => undefined)}
            onUpdateEngines={vi.fn(async () => undefined)}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    await clickButton("继续");

    expect(container.querySelector("[data-wuu-mascot-follows-pointer]")).not.toBeNull();
    expect(mascotStage()?.getAttribute("data-onboarding-engine")).toBe("wuu");
    expect(mascotStage()?.querySelector("[data-onboarding-engine-mark]")).toBeNull();

    await act(async () => {
      container.querySelector<HTMLButtonElement>("[data-testid=onboarding-engine-codex]")
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(mascotStage()?.querySelector("[data-onboarding-engine-mark]")?.getAttribute("data-onboarding-engine-mark")).toBe("codex");

    await act(async () => {
      container.querySelector<HTMLButtonElement>("[data-testid=onboarding-engine-claude]")
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(mascotStage()?.querySelector("[data-onboarding-engine-mark]")?.getAttribute("data-onboarding-engine-mark")).toBe("claude");

    await act(async () => {
      container.querySelector<HTMLButtonElement>("[data-testid=onboarding-engine-wuu]")
        ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(mascotStage()?.querySelector("[data-onboarding-engine-mark]")).toBeNull();
  });

  it("discovers a local Codex login without treating it as already configured", () => {
    const providers = [{
      name: "openai-codex",
      type: "openai-codex",
      model: "gpt-6-astra",
      connection_locked: true,
      codex_credential_source: "codex-cli",
    }];
    expect(discoveredCodexCredential(providers)?.name).toBe("openai-codex");
    expect(hasOnboardingProvider(providers)).toBe(false);
  });

  it("does not request credentials when a model provider is already configured", async () => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("todo", true)]}
            providers={[{ name: "openai", type: "openai-compatible", model: "gpt-5", api_key_configured: true }]}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={vi.fn(async () => undefined)}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });

    await clickButton("开始设置");
    await clickButton("继续");
    await clickButton("继续");

    expect(container.querySelector(".onboarding-stage-provider")).not.toBeNull();
    expect(container.querySelector("input")).toBeNull();
    expect(container.querySelector("dl")?.textContent).toContain("openai");
    expect(container.querySelector("dl")?.textContent).toContain("gpt-5");
    await clickButton("继续");
    expect(container.querySelector(".onboarding-stage-ready")).not.toBeNull();
  });

  it("selects the recommended subject IDs when inventory arrives after mount", async () => {
    const props = {
      providers: [],
      onUpdateExtensionPackage: vi.fn(async () => undefined),
      onSaveProvider: vi.fn(async () => undefined),
      onComplete: vi.fn(async () => undefined),
    };

    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding {...props} />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    expect(container.textContent).toContain("正在准备随包插件");

    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            {...props}
            inventory={[
              plugin("ask-user", true),
              plugin("todo", false),
              plugin("automation", false),
              plugin("subagent", true),
              plugin("peers", true),
              plugin("memory", true),
            ]}
          />
        </I18nProvider>,
      );
    });

    expect(container.textContent).not.toContain("正在准备随包插件");
    const selectedPlugins = () => [...container.querySelectorAll(".onboarding-plugin[aria-pressed=true] strong")]
      .map((node) => node.textContent);
    expect(selectedPlugins()).toEqual(["todo", "automation"]);
    expect(container.querySelector(".onboarding-presets .is-selected")?.textContent).toBe("推荐");
    await clickButton("全部");
    expect(selectedPlugins()).toHaveLength(6);
    await clickButton("推荐");
    expect(selectedPlugins()).toEqual(["todo", "automation"]);
    expect(props.onUpdateExtensionPackage).not.toHaveBeenCalled();
    await clickButton("继续");
    expect(props.onUpdateExtensionPackage).toHaveBeenCalledTimes(6);
    for (const id of ["todo", "automation"]) {
      expect(props.onUpdateExtensionPackage).toHaveBeenCalledWith({ id: `plugin:bundled:${id}`, action: "enable" });
    }
    for (const id of ["ask-user", "subagent", "peers", "memory"]) {
      expect(props.onUpdateExtensionPackage).toHaveBeenCalledWith({ id: `plugin:bundled:${id}`, action: "disable" });
    }
  });

  it("splits the mascot into three colored clones that keep stacked decorations", async () => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("automation", false), plugin("subagent", false)]}
            providers={[]}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={vi.fn(async () => undefined)}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    expect(mascotStage()?.hasAttribute("data-onboarding-split")).toBe(false);
    expect(visibleClones()).toHaveLength(1);
    expect(wornCapabilities()).toEqual(["automation"]);

    await clickPlugin("subagent");
    expect(mascotStage()?.hasAttribute("data-onboarding-split")).toBe(true);
    expect(visibleClones()).toHaveLength(3);
    const alarm = mascotStage()?.querySelector("[data-onboarding-capability=automation]");
    expect(alarm).not.toBeNull();

    await clickPlugin("subagent");
    expect(visibleClones()).toHaveLength(1);
    expect(mascotStage()?.querySelector("[data-onboarding-capability=automation]")).toBe(alarm);
    await clickPlugin("subagent");
    expect(visibleClones()).toHaveLength(3);
    expect(mascotStage()?.querySelector("[data-onboarding-capability=automation]")).toBe(alarm);
  });

  it("stacks every official accessory through selection and dismissal without writing settings", async () => {
    const update = vi.fn(async () => undefined);
    const complete = vi.fn(async () => undefined);
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={ONBOARDING_PLUGIN_ORDER.map((id) => plugin(id, false))}
            providers={[]}
            onUpdateExtensionPackage={update}
            onSaveProvider={vi.fn(async () => undefined)}
            onComplete={complete}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    await clickButton("极简");
    const selected: string[] = [];
    const rendered = new Map<string, Element>();
    for (const id of ONBOARDING_PLUGIN_ORDER) {
      await clickPlugin(id);
      selected.push(id);
      expect(container.querySelectorAll(".onboarding-plugin[aria-pressed=true]")).toHaveLength(selected.length);
      const expected = selected.filter((pluginID) => pluginID !== "subagent");
      expect(wornCapabilities().sort()).toEqual([...expected].sort());
      for (const decoration of expected) {
        const node = mascotStage()?.querySelector(`[data-onboarding-capability="${decoration}"]`);
        expect(node).not.toBeNull();
        if (rendered.has(decoration)) expect(node).toBe(rendered.get(decoration));
        else rendered.set(decoration, node!);
      }
      expect(visibleClones()).toHaveLength(selected.includes("subagent") ? 3 : 1);
    }
    // Removing one capability leaves all the others intact.
    await clickPlugin("memory");
    expect(wornCapabilities()).not.toContain("memory");
    expect(wornCapabilities()).toHaveLength(6);
    await clickButton("极简");
    expect(visibleClones()).toHaveLength(1);
    expect(wornCapabilities()).toEqual([]);
    await clickButton("全部");
    expect(visibleClones()).toHaveLength(3);
    expect(wornCapabilities().sort()).toEqual(ONBOARDING_PLUGIN_ORDER.filter((id) => id !== "subagent").sort());
    expect(update).not.toHaveBeenCalled();
    expect(complete).not.toHaveBeenCalled();
  });

  it("loads manifest icon assets with the plugin subject ID", async () => {
    const todo = plugin("todo", true);
    todo.icon = { path: "assets/icon.svg" };
    const loadPluginIcon = vi.fn(async (params) => ({
      ...params,
      url: "data:image/svg+xml;base64,PHN2Zy8+",
    }));
    window.wuu.loadPluginIcon = loadPluginIcon;

    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[todo]}
            providers={[]}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={vi.fn(async () => undefined)}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");

    expect(loadPluginIcon).toHaveBeenCalledWith({
      id: "plugin:bundled:todo",
      fingerprint: "todo-fingerprint",
      path: "assets/icon.svg",
    });
  });

  it("gates entry until explicit plugin choices are applied and completion is persisted", async () => {
    const update = vi.fn(async () => undefined);
    const complete = vi.fn(async () => undefined);

    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("ask-user", true), plugin("memory", false)]}
            providers={[]}
            onUpdateExtensionPackage={update}
            onSaveProvider={vi.fn(async () => undefined)}
            onComplete={complete}
          />
        </I18nProvider>,
      );
    });

    expect(container.querySelector('[data-testid="first-run-onboarding"]')).not.toBeNull();
    expect(container.textContent).toContain("保持简单，按需生长");
    expect(container.textContent).not.toContain("欢迎来到 Wuu");
    expect(container.textContent).not.toContain("Wuu 的核心负责可靠地运行 Agent");
    expect(document.documentElement.dataset.theme).toBe("light");

    await clickButton("开始设置");
    await clickButton("极简");
    await clickButton("继续");

    expect(update).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledWith({ id: "plugin:bundled:ask-user", action: "disable" });
    expect(complete).not.toHaveBeenCalled();

    expect(container.textContent).toContain("先决定谁来执行任务");
    await clickButton("继续");

    await clickButton("稍后配置");
    expect(container.textContent).toContain("你的 Wuu 已经准备好了");
    expect(complete).not.toHaveBeenCalled();

    await clickButton("开始使用 Wuu");
    expect(complete).toHaveBeenCalledTimes(1);

    act(() => root.unmount());
    expect(document.documentElement.dataset.theme).toBe("dark");
    root = createRoot(container);
  });

  it("configures Grok Build without asking for an API key", async () => {
    const save = vi.fn(async () => undefined);
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("ask-user", false)]}
            providers={[]}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={save}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    await clickButton("极简");
    await clickButton("继续");
    await clickButton("继续");

    const type = container.querySelector("select") as HTMLSelectElement;
    await act(async () => {
      type.value = "grok-build";
      type.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(container.textContent).toContain("grok login");
    expect(container.querySelector("input[type='password']")).toBeNull();
    await clickButton("继续");
    expect(save).toHaveBeenCalledWith("grok-build", "grok-4.5", {
      type: "grok-build",
      create_provider: true,
      base_url: "https://cli-chat-proxy.grok.com/v1",
    });
  });

  it("lets the user confirm a discovered Codex login before using it", async () => {
    const save = vi.fn(async () => undefined);
    const updateEngines = vi.fn(async () => undefined);
    const engines: EngineListResult = {
      engines: [
        { id: "wuu", enabled: true, binary_ok: true },
        { id: "codex", enabled: true, binary_ok: true },
      ],
      settings: { default_engine: "wuu" },
    };
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("todo", true)]}
            providers={[{
              name: "openai-codex",
              type: "openai-codex",
              model: "gpt-6-astra",
              connection_locked: true,
              codex_credential_source: "codex-cli",
            }]}
            engines={engines}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={save}
            onUpdateEngines={updateEngines}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    await clickButton("继续");
    expect(container.querySelector('[data-testid="onboarding-reuse-codex"]')).toBeNull();
    await clickButton("继续");
    expect(updateEngines).toHaveBeenCalledWith({ default_engine: "wuu" });
    expect(save).not.toHaveBeenCalled();
    const reuse = container.querySelector<HTMLButtonElement>('[data-testid="onboarding-reuse-codex"]');
    expect(reuse).not.toBeNull();
    await act(async () => reuse!.click());
    expect(save).toHaveBeenCalledWith("openai-codex", "gpt-6-astra", {
      reuse_codex_credentials: true,
    });
    expect(container.querySelector(".onboarding-stage-ready")).not.toBeNull();
  });

  it.each(["skip", "retry"])("handles Codex reuse through %s without implicit credential changes", async (mode) => {
    const save = vi.fn(async () => undefined);
    if (mode === "retry") save.mockRejectedValueOnce(new Error("Connection unavailable"));
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            inventory={[plugin("todo", true)]}
            providers={[{ name: "local-codex", type: "openai-codex", model: "test-model", codex_credential_source: "codex-cli" }]}
            onUpdateExtensionPackage={vi.fn(async () => undefined)}
            onSaveProvider={save}
            onComplete={vi.fn(async () => undefined)}
          />
        </I18nProvider>,
      );
    });
    await clickButton("开始设置");
    await clickButton("继续");
    await clickButton("继续");
    expect(save).not.toHaveBeenCalled();
    if (mode === "skip") {
      await clickButton("稍后配置");
    } else {
      const reuse = container.querySelector<HTMLButtonElement>('[data-testid="onboarding-reuse-codex"]')!;
      await act(async () => reuse.click());
      if (mode === "retry") {
        expect(container.querySelector('[role="alert"]')?.textContent).toBe("Connection unavailable");
        expect(container.querySelector(".onboarding-stage-provider")).not.toBeNull();
        expect(reuse.disabled).toBe(false);
        await act(async () => reuse.click());
        expect(save).toHaveBeenCalledTimes(2);
      }
    }
    expect(container.querySelector(".onboarding-stage-ready")).not.toBeNull();
    if (mode !== "retry") expect(save).not.toHaveBeenCalled();
  });

  it.each([
    ["skip", "codex", "claude"],
    ["connect", "claude", "codex"],
  ])("previews %s with installed %s and missing %s without writing local settings", async (mode, installed, missing) => {
    // The standalone preview has no product preload bridge at all.
    Reflect.deleteProperty(window, "wuu");
    vi.spyOn(navigator, "language", "get").mockReturnValue("zh-CN");
    const updateExtension = vi.fn(async () => undefined);
    const save = vi.fn(async () => undefined);
    const updateEngines = vi.fn(async () => undefined);
    const onComplete = vi.fn(async () => undefined);
    const onDismissPreview = vi.fn();
    await act(async () => {
      root.render(
        <I18nProvider>
          <FirstRunOnboarding
            preview
            inventory={[]}
            providers={[
              { name: "openai", type: "openai-compatible", model: "gpt-5", api_key_configured: true },
              { name: "local-codex", type: "openai-codex", model: "test-model", codex_credential_source: "codex-cli" },
            ]}
            engines={{ engines: [
              { id: installed, enabled: true, binary_ok: true },
              { id: missing, enabled: false, binary_ok: false },
            ], settings: { default_engine: installed } }}
            onDismissPreview={onDismissPreview}
            onUpdateExtensionPackage={updateExtension}
            onSaveProvider={save}
            onUpdateEngines={updateEngines}
            onComplete={onComplete}
          />
        </I18nProvider>,
      );
    });

    expect(container.querySelector("[data-testid=\"onboarding-preview-exit\"]")?.textContent).toBe("退出预览");
    await clickButton("开始设置");
    expect(container.querySelectorAll('.onboarding-plugin[aria-pressed="true"]').length).toBeGreaterThan(0);
    await clickPlugin("Goal");
    expect(mascotStage()?.querySelector('[data-onboarding-capability="goal"]')).not.toBeNull();
    await clickButton("继续");
    const installedChoice = container.querySelector<HTMLButtonElement>(`[data-testid="onboarding-engine-${installed}"]`)!;
    expect(installedChoice.disabled).toBe(false);
    expect(container.querySelector<HTMLButtonElement>(`[data-testid="onboarding-engine-${missing}"]`)?.disabled).toBe(true);
    await act(async () => installedChoice.click());
    expect(installedChoice.getAttribute("aria-checked")).toBe("true");
    await clickButton("继续");
    expect(container.querySelector('[data-testid="onboarding-reuse-codex"]')).toBeNull();
    const inputs = [...container.querySelectorAll<HTMLInputElement>("input")];
    expect(inputs).toHaveLength(3);
    expect(inputs.every((input) => input.value === "")).toBe(true);
    if (mode === "skip") {
      await clickButton("稍后配置");
    } else {
      await act(async () => {
        for (const [index, value] of ["preview-provider", "preview-model", "not-a-real-key"].entries()) {
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(inputs[index], value);
          inputs[index].dispatchEvent(new Event("input", { bubbles: true }));
        }
      });
      await clickButton("继续");
    }
    expect(container.querySelector(".onboarding-stage-ready")).not.toBeNull();
    await clickButton("开始使用 Wuu");

    expect(updateExtension).not.toHaveBeenCalled();
    expect(save).not.toHaveBeenCalled();
    expect(updateEngines).not.toHaveBeenCalled();
    expect(onComplete).not.toHaveBeenCalled();
    expect(onDismissPreview).toHaveBeenCalledTimes(1);
  });

  async function clickButton(label: string): Promise<void> {
    const button = [...container.querySelectorAll("button")].find(
      (candidate) => candidate.textContent?.trim() === label,
    );
    expect(button, `missing button ${label}`).toBeDefined();
    await act(async () => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
  }

  async function clickPlugin(name: string): Promise<void> {
    const button = [...container.querySelectorAll(".onboarding-plugin")].find(
      (candidate) => candidate.querySelector("strong")?.textContent === name,
    );
    expect(button, `missing plugin ${name}`).toBeDefined();
    await act(async () => {
      button?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
  }

  function mascotStage(): HTMLElement | null {
    return container.querySelector("[data-testid=\"onboarding-mascot-stage\"]");
  }

  function visibleClones(): Element[] {
    return [...mascotStage()!.querySelectorAll("[data-onboarding-companion]:not([hidden])")];
  }

  function wornCapabilities(): string[] {
    return [...mascotStage()!.querySelectorAll("[data-onboarding-capability]")]
      .map((node) => node.getAttribute("data-onboarding-capability")!);
  }
});
