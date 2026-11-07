import { ipcMain, type IpcMainInvokeEvent } from "electron";
import type { WorkspaceFileService } from "./workspaceFiles";

/** File observers belong to the requesting renderer, including across reloads and crashes. */
export function registerWorkspaceFileWatches(files: (event: IpcMainInvokeEvent) => WorkspaceFileService): void {
  const owners = new Map<number, Map<string, () => void>>();
  ipcMain.handle("wuu:file-watch-start", async (event, id: string, path: string, root: string) => {
    if (typeof id !== "string" || !id || id.length > 200 || typeof path !== "string"
      || typeof root !== "string" || !root) throw new Error("invalid file watch parameters");
    const sender = event.sender;
    let watches = owners.get(sender.id);
    if (!watches) {
      watches = new Map();
      owners.set(sender.id, watches);
      const owned = watches;
      const clear = () => { for (const stop of owned.values()) stop(); owned.clear(); };
      sender.on("did-navigate", clear);
      sender.on("render-process-gone", clear);
      sender.once("destroyed", () => { clear(); owners.delete(sender.id); });
    }
    if (watches.has(id)) throw new Error("file watch already exists");
    let release: (() => void) | undefined;
    let stopped = false;
    const stop = () => {
      if (stopped) return;
      stopped = true;
      release?.();
      watches.delete(id);
    };
    watches.set(id, stop);
    try {
      release = await files(event).watch(path, root, change => {
        if (stopped || sender.isDestroyed()) return;
        if (change.type === "error") stop();
        sender.send("wuu:file-watch-change", id, change);
      });
      if (stopped) { release(); throw new Error("file watch owner retired during registration"); }
    } catch (error) { stop(); throw error; }
  });
  ipcMain.handle("wuu:file-watch-stop", (event, id: string) => {
    const watches = owners.get(event.sender.id);
    watches?.get(id)?.();
    watches?.delete(id);
  });
}
