import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { app, BrowserWindow, session } from "electron";
import { BrowserExtensionManager } from "../src/main/browserExtensions";
import { BrowserExtensionWindows } from "../src/main/browserExtensionWindows";

const profile = mkdtempSync(join(tmpdir(), "wuu-extension-options-"));
const artifacts = process.env.WUU_EXTENSION_OPTIONS_ARTIFACTS ?? "/tmp/wuu-extension-options-e2e";
app.setPath("userData", profile);
app.on("window-all-closed", () => undefined);
const checks: string[] = [];
async function waitFor(check: () => Promise<boolean> | boolean): Promise<void> {
  const deadline = Date.now() + 8000;
  while (!await check()) {
    if (Date.now() > deadline) throw new Error("Extension page did not reach the expected state");
    await new Promise(resolve => setTimeout(resolve, 25));
  }
}
app.whenReady().then(async () => {
  mkdirSync(artifacts, { recursive: true });
  for (const name of ["results.json", "failure.json"]) rmSync(join(artifacts, name), { force: true });
  const source = join(profile, "source");
  mkdirSync(source);
  writeFileSync(join(source, "manifest.json"), JSON.stringify({
    manifest_version: 3, name: "Reader preferences", version: "1.0",
    permissions: ["storage"], options_ui: { page: "options.html", open_in_tab: true },
    content_scripts: [{ matches: ["http://127.0.0.1/*"], js: ["content.js"], run_at: "document_end" }],
  }));
  writeFileSync(join(source, "options.html"), `<!doctype html><meta charset="utf-8">
    <title>Reader preferences</title><style>body{font:16px system-ui;margin:48px;color:#253142;background:#f6f8fa}button{font:inherit;padding:10px 16px}h1{font-size:24px}</style>
    <h1>Reader preferences</h1><p>This preference is shared with the current browser profile.</p>
    <button id="save">Use comfortable reading</button><p id="status">Ready</p><script src="options.js"></script>`);
  writeFileSync(join(source, "options.js"), `document.querySelector("#save").onclick=async()=>{
    await chrome.storage.local.set({readerMode:"comfortable"});document.querySelector("#status").textContent="Saved";};`);
  writeFileSync(join(source, "content.js"), `chrome.storage.local.get("readerMode").then(value=>{
    document.documentElement.dataset.readerMode=value.readerMode||"default";});`);
  const browserSession = session.fromPartition("persist:options-fixture");
  const manager = new BrowserExtensionManager(browserSession, join(profile, "extensions.json"));
  const windows = new BrowserExtensionWindows(browserSession, manager);
  const [extension] = await manager.addFromDirectory(source, async () => true);
  assert.equal(extension.hasOptions, true);
  const owner = new BrowserWindow({ show: false });
  const options = await windows.openOptions(extension.id, owner);
  await waitFor(() => options.webContents.executeJavaScript('Boolean(document.querySelector("#save"))'));
  assert.equal(options.webContents.session, browserSession);
  assert.deepEqual(await options.webContents.executeJavaScript("[typeof window.wuu,typeof require,typeof process]"),
    ["undefined", "undefined", "undefined"]);
  assert.equal(options.webContents.getLastWebPreferences().sandbox, true);
  checks.push("Options use the browser partition with sandboxing and no Wuu or Node bridge");
  assert.equal(await windows.openOptions(extension.id, owner), options);
  checks.push("Opening preferences again focuses the same window");
  assert.equal(await options.webContents.executeJavaScript('window.open("https://example.com") === null'), true);
  checks.push("Extension settings cannot create untracked popup windows");
  await options.webContents.executeJavaScript('document.querySelector("#save").click()');
  await waitFor(() => options.webContents.executeJavaScript('document.querySelector("#status").textContent==="Saved"'));
  await options.webContents.capturePage().then(image => writeFileSync(join(artifacts, "options.png"), image.toPNG()));
  const server = createServer((_req, res) => res.end("<!doctype html><title>Reader page</title><h1>Reading page</h1>"));
  await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const url = `http://127.0.0.1:${address.port}`;
  const page = new BrowserWindow({ show: false, webPreferences: { session: browserSession, sandbox: true } });
  await page.loadURL(url);
  await waitFor(() => page.webContents.executeJavaScript('document.documentElement.dataset.readerMode==="comfortable"'));
  checks.push("Saving extension settings changes content-script behavior on the user's browser profile");
  for (const target of [url, "file:///etc/passwd", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/options.html"]) {
    console.log(`Checking blocked navigation: ${target}`);
    const blocked = new Promise<void>(resolve => {
      const onNavigation = (event: Electron.Event) => {
        assert.equal(event.defaultPrevented, true);
        done();
      };
      const onConsole = (details: Electron.Event<Electron.WebContentsConsoleMessageEventParams>) => {
        if (details.message.includes("Not allowed to load local resource")) done();
      };
      const done = () => {
        options.webContents.removeListener("will-navigate", onNavigation);
        options.webContents.removeListener("console-message", onConsole);
        resolve();
      };
      options.webContents.once("will-navigate", onNavigation);
      options.webContents.on("console-message", onConsole);
    });
    await options.webContents.executeJavaScript(`location.href=${JSON.stringify(target)}`);
    await Promise.race([blocked, new Promise<void>((_resolve, reject) => setTimeout(() => reject(new Error(`No navigation event: ${target}`)), 3000))]);
    assert.ok(options.webContents.getURL().startsWith(`chrome-extension://${extension.id}/`));
  }
  checks.push("Remote, file and other-extension navigation cannot escape the options window");
  await manager.setEnabled(extension.id, false);
  await waitFor(() => options.isDestroyed());
  await assert.rejects(windows.openOptions(extension.id, owner), /enabled and loaded/);
  checks.push("Disabling closes settings immediately and prevents reopening");
  await manager.setEnabled(extension.id, true);
  const reopened = await windows.openOptions(extension.id, owner);
  await waitFor(() => reopened.webContents.executeJavaScript('Boolean(document.querySelector("#save"))'));
  reopened.close();
  await waitFor(() => reopened.isDestroyed());
  const afterClose = await windows.openOptions(extension.id, owner);
  await waitFor(() => afterClose.webContents.executeJavaScript('Boolean(document.querySelector("#save"))'));
  checks.push("Native close allows reopening extension preferences");
  owner.destroy();
  await waitFor(() => afterClose.isDestroyed());
  checks.push("Settings close with their owning Wuu window");
  page.destroy();
  await new Promise<void>(resolve => server.close(() => resolve()));
  writeFileSync(join(artifacts, "results.json"), JSON.stringify({
    status: "passed", platform: process.platform, electron: process.versions.electron, checks,
  }, null, 2));
  console.log(`PASS: ${checks.length} extension settings flows; artifacts: ${artifacts}`);
}).catch(error => {
  console.error(error);
  mkdirSync(artifacts, { recursive: true });
  writeFileSync(join(artifacts, "failure.json"), JSON.stringify({
    status: "failed", platform: process.platform, electron: process.versions.electron,
    checks, error: error instanceof Error ? error.stack : String(error),
  }, null, 2));
  process.exitCode = 1;
}).finally(() => {
  for (const window of BrowserWindow.getAllWindows()) window.destroy();
  rmSync(profile, { recursive: true, force: true });
  app.exit(process.exitCode ?? 0);
});
