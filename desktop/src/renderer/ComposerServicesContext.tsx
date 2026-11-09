import { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
import type { ApplicationComposerPresentationV1, ApplicationComposerPresentationViewV1 } from "../../../packages/plugin-sdk/src/ui-composer-presentation";
import type { UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationCatalogV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { connectionDisplayPreferences, connectionDisplaySnapshot, subscribeConnectionDisplay } from "./ConnectionDisplayPreferences";

export interface ComposerServices {
  slots: UICompositionV1;
  ownerTabId: string;
  presentation: ApplicationComposerPresentationV1;
  view: ApplicationComposerPresentationViewV1;
  catalog: ApplicationCatalogV1;
  settingsControls: ApplicationSettingsControlsV1;
  conversationPluginsSupported?: boolean;
  openPlugins?: (pluginId?: string) => Promise<void>;
}
const Context = createContext<ComposerServices | undefined>(undefined);
export function ComposerServicesProvider({ value, children }: { value: ComposerServices; children: ReactNode }): JSX.Element {
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useComposerServices(): ComposerServices | undefined { return useContext(Context); }
export function useComposerConnectionDisplay() {
  const services = useComposerServices();
  const controls = services?.settingsControls.connectionDisplay;
  useSyncExternalStore(controls?.subscribe ?? subscribeConnectionDisplay, controls?.getSnapshot ?? connectionDisplaySnapshot, () => "{}");
  return controls ?? connectionDisplayPreferences;
}

/** Connection discovery can be scoped without inheriting a Coding draft or its completions. */
export type ComposerPluginServices = Pick<ComposerServices, "catalog" | "conversationPluginsSupported" | "openPlugins"> & {
  settingsControls: Pick<ApplicationSettingsControlsV1, "listMCPServers">;
  includePluginTools?: boolean;
};
const PluginContext = createContext<ComposerPluginServices | undefined>(undefined);
export function ComposerPluginServicesProvider({ value, children }: { value: ComposerPluginServices; children: ReactNode }): JSX.Element {
  return <PluginContext.Provider value={value}>{children}</PluginContext.Provider>;
}
export function useComposerPluginServices(): ComposerPluginServices | undefined {
  const independent = useContext(PluginContext);
  const composer = useComposerServices();
  return independent ?? composer;
}
