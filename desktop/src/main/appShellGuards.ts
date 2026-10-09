import type { MenuItemConstructorOptions } from "electron";
import type { DesktopZoomAction } from "../shared/DesktopPageZoom";

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
): MenuItemConstructorOptions[] {
  return [
    { role: platform === "darwin" ? "appMenu" : "fileMenu" },
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

export function desktopZoomAction(
  input: AppShellKeyboardInput & { code?: string },
  platform: string,
): DesktopZoomAction | undefined {
  const modifier = platform === "darwin" ? input.meta : input.control;
  if (!modifier || input.alt || (platform === "darwin" && input.control)) return;
  if (input.key === "+" || input.key === "=") return "in";
  if (input.key === "-" || input.code === "NumpadSubtract") return "out";
  if (input.key === "0" && !input.shift) return "reset";
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
