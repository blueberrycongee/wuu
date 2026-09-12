import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { collectTurnArtifacts, TurnInlineArtifactOutputs } from "./ArtifactOutputs";
import type { Turn } from "../shared/protocol";
vi.mock("./plugins/DesktopPluginRuntime", () => ({desktopWorkbenchController:{ subscribe: () => () => {}, getSnapshot: () => 0 }}));
vi.mock("./plugins/Workbench", () => ({WorkbenchContentRenderer:({fallback}:{fallback:React.ReactNode})=>fallback}));
vi.mock("./i18n", () => ({useI18n:()=>({t:(key:string)=>key})}));
vi.mock("./ImagePreview", () => ({useImagePreview:()=>({openPreview:vi.fn()})}));

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
    await act(async()=>container.querySelector("button")!.click());
    expect(window.wuu.readRemoteAttachment).toHaveBeenCalledWith("thread:source");
    expect(container.querySelector("img")?.src).toBe("data:image/png;base64,aW1hZ2U=");
  } finally {act(()=>root.unmount());container.remove();window.wuu=prior;}
});
