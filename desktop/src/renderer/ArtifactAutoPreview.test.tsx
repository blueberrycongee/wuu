import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Thread, ToolResultContentPart, Turn } from "../shared/protocol";
import { useArtifactAutoPreview } from "./ArtifactAutoPreview";

vi.mock("./plugins/DesktopPluginRuntime", () => ({ desktopWorkbenchController: {} }));

const file: ToolResultContentPart = {
  type: "file", name: "report.html", mime_type: "text/html",
  uri: "wuu-artifact://workspace/thread/output/report.html",
  artifact: { placement: "turn_end", sha256: "snapshot", size_bytes: 120 },
};

function thread(status: Turn["status"], parts = [file]): Thread {
  return { id: "thread", cwd: "workspace", turns: [{ id: "turn", status, items: [{
    id: "deliver", type: "tool_call", status: "completed", result_detail: { content: parts },
  }] }] } as Thread;
}

type Options = Parameters<typeof useArtifactAutoPreview>[0];
function Harness(props: Options): null { useArtifactAutoPreview(props); return null; }

let container: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
let options: Options;
const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  container = document.createElement("div");
  root = createRoot(container);
  options = { thread: thread("in_progress"), enabled: true, panelOpen: false, onOpen: vi.fn() };
  fetchMock.mockReset().mockResolvedValue(new Response("ready"));
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => { act(() => root.unmount()); vi.unstubAllGlobals(); vi.useRealTimers(); });

async function render(patch: Partial<Options> = {}): Promise<void> {
  options = { ...options, ...patch };
  await act(async () => root.render(<Harness {...options} />));
}

function deferReadiness(): { resolve: (response: Response) => void; signal: () => AbortSignal } {
  let resolve!: (response: Response) => void;
  fetchMock.mockReturnValue(new Promise<Response>((done) => { resolve = done; }));
  return { resolve, signal: () => fetchMock.mock.calls[0][1]!.signal as AbortSignal };
}

it("opens a ready delivery once on live completion and does not replay updates or dismissal", async () => {
  await render();
  expect(fetchMock).not.toHaveBeenCalled();
  await render({ thread: thread("completed") });
  expect(options.onOpen).toHaveBeenCalledWith(expect.objectContaining({ threadID: "thread", cwd: "workspace", artifact: expect.objectContaining({ uri: file.uri }) }));
  await render({ panelOpen: true, activeTabID: "preview" });
  await render({ panelOpen: false });
  await render({ thread: thread("completed") });
  expect(options.onOpen).toHaveBeenCalledTimes(1);
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

it("does not open history on hydration or navigation", async () => {
  await render({ thread: thread("completed") });
  await render({ thread: { ...thread("completed"), id: "other" } });
  await render({ thread: thread("completed") });
  expect(fetchMock).not.toHaveBeenCalled();
});

it.each(["failed", "interrupted"] as const)("retains deliveries without auto-opening a %s turn", async (status) => {
  await render();
  await render({ thread: thread(status) });
  expect(fetchMock).not.toHaveBeenCalled();
});

it.each([
  ["a reference", [{ ...file, artifact: undefined }]],
  ["an external resource", [{ ...file, uri: "https://example.com/report.html" }]],
  ["a workspace file", [{ ...file, uri: "report.html" }]],
  ["multiple files", [file, { ...file, name: "other.html" }]],
  ["an unsupported format", [{ ...file, mime_type: "application/zip" }]],
  ["an oversized file", [{ ...file, artifact: { ...file.artifact, size_bytes: 30 * 1024 * 1024 } }]],
] as const)("does not automatically fetch %s", async (_label, parts) => {
  await render();
  await render({ thread: thread("completed", [...parts]) });
  expect(fetchMock).not.toHaveBeenCalled();
});

it("does not open output from a failed tool even if the turn completes", async () => {
  await render();
  const failed = thread("completed");
  failed.turns[0].items[0].result_detail!.is_error = true;
  await render({ thread: failed });
  expect(fetchMock).not.toHaveBeenCalled();
});

it("deduplicates repeated delivery of the same snapshot", async () => {
  await render();
  const completed = thread("completed");
  completed.turns[0].items.push({ ...completed.turns[0].items[0], id: "repeat" });
  await render({ thread: completed });
  expect(options.onOpen).toHaveBeenCalledTimes(1);
});

it("respects panel activity during generation even after the panel is closed", async () => {
  await render();
  await render({ panelOpen: true, activeTabID: "files" });
  await render({ panelOpen: false });
  await render({ thread: thread("completed") });
  expect(fetchMock).not.toHaveBeenCalled();
});

it.each([
  ["thread navigation", () => ({ thread: { ...thread("completed"), id: "other" } })],
  ["workspace navigation", () => ({ thread: { ...thread("completed"), cwd: "another-workspace" } })],
  ["a new turn", () => ({ thread: { ...thread("in_progress"), turns: [{ ...thread("in_progress").turns[0], id: "next" }] } })],
  ["panel use", () => ({ panelOpen: true, activeTabID: "browser" })],
  ["an overlay, narrow layout or foreground browser", () => ({ enabled: false })],
] satisfies [string, () => Partial<Options>][])("cancels pending readiness on %s", async (_label, change) => {
  const ready = deferReadiness();
  await render();
  await render({ thread: thread("completed") });
  await render(change());
  expect(ready.signal().aborted).toBe(true);
  await act(async () => ready.resolve(new Response("ready")));
  expect(options.onOpen).not.toHaveBeenCalled();
});

it("does not restart a skipped completion when the layout becomes available", async () => {
  await render();
  await render({ thread: thread("completed"), enabled: false });
  await render({ enabled: true });
  expect(fetchMock).not.toHaveBeenCalled();
});

it("preserves a pending check across unrelated renders and uses the current callback", async () => {
  const ready = deferReadiness();
  await render();
  await render({ thread: thread("completed") });
  const previousOpen = options.onOpen;
  await render({ onOpen: vi.fn(), thread: thread("completed") });
  await act(async () => ready.resolve(new Response("ready")));
  expect(previousOpen).not.toHaveBeenCalled();
  expect(options.onOpen).toHaveBeenCalledTimes(1);
});

it("leaves unavailable output on its card without retrying", async () => {
  fetchMock.mockResolvedValue(new Response(null, { status: 404 }));
  await render();
  await render({ thread: thread("completed") });
  await render();
  expect(options.onOpen).not.toHaveBeenCalled();
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

it("aborts a stalled check and ignores late success", async () => {
  vi.useFakeTimers();
  const ready = deferReadiness();
  await render();
  await render({ thread: thread("completed") });
  await act(async () => vi.advanceTimersByTime(5_000));
  expect(ready.signal().aborted).toBe(true);
  await act(async () => ready.resolve(new Response("late")));
  expect(options.onOpen).not.toHaveBeenCalled();
});
