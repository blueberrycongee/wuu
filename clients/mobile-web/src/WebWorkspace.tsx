import { useLayoutEffect } from "react";
import { App } from "../../../desktop/src/renderer/App";
import { I18nProvider } from "../../../desktop/src/renderer/i18n";
import { applyMessageFlowFontSize } from "../../../desktop/src/renderer/MessageFlowFontSizeSection";
import { applyPlatformStamp } from "../../../desktop/src/renderer/platform";
import { startRendererVisibilitySync } from "../../../desktop/src/renderer/RendererVisibility";
import {
  applyMeasuredScrollbarWidth,
  startScrollbarWidthSync,
} from "../../../desktop/src/renderer/ScrollbarMetrics";
import {
  applyThemePreference,
  startThemePreferenceSync,
} from "../../../desktop/src/renderer/Theme";
import { ToastViewport } from "../../../desktop/src/renderer/Toast";
import { WuuUIRoot } from "../../../desktop/src/renderer/ui/layers/UILayerHost";
import { startWebViewportSync } from "./lib/viewport";
import "../../../desktop/src/renderer/styles.css";
import "./workbench.css";

applyPlatformStamp();
applyMeasuredScrollbarWidth();
startScrollbarWidthSync();

export default function WebWorkspace(): React.JSX.Element {
  useLayoutEffect(() => {
    applyThemePreference(window.wuu.initialThemePreference ?? "system");
    applyMessageFlowFontSize(window.wuu.initialMessageFlowFontSize ?? 16);
    const stopTheme = startThemePreferenceSync();
    const stopVisibility = startRendererVisibilitySync();
    const stopViewport = startWebViewportSync();
    return () => {
      stopTheme();
      stopVisibility();
      stopViewport();
    };
  }, []);
  return (
    <I18nProvider>
      <WuuUIRoot>
        <App />
        <ToastViewport />
      </WuuUIRoot>
    </I18nProvider>
  );
}
