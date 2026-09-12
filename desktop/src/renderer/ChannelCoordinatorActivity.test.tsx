import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { NamedAgent } from "../shared/protocol";
import { ChannelCoordinatorActivity } from "./ChannelCoordinatorActivity";

const container = document.createElement("div");
let root = createRoot(container);
afterEach(async () => { await act(async () => root.unmount()); container.replaceChildren(); root = createRoot(container); });

it("shows responsible members without exposing the hidden session identity", async () => {
  await act(async () => root.render(<ChannelCoordinatorActivity
    status={{ state: "waiting", session_ref: "private-coordination-session", agent_ids: ["alice", "removed-member"] }}
    agents={[{ id: "alice", name: "Alice" } as NamedAgent]} onRetry={vi.fn()} />));
  expect(container.querySelector('[role="status"]')?.getAttribute("aria-label")).toContain("Alice");
  expect(container.textContent).toBe("");
  expect(container.querySelector('[data-agent-avatar-id="alice"]')).not.toBeNull();
  expect(container.textContent).not.toContain("private-coordination-session");
  expect(container.textContent).not.toContain("removed-member");
  expect(container.textContent).not.toContain("{");
  expect(container.querySelector("button")).toBeNull();
});

it("retries the failed coordinator once and reports a retry failure", async () => {
  let reject!: (error: Error) => void;
  const onRetry = vi.fn(() => new Promise<void>((_, rejectPromise) => { reject = rejectPromise; }));
  await act(async () => root.render(<ChannelCoordinatorActivity
    status={{ state: "failed", session_ref: "room-session", error: "Provider unavailable" }} agents={[]} onRetry={onRetry} />));
  const button = container.querySelector("button")!;
  await act(async () => button.click());
  expect(onRetry).toHaveBeenCalledExactlyOnceWith("room-session");
  expect(button.disabled).toBe(true);
  await act(async () => button.click());
  expect(onRetry).toHaveBeenCalledTimes(1);
  await act(async () => reject(new Error("Still unavailable")));
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("Still unavailable");
  expect(button.disabled).toBe(false);
});

it("removes coordination feedback when the room is idle", async () => {
  await act(async () => root.render(<ChannelCoordinatorActivity status={{ state: "idle" }} agents={[]} onRetry={vi.fn()} />));
  expect(container.childElementCount).toBe(0);
});

it("hands off to actual member activity without leaving duplicate accessible avatars", async () => {
  const agents = [{ id: "alice", name: "Alice" } as NamedAgent];
  await act(async () => root.render(<ChannelCoordinatorActivity
    status={{ state: "working" }} agents={agents} onRetry={vi.fn()} />));
  expect(container.querySelector("svg")).not.toBeNull();
  expect(container.textContent).toBe("");
  await act(async () => root.render(<ChannelCoordinatorActivity
    status={{ state: "waiting", agent_ids: ["alice"] }} agents={agents} activeAgentIDs={["alice"]} onRetry={vi.fn()} />));
  expect(container.querySelector(".channel-activity-slot:not([inert])")).toBeNull();
  expect(container.querySelector('[data-agent-avatar-id="alice"]')).toBeNull();
});
