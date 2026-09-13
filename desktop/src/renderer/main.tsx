import ReactDOM from "react-dom/client";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../shared/protocol";
import { AccountScreen } from "./AccountScreen";
import { App } from "./App";
import { startAppearanceSync } from "./AppearancePreferences";
import { applyMessageFlowFontSize } from "./MessageFlowFontSizeSection";
import { LinuxWindowControls, startLinuxTitlebarMaximizeGesture } from "./LinuxWindowControls";
import { applyPlatformStamp } from "./platform";
import { startRendererVisibilitySync } from "./RendererVisibility";
import { applyMeasuredScrollbarWidth, startScrollbarWidthSync } from "./ScrollbarMetrics";
import { applyThemePreference, startThemePreferenceSync } from "./Theme";
import "./styles.css";
import { I18nProvider } from "./i18n";
import { ToastViewport } from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";

// The preload script already stamped data-theme for the first paint;
// re-applying here takes over the "system" media-query subscription for
// the lifetime of the window.
applyThemePreference(window.wuu?.initialThemePreference ?? "system");

// Theme changes made in any window reach every other window through the
// main-process broadcast; keep this window's data-theme in step for the
// rest of its lifetime.
startThemePreferenceSync();
startAppearanceSync();

// Same story for data-platform: the preload stamps it pre-paint; this
// covers boots whose preload was replaced (e2e mocks).
applyPlatformStamp();
startLinuxTitlebarMaximizeGesture();

// Stamp the platform's real scrollbar gutter width before React renders so
// the dock composer and the message flow are centered in the same visible
// content box from the very first paint.
applyMeasuredScrollbarWidth();
// Re-sync on focus for rare mid-session OS scrollbar-mode changes. Not on
// resize: the gutter is constant during a live resize.
startScrollbarWidthSync();

// Pause ambient infinite animations while the native window is hidden or
// minimized. Finite UI transitions remain untouched so their lifecycle events
// can still complete normally.
startRendererVisibilitySync();

// Re-apply the message-stream font size in case the preload stamp was
// dropped (e.g. user unset window.wuu during boot, or the file was
// corrupted). Cheap and idempotent.
applyMessageFlowFontSize(
  window.wuu?.initialMessageFlowFontSize ?? MESSAGE_FLOW_FONT_SIZE_RANGE.default,
);

// Chromium's default for a file dropped anywhere without a drop handler is
// to navigate the window to it, replacing the whole UI. Composers handle
// (and prevent) their own file drops; this keeps stray file drops
// everywhere else inert. Text drags pass through so native textarea
// drag-and-drop keeps working.
const ignoreStrayFileDrop = (event: DragEvent): void => {
  if (event.dataTransfer?.types.includes("Files")) {
    event.preventDefault();
  }
};
window.addEventListener("dragover", ignoreStrayFileDrop);
window.addEventListener("drop", ignoreStrayFileDrop);

const rendererRoot = document.getElementById("root") as HTMLElement;
rendererRoot.dataset.wuuUiRoot = "true";
rendererRoot.dataset.wuuComponent = "ui-root";

const originalConsoleError = console.error;
console.error = (...args: unknown[]): void => {
  originalConsoleError(...args);
  if (args.some((arg) => typeof arg === "string" && arg.includes("Maximum update depth exceeded"))) {
    originalConsoleError("[update-depth-stack]", new Error().stack);
  }
};

ReactDOM.createRoot(rendererRoot).render(
  <I18nProvider>
    <WuuUIRoot>
      <LinuxWindowControls />
      {window.wuu?.isAccountWindow && window.wuu.remoteAccount
        ? <AccountScreen standalone driver={window.wuu.remoteAccount} onBack={() => { void window.wuu.closeAccountWindow?.(); }} />
        : <App />}
      <ToastViewport />
    </WuuUIRoot>
  </I18nProvider>,
);
