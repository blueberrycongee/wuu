import { toConversationProcessSnapshot } from "./ApplicationSnapshots";
export { toConversationProcessSnapshot } from "./ApplicationSnapshots";
import { useMemo, type ReactNode } from "react";

import type { ThreadItem } from "../../shared/protocol";
import { useI18n } from "../i18n";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import type { PluginHost } from "./PluginHost";
import { PluginPresentation } from "./PluginPresentation";
import type { WorkbenchController } from "./Workbench";

export interface ConversationProcessPresentationProps {
  processItems: readonly ThreadItem[];
  streaming: boolean;
  active?: boolean;
  fallback: ReactNode;
  host?: PluginHost;
  controller?: WorkbenchController;
}

/** Production boundary between private process records and public presenters. */
export function ConversationProcessPresentation({
  processItems,
  streaming,
  active,
  fallback,
  host = desktopPluginHost,
  controller = desktopWorkbenchController,
}: ConversationProcessPresentationProps): JSX.Element {
  const { locale } = useI18n();
  const snapshot = useMemo(
    () => toConversationProcessSnapshot(processItems, streaming, active ?? streaming, locale),
    [active, processItems, streaming, locale],
  );

  return (
    <PluginPresentation
      enabled={snapshot !== undefined}
      host={host}
      controller={controller}
      target="conversation.process"
      presentationKey={snapshot?.kind}
      snapshot={snapshot ?? EMPTY_PROCESS_SNAPSHOT}
      fallback={fallback}
    />
  );
}

const EMPTY_PROCESS_SNAPSHOT = Object.freeze({});
