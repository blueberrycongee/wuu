import type { MenuItemConstructorOptions } from "electron";

export type AppShellKeyboardInput = {
  key: string;
  alt: boolean;
  control: boolean;
  meta: boolean;
  shift: boolean;
};

type PreventableInputEvent = {
  preventDefault(): void;
};

export type AppShellWebContents = {
  onBeforeInputEvent(
    listener: (event: PreventableInputEvent, input: AppShellKeyboardInput) => void,
  ): void;
  onDevToolsOpened(listener: () => void): void;
  closeDevTools(): void;
};

type ProductionAppShellGuardOptions = {
  isPackaged: boolean;
  setApplicationMenu(): void;
  onWebContentsCreated(listener: (contents: AppShellWebContents) => void): void;
};

export function appShellWebPreferences(isPackaged: boolean): { devTools?: false } {
  return isPackaged ? { devTools: false } : {};
}

export function productionApplicationMenuTemplate(
  platform: string,
  updateItem?: MenuItemConstructorOptions,
): MenuItemConstructorOptions[] {
  return [
    platform === "darwin" && updateItem ? {
      role: "appMenu", submenu: [
        { role: "about" }, { type: "separator" }, updateItem,
        { type: "separator" }, { role: "services" }, { type: "separator" },
        { role: "hide" }, { role: "hideOthers" }, { role: "unhide" },
        { type: "separator" }, { role: "quit" },
      ],
    } : { role: platform === "darwin" ? "appMenu" : "fileMenu" },
    { role: "editMenu" },
    { role: "windowMenu" },
  ];
}

export function isBlockedProductionShortcut(input: AppShellKeyboardInput): boolean {
  const key = input.key.toLowerCase();
  const commandOrControl = input.meta || input.control;

  if (key === "f5" || (key === "r" && commandOrControl && !input.alt)) {
    return true;
  }

  if (key === "f12") {
    return true;
  }

  const devToolsKey = key === "i" || key === "j" || key === "c";
  const macDevToolsChord = input.meta && input.alt;
  const otherDevToolsChord = input.control && input.shift;
  return devToolsKey && (macDevToolsChord || otherDevToolsChord);
}

export function installProductionAppShellGuards(
  options: ProductionAppShellGuardOptions,
): void {
  if (!options.isPackaged) return;

  options.setApplicationMenu();
  options.onWebContentsCreated((contents) => {
    contents.onBeforeInputEvent((event, input) => {
      if (isBlockedProductionShortcut(input)) {
        event.preventDefault();
      }
    });
    contents.onDevToolsOpened(() => {
      contents.closeDevTools();
    });
  });
}
