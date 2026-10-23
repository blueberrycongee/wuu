import { createContext, useLayoutEffect, useRef, useState } from "react";
import type { ChromeAccountTrigger, ChromeDesktopUpdate, ChromeOverlayApi, ChromeOverlayCommand, ChromeOverlayModel, ChromeOverlayRect, ChromeOverlayRequest } from "../shared/chromeOverlay";
import { CHROME_OVERLAY_VERSION } from "../shared/chromeOverlay";
import { useI18n } from "./i18n";

export const NativeRibbonFooterContext = createContext<{
  publishAccount: (trigger: ChromeAccountTrigger | undefined, open?: (bounds: ChromeOverlayRect) => void) => void;
  publishUpdate: (model: ChromeDesktopUpdate | undefined, activate?: () => void) => void;
} | null>(null);

export function chromeOverlayApi(): ChromeOverlayApi | undefined {
  return (window.wuu as typeof window.wuu & { chromeOverlay?: ChromeOverlayApi })?.chromeOverlay;
}

/** Models contain presentation data; callbacks and account/browser authority stay here. */
export function useChromeOverlay(enabled: boolean, bounds: ChromeOverlayRect | null, model: ChromeOverlayModel,
  onCommand: (command: ChromeOverlayCommand) => void, onDismiss: () => void, focus = false, appearanceElement?: HTMLElement | null): boolean {
  const api = chromeOverlayApi();
  const { locale } = useI18n();
  const token = useRef<string | undefined>(undefined);
  const generation = useRef(0);
  const callbacks = useRef({ onCommand, onDismiss });
  callbacks.current = { onCommand, onDismiss };
  const [appearanceRevision, setAppearanceRevision] = useState(0);
  const active = Boolean(api && enabled && bounds);
  const modelKey = JSON.stringify(model), boundsKey = JSON.stringify(bounds);
  useLayoutEffect(() => {
    if (!active) return;
    const changed = () => setAppearanceRevision(value => value + 1);
    const observer = new MutationObserver(changed);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-appearance-motion", "data-shell-skin", "data-shell-image", "style", "class"] });
    return () => observer.disconnect();
  }, [active]);
  useLayoutEffect(() => {
    if (!api) return;
    return api.onEvent(event => {
      if (!token.current || event.token !== token.current) return;
      // Dismissal ends this opening, even if a presentation update crossed it.
      // Business commands still require the exact currently displayed model.
      const lifecycle = event.command.type === "dismiss" || event.command.type === "pointer" || event.command.type === "reveal";
      if (lifecycle ? event.generation > generation.current : event.generation !== generation.current) return;
      callbacks.current.onCommand(event.command);
      if (event.command.type === "dismiss") callbacks.current.onDismiss();
    });
  }, [api]);
  useLayoutEffect(() => {
    if (!api || !active) return;
    const currentToken = crypto.randomUUID();
    token.current = currentToken;
    generation.current = 0;
    return () => {
      void api.hide(currentToken, generation.current);
      if (token.current === currentToken) token.current = undefined;
    };
  }, [api, active]);
  useLayoutEffect(() => {
    if (!api || !active || !bounds || !token.current) return;
    const currentToken = token.current;
    const computed = getComputedStyle(appearanceElement ?? document.documentElement);
    const variables: Record<string, string> = {};
    // Keep user/theme tokens without accepting CSS rules or external resources.
    for (const name of computed) {
      if (!name.startsWith("--")) continue;
      const value = computed.getPropertyValue(name).trim();
      if (value.length <= 512 && !/url\s*\(/i.test(value)) variables[name] = value;
    }
    if (model.kind === "ribbon") {
      // The native view is only ribbon-sized; its wallpaper uses the owner's
      // viewport. Window resize dismisses it, so capture this when presenting.
      variables["--shell-viewport-width"] = `${window.innerWidth}px`;
      variables["--shell-viewport-height"] = `${window.innerHeight}px`;
    }
    const current = ++generation.current;
    const request: ChromeOverlayRequest = { version: CHROME_OVERLAY_VERSION, token: currentToken, generation: current,
      bounds, model, focus, appearance: { theme: document.documentElement.dataset.theme === "dark" ? "dark" : "light", locale, fontFamily: computed.fontFamily, variables,
        shellSkin: model.kind === "ribbon" && document.documentElement.hasAttribute("data-shell-skin"),
        shellImage: model.kind === "ribbon" ? document.documentElement.dataset.shellImage : undefined } };
    void api.show(request).catch(() => { if (token.current === currentToken && generation.current === current) callbacks.current.onDismiss(); });
  }, [api, active, modelKey, boundsKey, locale, appearanceRevision, focus, appearanceElement]);
  return active;
}
