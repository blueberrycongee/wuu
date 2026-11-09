import { toConversationItemSnapshot } from "./ApplicationSnapshots";
export { toConversationItemSnapshot } from "./ApplicationSnapshots";
import { useMemo, type ReactNode } from "react";

import type { ThreadItem } from "../../shared/protocol";
import {
  CONVERSATION_ITEM_ACTIONS,
  type ConversationItemSnapshotV1,
} from "../../shared/workbench";
import { useApplicationWorkbench } from "./ApplicationWorkbenchContext";
import type { UIContributionRegistryV1 as PluginHost } from "../../../../packages/plugin-sdk/src/ui-contributions";
import { PluginPresentation } from "./PluginPresentation";
import type { ApplicationWorkbenchV1 as WorkbenchController } from "../../../../packages/plugin-sdk/src/ui-contributions";

type ConversationItemKindV1 = ConversationItemSnapshotV1["kind"];

export interface ConversationItemPresentationProps {
  item: ThreadItem;
  fallback: ReactNode;
  text?: string;
  onEdit?: () => void;
  host?: PluginHost;
  controller?: WorkbenchController;
}

const EDIT_ACTIONS = Object.freeze([CONVERSATION_ITEM_ACTIONS.edit]);
const NO_ACTIONS: readonly string[] = Object.freeze([]);

/** Production boundary between private thread records and public item presenters. */
export function ConversationItemPresentation({
  item,
  fallback,
  text,
  onEdit,
  host: suppliedHost,
  controller: suppliedController,
}: ConversationItemPresentationProps): JSX.Element {
  const controller = useApplicationWorkbench(suppliedController);
  const host = suppliedHost ?? controller.registry;
  const kind = conversationItemKind(item);
  const snapshot = useMemo(
    () => kind === undefined ? undefined : toConversationItemSnapshot(item, kind, text),
    [item, kind, text],
  );

  if (kind === undefined || snapshot === undefined) return <>{fallback}</>;

  const actions = onEdit === undefined ? NO_ACTIONS : EDIT_ACTIONS;
  const dispatchAction = onEdit === undefined
    ? undefined
    : (action: string, input?: unknown): void => {
        if (action !== CONVERSATION_ITEM_ACTIONS.edit) {
          throw new Error(`Unsupported conversation item action: ${action}`);
        }
        if (input !== undefined) {
          throw new TypeError("conversation.item.edit does not accept input");
        }
        onEdit();
      };

  return (
    <PluginPresentation
      host={host}
      controller={controller}
      target="conversation.item"
      presentationKey={kind}
      snapshot={snapshot}
      fallback={fallback}
      actions={actions}
      dispatchAction={dispatchAction}
    />
  );
}

function conversationItemKind(item: ThreadItem): ConversationItemKindV1 | undefined {
  switch (item.type) {
    case "user_message": return "user-message";
    case "agent_message": return "assistant-message";
    case "reasoning": return "reasoning";
    case "context_compaction":
    case "error": return "notice";
    // Tool records have their own conversation.tool-activity presentation boundary.
    case "tool_call":
    default: return undefined;
  }
}
