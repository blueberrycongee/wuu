import type {
  MessageContentPart,
  SideThreadMessage,
  ThreadItem,
  Turn,
} from "../shared/protocol";

const SIDE_TURN_ID_PREFIX = "side-turn-";

/**
 * Projects side-thread history onto the canonical conversation model.
 *
 * Side-thread storage is message-oriented, while the shared renderer groups
 * one user prompt and its following assistant reply into a Turn. Keeping this
 * translation pure lets the side panel reuse the normal message flow without
 * introducing a second set of rendering rules.
 */
export function sideThreadMessagesToTurns(
  messages: readonly SideThreadMessage[],
): Turn[] {
  const turns: Turn[] = [];
  let pendingUserTurn: Turn | undefined;

  for (const message of messages) {
    if (message.role === "user") {
      if (pendingUserTurn) {
        pendingUserTurn.status = "completed";
      }

      const turn = userOnlyTurn(message);
      turns.push(turn);
      pendingUserTurn = turn;
      continue;
    }

    if (pendingUserTurn) {
      applyAssistantMessage(pendingUserTurn, message);
      pendingUserTurn = undefined;
      continue;
    }

    turns.push(assistantOnlyTurn(message));
  }

  return turns;
}

function userOnlyTurn(message: SideThreadMessage): Turn {
  return {
    id: turnID(message.id),
    status: "in_progress",
    started_at: message.created_at,
    items_view: "full",
    items: [userItem(message)],
  };
}

function assistantOnlyTurn(message: SideThreadMessage): Turn {
  const turn: Turn = {
    id: turnID(message.id),
    status: assistantTurnStatus(message.status),
    started_at: message.created_at,
    items_view: "full",
    items: assistantItems(message),
  };
  applyAssistantError(turn, message);
  return turn;
}

function applyAssistantMessage(
  turn: Turn,
  message: SideThreadMessage,
): void {
  turn.status = assistantTurnStatus(message.status);
  turn.items.push(...assistantItems(message));
  applyAssistantError(turn, message);
}

function assistantItems(message: SideThreadMessage): ThreadItem[] {
  if (message.items && message.items.length > 0) {
    return message.items.map((item) => ({ ...item }));
  }
  return [assistantItem(message)];
}

function userItem(message: SideThreadMessage): ThreadItem {
  const selection = message.selection;
  const selectionPart: MessageContentPart | undefined = selection?.type === "response"
    ? { type: "response_selection", text: "", selection: selection.response }
    : selection?.type === "file"
      ? { type: "file_selection", id: message.id, text: "", source: selection.file, intent: "quote" }
      : undefined;
  return {
    id: message.id,
    type: "user_message",
    status: "completed",
    role: "user",
    text: message.text,
    ...(selectionPart ? { content_parts: [selectionPart, { type: "text", text: message.text }] } : {}),
  };
}

function assistantItem(message: SideThreadMessage): ThreadItem {
  return {
    id: message.id,
    type: "agent_message",
    status: assistantItemStatus(message.status),
    terminal: true,
    role: "assistant",
    text: message.text,
  };
}

function assistantTurnStatus(
  status: SideThreadMessage["status"],
): Turn["status"] {
  switch (status) {
    case "streaming":
      return "in_progress";
    case "failed":
      return "failed";
    case "interrupted":
      return "interrupted";
    case "completed":
    case undefined:
      return "completed";
  }
}

function assistantItemStatus(
  status: SideThreadMessage["status"],
): ThreadItem["status"] {
  switch (status) {
    case "streaming":
      return "in_progress";
    case "failed":
      return "failed";
    case "interrupted":
    case "completed":
    case undefined:
      return "completed";
  }
}

function applyAssistantError(
  turn: Turn,
  message: SideThreadMessage,
): void {
  const errorMessage = message.error_message?.trim();
  if (message.status === "failed" && errorMessage) {
    turn.error = { message: errorMessage };
  }
}

function turnID(messageID: string): string {
  return `${SIDE_TURN_ID_PREFIX}${messageID}`;
}
