import type { ReactNode } from "react";
import { ArtifactsProvider } from "./ArtifactsContext";
import { ApplicationArtifacts } from "./plugins/ApplicationArtifacts";
import { act } from "react";
import { createRoot as createReactRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { ConversationControlsTestProvider, createConversationTestApplication } from "./ConversationControlsTestProvider";
import { ConversationTurnIdentityProvider } from "./ConversationControlsContext";
import { RemoteItemContent } from "./RemoteItemContent";
vi.mock("./i18n", () => ({useI18n: () => ({t:(key:string)=>key})}));

it("loads full content only on request and retries without losing its preview", async () => {
  const full = {id:"item",type:"tool_call" as const,result:"complete output"};
  const read = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(full);
  const application = { ...createConversationTestApplication(), invoke: read };
  const container = document.createElement("div"),root=createRoot(container);
  document.body.append(container);
  try {
    await act(async () => root.render(<ConversationControlsTestProvider service={application}><ConversationTurnIdentityProvider threadId="thread" turnId="turn"><RemoteItemContent item={{...full,result:"preview",remote_content_ref:"content:ref"}} render={item => <span>{item.result}</span>} /></ConversationTurnIdentityProvider></ConversationControlsTestProvider>));
    expect(read).not.toHaveBeenCalled();
    await act(async () => container.querySelector("button")!.click());
    expect(read).toHaveBeenCalledWith("conversation.load-item", { threadId: "thread", turnId: "turn", itemId: "item" }, { signal: expect.any(AbortSignal) });
    expect(container.textContent).toContain("preview");
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("offline");
    await act(async () => container.querySelector("button")!.click());
    expect(container.textContent).toBe("complete output");
    expect(container.querySelector("button")).toBeNull();
  } finally {act(() => root.unmount());container.remove();}
});

function createRoot(container: HTMLElement) {
  const root = createReactRoot(container);
  const artifacts = new ApplicationArtifacts(window.wuu ?? {}, {resolveResourceURL: () => undefined, openFile: async () => {}});
  return {...root, render: (children: ReactNode) => root.render(<ArtifactsProvider application={{artifacts}}>{children}</ArtifactsProvider>), unmount: () => root.unmount()};
}
