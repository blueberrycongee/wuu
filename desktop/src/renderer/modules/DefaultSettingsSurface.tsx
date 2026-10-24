import { useSyncExternalStore } from "react";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import { SettingsShellRenderer, type SettingsShellRendererProps } from "../ConversationShellRenderers";
import { useSettingsRuntimeState } from "../SettingsRuntimeState";
import type { SettingsPage } from "../SettingsView";
import { showErrorToast } from "../Toast";

// Provider configuration and archive operations remain supplied by their
// existing owners until their semantic application contracts are migrated.
type DefaultSettingsSurfaceProps = Omit<SettingsShellRendererProps,
  "page" | "onPageChange" | "onBack" | "usage" | "usageLoading" | "usageError"
  | "codexPets" | "codexPetsLoading" | "codexPetsError" | "onCodexPetsRefresh" | "onCodexPetsUpdate"
> & { application: ApplicationServiceV1 };

export function DefaultSettingsSurface({ application, ...props }: DefaultSettingsSurfaceProps): JSX.Element | null {
  const settings = useSyncExternalStore(application.subscribe, () => application.getSnapshot().settings);
  const runtime = useSettingsRuntimeState({ settingsOpen: settings.open });
  if (!settings.open) return null;
  return <div className={props.embedded ? "settings-workbench-layer" : "settings-standalone-layer"}>
    <SettingsShellRenderer
      {...props}
      page={(settings.activePageId ?? "providers") as SettingsPage}
      onPageChange={pageId => { void application.invoke("settings.open", { pageId }).catch(showErrorToast); }}
      onBack={() => { void application.invoke("settings.close", {}).catch(showErrorToast); }}
      usage={runtime.settingsUsage}
      usageLoading={runtime.settingsUsageLoading}
      usageError={runtime.settingsUsageError}
      codexPets={runtime.codexPets}
      codexPetsLoading={runtime.codexPetsLoading}
      codexPetsError={runtime.codexPetsError}
      onCodexPetsRefresh={runtime.refreshCodexPets}
      onCodexPetsUpdate={runtime.updateCodexPets}
    />
  </div>;
}
