import { WebContentsView, type WebContentsViewConstructorOptions } from "electron";
import { BROWSER_PARTITION } from "./browserHostWindows";

/** Use the same secure native-view construction for ordinary tabs and popups. */
export function createBrowserView(options?: WebContentsViewConstructorOptions): WebContentsView {
  const view = new WebContentsView({
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
  const contents = view.webContents;
  contents.on("dom-ready", () => {
    // Website titlebars can declare transparent drag regions even with
    // pointer-events:none. Only Wuu's chrome may own native window dragging;
    // user-origin !important also overrides a page's inline/important rules.
    void contents.insertCSS("*, *::before, *::after { -webkit-app-region: no-drag !important; }", { cssOrigin: "user" })
      .catch(error => {
        if (!contents.isDestroyed()) console.warn("[browser] Could not disable website drag regions", error);
      });
  });
  return view;
}
