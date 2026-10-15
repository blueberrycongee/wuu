import { app, dialog, Menu } from "electron";
import updater from "electron-updater";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { createUpdateController } from "./desktopUpdates";
import { mainTranslate as t } from "./i18n";
import { productionApplicationMenuTemplate } from "./appShellGuards";

export function installDesktopUpdates(shutdown: () => Promise<void>) {
  // Development/ad-hoc builds never carry the release update configuration.
  if (!app.isPackaged || process.platform !== "darwin" || !existsSync(join(process.resourcesPath, "app-update.yml"))) return;
  const { autoUpdater } = updater;
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = false;
  autoUpdater.allowPrerelease = false;
  autoUpdater.allowDowngrade = false;
  autoUpdater.logger = null;
  const refresh = () => {
    const item = Menu.getApplicationMenu()?.getMenuItemById("check-for-updates");
    if (!item) return;
    const state = controller.state;
    item.enabled = state.kind === "idle" || state.kind === "ready";
    item.label = state.kind === "ready" ? t("updateRestart")
      : state.kind === "downloading" ? t("updateProgress", { percent: state.percent })
      : state.kind === "checking" ? t("updateChecking") : t("updateCheck");
  };
  const controller = createUpdateController({
    updater: autoUpdater,
    prompt: (options) => dialog.showMessageBox(options),
    install: async () => { await shutdown(); autoUpdater.quitAndInstall(); },
    changed: refresh,
  });
  autoUpdater.on("error", (error) => {
    console.error("Desktop updater error", { error });
    if (controller.state.kind === "installing") {
      void dialog.showMessageBox({
        type: "error", title: "Wuu", message: t("updateFailed"), detail: t("updateInstallRecovery"),
        buttons: [t("updateReopen"), t("updateLater")], cancelId: 1,
      }).then(({ response }) => { if (response === 0) { app.relaunch(); app.quit(); } });
    }
  });
  autoUpdater.on("download-progress", ({ percent }) => controller.progress(percent));
  Menu.setApplicationMenu(Menu.buildFromTemplate(productionApplicationMenuTemplate(process.platform, {
    id: "check-for-updates", label: t("updateCheck"), click: () => { void controller.check(true); },
  })));
  const initial = setTimeout(() => { void controller.check(false); }, 30_000);
  const periodic = setInterval(() => { void controller.check(false); }, 6 * 60 * 60_000);
  initial.unref();
  periodic.unref();
  return {
    refresh,
    stop() { clearTimeout(initial); clearInterval(periodic); controller.stop(); },
  };
}
