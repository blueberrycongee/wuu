import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import type { ChromeOverlayCommand, ChromeOverlayRendererApi, ChromeOverlayRequest } from "../shared/chromeOverlay";
import { SidebarAccountPanel } from "./SidebarAccountParts";
import { ContextChatCapsuleSurface } from "./ContextChatCapsuleSurface";
import { I18nProvider } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { startFocusModality } from "./FocusModality";
import "./styles.css";
import "./styles/chrome-overlay.css";

const api = (window as unknown as Window & { wuuChromeOverlay: ChromeOverlayRendererApi }).wuuChromeOverlay;
startFocusModality();

function Overlay(): JSX.Element | null {
  const [request, setRequest] = useState<ChromeOverlayRequest | null>(null);
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
    previous.current = { token: request.token, focus: request.focus };
    // The capsule chooses between its editor and context-menu item itself.
    if (takeFocus && request.model.kind !== "capsule") {
      document.querySelector<HTMLElement>("button:not(:disabled), input, textarea")?.focus();
    }
    const frame = requestAnimationFrame(() => api.painted(request.token, request.generation));
    return () => cancelAnimationFrame(frame);
  }, [request]);
  if (!request) return null;
  const language = request.appearance.locale;
  return <I18nProvider key={language} preferenceStore={{ get: () => language, set: async () => {}, subscribe: () => () => {} }}>
    <WuuUIRoot><div className="chrome-overlay-surface" onKeyDownCapture={event => {
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      if (request.model.kind !== "capsule" && (event.key === "Escape" || event.key === "Tab")) {
        event.preventDefault(); event.stopPropagation(); send({ type: "dismiss", reason: event.key === "Escape" ? "escape" : "tab" });
      }
    }}>
      {request.model.kind === "capsule" ? <ContextChatCapsuleSurface key={request.token} model={request.model} send={send} />
        : <SidebarAccountPanel model={request.model.panel} onAction={action => send({ type: "account-action", action })} onClose={() => send({ type: "dismiss", reason: "escape" })} />}
    </div></WuuUIRoot>
  </I18nProvider>;
}

createRoot(document.getElementById("root")!).render(<Overlay />);
