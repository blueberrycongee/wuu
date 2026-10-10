import { Menu, nativeImage, shell, type BrowserWindow, type MenuItemConstructorOptions } from "electron";
import type { ArtifactItemMenuParams, ArtifactItemMenuResult } from "../shared/protocol";
import { artifactMenuFile, canOpenArtifactExternally, exportArtifactCopy } from "./artifactExternalFile";
import { mainTranslate } from "./i18n";
import { macWorkspaceApplications, openMacWorkspaceItemWithApplication, type MacWorkspaceApplication } from "./macWorkspaceApplications";

export async function showArtifactItemMenu(
  owner: BrowserWindow,
  input: ArtifactItemMenuParams,
  wuuHome: string,
  isCurrentSource: () => boolean,
): Promise<ArtifactItemMenuResult> {
  const artifact = await artifactMenuFile(input, wuuHome);
  const openable = process.platform === "darwin" && await canOpenArtifactExternally(artifact.filePath);
  const associations = openable
    ? await macWorkspaceApplications(artifact.filePath).catch(() => ({ applications: [], defaultApplication: undefined }))
    : undefined;
  if (!isCurrentSource()) return { action: "none" };

  return new Promise((resolve, reject) => {
    let selected = false;
    const choose = (action: () => Promise<ArtifactItemMenuResult>) => () => {
      selected = true;
      if (!isCurrentSource()) {
        resolve({ action: "none" });
        return;
      }
      void action().then(resolve, reject);
    };
    const openWith = (application: MacWorkspaceApplication) => choose(async () => {
      const path = await exportArtifactCopy(input, wuuHome);
      if (!await canOpenArtifactExternally(path)) throw new Error("This file can only be saved or shown in its folder");
      if (!isCurrentSource()) return { action: "none" };
      // The app path comes from Launch Services, never renderer input. Opening
      // explicitly with that app cannot fall back to executing the document.
      await openMacWorkspaceItemWithApplication(path, application.path);
      return { action: "none" };
    });
    const appIcon = (application: MacWorkspaceApplication) => {
      if (!application.iconPng) return undefined;
      const icon = nativeImage.createFromBuffer(Buffer.from(application.iconPng, "base64"), { scaleFactor: 2 });
      return icon.isEmpty() ? undefined : icon;
    };
    const template: MenuItemConstructorOptions[] = [];
    if (associations) {
      const defaultApp = associations.defaultApplication;
      if (defaultApp) template.push({
        label: mainTranslate("openInApplication", { application: defaultApp.name }),
        icon: appIcon(defaultApp),
        click: openWith(defaultApp),
      });
      template.push({
        label: mainTranslate("openWith"),
        enabled: associations.applications.length > 0,
        submenu: associations.applications.map(application => ({
          label: application.name,
          icon: appIcon(application),
          click: openWith(application),
        })),
      }, { type: "separator" });
    }
    template.push({
      label: mainTranslate("saveArtifactAs"),
      click: choose(async () => ({ action: "save" })),
    }, {
      label: mainTranslate(process.platform === "darwin" ? "showInFinder" : process.platform === "win32" ? "showInExplorer" : "showInFileManager"),
      click: choose(async () => {
        const path = await exportArtifactCopy(input, wuuHome);
        if (!isCurrentSource()) return { action: "none" };
        shell.showItemInFolder(path);
        return { action: "none" };
      }),
    });
    Menu.buildFromTemplate(template).popup({
      window: owner,
      callback: () => { if (!selected) resolve({ action: "none" }); },
    });
  });
}
