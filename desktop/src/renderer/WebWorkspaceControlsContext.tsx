import { createContext, useContext, type ReactNode } from "react";
import type { ApplicationWebWorkspaceV1 } from "../../../packages/plugin-sdk/src/ui-web-workspace";
const WebWorkspaceControlsContext=createContext<ApplicationWebWorkspaceV1|null>(null);
export function WebWorkspaceControlsProvider({controls,children}:{controls:ApplicationWebWorkspaceV1;children:ReactNode}):JSX.Element {
  return <WebWorkspaceControlsContext.Provider value={controls}>{children}</WebWorkspaceControlsContext.Provider>;
}
/** Generic tooltips can render outside a browser surface; they never reach a private host. */
export function useOptionalWebWorkspaceControls():ApplicationWebWorkspaceV1|null {return useContext(WebWorkspaceControlsContext);}
export function useWebWorkspaceControls():ApplicationWebWorkspaceV1 {
  const controls=useOptionalWebWorkspaceControls();if(!controls)throw new Error("Web workspace requires the selected application service");return controls;
}
