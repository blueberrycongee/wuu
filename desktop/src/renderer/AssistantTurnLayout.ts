import type { Turn } from "../shared/protocol";
import type { TurnArtifact } from "./ArtifactOutputs";
import type { AssistantTurnDisplay, TurnEntry } from "./AssistantTurnDisplay";
import { streamFieldValue } from "./ThreadItemText";

type Output = { key: string } & (
  | { entry: TurnEntry; artifacts?: never }
  | { artifacts: TurnArtifact[]; entry?: never }
);

/** Keep published output and live work in chronological reading order until
 * a visible final answer takes over. At that handoff, all process entries join
 * the fold while artifacts and answer renderers keep their keys and order.
 */
export function layoutAssistantTurn(
  turn: Turn,
  display: AssistantTurnDisplay,
  artifacts: readonly TurnArtifact[],
): { processEntries: TurnEntry[]; output: Output[]; answerHandoffRequested: boolean } {
  const answerHandoffRequested = display.entries.some((entry) =>
    entry.position === "answer" && entry.item.type === "agent_message" &&
    (streamFieldValue(turn.id, entry.item, "text").trim().length > 0 || (entry.item.images?.length ?? 0) > 0),
  );
  const positions = new Map(turn.items.map((item, index) => [item.id, index * 2]));
  const inline = artifacts.filter((artifact) => artifact.placement === "inline" && artifact.type !== "text" && !artifact.foldPreview);
  if (inline.length === 0) {
    return {
      answerHandoffRequested,
      processEntries: display.entries.filter((entry) => entry.position === "process"),
      output: display.entries.filter((entry) => entry.position === "answer").map((entry) => ({ key: entry.key, entry })),
    };
  }
  const groups: TurnArtifact[][] = [];
  for (const artifact of inline) {
    const group = groups.at(-1);
    const previous = group?.at(-1);
    const adjacent = previous && (previous.itemId === artifact.itemId
      ? artifact.index === previous.index + 1
      : positions.get(artifact.itemId) === (positions.get(previous.itemId) ?? 0) + 2);
    if (group && previous?.mimeType.startsWith("image/") && artifact.mimeType.startsWith("image/") && adjacent) {
      group.push(artifact);
    } else {
      groups.push([artifact]);
    }
  }
  // Keep the group's producing activity together before its images. Splitting
  // at every publication would insert a process row between adjacent previews.
  const boundaries = new Set(groups.map((group) => group[group.length - 1].itemId));
  const ordered: (Output & { order: number })[] = groups.map((group) => ({
    key: group[0].id,
    artifacts: group,
    order: (positions.get(group[group.length - 1].itemId) ?? 0) + 1,
  }));
  for (const entry of display.entries) {
    const items = entry.items ?? [entry.item];
    let start = 0;
    for (let index = 0; index < items.length; index++) {
      if (index !== items.length - 1 && !boundaries.has(items[index].id)) continue;
      const slice = items.slice(start, index + 1);
      const key = start === 0 ? entry.key : `${entry.key}:${slice[0].id}`;
      const segment = slice.length === items.length ? entry : {
        ...entry,
        key,
        item: slice[0],
        items: slice,
        count: slice.length,
        settled: slice.every((item) => item.status !== "in_progress"),
        streaming: turn.status === "in_progress" && slice.some((item) => item.status === "in_progress"),
      };
      ordered.push({ key, entry: segment, order: positions.get(slice[0].id) ?? 0 });
      start = index + 1;
    }
  }
  ordered.sort((a, b) => a.order - b.order);
  const processEntries: TurnEntry[] = [];
  const output: Output[] = [];
  let published = false;
  for (const item of ordered) {
    if (item.artifacts) published = true;
    // Publication does not turn later commentary or grouped tool/reasoning
    // records into answers. Use the same handoff boundary as the fold itself,
    // including while the final answer streams and provider cleanup continues.
    if (
      item.entry?.position === "process" &&
      (!published || answerHandoffRequested)
    ) {
      processEntries.push(item.entry);
    } else {
      output.push(item);
    }
  }
  return { processEntries, output, answerHandoffRequested };
}
