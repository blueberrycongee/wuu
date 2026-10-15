import type {
  PluginVisibleTurn, SideThreadHistoryResult, ThreadItem, Turn,
} from "../shared/protocol";

function visibleText(item: ThreadItem): string | null {
  if (item.remote_content_ref || item.error || item.truncated || (item.finish_reason && item.finish_reason !== "stop")
    || (item.status && item.status !== "completed")
    || typeof item.text !== "string") return null;
  return item.text;
}

/** Project only displayed user/final-answer text; media bytes and paths never leave this boundary. */
export function visibleMainTurns(turns: readonly Turn[]): PluginVisibleTurn[] {
  const result: PluginVisibleTurn[] = [];
  for (const turn of turns) {
    if (turn.status !== "completed" || (turn.kind && turn.kind !== "user") || turn.error
      || turn.truncated || (turn.finish_reason && turn.finish_reason !== "stop")) continue;
    const users = turn.items.filter((item) => item.type === "user_message");
    if (!users.length || users.some((item) => item.read_only || (item.origin && item.origin !== "user")
      || item.cause || item.presentation_kind || visibleText(item) === null
      || item.content_parts?.some((part) => part.type !== "text" && part.type !== "file_selection"))) continue;
    const answer = turn.items.filter((item) => item.type === "agent_message" && item.terminal).at(-1);
    if (!answer || !visibleText(answer)?.trim()) continue;
    const userText = users.map((item) => item.content_parts
      ? item.content_parts.filter((part) => part.type === "text").map((part) => part.text).join("")
      : item.text ?? "").join("\n");
    const imageCount = users.reduce((count, item) => count + (item.images?.length ?? 0) + (item.markdown_images?.length ?? 0), 0);
    const fileCount = users.reduce((count, item) => count + (item.files?.length ?? 0)
      + (item.content_parts?.filter((part) => part.type === "file_selection").length ?? 0), 0);
    if (!userText.trim()) continue;
    result.push({ id: turn.id, userText, assistantText: answer.text ?? "", imageCount, fileCount });
  }
  return result.slice(-20);
}

export function visibleSideTurns(history: SideThreadHistoryResult, sideThreadId: string): PluginVisibleTurn[] {
  if (history.summary.side_thread_id !== sideThreadId) return [];
  const result: PluginVisibleTurn[] = [];
  let userText: string | undefined;
  for (const message of history.messages) {
    if (message.side_thread_id !== sideThreadId) return [];
    if (message.role === "user") {
      userText = message.text;
    } else {
      if (userText?.trim() && message.status === "completed" && message.text.trim()) {
        result.push({ id: message.id, userText, assistantText: message.text, imageCount: 0, fileCount: 0 });
      }
      userText = undefined;
    }
  }
  return result.slice(-20);
}
