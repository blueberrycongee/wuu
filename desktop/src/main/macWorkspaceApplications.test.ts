import { describe, expect, it, vi } from "vitest";
import {
  macWorkspaceItemMenuTemplate,
  macWorkspaceApplications,
  normalizeMacWorkspaceApplications,
  openMacWorkspaceItemWithApplication,
} from "./macWorkspaceApplications";
import type { MenuItemConstructorOptions } from "electron";

describe("macWorkspaceItemMenuTemplate", () => {
  it("builds the native capability menu from discovered applications", () => {
    const onOpenDefault = vi.fn();
    const onCopyPath = vi.fn();
    const template = macWorkspaceItemMenuTemplate({
      associations: {
        defaultApplication: { path: "/Applications/Cursor.app", name: "Cursor" },
        applications: [
          { path: "/Applications/Cursor.app", name: "Cursor" },
          { path: "/Applications/Zed.app", name: "Zed" },
        ],
      },
      icons: new Map(),
      labels: {
        open: "打开",
        openInApplication: (application) => `在 ${application} 中打开`,
        openWith: "打开方式",
        copyPath: "复制路径",
      },
      onOpenDefault,
      onOpenWith: vi.fn(),
      onCopyPath,
    });
    const openWith = template[1]?.submenu as MenuItemConstructorOptions[];

    expect(template.map((item) => item.label ?? item.type)).toEqual([
      "在 Cursor 中打开",
      "打开方式",
      "separator",
      "复制路径",
    ]);
    expect(openWith.map((item) => item.label)).toEqual(["Cursor", "Zed"]);
    expect(template[0]?.click).toBe(onOpenDefault);
    expect(template[3]?.click).toBe(onCopyPath);
  });
});

describe("normalizeMacWorkspaceApplications", () => {
  it("keeps the real default app and removes generated and duplicate apps", () => {
    expect(normalizeMacWorkspaceApplications({
      default_path: "/Applications/Cursor.app",
      applications: [
        {
          path: "/Applications/Cursor.app",
          name: "Cursor",
          bundle_id: "com.cursor",
          icon_png: "cursor-png",
        },
        { path: "/Users/me/Desktop/Cursor.app", name: "Cursor copy", bundle_id: "com.cursor" },
        { path: "/Users/me/Library/Caches/ms-playwright/Playwright.app", bundle_id: "org.webkit.Playwright" },
        { path: "/System/Applications/TextEdit.app", name: "TextEdit", bundle_id: "com.apple.TextEdit" },
      ],
    })).toEqual({
      defaultApplication: {
        path: "/Applications/Cursor.app",
        name: "Cursor",
        bundleId: "com.cursor",
        iconPng: "cursor-png",
      },
      applications: [
        {
          path: "/Applications/Cursor.app",
          name: "Cursor",
          bundleId: "com.cursor",
          iconPng: "cursor-png",
        },
        {
          path: "/System/Applications/TextEdit.app",
          name: "TextEdit",
          bundleId: "com.apple.TextEdit",
          iconPng: undefined,
        },
      ],
    });
  });

  it("uses the app bundle name when Launch Services omits a display name", () => {
    expect(normalizeMacWorkspaceApplications({
      default_path: "/Applications/Zed.app",
      applications: [],
    }).defaultApplication).toEqual({
      path: "/Applications/Zed.app",
      name: "Zed",
      bundleId: undefined,
      iconPng: undefined,
    });
  });

  it("keeps a nested default helper but omits helper apps from Open With", () => {
    const helper = "/Applications/Browser.app/Contents/Helpers/Browser Helper.app";
    expect(normalizeMacWorkspaceApplications({
      default_path: helper,
      applications: [
        { path: helper, name: "Browser Helper", bundle_id: "com.example.helper" },
        { path: "/Applications/Editor.app", name: "Editor", bundle_id: "com.example.editor" },
      ],
    })).toEqual({
      defaultApplication: {
        path: helper,
        name: "Browser Helper",
        bundleId: "com.example.helper",
        iconPng: undefined,
      },
      applications: [{
        path: "/Applications/Editor.app",
        name: "Editor",
        bundleId: "com.example.editor",
        iconPng: undefined,
      }],
    });
  });
});

describe("macWorkspaceApplications", () => {
  it("passes the file path as an argument instead of interpolating it into JXA", async () => {
    const run = vi.fn().mockResolvedValue({
      stdout: JSON.stringify({
        default_path: "/Applications/Cursor.app",
        applications: [{ path: "/Applications/Cursor.app", name: "Cursor" }],
      }),
    });

    await expect(macWorkspaceApplications("/repo/a 'quoted'.ts", run)).resolves.toMatchObject({
      defaultApplication: { name: "Cursor" },
    });
    expect(run).toHaveBeenCalledWith(
      "/usr/bin/osascript",
      expect.arrayContaining(["--", "/repo/a 'quoted'.ts"]),
      { encoding: "utf8", timeout: 1000, maxBuffer: 1024 * 1024 },
    );
  });

  it("opens with the selected app using argument-safe /usr/bin/open invocation", async () => {
    const run = vi.fn().mockResolvedValue({ stdout: "" });

    await openMacWorkspaceItemWithApplication(
      "/repo/-draft with spaces.md",
      "/Applications/My Editor.app",
      run,
    );

    expect(run).toHaveBeenCalledWith("/usr/bin/open", [
      "-a",
      "/Applications/My Editor.app",
      "--",
      "/repo/-draft with spaces.md",
    ]);
  });
});
