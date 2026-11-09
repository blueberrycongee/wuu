// Subagent-handoff detection, mirrored from desktop AgentHandoff.ts: inter-
// agent notifications arrive as self-addressed user_message items wrapping a
// <subagent_notification> payload. They are working-transcript machinery and
// must never render as chat bubbles.

const NOTIFICATION_OPEN = "<subagent_notification>";
const NOTIFICATION_CLOSE = "</subagent_notification>";

// The protocol-level name the backend stamps on inter-agent self-addressed
// user_message items. Mirrors the desktop AGENT_NOTIFICATION_NAME and the
// `name == "wuu_agent_notification"` branch of `IsAgentNotification` in
// internal/context/context.go: this is the one reliable wire signal, and
// we use it as the primary gate before falling back to text sniffing.
export const AGENT_NOTIFICATION_NAME = "wuu_agent_notification";

type HandoffItem = {
  name?: string;
  text?: string;
};

export function isAgentHandoffText(text: string | undefined): boolean {
  const trimmed = text?.trim();
  if (!trimmed) return false;
  if (isNotificationPayload(trimmed)) return true;
  const envelope = parseJSON<{ content?: unknown }>(trimmed);
  if (!envelope || typeof envelope.content !== "string") return false;
  return isNotificationPayload(envelope.content.trim());
}

// Item-aware gate: the `name` field is the wire signal the backend stamps
// on every agent self-addressed user_message (single or combined envelope).
// A name match is enough to classify the item as a handoff regardless of
// payload parseability — text sniffing breaks on `\n\n`-joined envelopes
// (combineAgentCompletionMessages) and on <changed_file_overlap> tails
// (AgentCompletionChatMessage's overlap warning).
export function isAgentHandoffItem(item: HandoffItem | undefined): boolean {
  if (!item) return false;
  if (item.name === AGENT_NOTIFICATION_NAME) return true;
  return isAgentHandoffText(item.text);
}

function isNotificationPayload(content: string): boolean {
  if (!content.startsWith(NOTIFICATION_OPEN) || !content.endsWith(NOTIFICATION_CLOSE)) {
    return false;
  }
  const raw = content.slice(NOTIFICATION_OPEN.length, content.length - NOTIFICATION_CLOSE.length).trim();
  // Truthiness mirrors the desktop's `payload ? ... : undefined`: a parse
  // producing null/false/0 is not a handoff payload.
  return Boolean(parseJSON<unknown>(raw));
}

function parseJSON<T>(text: string): T | undefined {
  try {
    return JSON.parse(text) as T;
  } catch {
    return undefined;
  }
}
