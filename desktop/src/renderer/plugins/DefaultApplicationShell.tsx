import type { ApplicationShellRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import { App } from "../App";
import { AppBackground } from "../background/AppBackground";
import { AppContextCaptureHost } from "../AppContextCapture";
import { ENABLE_APP_SNAPSHOTS } from "../../shared/featureFlags";
import type { PluginGenerationApi } from "./PluginHost";

/** Shipped module uses the same registration, selection and unload path as an external shell. */
export function activate(api: PluginGenerationApi): void {
  api.registerUISlot({ id: "application.before", cardinality: "many" });
  api.registerUISlot({ id: "application.after", cardinality: "many" });
  function DefaultShell({ application, slots }: ApplicationShellRenderProps): JSX.Element {
    return <>
      <AppBackground />
      <slots.Slot name={`${api.pluginId}/application.before`} />
      <App application={application} />
      <slots.Slot name={`${api.pluginId}/application.after`} />
      {ENABLE_APP_SNAPSHOTS && <AppContextCaptureHost />}
    </>;
  }
  api.registerApplicationShell({ id: "ui.shell", render: DefaultShell });
}
