import { app, shell, type BrowserWindow, type DownloadItem } from "electron";
import { randomUUID } from "node:crypto";
import { stat } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { basename, isAbsolute, join } from "node:path";
import { writeTextFileAtomicSync } from "./atomicFile";
import type { WebWorkspaceDownloadCommand, WebWorkspaceDownloadState } from "../shared/webWorkspaceDownloads";

type Download = {
  owner?: BrowserWindow;
  state: WebWorkspaceDownloadState;
  item?: DownloadItem;
  detach: () => void;
};

const HISTORY_LIMIT = 100;

/** Downloads belong to the shell window, so changing or closing tabs cannot
 * lose progress. Only the shared session's ownership router may start one. */
export class WebWorkspaceDownloads {
  private downloads = new Map<string, Download>();
  private owners = new Map<BrowserWindow, () => void>();

  private loaded = false;
  private readError?: Error;
  private writeError = false;
  private checkpoint?: ReturnType<typeof setTimeout>;

  constructor(
    private readonly onChange: (owner: BrowserWindow, items: WebWorkspaceDownloadState[]) => void,
    private readonly historyPath?: string,
  ) {}

  start(owner: BrowserWindow, websiteId: string, item: DownloadItem): void {
    if (owner.isDestroyed()) { item.cancel(); return; }
    this.load();
    // Leave savePath unset: Electron asks where to save, with its normal
    // overwrite confirmation. A website never chooses an on-disk destination.
    item.setSaveDialogOptions({ defaultPath: join(app.getPath("downloads"), basename(item.getFilename()) || "download") });
    this.observeOwner(owner);
    const download: Download = {
      owner,
      state: {
        id: randomUUID(), websiteId, filename: item.getFilename(), savePath: "",
        receivedBytes: 0, totalBytes: 0, state: "progressing", paused: false,
        canResume: false, done: false, startedAt: item.getStartTime() * 1000,
      },
      item,
      detach: () => { item.removeListener("updated", updated); item.removeListener("done", done); },
    };
    const updated = (): void => { this.refresh(download); this.scheduleCheckpoint(); this.emit(owner); };
    const done = (_event: Electron.Event, state: "completed" | "cancelled" | "interrupted"): void => {
      this.refresh(download);
      download.state = { ...download.state, state, done: true, paused: false, canResume: false };
      download.detach();
      download.item = undefined;
      // Release the callbacks' closure over the native DownloadItem as well.
      download.detach = () => {};
      this.prune();
      this.persist();
      this.emit(owner);
    };
    this.downloads.set(download.state.id, download);
    item.on("updated", updated);
    item.once("done", done);
    this.refresh(download);
    this.prune();
    this.persist();
    this.emit(owner);
  }

  list(owner: BrowserWindow): WebWorkspaceDownloadState[] {
    this.load();
    const items = this.snapshot(owner);
    if (this.readError && !items.length) throw this.readError;
    this.observeOwner(owner);
    return items;
  }

  async command(owner: BrowserWindow, id: string, command: WebWorkspaceDownloadCommand): Promise<void> {
    this.load();
    const download = this.downloads.get(id);
    if (!download || (download.owner && download.owner !== owner) || owner.isDestroyed()) {
      throw new Error("This download is no longer available in this window.");
    }
    const item = download.item;
    switch (command) {
      case "pause":
        if (!item || item.getState() !== "progressing") throw new Error("This download cannot be paused.");
        item.pause();
        break;
      case "resume":
        if (!item || (!item.isPaused() && (item.getState() !== "interrupted" || !item.canResume()))) throw new Error("This download cannot resume. Download the file again from the website.");
        item.resume();
        break;
      case "cancel":
        if (!item) throw new Error("This download has already ended.");
        item.cancel();
        // cancel() may emit done synchronously and release the native item.
        break;
      case "open":
      case "reveal": {
        if (download.state.state !== "completed" || !download.state.savePath) {
          throw new Error("Wait for this download to finish before opening it.");
        }
        const path = download.state.savePath;
        const file = await stat(path).catch(() => undefined);
        if (!file?.isFile()) throw new Error("This file was moved or removed. Download it again from the website.");
        if (owner.isDestroyed()) return;
        // These are explicit shell UI actions, never download completion hooks.
        if (command === "reveal") shell.showItemInFolder(path);
        else {
          const error = await shell.openPath(path);
          if (error) throw new Error(error);
        }
        return;
      }
      default:
        throw new Error("Unsupported download command.");
    }
    this.refresh(download);
    this.persist();
    this.emit(owner);
  }

  destroyAll(): void {
    for (const owner of [...this.owners.keys()]) this.releaseOwner(owner);
  }

  private refresh(download: Download): void {
    const item = download.item;
    if (!item) return;
    const savePath = item.getSavePath();
    download.state = {
      ...download.state,
      filename: savePath ? basename(savePath) : item.getFilename(),
      savePath,
      receivedBytes: item.getReceivedBytes(),
      totalBytes: item.getTotalBytes(),
      state: item.getState(),
      paused: item.isPaused(),
      canResume: item.isPaused() || item.canResume(),
    };
  }

  private observeOwner(owner: BrowserWindow): void {
    if (this.owners.has(owner)) return;
    const closed = (): void => this.releaseOwner(owner);
    this.owners.set(owner, closed);
    owner.once("closed", closed);
  }

  private releaseOwner(owner: BrowserWindow): void {
    const closed = this.owners.get(owner);
    if (closed) owner.removeListener("closed", closed);
    this.owners.delete(owner);
    for (const download of this.downloads.values()) {
      if (download.owner !== owner) continue;
      download.detach();
      if (download.item) {
        this.refresh(download);
        download.item.cancel();
        download.state = { ...download.state, state: "cancelled", done: true, paused: false, canResume: false };
      }
      download.item = undefined;
      download.detach = () => {};
      download.owner = undefined;
    }
    this.prune();
    this.persist();
    for (const remaining of this.owners.keys()) this.emit(remaining);
  }

  private prune(): void {
    const finished = [...this.downloads.values()].filter(download => download.state.done);
    for (const download of finished.slice(0, Math.max(0, finished.length - HISTORY_LIMIT))) {
      this.downloads.delete(download.state.id);
    }
  }

  private snapshot(owner: BrowserWindow): WebWorkspaceDownloadState[] {
    return [...this.downloads.values()]
      .filter(download => !download.owner || download.owner === owner)
      .map(download => ({ ...download.state,
        ...(this.readError ? { persistenceError: "read" as const } : this.writeError ? { persistenceError: "write" as const } : {}),
      }))
      .sort((left, right) => right.startedAt - left.startedAt);
  }

  private emit(owner: BrowserWindow): void {
    if (!owner.isDestroyed() && !owner.webContents.isDestroyed()) this.onChange(owner, this.snapshot(owner));
  }

  private scheduleCheckpoint(): void {
    if (!this.historyPath || this.checkpoint) return;
    // Progress can arrive several times a second. Bound disk writes while still
    // retaining a recent interrupted record after an unclean process exit.
    this.checkpoint = setTimeout(() => {
      this.persist();
      for (const owner of this.owners.keys()) this.emit(owner);
    }, 1000);
    this.checkpoint.unref();
  }

  private persist(): void {
    if (this.checkpoint) clearTimeout(this.checkpoint);
    this.checkpoint = undefined;
    if (!this.historyPath || this.readError) return;
    try {
      writeTextFileAtomicSync(this.historyPath, JSON.stringify({
        version: 1,
        downloads: [...this.downloads.values()].map(download => download.state),
      }));
      this.writeError = false;
    } catch (error) {
      this.writeError = true;
      // A history write failure must not cancel the native file transfer.
      console.warn("[web-workspace] Could not save download history:", error);
    }
  }

  private load(): void {
    if (this.loaded) return;
    this.loaded = true;
    if (!this.historyPath) return;
    try {
      const data: unknown = JSON.parse(readFileSync(this.historyPath, "utf8"));
      if (!data || typeof data !== "object" || !("version" in data) || data.version !== 1
        || !("downloads" in data) || !Array.isArray(data.downloads)) throw new Error("Unsupported download history file.");
      for (const entry of data.downloads.slice(-HISTORY_LIMIT)) {
        if (!entry || typeof entry.id !== "string" || !entry.id || entry.id.length > 256
          || typeof entry.websiteId !== "string" || entry.websiteId.length > 256
          || typeof entry.filename !== "string" || entry.filename.length > 4096
          || typeof entry.savePath !== "string" || entry.savePath.length > 32768
          || (entry.savePath && !isAbsolute(entry.savePath))
          || !["progressing", "completed", "cancelled", "interrupted"].includes(entry.state)
          || ![entry.receivedBytes, entry.totalBytes, entry.startedAt].every(value => typeof value === "number" && Number.isFinite(value) && value >= 0)) continue;
        // Chromium's DownloadItem cannot be resurrected by retaining its path.
        // Never present stale pause/resume controls after a restart.
        const state: WebWorkspaceDownloadState = {
          id: entry.id, websiteId: entry.websiteId, filename: entry.filename, savePath: entry.savePath,
          receivedBytes: entry.receivedBytes, totalBytes: entry.totalBytes, startedAt: entry.startedAt,
          state: entry.state === "progressing" ? "interrupted" : entry.state,
          done: true, paused: false, canResume: false,
        };
        this.downloads.set(state.id, { state, detach: () => {} });
      }
    } catch (error) {
      if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") return;
      this.readError = new Error("Browser download history could not be read. The original file has been preserved.", { cause: error });
      console.warn("[web-workspace] Could not read download history:", error);
    }
  }
}
