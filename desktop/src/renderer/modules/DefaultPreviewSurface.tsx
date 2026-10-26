import { registerPreviewContributions } from "../plugins/DefaultUIContributions";
import { useEffect } from "react";
import { registerConversationSlots } from "../ConversationSlots";
import { showApplicationActionError } from "../ApplicationActionError";
import type { ApplicationPresentationAPI, UISlotRenderProps } from "../../../../packages/plugin-sdk/src/ui";
import { WorkspaceRightPanel, type WorkspacePanelLayout } from "../WorkspacePanels";

/** The selected preview owns its views and services; the shell supplies geometry only. */
export function DefaultPreviewSurface({ application, slots, context, signal }: UISlotRenderProps & { signal: AbortSignal }): JSX.Element {
  const layout = context.layout as WorkspacePanelLayout;
  useEffect(() => {
    const report = (open: boolean) => {
      if (signal.aborted) return;
      void application.invoke("preview.set-visibility", { open }).catch(error => {
        if (!signal.aborted) showApplicationActionError(error);
      });
    };
    report(layout.open);
    return () => report(false);
  }, [application, signal, layout.open]);
  return <WorkspaceRightPanel {...layout}
    application={application} slots={slots} />;
}

export function activatePresentation(api: ApplicationPresentationAPI): void {
  registerConversationSlots(api);
  api.registerUISlot({ id: "workspace.header", cardinality: "many" });
  api.registerUISlot({ id: "header.workspace", cardinality: "one" });
  api.registerUISlot({ id: "workspace.tools", cardinality: "many" });
  api.registerUISlot({ id: "workspace.view", cardinality: "one" });
  api.registerUISlot({ id: "content.preview", cardinality: "one" });
  registerPreviewContributions(api);
  api.registerUISlot({ id: "application.composer", cardinality: "one", service: "ui.composer" });
  api.registerUIContribution({ id: "ui.preview", target: "wuu.default-shell/application.preview", render: props => <DefaultPreviewSurface {...props} signal={api.signal} /> });
}

// The legacy entry remains available to standalone authoring hosts.
export const activate = activatePresentation;
