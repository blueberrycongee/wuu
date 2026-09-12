import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChannelContinuityResult, WuuDesktopApi } from "../shared/protocol";
import { ChannelContinuity } from "./ChannelContinuity";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
let host: HTMLDivElement;
let root: Root;
let api: Partial<WuuDesktopApi>;
const plan = { id: "weekly", owner_id: "agent", room_id: "room", scope: "agent" as const, mode: "wake" as const, note: "Weekly review", state: "active" as const, next_at: "2026-09-18T01:00:00Z", revision: 7 };
beforeEach(() => {
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  api = { channelContinuity: vi.fn(async () => ({ arrangements: [plan] })) };
  Object.defineProperty(window, "wuu", { configurable: true, value: api });
});
afterEach(() => { act(() => root.unmount()); host.remove(); });
async function render(roomId = "room") { await act(async () => root.render(<WuuUIRoot><ChannelContinuity roomId={roomId} agents={[]} /></WuuUIRoot>)); }
async function open() { await act(async () => document.querySelector<HTMLButtonElement>(".channel-continuity-launcher")!.click()); }
it("controls the observed revision and reloads the actual saved state", async () => {
  api.channelContinuity = vi.fn(async p => p.action === "control" ? {} : { arrangements: [plan] });
  await render(); await open();
  const pause = document.querySelector<HTMLButtonElement>(".channel-continuity-actions button")!;
  await act(async () => pause.click());
  expect(api.channelContinuity).toHaveBeenCalledWith({ action: "control", roomId: "room", id: "weekly", state: "paused", revision: 7 });
  expect(api.channelContinuity).toHaveBeenCalledTimes(3);
});
it("does not display another room's late plans", async () => {
  let release!: (result: ChannelContinuityResult) => void;
  api.channelContinuity = vi.fn().mockImplementationOnce(() => new Promise(resolve => { release = resolve; })).mockResolvedValue({ arrangements: [] });
  await render(); await open(); await render("other-room");
  await act(async () => release({ arrangements: [plan] }));
  expect(document.querySelector(".channel-continuity-body")!.textContent).not.toContain("Weekly review");
});
