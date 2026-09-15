import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { collectTurnArtifacts, TurnInlineArtifactOutputs } from "./ArtifactOutputs";
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

it("renders the managed SVG snapshot as an image with its filename and opens that exact snapshot", async () => {
  const turn = { id: "turn", items: [presentedImage("first", "old")] } as Turn;
  const artifact = collectTurnArtifacts(turn)[0];
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  openPreview.mockClear();
  try {
    await act(async () => root.render(<TurnInlineArtifactOutputs artifacts={[artifact]} />));
    expect(container.querySelector("img")?.getAttribute("src")).toBe(artifact.uri);
    expect(container.querySelector("figcaption")?.textContent).toBe("chart.svg");
    expect(container.querySelector(".composer-image-attachment")).toBeNull();
    expect(container.querySelector(".turn-artifact-image-preview svg, iframe")).toBeNull();
    expect(container.querySelector("figcaption .lucide-zoom-in")?.getAttribute("aria-hidden")).toBe("true");
    await act(async () => container.querySelector("button")!.click());
    expect(openPreview).toHaveBeenCalledWith({ src: artifact.uri, alt: "chart.svg", title: "chart.svg" });
    await act(async () => container.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(container.querySelector(".turn-artifact-unavailable")?.textContent).toBe("imagePreview.loadFailed");
    expect(container.querySelector("button")?.disabled).toBe(true);
    expect(container.querySelector("figcaption .lucide-zoom-in")).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("folds machine-readable data beside images without hiding captions or losing the full result", async () => {
  const data = { messages: Array.from({ length: 100 }, (_, seq) => ({ seq, body: `Message ${seq}` })) };
  const turn = { id: "turn", items: [{ id: "chat-read", type: "tool_call", name: "chat_read", result_detail: { content: [
    { type: "text", text: JSON.stringify(data) },
    { type: "text", text: "Screenshot of the current conversation" },
    { type: "image", mime_type: "image/png", data: "aW1hZ2U=", name: "Screenshot" },
  ] } }] } as Turn;
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  try {
    await act(async () => root.render(<TurnInlineArtifactOutputs artifacts={collectTurnArtifacts(turn)} />));
    expect(container.textContent).toContain("Screenshot of the current conversation");
    expect(container.textContent).not.toContain("Message 99");
    expect(container.querySelector("img")).not.toBeNull();
    const fold = container.querySelector<HTMLDetailsElement>(".tool-result-data details")!;
    expect(fold.open).toBe(false);
    await act(async () => { fold.open = true; fold.dispatchEvent(new Event("toggle")); });
    expect(JSON.parse(container.querySelector(".tool-result-data-json")!.textContent!)).toEqual(data);
    await act(async () => { fold.open = false; fold.dispatchEvent(new Event("toggle")); });
    expect(container.querySelector(".tool-result-data-json")).toBeNull();
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
    expect(container.querySelector(".turn-artifact-image-preview button")?.textContent).toBe("Screenshot");
    expect(container.querySelector(".turn-artifact-image-name")?.textContent).toBe("Screenshot");
    await act(async()=>container.querySelector("button")!.click());
    expect(window.wuu.readRemoteAttachment).toHaveBeenCalledWith("thread:source");
    expect(container.querySelector("img")?.src).toBe("data:image/png;base64,aW1hZ2U=");
    expect(container.querySelector(".turn-artifact-image-preview img")?.getAttribute("alt")).toBe("Screenshot");
  } finally {act(()=>root.unmount());container.remove();window.wuu=prior;}
});
