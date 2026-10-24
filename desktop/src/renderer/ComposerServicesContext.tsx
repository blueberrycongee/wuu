import { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
import type { ApplicationComposerPresentationV1, ApplicationComposerPresentationViewV1 } from "../../../packages/plugin-sdk/src/ui-composer-presentation";
import type { ApplicationCatalogV1 } from "../../../packages/plugin-sdk/src/ui-catalog";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { connectionDisplayPreferences, connectionDisplaySnapshot, subscribeConnectionDisplay } from "./ConnectionDisplayPreferences";

export interface ComposerServices {
  ownerTabId: string;
  presentation: ApplicationComposerPresentationV1;
  view: ApplicationComposerPresentationViewV1;
  catalog: ApplicationCatalogV1;
  settingsControls: ApplicationSettingsControlsV1;
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
