import * as React from "react";
import { vi } from "vitest";
import * as applicationState from "../plugins/ApplicationController.test-support";
import { PluginHost } from "../plugins/PluginHost";
import { ApplicationShellOutlet } from "../plugins/ApplicationShell";
import { activate as activateShell } from "../plugins/DefaultApplicationShell";
import { activate as activateNavigation } from "../modules/DefaultNavigationSurface";
import { activate as activateComposer } from "../modules/DefaultComposerSurface";
import { activate as activateConversation } from "../modules/DefaultConversationSurface";
import { activate as activatePreview } from "../modules/DefaultPreviewSurface";
import { activate as activateSettings } from "../modules/DefaultSettingsSurface";

export function createDefaultTestHost(application: applicationState.ApplicationController): PluginHost {
  return application.contributions as PluginHost;
}

export const defaultSurfaceBindings = {
  "ui.composer": { pluginId: "wuu.default-composer", generation: "fixture", id: "ui.composer" },
  "ui.conversation": { pluginId: "wuu.default-conversation", generation: "fixture", id: "ui.conversation" },
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
  await host.activateGeneration({ pluginId: "wuu.default-composer", generation: "fixture", register: activateComposer });
  await host.activateGeneration({ pluginId: "wuu.default-conversation", generation: "fixture", register: activateConversation });
  await host.activateGeneration({ pluginId: "wuu.default-preview", generation: "fixture", register: activatePreview });
  await host.activateGeneration({ pluginId: "wuu.default-settings", generation: "fixture", register: activateSettings });
}

/** Mirrors DesktopApplicationRoot: replacing a selected shell cannot stop its host. */
export function TestApplicationShellOutlet(props: React.ComponentProps<typeof ApplicationShellOutlet>): React.ReactElement {
  const [application] = React.useState(applicationState.getDesktopApplication);
  React.useEffect(() => application.start(), [application]);
  return <ApplicationShellOutlet {...props} />;
}

/** Conversation behavior suites explicitly open their starting session. */
export async function createConversationTestView(threadId: string): Promise<React.ReactElement> {
  const application = new applicationState.ApplicationController();
  await application.initialize();
  await application.invoke("thread.activate", { threadId });
  const pending = application.getSnapshot().tabs.pending;
  if (pending?.contentReady && pending.requestId !== undefined) await application.invoke("navigation.reveal-complete", { requestId: pending.requestId });
  return createDefaultApplicationTestView(application);
}

/** Behavior suites mount the shipped shell and independently selected providers. */
export async function createDefaultApplicationTestView(
  application = new applicationState.ApplicationController(),
): Promise<React.ReactElement> {
  document.title = document.title || "Wuu";
  vi.spyOn(applicationState, "getDesktopApplication").mockReturnValue(application);
  const host = createDefaultTestHost(application);
  await registerDefaultTestSurfaces(host);
  await host.activateGeneration({ pluginId: "wuu.default-shell", generation: "fixture", register: activateShell });
  function View(): React.ReactNode {
    React.useEffect(() => () => {
      host.unload("wuu.default-shell");
      host.unload("wuu.default-settings");
      host.unload("wuu.default-navigation");
      host.unload("wuu.default-preview");
      host.unload("wuu.default-conversation");
      host.unload("wuu.default-composer");
    }, []);
    return <TestApplicationShellOutlet host={host}
      binding={{ pluginId: "wuu.default-shell", generation: "fixture", id: "ui.shell" }}
      surfaceBindings={defaultSurfaceBindings} />;
  }
  return <View />;
}
