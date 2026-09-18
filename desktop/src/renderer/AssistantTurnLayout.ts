import type { Turn } from "../shared/protocol";
import type { TurnArtifact } from "./ArtifactOutputs";
import type { AssistantTurnDisplay, TurnEntry } from "./AssistantTurnDisplay";

type Output = { key: string } & (
  | { entry: TurnEntry; artifact?: never }
  | { artifact: TurnArtifact; entry?: never }
);

/** Keep published output and everything after it in chronological reading order.
 * A message's terminal flag can change without moving it across an image or
 * remounting its streaming renderer. Process semantics remain on each entry.
 */
export function layoutAssistantTurn(
  turn: Turn,
  display: AssistantTurnDisplay,
  artifacts: readonly TurnArtifact[],
): { processEntries: TurnEntry[]; output: Output[] } {
  const positions = new Map(turn.items.map((item, index) => [item.id, index * 2]));
  const inline = artifacts.filter((artifact) => artifact.placement === "inline" && artifact.type !== "text");
  if (inline.length === 0) {
    return {
      processEntries: display.entries.filter((entry) => entry.position === "process"),
      output: display.entries.filter((entry) => entry.position === "answer").map((entry) => ({ key: entry.key, entry })),
    };
  }
  const boundaries = new Set(inline.map((artifact) => artifact.itemId));
  const ordered: (Output & { order: number })[] = inline.map((artifact) => ({
    key: artifact.id,
    artifact,
    order: (positions.get(artifact.itemId) ?? 0) + 1,
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
    if (item.artifact) published = true;
    if (!published && item.entry?.position === "process") {
      processEntries.push(item.entry);
    } else {
      output.push(item);
    }
  }
  return { processEntries, output };
}
