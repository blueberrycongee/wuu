import { useLayoutEffect, useRef, useState } from "react";
import type { ApplicationDesktopChromeOverlayApiV1 as ChromeOverlayApi, ApplicationDesktopChromeOverlayCommandV1 as ChromeOverlayCommand, ApplicationDesktopChromeOverlayModelV1 as ChromeOverlayModel, ApplicationDesktopChromeOverlayRectV1 as ChromeOverlayRect, ApplicationDesktopChromeOverlayRequestV1 as ChromeOverlayRequest } from "../../../packages/plugin-sdk/src/ui-desktop";
import { APPLICATION_DESKTOP_CHROME_OVERLAY_VERSION as CHROME_OVERLAY_VERSION } from "../../../packages/plugin-sdk/src/ui-desktop";
import { useDesktopControls } from "./DesktopControlsContext";
import { useI18n } from "./i18n";

export function useChromeOverlayApi(): ChromeOverlayApi | undefined {
  return useDesktopControls().chromeOverlay;
}

/** Models contain presentation data; callbacks and account/browser authority stay here. */
export function useChromeOverlay(enabled: boolean, bounds: ChromeOverlayRect | null, model: ChromeOverlayModel,
  onCommand: (command: ChromeOverlayCommand) => void, onDismiss: () => void, focus = false, appearanceElement?: HTMLElement | null): boolean {
  const api = useChromeOverlayApi();
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
      const lifecycle = event.command.type === "dismiss";
      if (lifecycle ? event.generation > generation.current : event.generation !== generation.current) return;
      // Failure is terminal for this opening. Persistent panels may update
      // their collapsed model in onDismiss; that must not retry a broken view.
      if (event.command.type === "dismiss" && event.command.reason === "failed") token.current = undefined;
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
      void api.hide(currentToken, generation.current).catch(() => {});
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
    const current = ++generation.current;
    const request: ChromeOverlayRequest = { version: CHROME_OVERLAY_VERSION, token: currentToken, generation: current,
      bounds, model, focus, appearance: { theme: document.documentElement.dataset.theme === "dark" ? "dark" : "light", locale, fontFamily: computed.fontFamily, variables } };
    void api.show(request).catch(() => { if (token.current === currentToken && generation.current === current) callbacks.current.onDismiss(); });
  }, [api, active, modelKey, boundsKey, locale, appearanceRevision, focus, appearanceElement]);
  return active;
}
