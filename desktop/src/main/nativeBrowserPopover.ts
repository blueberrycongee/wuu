import { View, WebContentsView, type BrowserWindow, type Event, type MouseInputEvent, type WebContents, type WebContentsViewConstructorOptions, type WindowOpenHandlerResponse } from "electron";
import type { WebWorkspacePopoverBounds } from "../shared/webWorkspace";

/** A clipped shell-owned DOM portal above live website views. */
export class NativeBrowserPopover {
  private viewport = new View();
  private view?: WebContentsView;
  private token?: string;
  private watched: WebContents[] = [];

  constructor(private owner: BrowserWindow) {
    owner.on("blur", this.dismiss).on("hide", this.dismiss).on("minimize", this.dismiss).on("resize", this.dismiss);
    owner.webContents.on("did-start-navigation", details => {
      if (details.isMainFrame && !details.isSameDocument) this.destroy();
    });
    owner.once("closed", () => this.destroy());
  }

  open = (details: { url: string; frameName?: string }): WindowOpenHandlerResponse | undefined => {
    if (details.url !== "about:blank" || details.frameName !== "wuu-browser-popover") return;
    return { action: "allow", createWindow: options => {
      this.destroy();
      // Electron supplies the opener-connected guest here even though the
      // createWindow callback's BrowserWindow options omit it from their type.
      const guest = options as WebContentsViewConstructorOptions;
      const view = this.view = new WebContentsView({
        ...(guest.webContents ? { webContents: guest.webContents } : {}),
        webPreferences: options.webPreferences,
      });
      const [width, height] = this.owner.getContentSize();
      view.setBounds({ x: 0, y: 0, width, height });
      view.webContents.setZoomFactor(this.owner.webContents.getZoomFactor());
      view.setBackgroundColor("#00000000");
      this.viewport.setVisible(false);
      this.viewport.addChildView(view);
      this.owner.contentView.addChildView(this.viewport);
      view.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
      view.webContents.on("will-navigate", event => event.preventDefault());
      view.webContents.on("will-attach-webview", event => event.preventDefault());
      // Browser shortcuts still belong to the shell while a chrome panel has
      // focus; in particular Cmd/Ctrl+W must not close the application window.
      view.webContents.on("before-input-event", (event, input) => {
        this.owner.webContents.emit("before-input-event", event, input);
        if (event.defaultPrevented && input.type === "keyDown") this.dismiss();
      });
      view.webContents.on("blur", this.dismiss);
      return view.webContents;
    } };
  };

  report(token: string, bounds: WebWorkspacePopoverBounds | null): void {
    if (!bounds) { if (this.token === token) this.hide(); return; }
    const view = this.view;
    if (!view || view.webContents.isDestroyed() || this.owner.isDestroyed()) return;
    if (![bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite)) throw new Error("Popover bounds must be finite.");
    const opening = this.token !== token;
    this.token = token;
    const zoom = this.owner.webContents.getZoomFactor();
    const [width, height] = this.owner.getContentSize();
    const x = Math.max(0, Math.floor(bounds.x * zoom)), y = Math.max(0, Math.floor(bounds.y * zoom));
    const right = Math.min(width, Math.ceil((bounds.x + bounds.width) * zoom));
    const bottom = Math.min(height, Math.ceil((bounds.y + bounds.height) * zoom));
    if (view.webContents.getZoomFactor() !== zoom) view.webContents.setZoomFactor(zoom);
    // Keep CSS coordinates in the owner's viewport. Only the native parent
    // clips; the website below is neither resized nor hidden.
    const contentBounds = { x: -x, y: -y, width, height };
    const viewportBounds = { x, y, width: Math.max(0, right - x), height: Math.max(0, bottom - y) };
    for (const [target, next] of [[view, contentBounds], [this.viewport, viewportBounds]] as const) {
      const previous = target.getBounds();
      if (previous.x !== next.x || previous.y !== next.y || previous.width !== next.width || previous.height !== next.height) target.setBounds(next);
    }
    // Reattaching an already topmost native view needlessly rebuilds stacking
    // during suggestion/filter resizing. Only raise after a sibling was added.
    if (this.owner.contentView.children.at(-1) !== this.viewport) this.owner.contentView.addChildView(this.viewport);
    if (!this.viewport.getVisible()) this.viewport.setVisible(true);
    if (opening) {
      const watch = (parent: View): void => {
        for (const child of parent.children) {
          if (child instanceof WebContentsView && child !== view) {
            child.webContents.on("before-mouse-event", this.outside);
            this.watched.push(child.webContents);
          }
          watch(child);
        }
      };
      watch(this.owner.contentView);
      if (bounds.focus) view.webContents.focus();
    }
  }

  private outside = (_event: Event, mouse: MouseInputEvent): void => {
    if (mouse.type === "mouseDown" || mouse.type === "mouseWheel") this.dismiss();
  };

  private dismiss = (): void => {
    const token = this.token;
    this.hide();
    if (token && !this.owner.isDestroyed()) this.owner.webContents.send("wuu:web-workspace-popover-dismiss", token);
  };

  private hide(): void {
    this.token = undefined;
    if (this.view && !this.view.webContents.isDestroyed() && this.view.webContents.isFocused() && !this.owner.isDestroyed()) this.owner.webContents.focus();
    this.viewport.setVisible(false);
    for (const contents of this.watched) if (!contents.isDestroyed()) contents.removeListener("before-mouse-event", this.outside);
    this.watched = [];
  }

  private destroy(): void {
    this.hide();
    if (this.view) {
      this.viewport.removeChildView(this.view);
      if (!this.view.webContents.isDestroyed()) this.view.webContents.close();
      this.view = undefined;
    }
    if (!this.owner.isDestroyed()) this.owner.contentView.removeChildView(this.viewport);
  }
}
