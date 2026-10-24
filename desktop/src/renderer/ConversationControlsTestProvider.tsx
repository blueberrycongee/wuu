import type { ReactNode } from "react";
import type { ApplicationServiceV1, UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import { createPreviewApplication } from "../../../packages/plugin-sdk/preview/application";
import { ConversationControlsProvider } from "./ConversationControlsContext";

/** Explicit public authorities for leaf tests; unsupported actions reject in the preview host. */
export function createConversationTestApplication(): ApplicationServiceV1 {
  return createPreviewApplication();
}
const application = createConversationTestApplication();
const slots: UICompositionV1 = { Slot: ({ fallback }) => fallback ?? null };
export function ConversationControlsTestProvider({ children, service = application, composition = slots }: {
  children: ReactNode; service?: ApplicationServiceV1; composition?: UICompositionV1;
}): JSX.Element {
  return <ConversationControlsProvider application={service} slots={composition}>{children}</ConversationControlsProvider>;
}
