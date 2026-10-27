import { useMemo, type ReactNode } from "react";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import { createPreviewApplication } from "../../../packages/plugin-sdk/preview/application";
import { resolvePreviewResourceURL } from "./PreviewResourceURL";
import { ArtifactsProvider } from "./ArtifactsContext";
import { ApplicationArtifacts } from "./plugins/ApplicationArtifacts";
import { ConversationControlsProvider } from "./ConversationControlsContext";

/** Explicit public authorities for leaf tests; unsupported actions reject in the preview host. */
export function createConversationTestApplication(): ApplicationServiceV1 {
  return { ...createPreviewApplication(), artifacts: new ApplicationArtifacts(window.wuu ?? {}, { resolveResourceURL: ({ source, cwd }) => resolvePreviewResourceURL(source, cwd, encoded => `wuu-file://local/${encoded}`), openFile: async () => {} }) };
}
const slots: UICompositionV1 = { Slot: ({ fallback }) => fallback ?? null };
export function ConversationControlsTestProvider({ children, service, composition = slots }: {
  children: ReactNode; service?: ApplicationServiceV1; composition?: UICompositionV1;
}): JSX.Element {
  const application = useMemo(() => service ?? createConversationTestApplication(), [service]);
  return <ConversationControlsProvider application={application} slots={composition} slotOwner="wuu.default-conversation"><ArtifactsProvider application={application}>{children}</ArtifactsProvider></ConversationControlsProvider>;
}
