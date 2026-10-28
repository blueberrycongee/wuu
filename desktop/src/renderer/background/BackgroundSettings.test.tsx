import { PluginLifecycleCancellationError } from "../../shared/PluginLifecycle";
import { SettingsControlsProvider } from "../SettingsControlsContext";
import { createApplicationSettingsControls } from "../plugins/hostApplicationSettingsControls";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BackgroundSettings } from "./BackgroundSettings";
import type { BackgroundPreferences } from "./preferences";

const mocks = vi.hoisted(() => ({
  preferences: null as BackgroundPreferences | null,
  process: vi.fn(),
  update: vi.fn(),
}));
vi.mock("./image", () => ({ processBackground: mocks.process }));
vi.mock("./useBackground", () => ({
  useBackground: () => ({ preferences: mocks.preferences, error: false, loading: false }),
}));
vi.mock("./preferences", async importOriginal => ({
  ...await importOriginal<typeof import("./preferences")>(),
  updateBackground: mocks.update,
}));

let root: Root;
let container: HTMLDivElement;
beforeEach(() => {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  mocks.preferences = { image: new Blob(["original"]), imageID: "original", name: "original.png" };
  mocks.process.mockReset();
  mocks.update.mockReset().mockImplementation(async update => {
    mocks.preferences = update(mocks.preferences);
  });
});
afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

async function mount() {
  await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{<BackgroundSettings />}</SettingsControlsProvider>));
}
async function remount() {
  await act(async () => root.render(<SettingsControlsProvider controls={createApplicationSettingsControls()}>{null}</SettingsControlsProvider>));
  await mount();
}
async function chooseImage() {
  const input = container.querySelector<HTMLInputElement>("input[type=file]")!;
  Object.defineProperty(input, "files", { configurable: true, value: [new File(["source"], "replacement.png", { type: "image/png" })] });
  await act(async () => input.dispatchEvent(new Event("change", { bubbles: true })));
}
async function removeImage() {
  await act(async () => container.querySelector<HTMLButtonElement>("button.settings-button")!.click());
  expect(mocks.preferences).toBeNull();
}

it.each(["unmount", "renewal"])("cancels an import on %s and ignores its late result after a later removal", async mode => {
  let finish!: (image: Blob) => void;
  mocks.process.mockReturnValue(new Promise<Blob>(resolve => { finish = resolve; }));
  await mount();
  await chooseImage();
  const signal = mocks.process.mock.calls[0][2] as AbortSignal | undefined;
  if (mode === "unmount") await remount();
  else await mount();
  await removeImage();
  await act(async () => finish(new Blob(["late result"])));
  expect(mocks.preferences).toBeNull();
  expect(signal?.aborted).toBe(true);
});

it.each(["unmount", "renewal"])("does not write an import retired by %s when its storage transaction starts late", async mode => {
  let commit!: () => void;
  mocks.process.mockResolvedValue(new Blob(["processed"]));
  mocks.update.mockImplementationOnce(update => new Promise<void>(resolve => {
    commit = () => { mocks.preferences = update(mocks.preferences); resolve(); };
  }));
  await mount();
  await chooseImage();
  if (mode === "unmount") await remount();
  else await mount();
  await removeImage();
  await act(async () => commit());
  expect(mocks.preferences).toBeNull();
});

it("persists an active import with a new image identity", async () => {
  const image = new Blob(["processed"]);
  mocks.process.mockResolvedValue(image);
  await mount();
  await chooseImage();
  expect(mocks.preferences).toMatchObject({ image, name: "replacement.png" });
  expect(mocks.preferences?.imageID).not.toBe("original");
  expect(container.querySelector<HTMLInputElement>("input[type=file]")!.disabled).toBe(false);
});

it.each(["processing", "storage"])("retains the saved image and allows retry after %s fails", async stage => {
  const previous = mocks.preferences;
  mocks.process.mockResolvedValue(new Blob(["processed"]));
  if (stage === "processing") mocks.process.mockRejectedValueOnce(new Error("decode failed"));
  else mocks.update.mockRejectedValueOnce(new Error("write failed"));
  await mount();
  await chooseImage();
  expect(mocks.preferences).toBe(previous);
  expect(container.querySelector("[role=alert]")).not.toBeNull();
  expect(container.querySelector<HTMLInputElement>("input[type=file]")!.disabled).toBe(false);
  await chooseImage();
  expect(mocks.preferences?.name).toBe("replacement.png");
  expect(container.querySelector("[role=alert]")).toBeNull();
});

it("keeps a retired-authority cancellation quiet while the retained form awaits new controls", async () => {
  mocks.process.mockRejectedValue(new PluginLifecycleCancellationError());
  await mount();
  await chooseImage();
  expect(container.querySelector("[role=alert]")).toBeNull();
});
