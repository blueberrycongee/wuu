import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginConversationView } from "./PluginConversation";

vi.mock("../JumpToLatestPill", () => ({
  JumpToLatestPill: () => <button data-testid="jump">Jump to latest</button>,
}));
const container = document.createElement("div");
document.body.append(container);
const root = createRoot(container);
afterEach(() => { act(() => root.unmount()); container.remove(); });

it("removes a plugin chat's portal control while its host is inert, then restores it", async () => {
  await act(async () => root.render(
    <main><PluginConversationView conversationId="visibility-test" title="Twin" searchLabel="Search" searchContent={null} composer={<textarea />}>
      <p>Earlier message</p>
    </PluginConversationView></main>,
  ));
  expect(container.querySelector('[data-testid="jump"]')).not.toBeNull();
  const host = container.querySelector("main")!;
  await act(async () => { host.setAttribute("inert", ""); });
  expect(container.querySelector('[data-testid="jump"]')).toBeNull();
  expect(container.textContent).toContain("Earlier message");
  await act(async () => { host.removeAttribute("inert"); });
  expect(container.querySelector('[data-testid="jump"]')).not.toBeNull();
});
