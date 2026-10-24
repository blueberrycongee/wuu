import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";
const SettingsControlsContext = createContext<ApplicationSettingsControlsV1 | null>(null);
export function SettingsControlsProvider({
  controls,
  children
}: {
  controls: ApplicationSettingsControlsV1;
  children: ReactNode;
}): JSX.Element {
  return <SettingsControlsContext.Provider value={controls}>{children}</SettingsControlsContext.Provider>;
}
export function useSettingsControls(): ApplicationSettingsControlsV1 {
  const controls = useContext(SettingsControlsContext);
  if (!controls) throw new Error("Settings controls require the selected application service");
  return controls;
}
