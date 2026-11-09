import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { UILayerTarget } from "./ui/layers/UILayerHost";
import type { ApplicationWebWorkspaceV1 } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { startFocusModality } from "./FocusModality";

let popup: Window | null = null;

/** Reuse the existing React controls in a same-origin native overlay view. */
export function NativeBrowserPopover({ controls, children, onDismiss, focus = true }: {
  controls?: ApplicationWebWorkspaceV1; children: ReactNode; onDismiss: () => void; focus?: boolean;
}): ReactNode {
  const api = controls?.capabilities.nativeOverlays ? controls : undefined;
  const [target, setTarget] = useState<HTMLElement>();
  const dismiss = useRef(onDismiss);
  dismiss.current = onDismiss;
  useLayoutEffect(() => { if (target) window.dispatchEvent(new Event("resize")); }, [target]);
  useLayoutEffect(() => {
    if (!api?.reportPopover) return;
    if (!popup || popup.closed) popup = window.open("about:blank", "wuu-browser-popover");
    if (!popup) { dismiss.current(); return; }
    const child = popup, doc = child.document, token = crypto.randomUUID();
    const base = doc.createElement("base"); base.href = document.baseURI;
    doc.head.replaceChildren(base, ...[...document.querySelectorAll('link[rel="stylesheet"], style')].map(node => node.cloneNode(true)));
    for (const attribute of [...document.documentElement.attributes]) doc.documentElement.setAttribute(attribute.name, attribute.value);
    doc.body.className = document.body.className;
    const style = doc.createElement("style");
    style.textContent = 'html,body{background:transparent!important;margin:0!important;overflow:hidden!important}body{height:100vh}';
    doc.head.append(style);
    doc.body.dataset.browserPopover = "";
    const stopFocusModality = startFocusModality(doc);
    let disposed = false, last = "", first = true;
    const report = (): void => {
      if (disposed || !doc.body.firstElementChild) return;
      const rect = doc.body.firstElementChild.getBoundingClientRect();
      if (!rect.width || !rect.height || child.getComputedStyle(doc.body.firstElementChild).visibility === "hidden") return;
      const bounds = { x: rect.x - 12, y: rect.y - 12, width: rect.width + 24, height: rect.height + 24, focus };
      const key = JSON.stringify(bounds);
      if (key === last) return;
      last = key;
      void api.reportPopover(token, bounds).then(() => {
        if (disposed || !first) return;
        first = false;
        if (focus) (doc.querySelector<HTMLElement>('input:not(:disabled)') ?? doc.querySelector<HTMLElement>('button:not(:disabled)'))?.focus();
      }).catch(() => { if (!disposed) dismiss.current(); });
    };
    const mutation = new MutationObserver(report);
    mutation.observe(doc.body, { childList: true, subtree: true, attributes: true });
    const resize = new ResizeObserver(report); resize.observe(doc.body);
    const unsubscribe = api.onPopoverDismiss(value => { if (value === token) dismiss.current(); });
    const escape = (event: KeyboardEvent): void => {
      // Component handlers (such as history clear confirmation) get first use.
      if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); dismiss.current(); }
      if (!event.defaultPrevented && (event.metaKey || event.ctrlKey)) {
        const shortcut = new KeyboardEvent("keydown", { key: event.key, code: event.code, metaKey: event.metaKey, ctrlKey: event.ctrlKey, shiftKey: event.shiftKey, altKey: event.altKey, cancelable: true });
        window.dispatchEvent(shortcut);
        if (shortcut.defaultPrevented) { event.preventDefault(); dismiss.current(); }
      }
    };
    child.addEventListener("keydown", escape);
    const loaded = Promise.all([...doc.querySelectorAll('link[rel="stylesheet"]')].map(link => new Promise<void>(resolve => {
      if ((link as HTMLLinkElement).sheet) resolve(); else { link.addEventListener("load", () => resolve(), { once: true }); link.addEventListener("error", () => resolve(), { once: true }); }
    })));
    void loaded.then(() => { if (!disposed) setTarget(doc.body); });
    window.addEventListener("resize", report);
    return () => {
      disposed = true; mutation.disconnect(); resize.disconnect(); unsubscribe();
      stopFocusModality();
      child.removeEventListener("keydown", escape); window.removeEventListener("resize", report);
      void api.reportPopover(token, null).catch(error => console.error("Browser popover cleanup failed", error));
    };
  }, [api, focus]);
  if (!api?.reportPopover) return children;
  return target ? <UILayerTarget target={target}>{children}</UILayerTarget> : null;
}
