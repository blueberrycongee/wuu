import { ArtifactsProvider } from "../ArtifactsContext";
import { WebWorkspaceControlsProvider } from "../WebWorkspaceControlsContext";
import { MacWindowControls } from "../MacWindowControls";
import { LinuxWindowControls } from "../LinuxWindowControls";
import { DesktopControlsProvider } from "../DesktopControlsContext";
import { BrowserControlsProvider } from "../BrowserControlsContext";
import { TerminalControlsProvider } from "../TerminalControlsContext";
import type { ApplicationGenerationAPI, ApplicationShellRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import { PreviewContentProvider } from "../PreviewContentContext";
import { App } from "../App";
import { AppBackground } from "../background/AppBackground";
import { AppContextCaptureHost } from "../AppContextCapture";
import { ENABLE_APP_SNAPSHOTS } from "../../shared/featureFlags";

/** Shipped module uses the same registration, selection and unload path as an external shell. */
export function activate(api: ApplicationGenerationAPI): void {
  api.registerUISlot({ id: "application.before", cardinality: "many" });
  api.registerUISlot({ id: "application.after", cardinality: "many" });
  api.registerUISlot({ id: "workbench.layers", cardinality: "many" });
  api.registerUISlot({ id: "inspector.sections", cardinality: "many" });
  api.registerUISlot({ id: "header.conversation", cardinality: "one" });
  api.registerUISlot({ id: "header.workspace", cardinality: "one" });
  api.registerUISlot({ id: "conversation.header", cardinality: "many" });
  api.registerUISlot({ id: "workspace.header", cardinality: "many" });
  api.registerUISlot({ id: "application.navigation", cardinality: "one", service: "ui.navigation" });
  api.registerUISlot({ id: "application.composer", cardinality: "one", service: "ui.composer" });
  api.registerUISlot({ id: "application.conversation", cardinality: "one", service: "ui.conversation" });
  api.registerUISlot({ id: "application.preview", cardinality: "one", service: "ui.preview" });
  api.registerUISlot({ id: "application.settings", cardinality: "one", service: "ui.settings" });
  function DefaultShell({ application, slots }: ApplicationShellRenderProps): JSX.Element {
    return <DesktopControlsProvider controls={application.desktop}>
      <WebWorkspaceControlsProvider controls={application.webWorkspace}>
      <BrowserControlsProvider service={application.browser}>
      <TerminalControlsProvider controls={application.terminals} appearance={application.settingsControls}>
      <ArtifactsProvider application={application}>
      <AppBackground />
      <slots.Slot name={`${api.manifestId}/application.before`} />
      <PreviewContentProvider service={application.previewContent} slots={slots}>
        <App application={application} slots={slots} shellPluginId={api.manifestId} />
        <LinuxWindowControls desktop={application.desktop} />
        <MacWindowControls desktop={application.desktop} />
      </PreviewContentProvider>
      <slots.Slot name={`${api.manifestId}/application.after`} />
      {ENABLE_APP_SNAPSHOTS && <AppContextCaptureHost application={application} />}
      </ArtifactsProvider>
      </TerminalControlsProvider>
      </BrowserControlsProvider>
      </WebWorkspaceControlsProvider>
    </DesktopControlsProvider>;
  }
  api.registerApplicationShell({ id: "ui.shell", render: DefaultShell });
}
