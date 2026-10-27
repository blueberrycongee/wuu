import { tmpdir } from "node:os";
import { extname, join } from "node:path";
import { Menu, nativeImage, shell, type BrowserWindow, type MenuItemConstructorOptions } from "electron";
import type { ArtifactSourceLease, ArtifactItemMenuResult } from "../shared/protocol";
import { isExternalDocumentName, canOpenArtifactExternally, exportArtifactCopy } from "./artifactExternalFile";
import type { ArtifactSources } from "./artifactSources";
import { mainTranslate } from "./i18n";
import { macWorkspaceApplications, openMacWorkspaceItemWithApplication, type MacWorkspaceApplication } from "./macWorkspaceApplications";

export async function showArtifactItemMenu(
  owner: BrowserWindow,
  input: ArtifactSourceLease,
  artifacts: ArtifactSources,
  isCurrentSource: () => boolean,
): Promise<ArtifactItemMenuResult> {
  const artifact = artifacts.metadata(input.leaseId);
  const openable = process.platform === "darwin" && isExternalDocumentName(artifact.name);
  const associations = openable
    ? await macWorkspaceApplications(join(tmpdir(), `wuu-artifact-association${extname(artifact.name)}`)).catch(() => ({ applications: [], defaultApplication: undefined }))
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
      const copy = await exportArtifactCopy(input, artifacts);
      try {
        if (!await canOpenArtifactExternally(copy.path)) throw new Error("This file can only be saved or shown in its folder");
        if (!isCurrentSource()) { await copy.dispose(); return { action: "none" }; }
        // The app path comes from Launch Services, never renderer input. Opening
        // explicitly with that app cannot fall back to executing the document.
        await openMacWorkspaceItemWithApplication(copy.path, application.path);
        return { action: "none" };
      } catch (error) { await copy.dispose(); throw error; }
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
        const copy = await exportArtifactCopy(input, artifacts);
        if (!isCurrentSource()) { await copy.dispose(); return { action: "none" }; }
        shell.showItemInFolder(copy.path);
        return { action: "none" };
      }),
    });
    Menu.buildFromTemplate(template).popup({
      window: owner,
      callback: () => { if (!selected) resolve({ action: "none" }); },
    });
  });
}
