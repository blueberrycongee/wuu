import type { PluginHost } from "./PluginHost";
import type { ApplicationComposerPresentationV1 } from "../../../../packages/plugin-sdk/src/ui-composer-presentation";
import type { ApplicationServiceV1 } from "../../../../packages/plugin-sdk/src/ui";
import type { ApplicationDesktopControlsV1 } from "../../../../packages/plugin-sdk/src/ui-desktop";

/** Internal composition seam; selected UI providers receive only their scoped controls. */
export interface HostApplicationService extends Omit<ApplicationServiceV1, "desktop" | "composerPresentation"> {
  setPreviewVisibility(open: boolean, signal?: AbortSignal): void;
  createComposerPresentationScope(signal: AbortSignal, getPluginHost: () => PluginHost): ApplicationComposerPresentationV1;
  createDesktopScope(signal: AbortSignal): ApplicationDesktopControlsV1;
}
