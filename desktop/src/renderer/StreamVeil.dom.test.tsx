import { act, useRef } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useStreamVeil } from "./StreamVeil";
import { StreamingMarkdown } from "./StreamingMarkdown";

const registry = new Map<string, Set<Range>>();
let frame: FrameRequestCallback | undefined;
let reduced = false;
const host = document.createElement("div");
const root = createRoot(host);
function Surface({ text, live = true }: { text: string; live?: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  useStreamVeil(ref, text, live, "message");
  return <div ref={ref}><p>{text}<strong>!</strong></p><button>Copy</button></div>;
}
function Blocks({ blocks, tail, identity = "blocks" }: { blocks: string[]; tail: string; identity?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useStreamVeil(ref, blocks.join("\n\n") + tail, true, identity, blocks.length);
  return <div ref={ref}>{blocks.map((text, index) => <div key={index}><p>{text}</p></div>)}<p key="tail">{tail}</p></div>;
}
function showBlocks(blocks: string[], tail: string, identity?: string) {
  act(() => root.render(<Blocks blocks={blocks} tail={tail} identity={identity} />));
}
function mount(text: string, live = true) {
  act(() => root.render(<Surface text={text} live={live} />));
}
function setup() {
  document.body.append(host);
  vi.stubGlobal("CSS", { highlights: registry });
  vi.stubGlobal("Highlight", class extends Set<Range> { constructor(...ranges: Range[]) { super(ranges); } });
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frame = callback; return 1; });
  vi.stubGlobal("cancelAnimationFrame", () => { frame = undefined; });
  vi.stubGlobal("matchMedia", () => ({ matches: reduced, addEventListener() {}, removeEventListener() {} }));
}
afterEach(() => {
  act(() => root.render(null));
  host.remove();
  registry.clear();
  reduced = false;
  delete document.documentElement.dataset.appearanceMotion;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("streaming paint integration", () => {
  it("paints appended DOM ranges without changing text nodes and cleans up on settlement", () => {
    setup();
    mount("wor");
    expect(registry.size).toBe(0);
    const node = host.querySelector("p")!.firstChild;
    mount("world");
    expect(host.querySelector("p")!.firstChild).toBe(node);
    expect(host.textContent).toBe("world!Copy");
    const ranges = [...registry.values()].flatMap(highlight => [...highlight]);
    expect(ranges.map(range => range.toString()).join("")).toBe("ld!");
    const callback = frame!;
    frame = undefined;
    callback(performance.now() + 1000);
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
    mount("world next");
    expect(registry.size).toBeGreaterThan(0);
    mount("world next", false);
    expect(registry.size).toBeGreaterThan(0);
    act(() => {
      const finish = frame!;
      frame = undefined;
      finish(performance.now() + 1000);
    });
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
  });
  it("finishes pending fades on their original clock after completion", () => {
    setup();
    let now = 0;
    vi.spyOn(performance, "now").mockImplementation(() => now);
    mount("old");
    mount("old new");
    const [name, highlight] = [...registry][0];
    const tick = (time: number) => act(() => {
      now = time;
      const callback = frame!;
      frame = undefined;
      callback(time);
    });
    tick(100);
    mount("old new", false);
    expect(registry.get(name)).toBe(highlight);
    expect([...highlight].map(range => range.toString()).join("")).toBe(" new!");
    tick(399);
    expect(registry.get(name)).toBe(highlight);
    tick(400);
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
    expect(host.querySelector("[data-stream-veil]")).toBeNull();
    mount("old new", true);
    mount("old new again", true);
    expect(registry.size).toBeGreaterThan(0);
  });
  it("includes a final append while draining and cleans up if unmounted", () => {
    setup();
    mount("old");
    mount("old next");
    mount("old next final", false);
    expect([...registry.values()].flatMap(value => [...value]).map(range => range.toString()).join("")).toBe(" next final!");
    act(() => root.render(null));
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
  });
  it("does not animate a historical completed message", () => {
    setup();
    mount("complete", false);
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
    expect(host.textContent).toBe("complete!Copy");
  });
  it("keeps a promoted block's active range and rule across later tail updates", () => {
    setup();
    showBlocks(["history"], "wor");
    showBlocks(["history"], "world");
    const [name, highlight] = [...registry][0];
    const styles = [...document.querySelectorAll("style")];
    const sheet = styles.at(-1)!.sheet!;
    const rule = sheet.cssRules[0];
    showBlocks(["history", "world"], "");
    expect(registry.get(name)).toBe(highlight);
    expect(sheet.cssRules[0]).toBe(rule);
    const range = [...highlight][0];
    expect(range.toString()).toBe("ld");
    expect(range.startContainer.isConnected).toBe(true);
    showBlocks(["history", "world"], "next");
    expect(registry.get(name)).toBe(highlight);
    expect([...highlight][0]).toBe(range);
    expect(range.toString()).toBe("ld");
    expect(sheet.cssRules[0]).toBe(rule);
  });
  it("retains the stable portion of a fade when Markdown rewrites its live suffix", () => {
    setup();
    showBlocks(["history"], "");
    showBlocks(["history"], "oneTwo");
    const [name, highlight] = [...registry][0];
    showBlocks(["history", "one"], "Two");
    showBlocks(["history", "one"], "Tx");
    expect(registry.get(name)).toBe(highlight);
    expect([...highlight].map(range => range.toString()).join("")).toBe("oneT");
    expect([...registry.values()].flatMap(value => [...value]).map(range => range.toString()).join("")).toBe("oneTx");
  });
  it("does not read stable history when a short tail grows", () => {
    setup();
    const blocks = Array.from({ length: 1000 }, (_, index) => `history ${index}`);
    showBlocks(blocks, "tail");
    const readColor = vi.spyOn(window, "getComputedStyle");
    const walk = vi.spyOn(document, "createTreeWalker");
    showBlocks(blocks, "tail next");
    expect(readColor).toHaveBeenCalledTimes(1);
    expect(walk).toHaveBeenCalledTimes(1);
    expect(walk.mock.calls[0][0]).toBe(host.querySelector("div")!.lastElementChild);
    readColor.mockRestore();
    walk.mockRestore();
  });
  it("drops stale paint on source replacement and seeds the new message", () => {
    setup();
    showBlocks(["history"], "old");
    showBlocks(["history"], "old more");
    expect(registry.size).toBeGreaterThan(0);
    showBlocks([], "replacement", "new-source");
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
    showBlocks([], "replacement next", "new-source");
    expect([...registry.values()].flatMap(value => [...value]).map(range => range.toString()).join("")).toBe(" next");
  });
  it("carries fades through the real Markdown tail-to-stable-block handoff", () => {
    setup();
    const render = (text: string, live = true) => act(() => root.render(
      <StreamingMarkdown streamKey="handoff-paint" initialText={text} isLive={live} phase="final_answer" />,
    ));
    render("intro\n\nwor");
    render("intro\n\nworld");
    const [name, highlight] = [...registry][0];
    expect([...highlight].map(range => range.toString()).join("")).toBe("ld");
    render("intro\n\nworld\n\nnext");
    expect(registry.get(name)).toBe(highlight);
    expect([...highlight].map(range => range.toString()).join("")).toBe("ld");
    expect([...registry.values()].flatMap(value => [...value]).every(range => range.startContainer.isConnected)).toBe(true);
    render("intro\n\nworld\n\nnext", false);
    expect(registry.get(name)).toBe(highlight);
    act(() => {
      const finish = frame!;
      frame = undefined;
      finish(performance.now() + 1000);
    });
    expect(registry.size).toBe(0);
    expect(host.textContent).toContain("world");
  });
  it("honors the application motion preference independently of the OS", () => {
    document.documentElement.dataset.appearanceMotion = "reduce";
    setup();
    mount("old");
    mount("old new");
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
  });
  it("shows full text without animation when reduced motion is enabled", () => {
    reduced = true;
    setup();
    const readColor = vi.spyOn(window, "getComputedStyle");
    mount("old");
    mount("old 中文👩‍💻");
    expect(readColor).not.toHaveBeenCalled();
    readColor.mockRestore();
    expect(host.textContent).toBe("old 中文👩‍💻!Copy");
    expect(registry.size).toBe(0);
    expect(frame).toBeUndefined();
  });
});
