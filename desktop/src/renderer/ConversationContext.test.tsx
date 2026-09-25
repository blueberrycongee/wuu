import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";
import { ConversationContextPanel, useConversationTimers, type ConversationContextTab } from "./ConversationContext";
import { translateCurrent as t } from "./i18n";

function Panel() {
  const [tab, setTab] = useState<ConversationContextTab>("memory");
  const timers = useConversationTimers("project-dm", 0);
  return <ConversationContextPanel roomID="project-dm" agentID="identity" agentName="Ada" projectName="catalog" tab={tab} timers={timers}
    overlay={false} closing={false} onTabChange={setTab} onClose={() => {}} />;
}

it("edits the selected memory scope and fences timer controls with its loaded revision", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const api = vi.fn(async (p: { action: string; ownerId?: string; memory?: { action: string } }) => p.action === "memory"
    ? { entries: p.memory?.action === "list" && !p.ownerId ? [] : [{ name: "MEMORY.md", revision: "loaded-memory", content: "Start the search service" }] }
    : { arrangements: [{ id: "timer", state: "active", revision: 3, note: "Check search", next_at: "2026-09-26T09:00:00Z" }] });
  Object.defineProperty(window, "wuu", { configurable: true, value: { channelContinuity: api } as unknown as WuuDesktopApi });
  const click = async (element: Element | undefined | null) => { expect(element).toBeTruthy(); await act(async () => (element as HTMLElement).click()); };
  const button = (label: string) => [...container.querySelectorAll("button")].find(item => item.textContent === label || item.getAttribute("aria-label") === label);
  try {
    await act(async () => root.render(<Panel />));
    const identity = [...container.querySelectorAll("section")].find(section => section.getAttribute("aria-label") === t("channels.context.identityGroup", { name: "Ada" }));
    await click(identity?.querySelector(".conversation-context-row"));
    await click(button(t("common.edit")));
    await act(async () => {
      const editor = container.querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "Use local ES");
      editor.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await click(button(t("common.save")));
    expect(api).toHaveBeenCalledWith({ action: "memory", roomId: "project-dm", ownerId: "identity", memory: { action: "write", name: "MEMORY.md", revision: "loaded-memory", content: "Use local ES" } });
    await click(button(t("channels.context.timers")));
    await click(button(t("channels.context.timerActions")));
    await click([...document.querySelectorAll('[role="menuitem"]')].find(item => item.textContent === t("channels.context.pause")));
    expect(api).toHaveBeenCalledWith({ action: "control", roomId: "project-dm", id: "timer", state: "paused", revision: 3 });
  } finally { await act(async () => root.unmount()); container.remove(); }
});
