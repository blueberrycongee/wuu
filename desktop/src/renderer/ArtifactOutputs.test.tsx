import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { ArtifactPreview, collectTurnArtifacts, TurnEndArtifactOutputs, TurnInlineArtifactOutputs } from "./ArtifactOutputs";
import { ArtifactPreviewContext, ArtifactThreadContext } from "./ArtifactPreviewContext";
import type { ThreadItem, Turn } from "../shared/protocol";
const { openPreview } = vi.hoisted(() => ({ openPreview: vi.fn() }));
vi.mock("./plugins/DesktopPluginRuntime", () => ({desktopWorkbenchController:{ subscribe: () => () => {}, getSnapshot: () => 0 }}));
vi.mock("./plugins/Workbench", () => ({WorkbenchContentRenderer:({fallback}:{fallback:React.ReactNode})=>fallback}));
vi.mock("./i18n", () => ({useI18n:()=>({t:(key:string)=>key, formatNumber:(value:number)=>String(value)})}));
vi.mock("./ImagePreview", () => ({useImagePreview:()=>({openPreview})}));

function presentedImage(id: string, hash: string, name = "chart.svg"): ThreadItem {
  return { id, type: "tool_call", name: "present_artifact", status: "completed", result_detail: { content: [{
    type: "image", mime_type: "image/svg+xml", name,
    uri: `wuu-artifact://workspace/thread/${id}/${name}?sha256=${hash}`,
    artifact: { ref: id, sha256: hash, placement: "inline", size_bytes: 100 },
  }] } };
}

it("routes a delivered file card using its source thread rather than the active conversation", async () => {
  const artifact = { ...collectTurnArtifacts({ items: [presentedImage("file", "snapshot")] } as Turn)[0], type: "file" as const, placement: "turn_end" as const, mimeType: "text/html" };
  const open = vi.fn();
  const container = document.createElement("div"), root = createRoot(container);
  try {
    await act(async () => root.render(
      <ArtifactPreviewContext.Provider value={open}>
        <ArtifactThreadContext.Provider value="source-thread">
          <TurnEndArtifactOutputs artifacts={[artifact]} cwd="source-workspace" />
        </ArtifactThreadContext.Provider>
      </ArtifactPreviewContext.Provider>,
    ));
    await act(async () => container.querySelector("button")!.click());
    expect(open).toHaveBeenCalledWith({ threadID: "source-thread", cwd: "source-workspace", artifact });
    expect(container.querySelector('[role="dialog"]')).toBeNull();
  } finally { act(() => root.unmount()); }
});

it("previews HTML in a non-modal sandbox without moving focus or downloading", async () => {
  const artifact = { ...collectTurnArtifacts({ items: [presentedImage("html", "snapshot")] } as Turn)[0], mimeType: "text/html" };
  const container = document.createElement("div"), root = createRoot(container);
  const composer = document.createElement("textarea");
  document.body.append(composer, container);
  composer.focus();
  const previous = window.wuu;
  const save = vi.fn().mockResolvedValue(undefined);
  window.wuu = { saveArtifactFile: save } as unknown as typeof window.wuu;
  try {
    await act(async () => root.render(<ArtifactPreview artifact={artifact} mode="panel" onClose={() => {}} />));
    expect(document.activeElement).toBe(composer);
    const iframe = container.querySelector("iframe")!;
    expect(iframe.getAttribute("sandbox")).toBe("");
    expect(iframe.getAttribute("referrerpolicy")).toBe("no-referrer");
    expect(save).not.toHaveBeenCalled();
    await act(async () => container.querySelector("button")!.click());
    expect(save).toHaveBeenCalledWith(artifact.name, artifact.uri);
  } finally { act(() => root.unmount()); container.remove(); composer.remove(); window.wuu = previous; }
});

it.each([
  { name: "clip.mp4", uri: "wuu-artifact://workspace/thread/snapshot/clip.mp4?sha256=hash", mime_type: "video/mp4" },
  { name: "clip.WEBM", uri: "clips/clip.WEBM", mime_type: undefined },
])("opens a video card in the preview and keeps download available after playback failure ($name)", async (part) => {
  const artifact = collectTurnArtifacts({ items: [{
    id: "video", type: "tool_call", status: "completed", result_detail: { content: [{
      type: "file", ...part, artifact: { placement: "turn_end" },
    }] },
  }] } as Turn)[0];
  const container = document.createElement("div"), root = createRoot(container);
  const onOpenFile = vi.fn();
  try {
    await act(async () => root.render(<TurnEndArtifactOutputs artifacts={[artifact]} cwd="/workspace" onOpenFile={onOpenFile} />));
    await act(async () => container.querySelector("button")!.click());
    const video = container.querySelector("video")!;
    expect(video).not.toBeNull();
    expect(video.controls).toBe(true);
    expect(video.autoplay).toBe(false);
    expect(video.src).toBe(part.uri.startsWith("wuu-artifact:") ? part.uri
      : `wuu-file://local/${btoa(`/workspace/${part.uri}`).replace(/=/g, "")}`);
    expect(onOpenFile).not.toHaveBeenCalled();
    await act(async () => video.dispatchEvent(new Event("error")));
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(container.querySelector('[aria-label="artifacts.downloadNamed"]')).not.toBeNull();
  } finally { act(() => root.unmount()); }
});

it("loads managed text content and reports an oversized preview without losing download access", async () => {
  const artifact = { ...collectTurnArtifacts({ items: [presentedImage("text", "snapshot")] } as Turn)[0], mimeType: "text/plain" };
  const fetchMock = vi.fn().mockResolvedValueOnce(new Response("Delivered text"))
    .mockResolvedValueOnce(new Response("x".repeat(2 * 1024 * 1024 + 1)));
  vi.stubGlobal("fetch", fetchMock);
  const container = document.createElement("div"), root = createRoot(container);
  try {
    await act(async () => root.render(<ArtifactPreview artifact={artifact} mode="panel" onClose={() => {}} />));
    expect(container.querySelector("pre")?.textContent).toBe("Delivered text");
    await act(async () => root.render(<ArtifactPreview key="large" artifact={{ ...artifact, uri: `${artifact.uri}&revision=large` }} mode="panel" onClose={() => {}} />));
    expect(container.querySelector("pre")).toBeNull();
    expect(container.textContent).toContain("artifacts.previewUnavailable");
    expect(container.querySelector('[aria-label="artifacts.downloadNamed"]')).not.toBeNull();
  } finally { act(() => root.unmount()); vi.unstubAllGlobals(); }
});

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
    expect(openPreview).toHaveBeenCalledWith({ src: artifact.uri, alt: "chart.svg", title: "chart.svg" }, container.querySelector("button"));
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
    expect(container.textContent).not.toContain("application/pdf");
    expect(container.textContent).not.toContain("Message 99");
    expect(container.querySelector(".tool-result-data")).toBeNull();
  } finally { act(() => root.unmount()); container.remove(); }
});

it("uses the file-change summary chrome for a single presented file", async () => {
  const turn = { id: "turn", items: [{ id: "present", type: "tool_call", name: "present_artifact", result_detail: { content: [{
    type: "file", mime_type: "video/mp4", name: "wuu-promo.mp4", uri: "wuu-artifact://workspace/thread/video/wuu-promo.mp4",
    artifact: { placement: "turn_end", sha256: "vid" },
  }] } }] } as Turn;
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  try {
    await act(async () => root.render(<TurnEndArtifactOutputs artifacts={collectTurnArtifacts(turn)} />));
    expect(container.querySelector(".turn-edit-summary-overview-title")?.textContent).toBe("artifacts.countOne");
    expect(container.querySelector(".turn-edit-summary-overview-path")?.textContent).toBe("wuu-promo.mp4");
    expect(container.querySelector(".turn-edit-summary-row")).toBeNull();
    expect(container.textContent).not.toContain("video/mp4");
  } finally { act(() => root.unmount()); container.remove(); }
});

it("lists multiple presented files in the same summary rows as file changes", async () => {
  const file = (id: string, name: string): ThreadItem => ({
    id, type: "tool_call", name: "present_artifact", result_detail: { content: [{
      type: "file", mime_type: "application/pdf", name, uri: `wuu-artifact://workspace/thread/${id}/${name}`,
      artifact: { placement: "turn_end", sha256: id },
    }] },
  });
  const turn = { id: "turn", items: [file("a", "one.pdf"), file("b", "two.pdf")] } as Turn;
  const container = document.createElement("div"), root = createRoot(container); document.body.append(container);
  try {
    await act(async () => root.render(<TurnEndArtifactOutputs artifacts={collectTurnArtifacts(turn)} />));
    expect(container.querySelector(".turn-edit-summary-overview-title")?.textContent).toBe("artifacts.count");
    expect(Array.from(container.querySelectorAll(".turn-edit-summary-row"), (row) => row.textContent)).toEqual([
      "one.pdf",
      "two.pdf",
    ]);
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
