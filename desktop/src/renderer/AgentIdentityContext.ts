import { createContext } from "react";
import type { NamedAgent } from "../shared/protocol";

/** The owning agent's appearance for nested conversation process surfaces. */
export const AgentIdentityContext = createContext<NamedAgent | undefined>(undefined);
