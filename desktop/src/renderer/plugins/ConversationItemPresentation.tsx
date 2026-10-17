import { useMemo, type ReactNode } from "react";

import type { ThreadItem } from "../../shared/protocol";
import {
  CONVERSATION_ITEM_ACTIONS,
  type AttachmentDescriptorV1,
  type ConversationItemSnapshotV1,
} from "../../shared/workbench";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import type { PluginHost } from "./PluginHost";
import { PluginPresentation } from "./PluginPresentation";
import type { WorkbenchController } from "./Workbench";

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
  host = desktopPluginHost,
  controller = desktopWorkbenchController,
}: ConversationItemPresentationProps): JSX.Element {
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

export function toConversationItemSnapshot(
  item: ThreadItem,
  kind: ConversationItemKindV1,
  displayText = item.text,
): ConversationItemSnapshotV1 {
  const contentType = kind === "notice" ? "plain-text" as const : "markdown" as const;
  const attachments = attachmentDescriptors(item);
  const content = displayText === undefined
    ? undefined
    : Object.freeze([Object.freeze({ type: contentType, text: displayText })]);

  return Object.freeze({
    contractVersion: 1 as const,
    id: item.id,
    kind,
    status: publicStatus(item.status),
    phase:
      item.type === "agent_message"
        ? item.terminal
          ? "final_answer"
          : "commentary"
        : undefined,
    text: displayText,
    contentType,
    content,
    attachments,
    // Context-compaction notices carry the replacement-context body so
    // notice presenters can show what the model now runs on.
    summary: item.summary,
    // Plugin-generated wake messages can hide the real delivered prompt
    // behind a generic query bubble; expose that raw input to presenters.
    inputText: item.input_text,
  });
}

function publicStatus(status: ThreadItem["status"]): ConversationItemSnapshotV1["status"] {
  if (status === "in_progress") return "streaming";
  return status;
}

function attachmentDescriptors(item: ThreadItem): readonly AttachmentDescriptorV1[] | undefined {
  const images = (item.images ?? []).map((image, index) => Object.freeze({
    id: `${item.id}:image:${index}`,
    name: `image-${index + 1}`,
    mimeType: image.media_type,
  }));
  const files = (item.files ?? []).map((file, index) => Object.freeze({
    id: `${item.id}:file:${index}`,
    name: file.filename ?? `attachment-${index + 1}`,
    mimeType: file.media_type,
  }));
  return images.length + files.length === 0 ? undefined : Object.freeze([...images, ...files]);
}
