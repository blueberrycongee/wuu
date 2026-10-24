import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";

const DesktopControlsContext = createContext<ApplicationDesktopControlsV1 | undefined>(undefined);

export function DesktopControlsProvider({ controls, children }: { controls: ApplicationDesktopControlsV1; children: ReactNode }): React.JSX.Element {
  return <DesktopControlsContext.Provider value={controls}>{children}</DesktopControlsContext.Provider>;
}

export function useDesktopControls(): ApplicationDesktopControlsV1 {
  const controls = useContext(DesktopControlsContext);
  if (!controls) throw new Error("Desktop controls provider is missing");
  return controls;
}
