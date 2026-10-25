import { BrowserWindow, Menu, type WebContents, type MenuItemConstructorOptions } from "electron";
import type { ApplicationDesktopContextMenuV1 } from "../../../packages/plugin-sdk/src/ui-desktop";

const menus = new Map<number, { token: string; close: () => void }>();

export function dismissContextMenu(contents: WebContents, token: string): void {
  const current = menus.get(contents.id);
  if (current?.token === token) current.close();
}

/** The renderer owns actions; the host owns native presentation and its lifetime. */
export function showContextMenu(contents: WebContents, request: Parameters<ApplicationDesktopContextMenuV1["show"]>[0]): Promise<string | null> {
  const owner = BrowserWindow.fromWebContents(contents);
  if (!owner || owner.isDestroyed()) return Promise.resolve(null);
  const editRoles = new Set(["undo", "redo", "cut", "copy", "paste", "delete", "selectAll"]);
  for (const item of request.items) {
    if (!("separator" in item) && item.role && !editRoles.has(item.role)) throw new Error("Unsupported context menu role");
  }
  menus.get(contents.id)?.close();
  return new Promise((resolve) => {
    let selected: string | null = null;
    let finished = false;
    const finish = () => {
      if (finished) return;
      finished = true;
      if (menus.get(contents.id)?.token === request.token) menus.delete(contents.id);
      contents.removeListener("destroyed", close);
      contents.removeListener("render-process-gone", close);
      contents.removeListener("did-start-navigation", onNavigation);
      resolve(selected);
    };
    const close = () => { selected = null; menu.closePopup(); finish(); };
    const onNavigation = (details: { isMainFrame: boolean; isSameDocument: boolean }) => {
      if (details.isMainFrame && !details.isSameDocument) close();
    };
    const editAccelerators = {
      undo: "CommandOrControl+Z", redo: process.platform === "darwin" ? "Command+Shift+Z" : "Control+Y",
      cut: "CommandOrControl+X", copy: "CommandOrControl+C", paste: "CommandOrControl+V",
      selectAll: "CommandOrControl+A", delete: undefined,
    };
    const template: MenuItemConstructorOptions[] = request.items.map(item => "separator" in item
      ? { type: "separator" }
      : { label: item.label, enabled: !item.disabled,
        accelerator: item.role ? editAccelerators[item.role] : undefined, registerAccelerator: false,
        click: () => {
          // Target the requesting editor even when another Wuu window has focus.
          // Electron's macOS roles dispatch to the application's first responder.
          if (item.role) contents[item.role]();
          else selected = item.id;
        } });
    const menu = Menu.buildFromTemplate(template);
    menus.set(contents.id, { token: request.token, close });
    contents.once("destroyed", close);
    contents.once("render-process-gone", close);
    contents.on("did-start-navigation", onNavigation);
    const zoom = contents.getZoomFactor();
    menu.popup({ window: owner,
      ...(request.x !== undefined && request.y !== undefined ? { x: Math.round(request.x * zoom), y: Math.round(request.y * zoom) } : {}),
      callback: finish });
  });
}
