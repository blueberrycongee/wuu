import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationTerminalsV1 } from "../../../packages/plugin-sdk/src/ui-terminals";
import type { ApplicationSettingsControlsV1 } from "../../../packages/plugin-sdk/src/ui-settings-controls";

export type TerminalAppearanceControls = Pick<ApplicationSettingsControlsV1, "readAppearance" | "observeAppearance" | "currentAppliedTheme" | "observeAppliedTheme">;
const TerminalControlsContext = createContext<{controls: ApplicationTerminalsV1; appearance: TerminalAppearanceControls} | null>(null);
export function TerminalControlsProvider({controls, appearance, children}: {controls: ApplicationTerminalsV1; appearance: TerminalAppearanceControls; children: ReactNode}): JSX.Element {
  return <TerminalControlsContext.Provider value={{controls,appearance}}>{children}</TerminalControlsContext.Provider>;
}
export function useTerminalControls() {
  const value = useContext(TerminalControlsContext);
  if (!value) throw new Error("Terminal controls require the selected application service");
  return value;
}
