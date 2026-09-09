import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { RemoteItemContent } from "./RemoteItemContent";
vi.mock("./i18n", () => ({useI18n: () => ({t:(key:string)=>key})}));

it("loads full content only on request and retries without losing its preview", async () => {
  const prior = window.wuu;
  const full = {id:"item",type:"tool_call" as const,result:"complete output"};
  const read = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(full);
  window.wuu = {readRemoteItem:read} as unknown as typeof window.wuu;
  const container = document.createElement("div"),root=createRoot(container);
  document.body.append(container);
  try {
    await act(async () => root.render(<RemoteItemContent item={{...full,result:"preview",remote_content_ref:"content:ref"}} render={item => <span>{item.result}</span>} />));
    expect(read).not.toHaveBeenCalled();
    await act(async () => container.querySelector("button")!.click());
    expect(container.textContent).toContain("preview");
    expect(container.querySelector('[role="alert"]')?.textContent).toBe("offline");
    await act(async () => container.querySelector("button")!.click());
    expect(container.textContent).toBe("complete output");
    expect(container.querySelector("button")).toBeNull();
  } finally {act(() => root.unmount());container.remove();window.wuu=prior;}
});
