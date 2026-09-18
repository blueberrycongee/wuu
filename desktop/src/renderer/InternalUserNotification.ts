export const PROCESS_NOTIFICATION_NAME = "wuu_process_notification";
export const AGENT_NOTIFICATION_NAME = "wuu_agent_notification";

type InternalUserNotificationItem = {
  name?: string;
  text?: string;
  origin?: string;
  presentation_kind?: string;
};

export function isProcessNotificationText(text: string | undefined): boolean {
  const trimmed = text?.trim();
  return Boolean(
    trimmed?.startsWith("<process_notification>") &&
      trimmed.endsWith("</process_notification>"),
  );
}

export function isProcessNotificationItem(
  item: InternalUserNotificationItem | undefined,
): boolean {
  if (!item) {
    return false;
  }
  if (item.origin === "plugin" && item.presentation_kind === "session_message") return false;
  if (item.name === PROCESS_NOTIFICATION_NAME) {
    return true;
  }
  return isProcessNotificationText(item.text);
}

export function isAgentNotificationText(text: string | undefined): boolean {
  const trimmed = text?.trim();
  if (!trimmed) {
    return false;
  }
  if (
    trimmed.startsWith("<subagent_notification>") &&
    trimmed.endsWith("</subagent_notification>")
  ) {
    return true;
  }
  try {
    const envelope = JSON.parse(trimmed) as unknown;
    if (!envelope || typeof envelope !== "object" || Array.isArray(envelope)) {
      return false;
    }
    const value = envelope as Record<string, unknown>;
    if (
      typeof value.content === "string" &&
      isAgentNotificationText(value.content)
    ) {
      return true;
    }
    return isAgentPath(value.author) && isAgentPath(value.recipient);
  } catch {
    return false;
  }
}

export function isAgentNotificationItem(
  item: InternalUserNotificationItem | undefined,
): boolean {
  if (!item) {
    return false;
  }
  if (item.origin === "plugin" && item.presentation_kind === "session_message") return false;
  if (item.name === AGENT_NOTIFICATION_NAME) {
    return true;
  }
  return isAgentNotificationText(item.text);
}

export function isInternalUserNotificationItem(
  item: InternalUserNotificationItem | undefined,
): boolean {
  return isProcessNotificationItem(item) || isAgentNotificationItem(item);
}

function isAgentPath(value: unknown): boolean {
  return (
    typeof value === "string" &&
    (value.trim() === "/root" || value.trim().startsWith("/root/"))
  );
}
