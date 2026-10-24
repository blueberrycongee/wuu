import type { ApplicationGenerationAPI, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import { WorkspaceRightPanel, type WorkspacePanelLayout } from "../WorkspacePanels";

/** The selected preview owns its views and services; the shell supplies geometry only. */
export function DefaultPreviewSurface({ application, slots, context }: UISlotRenderProps): JSX.Element {
  return <WorkspaceRightPanel {...context.layout as WorkspacePanelLayout}
    application={application} slots={slots} />;
}

export function activate(api: ApplicationGenerationAPI): void {
  api.registerUISlot({ id: "workspace.header", cardinality: "many" });
  api.registerUISlot({ id: "header.workspace", cardinality: "one" });
  api.registerUISlot({ id: "workspace.tools", cardinality: "many" });
  api.registerUISlot({ id: "workspace.view", cardinality: "one" });
  api.registerUISlot({ id: "content.preview", cardinality: "one" });
  api.registerUISlot({ id: "application.composer", cardinality: "one", service: "ui.composer" });
  api.registerUIContribution({ id: "ui.preview", target: "wuu.default-shell/application.preview", render: DefaultPreviewSurface });
}
