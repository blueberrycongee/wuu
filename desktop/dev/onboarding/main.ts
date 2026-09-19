import { app, BrowserWindow, screen } from "electron";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { computeOnboardingWindowBounds } from "../../src/main/windowState";
import { discoverPreviewEngines } from "./discoverEngines";

// No product main/preload, app-server, or persistent Electron profile.
const profile = mkdtempSync(join(tmpdir(), "wuu-onboarding-preview-"));
app.setPath("userData", profile);
app.setPath("sessionData", profile);
app.on("quit", () => rmSync(profile, { recursive: true, force: true }));
app.on("window-all-closed", () => app.quit());

app.whenReady().then(async () => {
  const url = process.env.ELECTRON_RENDERER_URL;
  if (!url) throw new Error("Start this preview with npm run dev:onboarding");
  const display = screen.getDisplayNearestPoint(screen.getCursorScreenPoint());
  const win = new BrowserWindow({
    ...computeOnboardingWindowBounds(display.workArea),
    resizable: false,
    title: "Wuu — Onboarding preview (not saved)",
    ...(process.platform === "darwin" ? {
      titleBarStyle: "hiddenInset" as const,
      trafficLightPosition: { x: 18, y: 17 },
    } : {}),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      partition: "onboarding-preview",
    },
  });
  const previewURL = new URL("/dev/onboarding/index.html", url);
  for (const engine of discoverPreviewEngines().engines) {
    if (engine.binary_ok) previewURL.searchParams.append("availableEngine", engine.id);
  }
  await win.loadURL(previewURL.href);
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
