import { createContext, useContext } from "react";

// Defaults to connected so desktop surfaces need no provider. Remote shells
// provide their live `ready` snapshot; consumers gate sends without readOnly.
export const WorkbenchConnectionContext = createContext(true);

export function useWorkbenchConnected(): boolean {
  return useContext(WorkbenchConnectionContext);
}
