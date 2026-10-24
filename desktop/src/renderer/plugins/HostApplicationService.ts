import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationDesktopControlsV1 } from "../../../../packages/plugin-sdk/src/ui-desktop";

/** Internal composition seam; selected UI providers receive only their scoped controls. */
export interface HostApplicationService extends Omit<ApplicationServiceV1, "desktop"> {
  createDesktopScope(signal: AbortSignal): ApplicationDesktopControlsV1;
}
