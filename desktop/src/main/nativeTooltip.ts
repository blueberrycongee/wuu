import { BrowserWindow } from "electron";
import type { WebWorkspaceTooltip } from "../shared/webWorkspace";

/** One non-interactive hint window above the owner's native web surfaces. */
export class NativeTooltip {
  private window?: BrowserWindow;
  private owner?: BrowserWindow;
  private token?: string;
  private revision = 0;
  private ready?: Promise<void>;

  async show(owner: BrowserWindow, token: string, hint: WebWorkspaceTooltip | null): Promise<void> {
    if (!hint) {
      if (this.token === token) this.hide();
      return;
    }
    if (!owner.isFocused() || !owner.isVisible() || owner.isMinimized()) return;
    this.token = token;
    const revision = ++this.revision;
    if (!this.window) {
      this.owner = owner;
      const window = this.window = new BrowserWindow({
        parent: owner, show: false, frame: false, transparent: true, focusable: false,
        resizable: false, skipTaskbar: true, hasShadow: false,
        // Two paint frames prepare each hint before it is shown. There is no
        // animation or polling while this small renderer is hidden.
        webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
      });
      window.setIgnoreMouseEvents(true);
      this.ready = window.loadURL('data:text/html;charset=utf-8,' + encodeURIComponent('<!doctype html><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src \'none\'; style-src \'unsafe-inline\'"><style>html,body{margin:0;background:transparent}body{overflow:hidden}#hint{box-sizing:border-box;overflow-wrap:anywhere;white-space:pre-line}</style><div id="hint"></div>'));
      owner.on("blur", this.hide).on("hide", this.hide).on("minimize", this.hide).on("resize", this.hide).on("move", this.hide);
      // Reloads and crashes cannot run the old renderer's tooltip cleanup.
      // Retire its hint before a replacement document can reuse a React token.
      const ownerContents = owner.webContents;
      const onNavigation = (details: Electron.Event<Electron.WebContentsDidStartNavigationEventParams>): void => {
        if (details.isMainFrame && !details.isSameDocument) this.hide();
      };
      ownerContents.on("did-start-navigation", onNavigation).on("render-process-gone", this.hide);
      owner.once("closed", () => {
        owner.removeListener("blur", this.hide).removeListener("hide", this.hide).removeListener("minimize", this.hide)
          .removeListener("resize", this.hide).removeListener("move", this.hide);
        ownerContents.removeListener("did-start-navigation", onNavigation).removeListener("render-process-gone", this.hide);
        if (!window.isDestroyed()) window.destroy();
        this.window = undefined; this.owner = undefined; this.ready = undefined;
        this.hide();
      });
    }
    await this.ready;
    const window = this.window;
    if (revision !== this.revision || !window || window.isDestroyed() || this.owner !== owner) return;
    const zoom = owner.webContents.getZoomFactor(), origin = owner.getContentBounds();
    window.webContents.setZoomFactor(zoom);
    window.setBounds({ x: Math.round(origin.x + hint.x * zoom), y: Math.round(origin.y + hint.y * zoom),
      width: Math.ceil(hint.width * zoom), height: Math.ceil(hint.height * zoom) });
    const height = await window.webContents.executeJavaScript(`new Promise(resolve => {
      const hint = document.getElementById('hint'), value = ${JSON.stringify(hint)};
      hint.textContent = value.text; Object.assign(hint.style, value.style, {width: value.width + 'px'});
      requestAnimationFrame(() => requestAnimationFrame(() => resolve(hint.getBoundingClientRect().height)));
    })`);
    if (revision !== this.revision || window.isDestroyed()) return;
    // The native renderer may resolve a fallback font differently from the shell.
    // Measure its actual text so long hints cannot be clipped at a line break.
    window.setBounds({ x: Math.round(origin.x + hint.x * zoom), y: Math.round(origin.y + (hint.y + (hint.height - height) / 2) * zoom),
      width: Math.ceil(hint.width * zoom), height: Math.ceil(height * zoom) });
    window.showInactive();
  }

  private hide = (): void => {
    this.revision++;
    this.token = undefined;
    if (this.window && !this.window.isDestroyed()) this.window.hide();
  };
}
