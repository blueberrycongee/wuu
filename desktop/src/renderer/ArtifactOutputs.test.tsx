import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { collectTurnArtifacts, TurnEndArtifactOutputs, TurnInlineArtifactOutputs } from "./ArtifactOutputs";
import type { ThreadItem, Turn } from "../shared/protocol";
const { openPreview } = vi.hoisted(() => ({ openPreview: vi.fn() }));
vi.mock("./plugins/DesktopPluginRuntime", () => ({desktopWorkbenchController:{ subscribe: () => () => {}, getSnapshot: () => 0 }}));
vi.mock("./plugins/Workbench", () => ({WorkbenchContentRenderer:({fallback}:{fallback:React.ReactNode})=>fallback}));
vi.mock("./i18n", () => ({useI18n:()=>({t:(key:string)=>key})}));
vi.mock("./ImagePreview", () => ({useImagePreview:()=>({openPreview})}));

function presentedImage(id: string, hash: string, name = "chart.svg"): ThreadItem {
  return { id, type: "tool_call", name: "present_artifact", status: "completed", result_detail: { content: [{
    type: "image", mime_type: "image/svg+xml", name,
    uri: `wuu-artifact://workspace/thread/${id}/${name}?sha256=${hash}`,
    artifact: { ref: id, sha256: hash, placement: "inline", size_bytes: 100 },
  }] } };
}

it("deduplicates identical published snapshots per turn without hiding new versions or other named outputs", () => {
  const first = presentedImage("first", "old");
  const turn = { id: "turn", items: [first, presentedImage("repeat", "old"), presentedImage("revision", "new"), presentedImage("other", "old", "other.svg")] } as Turn;
  expect(collectTurnArtifacts(turn).map(a => a.itemId)).toEqual(["first", "revision", "other"]);
  expect(collectTurnArtifacts({ ...turn, id: "next", items: [first] })).toHaveLength(1);
  const mixed: ThreadItem = { ...first, result_detail: { content: [first.result_detail!.content![0], { type: "text", text: "Compared with itself" }, first.result_detail!.content![0]] } };
  expect(collectTurnArtifacts({ ...turn, items: [mixed] }).map(a => a.type)).toEqual(["image", "text", "image"]);
});

it("does not infer artifacts from file diffs, file links, or inspection text", () => {
  const turn = { id: "turn", items: [
    { id: "edit", type: "tool_call", name: "write_file", result: '{"path":"chart.svg","diff":{"new_file":true,"lines":3}}' },
    { id: "read", type: "tool_call", name: "read_file", result_detail: { content: [{ type: "text", text: "<svg/>" }] } },
    { id: "answer", type: "agent_message", text: "[chart](chart.svg)" },
  ] } as Turn;
  expect(collectTurnArtifacts(turn)).toEqual([]);
});

it("renders only the managed SVG image and opens that exact snapshot", async () => {
  const turn = { id: "turn", items: [presentedImage("first", "old")] } as Turn;
  const artifact = collectTurnArtifacts(turn)[0];
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  openPreview.mockClear();
  try {
    await act(async () => root.render(<TurnInlineArtifactOutputs artifacts={[artifact]} />));
    expect(container.querySelector("img")?.getAttribute("src")).toBe(artifact.uri);
    expect(container.textContent).toBe("");
    expect(container.querySelector("img")?.alt).toBe(artifact.name);
    expect(container.querySelector(".composer-image-attachment")).toBeNull();
    expect(container.querySelector(".turn-artifact-image-preview svg, iframe")).toBeNull();
    await act(async () => container.querySelector("button")!.click());
    expect(openPreview).toHaveBeenCalledWith({ src: artifact.uri, alt: "chart.svg", title: "chart.svg" });
    await act(async () => container.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(container.querySelector(".turn-artifact-unavailable")?.textContent).toBe("imagePreview.loadFailed");
    expect(container.querySelector("button")?.disabled).toBe(true);
  } finally { act(() => root.unmount()); container.remove(); }
});

it("keeps document output data inspectable without projecting it into the image stream", async () => {
  const data = { messages: Array.from({ length: 100 }, (_, seq) => ({ seq, body: `Message ${seq}` })) };
  const turn = { id: "turn", items: [{ id: "chat-read", type: "tool_call", name: "chat_read", result_detail: { content: [
    { type: "text", text: JSON.stringify(data) },
    { type: "file", mime_type: "application/pdf", uri: "report.pdf", name: "Report" },
  ] } }] } as Turn;
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  try {
    const artifacts = collectTurnArtifacts(turn);
    await act(async () => root.render(<TurnInlineArtifactOutputs artifacts={artifacts} />));
    expect(container.childElementCount).toBe(0);
    await act(async () => root.render(<TurnEndArtifactOutputs artifacts={artifacts} />));
    expect(container.textContent).toContain("Report");
    expect(container.textContent).not.toContain("Message 99");
    const fold = container.querySelector<HTMLDetailsElement>(".tool-result-data details")!;
    expect(fold.open).toBe(false);
    await act(async () => { fold.open = true; fold.dispatchEvent(new Event("toggle")); });
    expect(JSON.parse(container.querySelector(".tool-result-data-json")!.textContent!)).toEqual(data);
    await act(async () => { fold.open = false; fold.dispatchEvent(new Event("toggle")); });
    expect(container.querySelector(".tool-result-data-json")).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("omits image-stream text and inspector controls without changing image order or the underlying result", async () => {
  const metadata = 'Application observation: frame=(100,200,800,600); coordinate space=normalized';
  const turn = { id: "turn", items: [{ id: "observe", type: "tool_call", result_detail: { content: [
    { type: "image", mime_type: "image/png", data: "Zmlyc3Q=", name: "Before" },
    { type: "text", text: JSON.stringify({ frame: [100, 200, 800, 600] }) },
    { type: "text", text: metadata },
    { type: "text", text: "Screenshot caption", artifact: { placement: "inline" } },
    { type: "image", mime_type: "image/png", data: "c2Vjb25k", name: "After" },
  ] } }] } as Turn;
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  try {
    const original = JSON.stringify(turn);
    const artifacts = collectTurnArtifacts(turn);
    await act(async () => root.render(<TurnInlineArtifactOutputs artifacts={artifacts} />));
    expect(container.textContent).toBe("");
    expect(Array.from(container.querySelectorAll("img"), image => image.alt)).toEqual(["Before", "After"]);
    expect(container.querySelector("details")).toBeNull();
    expect(container.querySelectorAll("button")).toHaveLength(2);
    expect(artifacts.filter(artifact => artifact.type === "text").map(artifact => artifact.text))
      .toEqual(turn.items[0].result_detail!.content!.filter(part => part.type === "text").map(part => part.text));
    expect(JSON.stringify(turn)).toBe(original);
  } finally { act(() => root.unmount()); container.remove(); }
});

it("renders deferred tool-result images as loadable attachments instead of unavailable artifacts", async () => {
  const turn = {id:"turn",items:[{id:"tool",type:"tool_call",result_detail:{content:[{type:"image",mime_type:"image/png",remote_ref:"thread:source",name:"Screenshot"}]}}]} as Turn;
  const artifacts=collectTurnArtifacts(turn);
  const prior=window.wuu;
  window.wuu={readRemoteAttachment:vi.fn().mockResolvedValue("aW1hZ2U=")} as unknown as typeof window.wuu;
  const container=document.createElement("div"),root=createRoot(container);document.body.append(container);
  try {
    await act(async()=>root.render(<TurnInlineArtifactOutputs artifacts={artifacts}/>));
    expect(container.querySelector(".turn-artifact-unavailable")).toBeNull();
    expect(window.wuu.readRemoteAttachment).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain("Screenshot");
    expect(container.querySelector("button")).not.toBeNull();
    await act(async()=>container.querySelector("button")!.click());
    expect(window.wuu.readRemoteAttachment).toHaveBeenCalledWith("thread:source");
    expect(container.querySelector("img")?.src).toBe("data:image/png;base64,aW1hZ2U=");
    expect(container.textContent).toBe("");
  } finally {act(()=>root.unmount());container.remove();window.wuu=prior;}
});
