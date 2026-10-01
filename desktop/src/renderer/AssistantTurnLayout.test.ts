import { expect, it } from "vitest";
import type { ThreadItem, Turn } from "../shared/protocol";
import { collectTurnArtifacts } from "./ArtifactOutputs";
import { buildAssistantTurnDisplay } from "./AssistantTurnDisplay";
import { layoutAssistantTurn } from "./AssistantTurnLayout";

it("splits grouped activity at image results without hiding later work or changing its live status", () => {
  const tool = (id: string, image = false): ThreadItem => ({
    id, type: "tool_call", name: "render", status: "completed",
    result_detail: image ? { content: [{ type: "image", mime_type: "image/png", data: id, artifact: { placement: "inline" } }] } : undefined,
  });
  const items: ThreadItem[] = [
    tool("prepare"), tool("first-image", true), tool("check"), tool("second-image", true),
    { id: "reasoning", type: "reasoning", text: "Checking the result", status: "in_progress" },
  ];
  const turn: Turn = { id: "turn", items, status: "in_progress", items_view: "full" };
  const layout = layoutAssistantTurn(turn, buildAssistantTurnDisplay(turn, undefined)!, collectTurnArtifacts(turn));
  const processItems = layout.processEntries.flatMap((entry) => entry.items ?? [entry.item]);
  // All of these items normally coalesce into one process group. Publishing
  // images must separate that group, or "jump to latest" lands after live work.
  expect(processItems).toEqual(items.slice(0, 2));
  const visibleOrder = layout.output.flatMap((output) => output.artifact
    ? [`image:${output.artifact.itemId}`]
    : (output.entry.items ?? [output.entry.item]).map((item) => item.id));
  expect(visibleOrder).toEqual(["image:first-image", "check", "second-image", "image:second-image", "reasoning"]);
  const activity = layout.output.flatMap((output) => output.entry ? [output.entry] : []);
  expect(layout.processEntries.every((entry) => entry.settled && !entry.streaming)).toBe(true);
  expect(activity[0].settled).toBe(true);
  expect(activity[0].streaming).toBe(false);
  expect(activity[1].settled).toBe(false);
  expect(activity[1].streaming).toBe(true);
  expect([...processItems, ...activity.flatMap((entry) => entry.items ?? [entry.item])]).toEqual(items);
});


it.each(["in_progress", "completed"] as const)("keeps ordinary image inspection in one process group (%s)", (status) => {
  const items: ThreadItem[] = [
    { id: "prepare", type: "tool_call", name: "run_shell", status: "completed" },
    ...["one", "two", "three"].map((id): ThreadItem => ({ id, type: "tool_call", name: "read_file", status: "completed",
      result_detail: { content: [{ type: "text", text: "Image metadata" }, { type: "image", name: `${id}.png`, mime_type: "image/png", data: id }] } })),
    { id: "reasoning", type: "reasoning", text: "Compare images", status },
  ];
  const turn: Turn = { id: "turn", items, status, items_view: "full" };
  const artifacts = collectTurnArtifacts(turn);
  const layout = layoutAssistantTurn(turn, buildAssistantTurnDisplay(turn, undefined)!, artifacts);
  expect(layout.processEntries).toHaveLength(1);
  expect(layout.processEntries[0].items).toEqual(items);
  expect(layout.output).toEqual([]);
  expect(artifacts.filter(item => item.type === "image").map(item => item.data)).toEqual(["one", "two", "three"]);
});
