import { useMemo, type ReactNode } from "react";

import type { ThreadItem } from "../../shared/protocol";
import { useI18n } from "../i18n";
import { toolDisplayLabel } from "../ToolActivityHelpers";
import type {
  ConversationProcessItemV1,
  ConversationProcessKindV1,
  ConversationProcessSnapshotV1,
  ConversationProcessStatusV1,
} from "../../shared/workbench";
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

export function toConversationProcessSnapshot(
  processItems: readonly ThreadItem[],
  streaming: boolean,
  active: boolean,
  locale?: string,
): ConversationProcessSnapshotV1 | undefined {
  const items = processItems.flatMap((item): ConversationProcessItemV1[] => {
    if (item.type === "reasoning") {
      return [Object.freeze({
        id: item.id,
        kind: "reasoning" as const,
        status: publicProcessStatus(item),
        text: item.text,
        error: item.error,
      })];
    }
    if (item.type === "tool_call") {
      return [Object.freeze({
        id: item.id,
        kind: "tool-activity" as const,
        status: publicProcessStatus(item),
        toolName: item.name,
        displayLabel: toolDisplayLabel(item.display, locale),
        capability: item.display?.capability,
        toolKind: item.display?.kind,
        error: item.error,
      })];
    }
    return [];
  });
  if (items.length === 0) return undefined;

  const frozenItems = Object.freeze(items);
  const kind = processKind(frozenItems);
  const status = frozenItems.some((item) => item.status === "failed")
    ? "failed"
    : streaming || frozenItems.some((item) => item.status === "running")
      ? "running"
      : "completed";
  return Object.freeze({
    contractVersion: 1 as const,
    kind,
    status,
    streaming,
    active,
    items: frozenItems,
  });
}

function publicProcessStatus(item: ThreadItem): ConversationProcessStatusV1 {
  if (item.status === "in_progress") return "running";
  if (item.status === "failed") return "failed";
  return "completed";
}

function processKind(items: readonly ConversationProcessItemV1[]): ConversationProcessKindV1 {
  const hasReasoning = items.some((item) => item.kind === "reasoning");
  const hasTools = items.some((item) => item.kind === "tool-activity");
  if (hasReasoning && hasTools) return "mixed";
  return hasReasoning ? "reasoning" : "tool-group";
}
