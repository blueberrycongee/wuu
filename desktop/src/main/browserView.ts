import { WebContentsView, type WebContentsViewConstructorOptions } from "electron";
import { BROWSER_PARTITION } from "./browserHostWindows";

/** Use the same secure native-view construction for ordinary tabs and popups. */
export function createBrowserView(options?: WebContentsViewConstructorOptions): WebContentsView {
  return new WebContentsView({
    // Ordinary tabs create their contents; popups retain Chromium's supplied guest.
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
