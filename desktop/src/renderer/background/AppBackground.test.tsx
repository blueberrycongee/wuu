import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AppBackground } from "./AppBackground";
import type { BackgroundPreferences } from "./preferences";

const mocks = vi.hoisted(() => ({ preferences: null as BackgroundPreferences | null, process: vi.fn() }));
vi.mock("./useBackground", () => ({ useBackground: () => ({ preferences: mocks.preferences }) }));
vi.mock("./image", () => ({ processBackground: mocks.process }));
let root: Root, container: HTMLDivElement;
beforeEach(() => {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
  mocks.preferences = null; mocks.process.mockReset();
  vi.stubGlobal("URL", { createObjectURL: vi.fn(() => "blob:processed"), revokeObjectURL: vi.fn() });
});
afterEach(() => { act(() => root.unmount()); container.remove(); vi.unstubAllGlobals(); });
async function render() { await act(async () => root.render(<AppBackground />)); }

it("does not restore a removed background when a slow processing result arrives", async () => {
  let finish!: (blob: Blob) => void;
  mocks.process.mockReturnValue(new Promise<Blob>(resolve => { finish = resolve; }));
  mocks.preferences = { image: new Blob(["source"]), imageID: "one", name: "image.png", effect: "none", opacity: 0.15 };
  await render();
  const signal = mocks.process.mock.calls[0][3] as AbortSignal;
  mocks.preferences = null;
  await render();
  expect(signal.aborted).toBe(true);
  await act(async () => finish(new Blob(["late"])));
  expect(document.querySelector(".app-background")).toBeNull();
  expect(URL.createObjectURL).not.toHaveBeenCalled();
});

it("changes strength without reprocessing and releases the image URL on removal", async () => {
  mocks.process.mockResolvedValue(new Blob(["result"]));
  mocks.preferences = { image: new Blob(["source"]), imageID: "one", name: "image.png", effect: "none", opacity: 0.15 };
  await render();
  expect(document.querySelector(".app-background")).not.toBeNull();
  mocks.preferences = { ...mocks.preferences, opacity: 0.25 };
  await render();
  expect(mocks.process).toHaveBeenCalledOnce();
  mocks.preferences = null;
  await render();
  expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:processed");
  expect(document.querySelector(".app-background")).toBeNull();
});
