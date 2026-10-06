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
  const visibleOrder = layout.output.flatMap((output) => output.artifacts
    ? output.artifacts.map(artifact => `image:${artifact.itemId}`)
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


// Gallery boundaries must follow the conversation, not individual tool calls.
it.each(["in_progress", "completed"] as const)("groups consecutive published images without crossing text or other work (%s)", (status) => {
  const image = (id: string, mime = "image/png"): ThreadItem => ({
    id, type: "tool_call", name: "present_artifact", status: "completed",
    result_detail: { content: [{ type: "image", mime_type: mime, data: id, artifact: { placement: "inline" } }] },
  });
  const first = image("first");
  const second = image("second", "image/gif");
  const items: ThreadItem[] = [first, second,
    { id: "explanation", type: "agent_message", text: "Compare these images", status: "completed" },
    image("third"),
    { id: "check", type: "tool_call", name: "run_shell", status: "completed" },
    image("fourth"),
  ];
  const layout = (items: ThreadItem[]) => {
    const turn: Turn = { id: "turn", items, status, items_view: "full" };
    return layoutAssistantTurn(turn, buildAssistantTurnDisplay(turn, undefined)!, collectTurnArtifacts(turn));
  };
  const result = layout(items);
  const galleries = result.output.filter(output => output.artifacts);
  expect(galleries.map(output => output.artifacts?.map(artifact => artifact.itemId))).toEqual([
    ["first", "second"], ["third"], ["fourth"],
  ]);
  expect(result.output.findIndex(output => output.entry?.item.id === "explanation")).toBe(1);
  expect(galleries[0].key).toBe(layout([first]).output[0].key);
  const activities = [...result.processEntries, ...result.output.flatMap(output => output.entry ? [output.entry] : [])];
  expect(activities.flatMap(entry => entry.items ?? [entry.item])).toEqual(items);
});

it("keeps inline documents out of image groups", () => {
  const turn: Turn = { id: "turn", status: "completed", items_view: "full", items: [
    { id: "mixed", type: "tool_call", name: "render", status: "completed", result_detail: { content:
      ["image/png", "text/html", "image/gif"].map(mime_type => ({
        type: "resource", mime_type, text: "fixture", artifact: { placement: "inline" },
      })),
    } },
  ] };
  const result = layoutAssistantTurn(turn, buildAssistantTurnDisplay(turn, undefined)!, collectTurnArtifacts(turn));
  expect(result.output.map(output => output.artifacts?.map(artifact => artifact.mimeType))).toEqual([
    ["image/png"], ["text/html"], ["image/gif"],
  ]);
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
