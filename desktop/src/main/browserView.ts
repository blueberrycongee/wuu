import { WebContentsView, type WebContentsViewConstructorOptions } from "electron";
import { BROWSER_PARTITION } from "./browserHostWindows";

/** Use the same secure native-view construction for ordinary tabs and popups. */
export function createBrowserView(options?: WebContentsViewConstructorOptions): WebContentsView {
  return new WebContentsView({
    // Electron may supply an explicitly undefined guest for native link opens;
    // WebContentsView requires the property to be absent in that case.
    ...(options?.webContents ? { webContents: options.webContents } : {}),
    webPreferences: {
      ...options?.webPreferences,
      partition: BROWSER_PARTITION,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
}
