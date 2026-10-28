import { app, Menu } from "electron";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createDesktopRecovery } from "./desktopRecovery";
import { productionApplicationMenuTemplate } from "./appShellGuards";

app.whenReady().then(async () => {
  const recovery = createDesktopRecovery({ preloadPath: join(dirname(fileURLToPath(import.meta.url)), "../preload/recovery.cjs"), mode: "diagnostic", shutdown: async () => {} });
  Menu.setApplicationMenu(Menu.buildFromTemplate(productionApplicationMenuTemplate(process.platform, undefined, recovery.menuItem())));
  app.on("activate", () => { void recovery.open(); });
  await recovery.open();
});
app.on("window-all-closed", () => { app.quit(); });
