import { describe, expect, it, vi } from "vitest";
import {
  appShellWebPreferences,
  installProductionAppShellGuards,
  isBlockedProductionShortcut,
  productionApplicationMenuTemplate,
  type AppShellKeyboardInput,
  type AppShellWebContents,
} from "./appShellGuards";

const input = (
  key: string,
  modifiers: Partial<Omit<AppShellKeyboardInput, "key">> = {},
): AppShellKeyboardInput => ({
  key,
  alt: false,
  control: false,
  meta: false,
  shift: false,
  ...modifiers,
});

type TestWebContents = AppShellWebContents & {
  sendKey(value: AppShellKeyboardInput): ReturnType<typeof vi.fn>;
  openDevTools(): void;
};

function testWebContents(): TestWebContents {
  let inputListener:
    | ((event: { preventDefault(): void }, value: AppShellKeyboardInput) => void)
    | undefined;
  let devToolsListener: (() => void) | undefined;
  const closeDevTools = vi.fn();
  return {
    onBeforeInputEvent: (listener) => {
      inputListener = listener;
    },
    onDevToolsOpened: (listener) => {
      devToolsListener = listener;
    },
    closeDevTools,
    sendKey: (value) => {
      const preventDefault = vi.fn();
      inputListener?.({ preventDefault }, value);
      return preventDefault;
    },
    openDevTools: () => devToolsListener?.(),
  };
}

describe("production app shell shortcuts", () => {
  it.each([
    input("r", { meta: true }),
    input("R", { meta: true, shift: true }),
    input("r", { control: true }),
    input("R", { control: true, shift: true }),
    input("F5"),
    input("i", { meta: true, alt: true }),
    input("j", { meta: true, alt: true }),
    input("c", { meta: true, alt: true }),
    input("i", { control: true, shift: true }),
    input("j", { control: true, shift: true }),
    input("c", { control: true, shift: true }),
    input("F12"),
  ])("blocks packaged reload and DevTools chord %#", (value) => {
    expect(isBlockedProductionShortcut(value)).toBe(true);
  });

  it.each([
    input("r"),
    input("r", { alt: true }),
    input("b", { meta: true }),
    input("i", { control: true }),
  ])("leaves unrelated product input alone %#", (value) => {
    expect(isBlockedProductionShortcut(value)).toBe(false);
  });
});

describe("production app shell guards", () => {
  it("guards main and popped-out web contents uniformly in packaged builds", () => {
    const setApplicationMenu = vi.fn();
    let webContentsCreated: ((contents: AppShellWebContents) => void) | undefined;
    installProductionAppShellGuards({
      isPackaged: true,
      setApplicationMenu,
      onWebContentsCreated: (listener) => {
        webContentsCreated = listener;
      },
    });

    expect(setApplicationMenu).toHaveBeenCalledTimes(1);
    const mainWindow = testWebContents();
    const popOutWindow = testWebContents();
    webContentsCreated?.(mainWindow);
    webContentsCreated?.(popOutWindow);

    for (const contents of [mainWindow, popOutWindow]) {
      expect(contents.sendKey(input("r", { meta: true }))).toHaveBeenCalledTimes(1);
      expect(contents.sendKey(input("b", { meta: true }))).not.toHaveBeenCalled();
      contents.openDevTools();
      expect(contents.closeDevTools).toHaveBeenCalledTimes(1);
    }
  });

  it("does not alter menus, shortcuts, or DevTools in development", () => {
    const setApplicationMenu = vi.fn();
    const onWebContentsCreated = vi.fn();
    installProductionAppShellGuards({
      isPackaged: false,
      setApplicationMenu,
      onWebContentsCreated,
    });

    expect(setApplicationMenu).not.toHaveBeenCalled();
    expect(onWebContentsCreated).not.toHaveBeenCalled();
    expect(appShellWebPreferences(false)).toEqual({});
  });

  it("disables BrowserWindow DevTools in packaged builds", () => {
    expect(appShellWebPreferences(true)).toEqual({ devTools: false });
  });

  it("keeps standard menus without exposing the reload and DevTools view menu", () => {
    expect(productionApplicationMenuTemplate("darwin")).toEqual([
      { role: "appMenu" },
      { role: "editMenu" },
      { role: "windowMenu" },
    ]);
    expect(productionApplicationMenuTemplate("win32")).toEqual([
      { role: "fileMenu" },
      { role: "editMenu" },
      { role: "windowMenu" },
    ]);
  });
});

describe("native recovery entry", () => {
  it.each(["darwin", "win32", "linux"])("keeps recovery reachable with and without updater on %s", platform => {
    const click = vi.fn();
    const recovery = { id: "wuu-recovery", label: "Recovery", click };
    for (const update of [undefined, { id: "updates", label: "Updates" }]) {
      const menu = productionApplicationMenuTemplate(platform, update, recovery);
      const items = menu.flatMap(item => Array.isArray(item.submenu) ? item.submenu : []);
      const action = items.find(item => item.id === recovery.id);
      expect(action).toBe(recovery);
      (action!.click as () => void)();
    }
    expect(click).toHaveBeenCalledTimes(2);
  });
});
