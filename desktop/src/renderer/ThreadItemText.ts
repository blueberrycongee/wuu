import type { MessageContentPart, ThreadItem } from "../shared/protocol";
import {
  streamTextKey,
  streamTextStore,
  type StreamTextField,
} from "./StreamText";

export function streamFieldValue(
  turnID: string,
  item: ThreadItem,
  field: StreamTextField,
): string {
  const key = streamTextKey(turnID, item.id, field);
  return streamTextStore.has(key)
    ? streamTextStore.get(key)
    : (item[field] ?? "");
}

/** Text shown in the user-message body; structured references render as attachments. */
export function userMessageBodyText(text: string, contentParts?: MessageContentPart[]): string {
  return contentParts?.length
    ? contentParts.filter((part) => part.type === "text" || part.type === "plugin_reference").map((part) => part.text).join(contentParts.some(part => part.type === "plugin_reference") ? "" : "\n\n")
    : text;
}
