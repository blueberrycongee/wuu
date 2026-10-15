import type { MessageBoxOptions, MessageBoxReturnValue } from "electron";
import type { AppUpdater } from "electron-updater";
import { mainTranslate as t } from "./i18n";

export type UpdateState =
  | { kind: "idle" | "checking" | "prompting" | "installing" }
  | { kind: "downloading"; percent: number }
  | { kind: "ready"; version: string };

export function createUpdateController(options: {
  updater: Pick<AppUpdater, "checkForUpdates" | "downloadUpdate">;
  prompt(options: MessageBoxOptions): Promise<MessageBoxReturnValue>;
  install(): Promise<void>;
  changed(state: UpdateState): void;
}) {
  let state: UpdateState = { kind: "idle" };
  let busy = false;
  let stopped = false;
  let deferredVersion = "";
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
  return {
    get state() { return state; },
    stop() { stopped = true; },
    progress(percent: number) {
      if (state.kind === "downloading") set({ kind: "downloading", percent: Math.round(percent) });
    },
    async check(manual: boolean) {
      if (busy || stopped || state.kind === "installing") return;
      if (!manual && state.kind === "ready") return;
      busy = true;
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
        if (!manual && deferredVersion === version) { set({ kind: "idle" }); return; }
        set({ kind: "prompting" });
        const { response } = await options.prompt({
          type: "info", title: "Wuu", message: t("updateAvailable", { version }),
          detail: t("updateDownloadDetail"), buttons: [t("updateDownload"), t("updateLater")],
          defaultId: 1, cancelId: 1,
        });
        if (stopped) return;
        if (response !== 0) { deferredVersion = version; set({ kind: "idle" }); return; }
        showFailure = true;
        set({ kind: "downloading", percent: 0 });
        await options.updater.downloadUpdate();
        if (!stopped) await offerRestart(version);
      } catch (error) {
        console.error("Desktop update failed", { error });
        set({ kind: "idle" });
        if (showFailure && !stopped) await options.prompt({
          type: "error", title: "Wuu", message: t("updateFailed"), detail: t("updateRetryDetail"),
        });
      } finally { busy = false; }
    },
  };
}
