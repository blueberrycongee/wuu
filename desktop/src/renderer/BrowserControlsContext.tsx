import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationBrowserV1 } from "../../../packages/plugin-sdk/src/ui-browser";

const BrowserControlsContext = createContext<ApplicationBrowserV1 | undefined>(undefined);
export function BrowserControlsProvider({ service, children }: { service: ApplicationBrowserV1; children: ReactNode }): JSX.Element {
  return <BrowserControlsContext.Provider value={service}>{children}</BrowserControlsContext.Provider>;
}
export function useBrowserControls(): ApplicationBrowserV1 | undefined { return useContext(BrowserControlsContext); }
