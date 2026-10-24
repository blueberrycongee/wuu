import * as React from "react";
import { vi } from "vitest";
import * as applicationState from "../plugins/ApplicationController";
import { PluginHost } from "../plugins/PluginHost";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate as activateShell } from "../plugins/DefaultApplicationShell";
import { activate as activateNavigation } from "../modules/DefaultNavigationSurface";
import { activate as activatePreview } from "../modules/DefaultPreviewSurface";
import { activate as activateSettings } from "../modules/DefaultSettingsSurface";

export const defaultSurfaceBindings = {
  "ui.preview": { pluginId: "wuu.default-preview", generation: "fixture", id: "ui.preview" },
  "ui.navigation": { pluginId: "wuu.default-navigation", generation: "fixture", id: "ui.navigation" },
  "ui.settings": { pluginId: "wuu.default-settings", generation: "fixture", id: "ui.settings" },
};

export async function registerDefaultTestSurfaces(host: PluginHost): Promise<void> {
  // jsdom lacks the browser signal composition used by generation-scoped calls.
  if (!AbortSignal.any) vi.stubGlobal("AbortSignal", class extends AbortSignal {
    static any(signals: AbortSignal[]): AbortSignal {
      const controller = new AbortController();
      const abort = (): void => {
        const source = signals.find(signal => signal.aborted);
        if (!source) return;
        controller.abort(source.reason);
        for (const signal of signals) signal.removeEventListener("abort", abort);
      };
      for (const signal of signals) signal.addEventListener("abort", abort, { once: true });
      abort();
      return controller.signal;
    }
  });
  await host.activateGeneration({ pluginId: "wuu.default-navigation", generation: "fixture", register: activateNavigation });
  await host.activateGeneration({ pluginId: "wuu.default-preview", generation: "fixture", register: activatePreview });
  await host.activateGeneration({ pluginId: "wuu.default-settings", generation: "fixture", register: activateSettings });
}

/** Behavior suites mount the shipped shell and independently selected providers. */
export async function createDefaultApplicationTestView(
  application = new applicationState.ApplicationController(),
): Promise<React.ReactElement> {
  document.title = document.title || "Wuu";
  vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(application);
  const host = new PluginHost({ react: React, application });
  await registerDefaultTestSurfaces(host);
  await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "fixture", register: activateShell });
  function View(): React.ReactNode {
    React.useEffect(() => () => {
      host.unload("wuu.default-shell");
      host.unload("wuu.default-settings");
      host.unload("wuu.default-navigation");
      host.unload("wuu.default-preview");
    }, []);
    return <ApplicationShellOutlet host={host}
      binding={{ pluginId: "wuu.default-shell", generation: "fixture", id: "ui.shell" }}
      surfaceBindings={defaultSurfaceBindings} />;
  }
  return <View />;
}
