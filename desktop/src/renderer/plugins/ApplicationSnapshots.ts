import type { ApplicationConversationThreadV1, ApplicationConversationThreadItemV1, ApplicationConversationTurnV1 } from "../../../../packages/plugin-sdk/src/ui-conversation";
import { streamTextKey, streamTextStore } from "../StreamText";
import type { Thread, ThreadItem, Turn } from "../../shared/protocol";
import type {
  AttachmentDescriptorV1, ConversationItemSnapshotV1, ConversationProcessItemV1,
  ConversationProcessKindV1, ConversationProcessSnapshotV1, ConversationProcessStatusV1,
} from "../../shared/workbench";
import { toolDisplayLabel } from "../ToolActivityHelpers";
import { createImmutableSnapshotProjector } from "./ImmutableSnapshot";
import type { ApplicationTurnV1 } from "../../../../packages/plugin-sdk/src/ui";
type ConversationItemKindV1 = ConversationItemSnapshotV1["kind"];

export function toConversationItemSnapshot(
  item: ApplicationConversationThreadItemV1,
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

function attachmentDescriptors(item: ApplicationConversationThreadItemV1): readonly AttachmentDescriptorV1[] | undefined {
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

export function toConversationProcessSnapshot(
  processItems: readonly ApplicationConversationThreadItemV1[],
  streaming: boolean,
  active: boolean,
  locale?: string,
): ConversationProcessSnapshotV1 | undefined {
  // V1 presenters cannot represent compaction outcomes. Keep the native
  // group rather than allowing a replacement to silently hide those notices.
  if (processItems.some((item) => item.type === "context_compaction")) return undefined;
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

function publicProcessStatus(item: ApplicationConversationThreadItemV1): ConversationProcessStatusV1 {
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

/** Cache display copies while their source and live field values remain unchanged. */
export function createConversationThreadProjector() {
  // Protocol records are immutable JSON values in the host store. Detach their
  // containers, but retain immutable strings and unchanged subtrees: serializing
  // a whole history also duplicates large tool results and inline attachments.
  const immutable = createImmutableSnapshotProjector();
  const items = new WeakMap<ThreadItem, { values: (string | undefined)[]; view: ThreadItem }>();
  const turns = new WeakMap<Turn, { revision: number; view: Turn }>();
  const threads = new WeakMap<Thread, { revision: number; view: ApplicationConversationThreadV1 }>();
  return (thread: Thread): ApplicationConversationThreadV1 => {
    const revision = streamTextStore.getRevision();
    const previous = threads.get(thread);
    if (previous?.revision === revision) return previous.view;
    const projectedTurns = thread.turns.map(turn => {
      const turnRevision = streamTextStore.getTurnRevision(turn.id);
      const previous = turns.get(turn);
      if (previous?.revision === turnRevision) return previous.view;
      const projectedItems = turn.items.map(item => {
        const values = (["text", "arguments", "result"] as const).map(field => {
          const key = streamTextKey(turn.id, item.id, field);
          return streamTextStore.has(key) ? streamTextStore.get(key) : item[field];
        });
        const previous = items.get(item);
        if (previous && values.every((value, index) => value === previous.values[index])) return previous.view;
        const view = Object.freeze({ ...immutable(item), text: values[0], arguments: values[1], result: values[2] });
        items.set(item, { values, view });
        return view;
      });
      if (previous && projectedItems.every((item, index) => item === previous.view.items[index])) {
        previous.revision = turnRevision;
        return previous.view;
      }
      const view = Object.freeze({ ...immutable(turn), items: Object.freeze(projectedItems) }) as Turn;
      turns.set(turn, { revision: turnRevision, view });
      return view;
    });
    if (previous && projectedTurns.every((turn, index) => turn === previous.view.turns[index])) {
      previous.revision = revision;
      return previous.view;
    }
    const view = Object.freeze({ ...immutable(thread), turns: Object.freeze(projectedTurns) });
    threads.set(thread, { revision, view });
    return view;
  };
}

/** Project the selected conversation without rebuilding unchanged completed turns. */
export function createConversationTurnsProjector() {
  const cache = new WeakMap<ApplicationConversationTurnV1, { active: boolean; locale: string; view: ApplicationTurnV1 }>();
  const threads = new WeakMap<ApplicationConversationThreadV1, { active: boolean; locale: string; turns: readonly ApplicationTurnV1[] }>();
  const empty: readonly ApplicationTurnV1[] = Object.freeze([]);
  return (thread: ApplicationConversationThreadV1 | undefined, active: boolean, locale: string): readonly ApplicationTurnV1[] => {
    if (!thread) return empty;
    const previous = threads.get(thread);
    if (previous && previous.active === active && previous.locale === locale) return previous.turns;
    const turns = Object.freeze(thread.turns.map(turn => {
      const cached = cache.get(turn);
      if (cached && cached.active === active && cached.locale === locale) return cached.view;
      const items = turn.items.map(item => {
        const kind: ConversationItemKindV1 = item.type === "user_message" ? "user-message"
          : item.type === "agent_message" ? "assistant-message" : item.type === "reasoning" ? "reasoning"
          : item.type === "tool_call" ? "tool-reference" : "notice";
        return toConversationItemSnapshot(item, kind);
      });
      const view: ApplicationTurnV1 = Object.freeze({ id: turn.id,
        status: turn.status === "in_progress" ? "running" : turn.status === "interrupted" ? "cancelled" : turn.status,
        items: Object.freeze(items), process: toConversationProcessSnapshot(turn.items.filter(item => item.type === "reasoning" || item.type === "tool_call"), turn.status === "in_progress", active, locale) });
      cache.set(turn, { active, locale, view });
      return view;
    }));
    threads.set(thread, { active, locale, turns });
    return turns;
  };
}
