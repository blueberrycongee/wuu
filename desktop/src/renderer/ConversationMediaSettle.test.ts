import { afterEach, describe, expect, it, vi } from "vitest";
import type { Thread } from "../shared/protocol";
import {
  hasPendingConversationMedia,
  settleConversationMedia,
  threadHasConversationMedia,
  waitForConversationMedia,
} from "./ConversationMediaSettle";

const viewports: HTMLElement[] = [];

afterEach(() => {
  vi.useRealTimers();
  for (const node of viewports) node.remove();
  viewports.length = 0;
});

function viewport(): HTMLElement {
  const node = document.createElement("div");
  document.body.append(node);
  viewports.push(node);
  node.getBoundingClientRect = () => rect(0, 0, 800, 600);
  return node;
}

function rect(left: number, top: number, width: number, height: number): DOMRect {
  return {
    left, top, width, height, right: left + width, bottom: top + height, x: left, y: top,
    toJSON: () => ({}),
  } as DOMRect;
}

function imageViewportRect(node: HTMLElement, bounds = rect(20, 20, 200, 150)): void {
  node.getBoundingClientRect = () => bounds;
}

function setImageState(image: HTMLImageElement, complete: boolean, naturalWidth: number): void {
  Object.defineProperty(image, "complete", { configurable: true, get: () => complete });
  Object.defineProperty(image, "naturalWidth", { configurable: true, get: () => naturalWidth });
}

describe("conversation media settling", () => {
  it("detects only pending media near the active viewport", () => {
    const node = viewport();
    const near = document.createElement("img");
    near.className = "rich-image";
    imageViewportRect(near);
    setImageState(near, false, 0);
    node.append(near);
    const far = document.createElement("img");
    far.className = "rich-image";
    far.getBoundingClientRect = () => rect(20, 2000, 200, 150);
    setImageState(far, false, 0);
    node.append(far);
    const inactivePane = document.createElement("div");
    inactivePane.className = "cached-conversation-pane";
    inactivePane.dataset.active = "false";
    const inactive = document.createElement("img");
    inactive.className = "rich-image";
    imageViewportRect(inactive);
    setImageState(inactive, false, 0);
    inactivePane.append(inactive);
    node.append(inactivePane);
    expect(hasPendingConversationMedia(node)).toBe(true);
    near.remove();
    expect(hasPendingConversationMedia(node)).toBe(false);
  });

  it("detects media from the resumed thread before its components mount", () => {
    const withMarkdown = { turns: [{ items: [{ text: "![result](scan.png)" }] }] } as unknown as Thread;
    const withAttachment = { turns: [{ items: [{ images: [{ media_type: "image/png", data: "" }] }] }] } as unknown as Thread;
    const plain = { turns: [{ items: [{ text: "No images here" }] }] } as unknown as Thread;
    expect(threadHasConversationMedia(withMarkdown)).toBe(true);
    expect(threadHasConversationMedia(withAttachment)).toBe(true);
    expect(threadHasConversationMedia(plain)).toBe(false);
    expect(threadHasConversationMedia(undefined)).toBe(false);
  });

  it("discovers media that mounts after the first stable layout", async () => {
    const node = viewport();
    let settled = false;
    const waiting = waitForConversationMedia(node, 1000).then(() => { settled = true; });
    await Promise.resolve();
    expect(settled).toBe(false);
    const image = document.createElement("img");
    image.className = "rich-image";
    imageViewportRect(image);
    setImageState(image, false, 0);
    node.append(image);
    await new Promise(resolve => setTimeout(resolve, 0));
    await waiting;
    expect(settled).toBe(true);
  });

  it("waits for a remote image placeholder to be replaced before settling", async () => {
    const node = viewport();
    const placeholder = document.createElement("button");
    placeholder.setAttribute("data-conversation-image-pending", "");
    imageViewportRect(placeholder);
    node.append(placeholder);
    let settled = false;
    const waiting = settleConversationMedia(node, 1000).then(() => { settled = true; });
    await Promise.resolve();
    expect(settled).toBe(false);
    const image = document.createElement("img");
    imageViewportRect(image);
    setImageState(image, true, 100);
    placeholder.replaceWith(image);
    await waiting;
    expect(settled).toBe(true);
  });

  it("starts a near-viewport lazy image and waits for its load event", async () => {
    const node = viewport();
    const image = document.createElement("img");
    image.className = "rich-image";
    image.loading = "lazy";
    imageViewportRect(image);
    setImageState(image, false, 0);
    node.append(image);
    let settled = false;
    const waiting = settleConversationMedia(node, 1000).then(() => { settled = true; });
    await Promise.resolve();
    expect(image.loading).toBe("eager");
    expect(settled).toBe(false);
    setImageState(image, true, 100);
    image.dispatchEvent(new Event("load"));
    await waiting;
    expect(settled).toBe(true);
  });

  it("does not wait for remote placeholders outside the visible viewport", async () => {
    vi.useFakeTimers();
    const node = viewport();
    const placeholder = document.createElement("button");
    placeholder.setAttribute("data-conversation-image-pending", "");
    imageViewportRect(placeholder, rect(20, 900, 200, 50));
    node.append(placeholder);
    // Preview loading only begins near an intersection, not a full screen away.
    expect(hasPendingConversationMedia(node)).toBe(false);
    let settled = false;
    void settleConversationMedia(node).then(() => { settled = true; });
    await vi.advanceTimersByTimeAsync(0);
    expect(settled).toBe(true);
  });

  it.each(["timeout", "abort"])("removes pending image listeners on %s", async reason => {
    vi.useFakeTimers();
    const node = viewport();
    const image = document.createElement("img");
    image.className = "rich-image";
    imageViewportRect(image);
    setImageState(image, false, 0);
    node.append(image);
    const add = vi.spyOn(image, "addEventListener");
    const remove = vi.spyOn(image, "removeEventListener");
    const controller = new AbortController();
    const waiting = settleConversationMedia(node, 240, controller.signal);
    await vi.advanceTimersByTimeAsync(0);
    if (reason === "abort") controller.abort();
    else await vi.advanceTimersByTimeAsync(240);
    await waiting;
    for (const [event, listener] of add.mock.calls) {
      expect(remove).toHaveBeenCalledWith(event, listener);
    }
    expect(vi.getTimerCount()).toBe(0);
  });

});
