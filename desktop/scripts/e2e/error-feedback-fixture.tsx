// Production components with synthetic, in-memory failures. No account or network.
import React from "react";
import { createRoot } from "react-dom/client";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { ToastViewport, showActionErrorToast, showToast, clearToasts } from "../../src/renderer/Toast";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { ChatGPTConnectionDialog } from "../../src/renderer/ChatGPTConnectionDialog";
import { SettingsControlsProvider } from "../../src/renderer/SettingsControlsContext";
import { createApplicationSettingsControls } from "../../src/renderer/plugins/hostApplicationSettingsControls";
import { RichContent } from "../../src/renderer/RichContent";
import { BrowserControlsProvider } from "../../src/renderer/BrowserControlsContext";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import "../../src/renderer/styles.css";

const fixture = window as any;
let reads = 0;
fixture.wuu = {
  initialLanguagePreference: "en-US",
  listChatGPTAccounts: async () => {
    if (++reads < 3) throw new Error("Synthetic account read failure: " + "diagnostic ".repeat(40));
    return { accounts: [{ id: "synthetic", status: "connected" }] };
  },
  listChatGPTModels: async () => ({ models: [{ id: "synthetic-model", name: "Synthetic model" }] }),
};
const controls = createApplicationSettingsControls();
const browser = { openURL: async () => { throw new Error("Synthetic link operation failed"); } } as any;
const root = createRoot(document.getElementById("root")!);
const longError = "Could not open the file. Check its location and try again. " + "Additional diagnostic context. ".repeat(14) + "reference=" + "0123456789abcdef".repeat(24);
fixture.feedback = {
  reads: () => reads,
  applyAppearance: (theme: string, font: number) => {
    document.documentElement.dataset.theme = theme;
    applyMessageFlowFontSize(font);
  },
  notice: () => {
    clearToasts();
    showToast({ message: longError, tone: "error", action: { label: "Try again", onClick: () => {} } });
  },
  actionFailure: () => showActionErrorToast("Synthetic repeated action failure"),
  render: (scene: string) => {
    clearToasts();
    root.render(<I18nProvider><WuuUIRoot><main style={{ padding: 32, maxWidth: 900, margin: "auto" }}>
      <p>Synthetic error feedback acceptance · no live account</p>
      {scene === "settings" ? <SettingsControlsProvider controls={controls}>
        <ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={async () => { throw new Error("The fixture never saves"); }} onClose={() => {}} onConnected={() => {}} />
      </SettingsControlsProvider> : scene === "rich" ? <BrowserControlsProvider service={browser}><ImagePreviewProvider>
        <div id="fixture-prose"><RichContent text="Read [the reference](https://example.invalid/reference) before continuing." /></div>
      </ImagePreviewProvider></BrowserControlsProvider> : null}
    </main><ToastViewport /></WuuUIRoot></I18nProvider>);
  },
};
fixture.feedback.render("notice");
