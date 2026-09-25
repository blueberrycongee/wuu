import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";
import { ConversationContext } from "./ConversationContext";

it("edits the selected memory scope and fences timer controls with its loaded revision", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const api = vi.fn(async (p: { action: string }) => p.action === "memory"
    ? { entries: [{ name: "MEMORY.md", revision: "loaded-memory", content: "Start the search service" }] }
    : { arrangements: [{ id: "timer", state: "active", revision: 3, note: "Check search", next_at: "2026-09-26T09:00:00Z" }] });
  Object.defineProperty(window, "wuu", { configurable: true, value: { channelContinuity: api } as unknown as WuuDesktopApi });
  const click = async (text: string) => { await act(async () => [...container.querySelectorAll("button")].find(button => button.textContent === text)!.click()); };
  try {
    await act(async () => root.render(<ConversationContext roomID="project-dm" agentID="identity" />));
    await act(async () => { const details = container.querySelector("details")!; details.open = true; details.dispatchEvent(new Event("toggle")); });
    await click("身份记忆"); await click("MEMORY.md");
    await act(async () => {
      const editor = container.querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "Use local ES");
      editor.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click("保存");
    expect(api).toHaveBeenCalledWith({ action: "memory", roomId: "project-dm", ownerId: "identity", memory: { action: "write", name: "MEMORY.md", revision: "loaded-memory", content: "Use local ES" } });
    await click("定时任务"); await click("暂停");
    expect(api).toHaveBeenCalledWith({ action: "control", roomId: "project-dm", id: "timer", state: "paused", revision: 3 });
  } finally { await act(async () => root.unmount()); container.remove(); }
});
