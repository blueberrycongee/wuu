import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SidebarSettingsMenu } from "./SidebarSettingsMenu";

let root: Root;
let container: HTMLDivElement;
afterEach(() => { act(() => root?.unmount()); container?.remove(); });
async function mount(disabled = false) {
  container = document.createElement("div"); document.body.append(container);
  root = createRoot(container);
  const navigate = vi.fn();
  await act(async () => root.render(<SidebarSettingsMenu disabled={disabled} onOpenSettings={navigate} />));
  return navigate;
}
const gear = () => container.querySelector<HTMLButtonElement>(".sidebar-settings-trigger")!;

describe("SidebarSettingsMenu", () => {
  it("opens Settings directly from the gear", async () => {
    const navigate = await mount();
    expect(gear().getAttribute("data-settings-entry")).toBe("settings");
    await act(async () => gear().click());
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate.mock.calls[0]).toHaveLength(0);
    expect(document.querySelector(".sidebar-account-menu")).toBeNull();
  });
  it("does not open Settings while disabled", async () => {
    const navigate = await mount(true);
    await act(async () => gear().click());
    expect(navigate).not.toHaveBeenCalled();
  });
});
