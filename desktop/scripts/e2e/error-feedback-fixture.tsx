// Production components with synthetic, in-memory failures. No account or network.
import React from "react";
import { SelectedSurfaceScene, selectedSurfaceEvidence } from "./selected-surface-fixture";
import { createRoot } from "react-dom/client";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { I18nProvider } from "../../src/renderer/i18n";
import { ToastViewport, ScopedToast, showActionErrorToast, showToast, clearToasts } from "../../src/renderer/Toast";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { ChatGPTConnectionDialog } from "../../src/renderer/ChatGPTConnectionDialog";
import { SettingsControlsProvider } from "../../src/renderer/SettingsControlsContext";
import { createApplicationSettingsControls } from "../../src/renderer/plugins/hostApplicationSettingsControls";
import { SettingsRemotePageContainer } from "../../src/renderer/SettingsRemotePageContainer";
import { DraftRecoveryNotice } from "../../src/renderer/DraftRecoveryNotice";
import { RichContent } from "../../src/renderer/RichContent";
import { BrowserControlsProvider } from "../../src/renderer/BrowserControlsContext";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import "../../src/renderer/styles.css";

let recoveryOpened = 0;
const recoveryInputEvents: { type: string; trusted: boolean; x: number; y: number; at: number }[] = [];
for (const type of ["mousedown", "mouseup", "click"]) document.addEventListener(type, event => {
  if (!(event.target instanceof Element) || !event.target.closest('[data-wuu-component="draft-recovery"] button')) return;
  const pointer = event as MouseEvent;
  recoveryInputEvents.push({ type, trusted: event.isTrusted, x: pointer.clientX, y: pointer.clientY, at: performance.now() });
}, true);
const fixture = window as any;
let reads = 0;
let remoteReads = 0;
let remoteFail = true;
let remoteChanged = () => {};
const remoteSnapshot = { host_running: true, host_enabled: true, pair_uri: null,
  status: { fingerprint: "fixture", host_name: "Synthetic host", store: "", devices: [
    { pub: "synthetic-phone", fingerprint: "synthetic-phone", name: "Retained phone", added_at: "2026-10-01T00:00:00Z" },
  ] } };

fixture.wuu = {
  initialLanguagePreference: "en-US",
  getRemoteControlSnapshot: async () => {
    remoteReads++;
    if (remoteFail) throw new Error("Synthetic remote state read failed");
    return remoteSnapshot;
  },
  onRemoteControlEvent: (listener: () => void) => { remoteChanged = listener; return () => { remoteChanged = () => {}; }; },
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
  selectedSurfaceEvidence,
  reads: () => reads,
  remoteReads: () => remoteReads,
  recoveryOpened: () => recoveryOpened,
  recoveryInputEvents: () => recoveryInputEvents,
  remoteFailure: (fail: boolean, notify = false) => { remoteFail = fail; if (notify) remoteChanged(); },
  applyAppearance: (theme: string, font: number) => {
    document.documentElement.dataset.theme = theme;
    applyMessageFlowFontSize(font);
  },
  notice: () => {
    clearToasts();
    showToast({ message: longError, tone: "error", action: { label: "Try again", onClick: () => {} } });
  },
  shortNotice: () => showToast({ message: "Saved", tone: "info" }),
  actionFailure: () => showActionErrorToast("Synthetic repeated action failure"),
  render: (scene: string) => {
    clearToasts();
    if (scene === "owned-notices") showToast({ message: "Global feedback first" });
    root.render(<I18nProvider><WuuUIRoot><div className="settings-scroll" style={{ height: "100%" }}><main style={{ padding: 32, width: "100%", maxWidth: 900, margin: "0 auto", flexShrink: 0 }}>
      <p>Synthetic error feedback acceptance · no live account</p>
      {scene === "owned-notices" ? <>
        {/* The recovery row is production UI; this editable field only establishes the fixture's surrounding geometry. */}
        <div className="composer-stack" style={{ marginTop: 80 }}>
          <DraftRecoveryNotice interrupted={false} onOpen={() => { recoveryOpened++; }} />
          <textarea data-testid="retained-draft" className="settings-input" defaultValue="My newer draft remains editable" aria-label="Synthetic retained draft" />
        </div>
        <ScopedToast message="Composer feedback second" tone="error" dedupeKey="fixture-composer" />
        <ScopedToast message="Settings feedback third" dedupeKey="fixture-settings" />
      </> : scene === "selected-surface" ? <SelectedSurfaceScene /> : scene === "remote" ? <SettingsControlsProvider controls={controls}><SettingsRemotePageContainer /></SettingsControlsProvider> : scene === "settings" ? <SettingsControlsProvider controls={controls}>
        <ChatGPTConnectionDialog inline providers={[]} running={false} defaultOn={false} onSave={async () => { throw new Error("The fixture never saves"); }} onClose={() => {}} onConnected={() => {}} />
      </SettingsControlsProvider> : scene === "rich" ? <BrowserControlsProvider service={browser}><ImagePreviewProvider>
        <div id="fixture-prose"><RichContent text="Read [the reference](https://example.invalid/reference) before continuing." /></div>
      </ImagePreviewProvider></BrowserControlsProvider> : null}
    </main></div><ToastViewport /></WuuUIRoot></I18nProvider>);
  },
};
fixture.feedback.render("notice");
