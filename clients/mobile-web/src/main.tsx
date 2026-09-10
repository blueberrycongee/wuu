import { createRoot } from "react-dom/client";

import App from "./AccountApp";
import { startNativeLifecycle } from "./lib/native";
import { I18nProvider } from '../../../desktop/src/renderer/i18n';
import { languagePreferenceStore } from './lib/language';
import { startPhoneAppearance } from './lib/appearance';
import { startWebViewportSync } from './lib/viewport';
import "../../../desktop/src/renderer/styles.css";
import "./styles.css";
import "./workbench.css";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
root.dataset.wuuUiRoot = "true";
root.dataset.wuuComponent = "ui-root";
document.documentElement.dataset.hostKind = 'web';
startPhoneAppearance();
startWebViewportSync();

void startNativeLifecycle().then(() => createRoot(root).render(<I18nProvider preferenceStore={languagePreferenceStore}><App /></I18nProvider>)).catch(error => { root.textContent = `手机初始化失败：${String(error)}`; });
