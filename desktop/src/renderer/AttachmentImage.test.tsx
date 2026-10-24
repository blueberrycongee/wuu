import type { ReactNode } from "react";
import { ArtifactsProvider } from "./ArtifactsContext";
import { ApplicationArtifacts } from "./plugins/ApplicationArtifacts";
import { act } from "react";
import { createRoot as createReactRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { AttachmentImage } from "./AttachmentImage";
import { inputImagesFromComposer } from "./ComposerMessages";
vi.mock("./i18n", () => ({useI18n: () => ({t: (key: string) => key}), translateCurrent:(key:string)=>key}));
it("loads an image only on demand, retries failures and retains references when editing", async () => {
  const read = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce("aW1hZ2U=");
  const prior = window.wuu;
  window.wuu = {readRemoteAttachment:read} as unknown as typeof window.wuu;
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container), open = vi.fn();
  const image = {media_type:"image/png", data:"", remote_ref:"ref"};
  try {
    await act(async () => root.render(<AttachmentImage image={image} label="Image 1" onOpen={open} />));
    expect(read).not.toHaveBeenCalled();
    await act(async () => container.querySelector("button")!.click());
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("offline");
    expect(open).not.toHaveBeenCalled();
    await act(async () => container.querySelector("button")!.click());
    expect(open).toHaveBeenCalledWith("data:image/png;base64,aW1hZ2U=", expect.any(HTMLElement));
    expect(container.querySelector("img")?.src).toBe("data:image/png;base64,aW1hZ2U=");
    expect(inputImagesFromComposer([{...image,id:"edit"}])).toEqual([image]);
  } finally { act(() => root.unmount()); container.remove(); window.wuu = prior; }
});

it("requests a thumbnail near the viewport and downloads the original only when opened", async () => {
  let intersect!: (entries: Array<{isIntersecting:boolean}>) => void;
  const disconnect = vi.fn();
  vi.stubGlobal("IntersectionObserver", class { constructor(callback: typeof intersect) { intersect = callback; } observe() {} disconnect = disconnect; });
  const prior = window.wuu;
  const preview = vi.fn().mockResolvedValue("data:image/jpeg;base64,dGh1bWI=");
  const read = vi.fn().mockResolvedValue("b3JpZ2luYWw=");
  window.wuu = {readRemoteAttachment:read, readRemoteAttachmentPreview:preview} as unknown as typeof window.wuu;
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container), open = vi.fn();
  try {
    await act(async () => root.render(<AttachmentImage image={{media_type:"image/png",data:"",remote_ref:"thread:ref"}} label="Image" onOpen={open} />));
    expect(preview).not.toHaveBeenCalled();
    await act(async () => intersect([{isIntersecting:true}]));
    expect(container.querySelector("img")?.src).toBe("data:image/jpeg;base64,dGh1bWI=");
    expect(read).not.toHaveBeenCalled(); expect(open).not.toHaveBeenCalled();
    await act(async () => container.querySelector("img")!.click());
    expect(open).toHaveBeenCalledWith("data:image/png;base64,b3JpZ2luYWw=", container.querySelector("img"));
  } finally { act(() => root.unmount()); container.remove(); window.wuu = prior; vi.unstubAllGlobals(); }
});

function createRoot(container: HTMLElement) {
  const root = createReactRoot(container);
  const artifacts = new ApplicationArtifacts(window.wuu ?? {}, {resolveResourceURL: () => undefined, openFile: async () => {}});
  return {...root, render: (children: ReactNode) => root.render(<ArtifactsProvider application={{artifacts}}>{children}</ArtifactsProvider>), unmount: () => root.unmount()};
}

it("does not open an original whose attachment was replaced while reading", async () => {
  const prior = window.wuu;
  let finish!: (data: string) => void;
  const read = vi.fn(() => new Promise<string>(resolve => {finish = resolve;}));
  window.wuu = {readRemoteAttachment: read} as unknown as typeof window.wuu;
  const container = document.createElement("div"), root = createRoot(container), open = vi.fn();
  try {
    await act(async () => root.render(<AttachmentImage image={{media_type:"image/png", data:"", remote_ref:"first"}} label="First" onOpen={open} />));
    act(() => container.querySelector("button")!.click());
    await act(async () => root.render(<AttachmentImage image={{media_type:"image/png", data:"", remote_ref:"second"}} label="Second" onOpen={open} />));
    await act(async () => finish("b2xk"));
    expect(open).not.toHaveBeenCalled();
    expect(container.textContent).toBe("Second");
  } finally {act(() => root.unmount());window.wuu = prior;}
});
