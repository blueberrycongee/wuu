// macOS update E2E: real menu, renderer/preload, scheduler and MacUpdater download.
// Consent is scripted; installation is deliberately never requested.
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const desktop = path.resolve(__dirname, "..");
const output = path.resolve(process.env.WUU_UPDATE_E2E_OUTPUT || path.join(desktop, "out/e2e/desktop-update"));

async function main() {
  if (process.platform !== "darwin") throw new Error("This E2E exercises the macOS updater.");
  fs.mkdirSync(output, { recursive: true });
  fs.rmSync(path.join(output, "result.json"), { force: true });
  fs.rmSync(path.join(output, "failure.json"), { force: true });
  if (!fs.existsSync(path.join(output, "node_modules"))) {
    fs.symlinkSync(path.join(desktop, "node_modules"), path.join(output, "node_modules"), "dir");
  }
  const build = require("esbuild").build;
  await build({
    entryPoints: [path.join(desktop, "src/main/desktopUpdateMenu.ts")],
    outfile: path.join(output, "host.cjs"), bundle: true, platform: "node", format: "cjs",
    external: ["electron", "electron-updater"],
  });
  await build({
    entryPoints: [path.join(desktop, "src/main/preload.ts")],
    outfile: path.join(output, "preload.cjs"), bundle: true, platform: "node", format: "cjs", external: ["electron"],
  });
  await build({
    stdin: { contents: `
      import React from "react";
      import { createRoot } from "react-dom/client";
      import { DesktopUpdateButton } from "./src/renderer/DesktopUpdateButton";
      import { I18nProvider } from "./src/renderer/i18n";
      import "./src/renderer/styles/base.css";
      import "./src/renderer/styles/theme.css";
      import "./src/renderer/styles/appearance.css";
      import "./src/renderer/styles/sidebar.css";
      import "./src/renderer/styles/settings.css";
      createRoot(document.getElementById("root")).render(
        <I18nProvider><div className="sidebar-account" style={{position:"relative",height:60}}>
          <span className="sidebar-account-name">Update E2E</span><DesktopUpdateButton />
        </div></I18nProvider>);
    `, loader: "tsx", resolveDir: desktop },
    outfile: path.join(output, "renderer.js"), bundle: true, platform: "browser", jsx: "automatic",
    define: { "import.meta.env": JSON.stringify({ DEV: false, MODE: "production" }) },
  });
  fs.writeFileSync(path.join(output, "index.html"), `<!doctype html><html><head>
    <meta charset="UTF-8"><link rel="stylesheet" href="renderer.css">
    <style>:root{--conversation-message-font-size:15px}
      body{margin:0;padding:32px;background:var(--paper);font-size:var(--font-ui)}
      h1{font-size:20px;font-weight:500}.sidebar-account{max-width:360px;padding:12px;--sidebar-row-pad-x:12px}</style>
    </head><body><h1>Desktop update download</h1><div id="root" class="sidebar-content"></div><script src="renderer.js"></script></body></html>`);
  fs.copyFileSync(path.join(__dirname, "desktop-update-e2e-entry.cjs"), path.join(output, "entry.cjs"));
  fs.writeFileSync(path.join(output, "main.cjs"), `try { require("./entry.cjs"); }
    catch (error) { console.error(error); require("electron").app.exit(1); }`);
  fs.writeFileSync(path.join(output, "package.json"), JSON.stringify({ name: "wuu-update-e2e", version: "2026.10.15", main: "main.cjs" }));
  const binary = process.env.WUU_UPDATE_E2E_ELECTRON || String(require("electron"));
  const result = spawnSync(binary, [output], {
    stdio: "inherit", timeout: 90_000,
    env: { ...process.env, ELECTRON_RUN_AS_NODE: "", WUU_UPDATE_E2E_OUTPUT: output },
  });
  if (result.error) throw result.error;
  if (result.status === 0 && !fs.existsSync(path.join(output, "result.json"))) throw new Error("E2E exited without passing evidence.");
  process.exitCode = result.status ?? 1;
}
main().catch(error => { console.error(error); process.exitCode = 1; });
