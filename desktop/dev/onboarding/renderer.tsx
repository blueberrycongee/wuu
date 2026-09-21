import { createRoot } from "react-dom/client";
import { FirstRunOnboarding } from "../../src/renderer/FirstRunOnboarding";
import { WuuMascotRuntimeProvider } from "../../src/renderer/WuuMascot";
import { I18nProvider } from "../../src/renderer/i18n";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { applyPlatformStamp } from "../../src/renderer/platform";
import type { EngineListResult } from "../../src/shared/protocol";
import "../../src/renderer/styles.css";

async function rejectPersistence(): Promise<never> {
  throw new Error("Onboarding preview must not persist changes");
}

const availableEngines = new Set(new URLSearchParams(window.location.search).getAll("availableEngine"));
const engines: EngineListResult = {
  engines: ["wuu", "codex", "claude"].map((id) => ({
    id,
    enabled: id === "wuu" || availableEngines.has(id),
    binary_ok: id === "wuu" || availableEngines.has(id),
  })),
};

applyPlatformStamp();
startFocusModality();
createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuMascotRuntimeProvider>
      <FirstRunOnboarding
        preview
        engines={engines}
        onDismissPreview={() => window.close()}
        onUpdateExtensionPackage={rejectPersistence}
        onSaveProvider={rejectPersistence}
        onUpdateEngines={rejectPersistence}
        onComplete={rejectPersistence}
      />
    </WuuMascotRuntimeProvider>
  </I18nProvider>,
);
