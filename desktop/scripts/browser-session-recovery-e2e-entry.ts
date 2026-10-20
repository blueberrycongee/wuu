import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { app, BrowserWindow, WebContentsView } from "electron";
import { WebWorkspaceHost } from "../src/main/webWorkspaceHost";
import { WebWorkspaceLibrary } from "../src/main/webWorkspaceLibrary";
import { WEB_WORKSPACE_RIBBON_ID as id } from "../src/shared/webWorkspace";

// Failure cases: chosen zoom resets, same-origin tabs overwrite each other,
// startup eagerly loads background pages, or cookies/pin/order disappear.
const fixture = process.env.WUU_BROWSER_SESSION_FIXTURE!;
const output = process.env.WUU_BROWSER_SESSION_OUTPUT!;
const url = process.env.WUU_BROWSER_SESSION_URL!;
app.setPath("userData", join(fixture, "profile"));
app.on("window-all-closed", () => undefined);
const waitFor = async (check: () => boolean): Promise<void> => {
  const deadline = Date.now() + 10000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error("Native page did not reach its expected state");
    await new Promise(resolve => setTimeout(resolve, 20));
  }
};
app.whenReady().then(async () => {
  mkdirSync(output, { recursive: true });
  const owner = new BrowserWindow({ width: 980, height: 720, show: true,
    webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
  await owner.loadURL("data:text/html,<title>Session recovery</title>");
  const library = new WebWorkspaceLibrary(join(fixture, "library.json"));
  const host = new WebWorkspaceHost(() => undefined, () => undefined, { library });
  const initial = host.initializeTabs(owner, []);
  const view = (): WebContentsView => owner.contentView.children.find(child => child instanceof WebContentsView && child.getVisible()) as WebContentsView;
  const settle = async (): Promise<void> => {
    host.reportBounds(owner, id, { x: 0, y: 0, right: 0, bottom: 0 });
    await waitFor(() => Boolean(view() && !view().webContents.isLoadingMainFrame()));
  };
  if (process.env.WUU_BROWSER_SESSION_PHASE === "seed") {
    assert.equal(initial.tabs!.length, 0);
    const first = host.createTab(owner, id, `${url}/first`).activeTabId!;
    await settle();
    host.setZoom(owner, id, 1.5);
    host.setTabPinned(owner, id, first, true);
    host.setTabMuted(owner, id, first, true);
    await view().webContents.session.cookies.set({ url, name: "sessionFixture", value: "retained", expirationDate: Date.now() / 1000 + 3600 });
    const second = host.createTab(owner, id, `${url}/second`).activeTabId!;
    await settle();
    host.setZoom(owner, id, 0.8);
    host.reorderTabs(owner, id, [second, first]);
    host.selectTab(owner, id, first);
    await settle();
    assert.ok(Math.abs(view().webContents.getZoomFactor() - 1.5) < 1e-6);
    await view().webContents.session.flushStorageData();
    await view().webContents.session.cookies.flushStore();
    writeFileSync(join(fixture, "expected.json"), JSON.stringify({ first, second }));
    host.destroyAll();
    app.exit(0);
    return;
  }
  const { first, second } = JSON.parse(readFileSync(join(fixture, "expected.json"), "utf8"));
  assert.equal(owner.contentView.children.filter(child => child instanceof WebContentsView).length, 0, "Restoration must remain lazy");
  assert.deepEqual(initial.tabs!.map(tab => tab.id), [second, first]);
  assert.equal(initial.activeTabId, first);
  assert.equal(initial.tabs!.find(tab => tab.id === first)!.pinned, true);
  assert.equal(initial.tabs!.find(tab => tab.id === first)!.muted, true);
  assert.equal(initial.tabs!.find(tab => tab.id === first)!.zoomFactor, 1.5, "Chosen page zoom must survive restart");
  assert.equal(initial.tabs!.find(tab => tab.id === second)!.zoomFactor, 0.8);
  host.open(owner, { id, url: "about:blank" });
  await settle();
  assert.ok(Math.abs(view().webContents.getZoomFactor() - 1.5) < 1e-6);
  assert.equal(view().webContents.isAudioMuted(), true, "Restored muted tabs must stay silent before page playback starts");
  assert.equal((await view().webContents.session.cookies.get({ url, name: "sessionFixture" }))[0]?.value, "retained");
  assert.equal(owner.contentView.children.filter(child => child instanceof WebContentsView).length, 1);
  host.selectTab(owner, id, second); await settle();
  assert.equal(view().webContents.isAudioMuted(), false, "A same-origin sibling retains its own audio choice");
  assert.ok(Math.abs(view().webContents.getZoomFactor() - 0.8) < 1e-6);
  host.selectTab(owner, id, first); await settle();
  assert.ok(Math.abs(view().webContents.getZoomFactor() - 1.5) < 1e-6);
  writeFileSync(join(output, "restored-page.png"), (await view().webContents.capturePage()).toPNG());
  writeFileSync(join(output, "report.json"), JSON.stringify({ status: "passed", electron: process.versions.electron,
    chromium: process.versions.chrome, platform: process.platform, checks: [
      "Two fresh Electron processes retain tab order, selected tab, pin and persistent cookies",
      "Distinct zoom preferences survive restart and switching between same-origin tabs",
      "Muted tabs stay muted after restart without muting a same-origin sibling",
      "Restored background tabs do not create native views until selected",
    ], limitations: ["Local HTTP fixture; no account authentication", "Screenshot is the actual native webpage, not the full product shell"] }, null, 2));
  host.destroyAll();
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
