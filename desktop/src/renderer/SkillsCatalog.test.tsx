import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtensionInventoryRecord, SkillSummary, WuuDesktopApi } from "../shared/protocol";
import { SkillsCatalog } from "./SkillsCatalog";

const toastMocks = vi.hoisted(() => ({
  showErrorToast: vi.fn(),
  showToast: vi.fn(),
}));

vi.mock("./Toast", async (importOriginal) => ({
  ...await importOriginal<typeof import("./Toast")>(),
  showErrorToast: toastMocks.showErrorToast,
  showToast: toastMocks.showToast,
}));

vi.mock("./RichContent", () => ({
  RichContent: ({ text }: { text: string }) => <div data-testid="rich-content">{text}</div>,
}));

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  toastMocks.showErrorToast.mockClear();
  toastMocks.showToast.mockClear();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  delete (globalThis as { wuu?: WuuDesktopApi }).wuu;
  delete (window as { wuu?: WuuDesktopApi }).wuu;
  vi.restoreAllMocks();
});

describe("SkillsCatalog", () => {
  it("keeps local install available with an empty catalog and treats picker cancellation as a no-op", async () => {
    installSkillList([]);
    const onInstallPluginPackage = vi.fn().mockResolvedValue(undefined);

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog onInstallPluginPackage={onInstallPluginPackage} />,
      );
    });

    expect(container.textContent).toContain("安装本地插件");
    await act(async () => {
      buttonByText("安装本地插件")?.click();
    });

    expect(onInstallPluginPackage).toHaveBeenCalledOnce();
    expect(container.querySelector(".skills-catalog-error")).toBeNull();
    expect(container.textContent).toContain("当前运行时未发现 Skills");
  });

  it("shows install errors as a toast instead of inline catalog state", async () => {
    installSkillList([]);
    const onInstallPluginPackage = vi
      .fn()
      .mockRejectedValue(new Error("Package manifest is invalid"));

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog onInstallPluginPackage={onInstallPluginPackage} />,
      );
    });
    await act(async () => {
      buttonByText("安装本地插件")?.click();
    });

    expect(toastMocks.showErrorToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: "Package manifest is invalid" }),
      "无法安装插件",
    );
    expect(container.textContent).not.toContain("Package manifest is invalid");
    expect(container.querySelector(".skills-catalog-error")).toBeNull();
  });

  it("refreshes the complete extension catalog through the parent runtime", async () => {
    installSkillList([]);
    const refreshedSkills: SkillSummary[] = [{
      name: "fresh-skill",
      description: "Discovered after refresh",
      source: "plugin:fresh",
      user_invocable: true,
      disable_model_invoke: false,
    }];
    const onRefreshCatalog = vi.fn().mockResolvedValue(refreshedSkills);

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onRefreshCatalog={onRefreshCatalog} />);
    });
    await act(async () => {
      container.querySelector<HTMLButtonElement>(".catalog-refresh")?.click();
    });

    expect(onRefreshCatalog).toHaveBeenCalledOnce();
    expect(container.textContent).toContain("fresh-skill");
  });

  it.each([
    'cannot refresh plugin packages while a turn is running or background work remains on thread "thread-busy"',
    "cannot refresh plugin packages while another app-server is running a turn or background work",
    "cannot refresh plugin packages while another plugin change is running",
    "cannot refresh plugin packages while another app-server is changing the plugin catalog",
  ])("keeps the catalog and reports admission refusals through the shared notice: %s", async (reason) => {
    installSkillList([existingSkill]);
    const onRefreshCatalog = vi.fn().mockRejectedValue(new Error(
      `Error invoking remote method 'wuu:extension-catalog-refresh': Error: ${reason}`,
    ));
    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onRefreshCatalog={onRefreshCatalog} />);
    });
    const existingRow = skillButton(existingSkill.name);
    expect(existingRow).toBeTruthy();
    await act(async () => {
      container.querySelector<HTMLButtonElement>(".catalog-refresh")!.click();
    });

    expect(skillButton(existingSkill.name)).toBe(existingRow);
    expect(container.querySelector(".skills-catalog-error")).toBeNull();
    expect(toastMocks.showErrorToast).not.toHaveBeenCalled();
    expect(toastMocks.showToast).toHaveBeenCalledOnce();
    const notice = toastMocks.showToast.mock.calls[0][0];
    expect(notice.tone).toBe("info");
    expect(notice.message).toBeTruthy();
    expect(notice.message).not.toMatch(/thread-busy|wuu:|cannot refresh|app-server/);
  });

  it("preserves rows during refresh and failure, blocks duplicate clicks, and permits retry", async () => {
    installSkillList([existingSkill]);
    let rejectRefresh!: (error: Error) => void;
    const onRefreshCatalog = vi.fn()
      .mockImplementationOnce(() => new Promise<SkillSummary[]>((_, reject) => { rejectRefresh = reject; }))
      .mockResolvedValue([{ ...existingSkill, name: "updated-skill" }]);
    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onRefreshCatalog={onRefreshCatalog} />);
    });
    const refresh = container.querySelector<HTMLButtonElement>(".catalog-refresh")!;
    const existingRow = skillButton(existingSkill.name);
    expect(existingRow).toBeTruthy();
    await act(async () => { refresh.click(); });
    expect(refresh.disabled).toBe(true);
    expect(skillButton(existingSkill.name)).toBe(existingRow);
    await act(async () => { refresh.click(); });
    expect(onRefreshCatalog).toHaveBeenCalledOnce();

    const failure = new Error("Catalog unavailable");
    await act(async () => { rejectRefresh(failure); });
    expect(refresh.disabled).toBe(false);
    expect(skillButton(existingSkill.name)).toBe(existingRow);
    expect(container.querySelector(".skills-catalog-error")).toBeNull();
    expect(toastMocks.showErrorToast).toHaveBeenCalledWith(failure, expect.any(String));
    await act(async () => { refresh.click(); });
    expect(onRefreshCatalog).toHaveBeenCalledTimes(2);
    expect(skillButton("updated-skill")).toBeTruthy();
    expect(skillButton(existingSkill.name)).toBeUndefined();
  });

  it("releases the refresh button when the parent discards a result", async () => {
    installSkillList([existingSkill]);
    const onRefreshCatalog = vi.fn().mockResolvedValue(undefined);
    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onRefreshCatalog={onRefreshCatalog} />);
    });
    const refresh = container.querySelector<HTMLButtonElement>(".catalog-refresh")!;
    await act(async () => { refresh.click(); });
    expect(refresh.disabled).toBe(false);
    expect(skillButton(existingSkill.name)).toBeTruthy();
    expect(toastMocks.showToast).not.toHaveBeenCalled();
    expect(toastMocks.showErrorToast).not.toHaveBeenCalled();
  });

  it("separates official and personal skills and shows concise descriptions", async () => {
    installSkillList([
      {
        name: "browser",
        description: "Navigate and observe web pages. Use when no safer interface is available.",
        source: "bundled",
        user_invocable: true,
        disable_model_invoke: false,
      },
      {
        name: "write",
        description: "Rewrite prose",
        source: "user",
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog />);
    });

    expect(container.textContent).toContain("官方技能");
    expect(container.textContent).toContain("你的技能");
    expect(container.querySelector(".catalog-search input[type=\"search\"]")).toBeTruthy();
    expect(container.textContent).toContain("Navigate and observe web pages.");
    expect(container.textContent).not.toContain("Use when no safer interface is available.");
  });

  it("hides every section that a search leaves empty", async () => {
    installSkillList([existingSkill]);
    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={[{
            id: "plugin:user:scheduler",
            name: "Scheduler",
            kind: "plugin",
            provenance: { kind: "plugin", source: "community", scope: "user", plugin_id: "scheduler" },
            state: "read_only",
          }]}
        />,
      );
    });
    const sectionRows = () => Array.from(container.querySelectorAll("section section")).map(
      (section) => Array.from(section.querySelectorAll("button")).map((row) => row.textContent ?? ""),
    );
    const search = async (query: string) => {
      const input = container.querySelector<HTMLInputElement>(".catalog-search input")!;
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, query);
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
    };

    expect(sectionRows()).toHaveLength(2);

    await search("existing");
    expect(sectionRows()).toEqual([[expect.stringContaining("existing-skill")]]);

    await search("scheduler");
    expect(sectionRows()).toEqual([[expect.stringContaining("Scheduler")]]);

    await search("no such extension");
    expect(sectionRows()).toEqual([]);
  });

  it("lists installed plugins and tags plugin-provided skills", async () => {
    installSkillList([
      {
        name: "cua-mac",
        description: "Observe and control native macOS apps",
        source: "plugin:cua-mac",
        path: "/bundle/plugins/cua-mac/skills/cua-mac/SKILL.md",
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={[
            {
              id: "plugin:user:cua-mac",
              name: "cua-mac",
              description: "Control macOS apps through Accessibility.",
              kind: "plugin",
              icon: { name: "layout-grid" },
              provenance: {
                kind: "plugin",
                source: "wuu",
                scope: "user",
                plugin_id: "cua-mac",
                official: true,
              },
              state: "read_only",
            },
            {
              id: "mcp:plugin:cua-mac:computer",
              name: "computer",
              kind: "mcp",
              provenance: {
                kind: "mcp",
                source: "plugin:cua-mac",
                scope: "user",
                plugin_id: "cua-mac",
              },
              state: "granted",
            },
            {
              id: "plugin:user:community-tools",
              name: "community-tools",
              description: "Community-maintained utilities.",
              kind: "plugin",
              provenance: {
                kind: "plugin",
                source: "community",
                scope: "user",
                plugin_id: "community-tools",
                official: false,
              },
              state: "read_only",
            },
          ]}
        />,
      );
    });

    expect(container.textContent).toContain("插件");
    expect(container.textContent).toContain("Control macOS apps through Accessibility.");
    // Official provenance no longer gets a label in the list; the row carries
    // its runtime state instead.
    expect(container.textContent).toContain("已启用");
    expect(container.textContent).toContain("插件 · cua-mac");
    expect(container.querySelector(".skill-artwork-plugin-brand [data-icon=\"layout-grid\"]")).toBeTruthy();
    // Non-plugin inventory records (the plugin's MCP server) stay out of the
    // plugin list.
    expect(container.textContent).not.toContain("computer");
  });

  it("preserves plugin-owned artwork when using capability marks for the catalog", async () => {
    installSkillList([]);
    const loadPluginIcon = vi.fn().mockImplementation(async (request) => ({
      ...request,
      url: "data:image/svg+xml,%3Csvg/%3E",
    }));
    Object.assign(window.wuu!, { loadPluginIcon });
    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog extensionInventory={[{
        id: "plugin:user:brand-artwork",
        name: "Brand artwork",
        kind: "plugin",
        icon: { path: "assets/brand.svg" },
        fingerprint: "brand-revision",
        provenance: { kind: "plugin", source: "community", scope: "user" },
        state: "read_only",
      }]} />);
    });
    expect(loadPluginIcon).toHaveBeenCalledWith({
      id: "plugin:user:brand-artwork", fingerprint: "brand-revision", path: "assets/brand.svg",
    });
    const image = container.querySelector(".skill-artwork img");
    expect(image?.getAttribute("src")).toBe("data:image/svg+xml,%3Csvg/%3E");
    expect(image?.getAttribute("alt")).toBe("");
  });

  it("shows package permissions and grants a pending plugin through the update callback", async () => {
    installSkillList([]);
    const onUpdateExtensionPackage = vi.fn().mockResolvedValue(undefined);
    const extensionInventory = [
      {
        id: "plugin:project:docs",
        name: "docs",
        description: "Project documentation commands",
        kind: "plugin",
        provenance: {
          kind: "plugin",
          source: "project",
          scope: "project",
          plugin_id: "docs",
          official: false,
        },
        state: "pending",
        approval_state: "pending",
        runtime_state: "inactive",
        enabled: false,
        fingerprint: "sha256:docs",
        requested_permissions: ["file.read", "command.prompt"],
        contributions: {
          commands: [{ id: "ask-docs", title: "Ask docs", kind: "prompt_template", template: "Ask {{args}}" }],
          settings: [],
          themes: [],
        },
      },
    ] as unknown as ExtensionInventoryRecord[];

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={extensionInventory}
          onUpdateExtensionPackage={onUpdateExtensionPackage}
        />,
      );
    });

    expect(container.textContent).toContain("待授权");
    await act(async () => {
      skillButton("docs")?.click();
    });
    expect(document.body.textContent).toContain("file.read");
    expect(document.body.textContent).toContain("第三方");
    expect(document.body.textContent).toContain("技术信息");

    const grantButton = buttonByText("授权并启用");
    await act(async () => {
      grantButton?.click();
    });

    expect(onUpdateExtensionPackage).toHaveBeenCalledWith({
      id: "plugin:project:docs",
      fingerprint: "sha256:docs",
      action: "grant",
    });
  });

  it("shows a busy-thread authorization failure as a toast", async () => {
    installSkillList([]);
    const busyError = new Error(
      'Error invoking remote method \'wuu:extension-package-update\': Error: cannot change plugin packages while a turn is running or background work remains on thread "thread-busy"',
    );
    const onUpdateExtensionPackage = vi.fn().mockRejectedValue(busyError);
    const extensionInventory = [
      {
        id: "plugin:user:manga-studio",
        name: "manga-studio",
        kind: "plugin",
        provenance: {
          kind: "plugin",
          source: "user",
          scope: "user",
          plugin_id: "manga-studio",
          official: false,
        },
        state: "changed",
        approval_state: "changed",
        runtime_state: "inactive",
        enabled: false,
        fingerprint: "sha256:manga-studio",
      },
    ] as unknown as ExtensionInventoryRecord[];

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={extensionInventory}
          onUpdateExtensionPackage={onUpdateExtensionPackage}
        />,
      );
    });
    await act(async () => {
      skillButton("manga-studio")?.click();
    });
    await act(async () => {
      buttonByText("重新授权")?.click();
    });

    expect(toastMocks.showErrorToast).not.toHaveBeenCalled();
    expect(toastMocks.showToast).toHaveBeenCalledWith(expect.objectContaining({ tone: "info" }));
    expect(container.textContent).not.toContain("cannot change plugin packages");
  });

  it("renders Remove only for user-installed plugins and removes by plugin ID after confirmation", async () => {
    installSkillList([]);
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const onRemovePluginPackage = vi.fn().mockResolvedValue({
      id: "community-tools",
      removed: true,
      extension_inventory: [],
      skills: [
        {
          name: "remaining-skill",
          description: "Still installed",
          source: "user",
          user_invocable: true,
          disable_model_invoke: false,
        },
      ],
    });
    const extensionInventory = [
      {
        id: "plugin:user:community-tools",
        name: "community-tools",
        kind: "plugin",
        provenance: {
          kind: "plugin",
          source: "user",
          scope: "user",
          plugin_id: "community-tools",
          official: false,
        },
        state: "pending",
        approval_state: "pending",
      },
      {
        id: "plugin:user:official-tools",
        name: "official-tools",
        kind: "plugin",
        provenance: {
          kind: "plugin",
          source: "wuu",
          scope: "user",
          plugin_id: "official-tools",
          official: true,
        },
        state: "read_only",
        approval_state: "official",
      },
    ] as ExtensionInventoryRecord[];

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={extensionInventory}
          onRemovePluginPackage={onRemovePluginPackage}
        />,
      );
    });

    expect(buttonsByText("移除")).toHaveLength(0);
    await act(async () => {
      skillButton("community-tools")?.click();
    });
    await act(async () => {
      document.querySelector<HTMLButtonElement>('[aria-label="community-tools 的更多操作"]')?.click();
    });
    expect(buttonsByText("移除")).toHaveLength(1);
    await act(async () => {
      buttonByText("移除")?.click();
    });

    expect(confirm).toHaveBeenCalledWith(
      "确定移除用户插件 community-tools？Wuu 中已安装的插件文件将被删除。",
    );
    expect(onRemovePluginPackage).toHaveBeenCalledWith("community-tools");
    expect(container.textContent).toContain("remaining-skill");
  });

  it("requires an exact decision for a staged plugin update", async () => {
    installSkillList([]);
    const onUpdateExtensionPackage = vi.fn().mockResolvedValue(undefined);
    const extensionInventory = [
      {
        id: "plugin:user:update-demo",
        name: "update-demo",
        kind: "plugin",
        provenance: {
          kind: "plugin",
          source: "user",
          scope: "user",
          plugin_id: "update-demo",
        },
        state: "granted",
        fingerprint: "sha256:active",
        approval_state: "granted",
        runtime_state: "active",
        enabled: true,
        pending_update: {
          version: "2.0.0",
          fingerprint: "sha256:pending",
          active_fingerprint: "sha256:active",
        },
      },
    ] as ExtensionInventoryRecord[];

    await act(async () => {
      root = createRoot(container);
      root.render(
        <SkillsCatalog
          extensionInventory={extensionInventory}
          onUpdateExtensionPackage={onUpdateExtensionPackage}
        />,
      );
    });

    expect(container.textContent).toContain("更新待授权");
    await act(async () => {
      skillButton("update-demo")?.click();
    });
    expect(document.body.textContent).toContain("版本 2.0.0 已就绪");
    await act(async () => {
      buttonByText("授权并更新")?.click();
    });
    expect(onUpdateExtensionPackage).toHaveBeenLastCalledWith({
      id: "plugin:user:update-demo",
      fingerprint: "sha256:pending",
      action: "promote_update",
    });

    await act(async () => {
      document.querySelector<HTMLButtonElement>('[aria-label="update-demo 的更多操作"]')?.click();
    });
    await act(async () => {
      buttonByText("拒绝更新")?.click();
    });
    expect(onUpdateExtensionPackage).toHaveBeenLastCalledWith({
      id: "plugin:user:update-demo",
      fingerprint: "sha256:pending",
      action: "reject_update",
    });
  });

  it("surfaces changed fingerprints and runtime failures", async () => {
    installSkillList([]);
    const extensionInventory = [
      {
        id: "plugin:project:changed",
        name: "changed",
        kind: "plugin",
        provenance: { kind: "plugin", source: "project", scope: "project", plugin_id: "changed" },
        state: "changed",
        approval_state: "changed",
        runtime_state: "inactive",
        enabled: false,
      },
      {
        id: "plugin:user:broken",
        name: "broken",
        kind: "plugin",
        provenance: { kind: "plugin", source: "user", scope: "user", plugin_id: "broken" },
        state: "granted",
        approval_state: "granted",
        runtime_state: "failed",
        enabled: true,
        last_error: "Plugin process exited before initialize",
      },
    ] as unknown as ExtensionInventoryRecord[];

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog extensionInventory={extensionInventory} />);
    });

    expect(container.textContent).toContain("内容已更改");
    expect(container.textContent).toContain("启动失败");
    await act(async () => {
      skillButton("broken")?.click();
    });
    expect(document.body.textContent).toContain("Plugin process exited before initialize");
  });

  it("opens a skill preview dialog from a skill row", async () => {
    installSkillList([
      {
        name: "bug-fix",
        description: "Fix a bug from a report",
        when_to_use: "Use when the user reports a crash",
        trigger_condition: "Bug reports and stack traces",
        source: "bundled",
        argument_hint: "Describe the failing behavior",
        examples: ["Fix this stack trace"],
        verification_checklist: ["Run the targeted test"],
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog />);
    });

    await act(async () => {
      skillButton("bug-fix")?.click();
    });

    expect(document.querySelector('[role="dialog"]')).toBeTruthy();
    expect(document.body.textContent).toContain("# Bug Fix Workflow");
    expect(document.body.textContent).toContain("Read the report and inspect the code");
    expect(document.body.textContent).not.toContain("来源");
    expect(document.body.textContent).not.toContain("路径");
  });

  it("closes the preview and reports the selected skill when trying it", async () => {
    const onTrySkill = vi.fn();
    installSkillList([
      {
        name: "write",
        description: "Rewrite prose",
        source: "user",
        user_invocable: true,
        disable_model_invoke: false,
      },
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onTrySkill={onTrySkill} />);
    });

    await act(async () => {
      skillButton("write")?.click();
    });
    await act(async () => {
      buttonByText("立即试用")?.click();
    });

    expect(onTrySkill).toHaveBeenCalledWith(expect.objectContaining({ name: "write" }));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("does not offer Try now for a non-user-invocable skill", async () => {
    const onTrySkill = vi.fn();
    installSkillList([
      {
        name: "internal-review",
        description: "Model-only review workflow",
        source: "bundled",
        user_invocable: false,
        disable_model_invoke: false,
      },
    ]);

    await act(async () => {
      root = createRoot(container);
      root.render(<SkillsCatalog onTrySkill={onTrySkill} />);
    });
    await act(async () => {
      skillButton("internal-review")?.click();
    });

    expect(document.querySelector('[role="dialog"]')).toBeTruthy();
    expect(buttonByText("立即试用")).toBeUndefined();
    expect(onTrySkill).not.toHaveBeenCalled();
  });
});

const existingSkill: SkillSummary = {
  name: "existing-skill",
  description: "A previously loaded skill",
  source: "user",
  user_invocable: true,
  disable_model_invoke: false,
};

function installSkillList(skills: SkillSummary[]): void {
  const stub: Partial<WuuDesktopApi> = {
    listSkills: vi.fn().mockResolvedValue({ skills }),
    readSkillContent: vi.fn().mockResolvedValue({
      content: [
        "---",
        "name: bug-fix",
        "---",
        "# Bug Fix Workflow",
        "",
        "Read the report and inspect the code.",
      ].join("\n"),
    }),
  };
  (globalThis as { wuu?: WuuDesktopApi }).wuu = stub as WuuDesktopApi;
  (window as unknown as { wuu: WuuDesktopApi }).wuu = stub as WuuDesktopApi;
}

function skillButton(name: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll<HTMLButtonElement>("button")).find(
    (button) => button.textContent?.includes(name),
  );
}

function buttonByText(text: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find(
    (button) => button.textContent === text,
  );
}

function buttonsByText(text: string): HTMLButtonElement[] {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).filter(
    (button) => button.textContent === text,
  );
}
