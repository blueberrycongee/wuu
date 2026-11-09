import { createContext, useContext } from "react";
import type { ApplicationWorkbenchV1 } from "../../../../packages/plugin-sdk/src/ui-contributions";

const Workbench = createContext<ApplicationWorkbenchV1 | undefined>(undefined);
export const ApplicationWorkbenchProvider = Workbench.Provider;
export function useOptionalApplicationWorkbench(): ApplicationWorkbenchV1 | undefined { return useContext(Workbench); }
export function useApplicationWorkbench(explicit?: ApplicationWorkbenchV1): ApplicationWorkbenchV1 {
  const provided = useContext(Workbench);
  const workbench = explicit ?? provided;
  if (!workbench) throw new Error("No application workbench provider is selected");
  return workbench;
}
