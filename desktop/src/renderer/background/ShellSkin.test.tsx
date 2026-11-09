import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useSkinImage, type AvailableShellSkin } from "./ShellSkin";

const skin: AvailableShellSkin = {
  id: "art", name: "Art", key: "p:art", pluginId: "p", fingerprint: "f", pluginName: "P",
  light: { background: "#000", foreground: "#fff", image: "light.webp" },
  dark: { background: "#111", foreground: "#eee", image: "dark.webp" },
};

let latest: ReturnType<typeof useSkinImage>;
function Probe({ load }: { load: (input: { id: string; fingerprint: string; path: string }) => Promise<string> }): null {
  latest = useSkinImage(skin, "light", { loadSkinImage: load });
  return null;
}

let root: Root, container: HTMLDivElement;
beforeEach(() => {
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
});
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers(); });

async function flush() { await act(async () => Promise.resolve()); }

it("retries a transient read failure before falling back to the error state", async () => {
  vi.useFakeTimers();
  const load = vi.fn().mockRejectedValueOnce(new Error("plugin catalog is being published")).mockResolvedValue("wuu-plugin://asset/x");
  await act(async () => root.render(<Probe load={load} />));
  expect(latest.url).toBe("");
  await act(async () => { await vi.advanceTimersByTimeAsync(500); });
  expect(load).toHaveBeenCalledTimes(2);
  await flush();
  expect(latest).toMatchObject({ url: "wuu-plugin://asset/x", error: false });
});

it("marks the image failed once the retry delays run out", async () => {
  vi.useFakeTimers();
  const load = vi.fn().mockRejectedValue(new Error("plugin catalog is being published"));
  await act(async () => root.render(<Probe load={load} />));
  await act(async () => { await vi.advanceTimersByTimeAsync(11_000); });
  expect(load).toHaveBeenCalledTimes(5);
  expect(latest).toMatchObject({ url: "", error: true });
});

it("drops the dead URL and reloads when the <img> request fails", async () => {
  const load = vi.fn().mockResolvedValue("wuu-plugin://asset/x");
  await act(async () => root.render(<Probe load={load} />));
  expect(latest.url).toBe("wuu-plugin://asset/x");
  act(() => latest.retry());
  expect(latest.url).toBe("");
  await flush();
  expect(load).toHaveBeenCalledTimes(2);
  expect(latest).toMatchObject({ url: "wuu-plugin://asset/x", error: false });
});

it("bounds img-triggered reloads and settles on the error state", async () => {
  const load = vi.fn().mockResolvedValue("wuu-plugin://asset/x");
  await act(async () => root.render(<Probe load={load} />));
  act(() => latest.retry()); await flush();
  act(() => latest.retry()); await flush();
  act(() => latest.retry());
  expect(latest).toMatchObject({ url: "", error: true });
});
