import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ChannelActivityPresence } from "./ChannelActivityPresence";

const container = document.createElement("div");
let root = createRoot(container);
afterEach(async () => { await act(async () => root.unmount()); vi.useRealTimers(); container.replaceChildren(); root = createRoot(container); });

it("makes departing controls inert immediately and cancels removal when work resumes", async () => {
  vi.useFakeTimers();
  const render = (active: boolean) => act(async () => root.render(<ChannelActivityPresence>
    {active ? <button key="worker">Worker</button> : null}
  </ChannelActivityPresence>));
  await render(true);
  const button = container.querySelector("button");
  await render(false);
  expect(button?.closest("[inert][aria-hidden=true]")).not.toBeNull();
  await render(true);
  expect(container.querySelector("button")).toBe(button);
  expect(button?.closest("[inert]")).toBeNull();
  await act(async () => vi.advanceTimersByTime(500));
  expect(container.querySelectorAll("button")).toHaveLength(1);
  await render(false);
  await act(async () => vi.advanceTimersByTime(500));
  expect(container.querySelector("button")).toBeNull();
});
