import type { MessageBoxOptions, MessageBoxReturnValue } from "electron";
import type { AppUpdater } from "electron-updater";
import type { DesktopUpdateState } from "../shared/protocol";
import { mainTranslate as t } from "./i18n";

export type UpdateState = DesktopUpdateState;

export function createUpdateController(options: {
  updater: Pick<AppUpdater, "checkForUpdates" | "downloadUpdate">;
  prompt(options: MessageBoxOptions): Promise<MessageBoxReturnValue>;
  install(): Promise<void>;
  changed(state: UpdateState): void;
}) {
  let state: UpdateState = { kind: "idle" };
  let busy = false;
  let stopped = false;
  const set = (next: UpdateState) => { state = next; options.changed(next); };
  async function offerRestart(version: string) {
    set({ kind: "ready", version });
    const { response } = await options.prompt({
      type: "info", title: "Wuu", message: t("updateReady", { version }),
      detail: t("updateRestartDetail"), buttons: [t("updateRestart"), t("updateLater")],
      defaultId: 1, cancelId: 1,
    });
    if (response !== 0 || stopped) return;
    set({ kind: "installing" });
    await options.install();
  }
  async function download(version: string) {
    set({ kind: "downloading", version, percent: 0 });
    try {
      await options.updater.downloadUpdate();
      if (!stopped) set({ kind: "ready", version });
    } catch (error) {
      if (!stopped) set({ kind: "error", version });
      throw error;
    }
  }
  return {
    get state() { return state; },
    stop() { stopped = true; },
    progress(percent: number) {
      if (!stopped && state.kind === "downloading" && Number.isFinite(percent)) {
        set({ ...state, percent: Math.max(0, Math.min(100, Math.round(percent))) });
      }
    },
    async download() {
      if (busy || stopped || (state.kind !== "available" && state.kind !== "error")) return;
      busy = true;
      try { await download(state.version); }
      catch (error) { console.error("Desktop update download failed", { error }); }
      finally { busy = false; }
    },
    async restart() {
      if (busy || stopped || state.kind !== "ready") return;
      const version = state.version;
      busy = true;
      try { await offerRestart(version); }
      catch (error) {
        console.error("Desktop update restart failed", { error });
        set({ kind: "ready", version });
        await options.prompt({ type: "error", title: "Wuu", message: t("updateFailed"), detail: t("updateRetryDetail") });
      } finally { busy = false; }
    },
    async check(manual: boolean) {
      if (busy || stopped || state.kind === "installing") return;
      if (!manual && state.kind !== "idle") return;
      busy = true;
      const previous = state;
      let showFailure = manual;
      try {
        if (state.kind === "ready") { await offerRestart(state.version); return; }
        set({ kind: "checking" });
        const result = await options.updater.checkForUpdates();
        if (stopped) return;
        if (!result?.isUpdateAvailable) {
          set({ kind: "idle" });
          if (manual) await options.prompt({ type: "info", title: "Wuu", message: t("updateCurrent") });
          return;
        }
        const version = result.updateInfo.version;
        if (!manual) { set({ kind: "available", version }); return; }
        set({ kind: "prompting" });
        const { response } = await options.prompt({
          type: "info", title: "Wuu", message: t("updateAvailable", { version }),
          detail: t("updateDownloadDetail"), buttons: [t("updateDownload"), t("updateLater")],
          defaultId: 1, cancelId: 1,
        });
        if (stopped) return;
        if (response !== 0) { set({ kind: "available", version }); return; }
        showFailure = true;
        await download(version);
        if (!stopped) await offerRestart(version);
      } catch (error) {
        console.error("Desktop update failed", { error });
        if (state.kind !== "error") {
          set(previous.kind === "available" || previous.kind === "ready" || previous.kind === "error" ? previous : { kind: "idle" });
        }
        if (showFailure && !stopped) await options.prompt({
          type: "error", title: "Wuu", message: t("updateFailed"), detail: t("updateRetryDetail"),
        });
      } finally { busy = false; }
    },
  };
}
