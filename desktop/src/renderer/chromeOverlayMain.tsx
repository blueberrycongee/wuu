import "./ReactDevPerformance";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { ChromeOverlayCommand, ChromeOverlayRendererApi, ChromeOverlayRendererRequest } from "../shared/chromeOverlay";
import { SidebarAccountPanel } from "./SidebarAccountPanel";
import { ContextChatCapsuleSurface } from "./ContextChatCapsuleSurface";
import { I18nProvider } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { startFocusModality } from "./FocusModality";
import "./styles.css";
import "./styles/chrome-overlay.css";

const api = (window as unknown as Window & { wuuChromeOverlay: ChromeOverlayRendererApi }).wuuChromeOverlay;
startFocusModality();

function Overlay(): JSX.Element | null {
  const [request, setRequest] = useState<ChromeOverlayRendererRequest | null>(null);
  const [readyRequest, setReadyRequest] = useState<ChromeOverlayRendererRequest | null>(null);
  const contentReady = request?.phase === "presented" || Boolean(request && readyRequest === request);
  const previous = useRef<{ token: string; focus: boolean } | undefined>(undefined);
  useEffect(() => {
    let received = false;
    const off = api.onState(state => { received = true; setRequest(state); });
    void api.ready().then(state => { if (!received) setRequest(state); });
    return off;
  }, []);
  const send = (command: ChromeOverlayCommand) => { if (request) api.command({ token: request.token, generation: request.generation, command }); };
  useLayoutEffect(() => {
    if (!request) { previous.current = undefined; return; }
    const root = document.documentElement;
    root.dataset.theme = request.appearance.theme;
    root.lang = request.appearance.locale;
    root.style.cssText = "";
    root.style.fontFamily = request.appearance.fontFamily;
    for (const [name, value] of Object.entries(request.appearance.variables)) if (/^--[a-zA-Z\d_-]+$/.test(name) && !/url\s*\(/i.test(value)) root.style.setProperty(name, value);
    root.dataset.chromeSurface = request.model.kind;
    const takeFocus = request.focus && (previous.current?.token !== request.token || !previous.current.focus);
    // Preparation commits the new model hidden before the host makes the native
    // rectangle visible. Only then can Chromium deliver its real viewport.
    let frame: number;
    const paint = () => {
      if (request.phase === "prepare") {
        api.prepared(request.token, request.generation);
        return;
      }
      if (window.innerWidth <= 0 || window.innerHeight <= 0 || Math.abs(window.innerWidth - request.bounds.width) > 1
        || Math.abs(window.innerHeight - request.bounds.height) > 1) {
        frame = requestAnimationFrame(paint);
        return;
      }
      if (!contentReady) {
        setReadyRequest(request);
        return;
      }
      // The capsule chooses between its editor and context-menu item itself.
      if (takeFocus && request.model.kind !== "capsule") {
        document.querySelector<HTMLElement>("button:not(:disabled), input, textarea")?.focus({ preventScroll: true });
      }
      previous.current = { token: request.token, focus: request.focus };
      api.painted(request.token, request.generation, { width: window.innerWidth, height: window.innerHeight });
    };
    frame = requestAnimationFrame(paint);
    return () => cancelAnimationFrame(frame);
  }, [request, contentReady]);
  if (!request) return null;
  const language = request.appearance.locale;
  return <I18nProvider key={language} preferenceStore={{ get: () => language, set: async () => {}, subscribe: () => () => {} }}>
    <WuuUIRoot><div className="chrome-overlay-surface" style={{ visibility: contentReady ? "visible" : "hidden" }} inert={!contentReady} aria-hidden={!contentReady || undefined} onKeyDownCapture={event => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (request.model.kind !== "capsule" && (event.key === "Escape" || event.key === "Tab")) {
        event.preventDefault(); event.stopPropagation(); send({ type: "dismiss", reason: event.key === "Escape" ? "escape" : "tab" });
      }
    }}>
      {request.model.kind === "capsule" ? <ContextChatCapsuleSurface key={request.token} model={request.model} send={send} focusReady={contentReady} />
        : <SidebarAccountPanel model={request.model.panel} onAction={action => send({ type: "account-action", action })} onClose={() => send({ type: "dismiss", reason: "escape" })} />}
    </div></WuuUIRoot>
  </I18nProvider>;
}

createRoot(document.getElementById("root")!).render(<Overlay />);
