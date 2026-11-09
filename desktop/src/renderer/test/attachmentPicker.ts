import { act } from "react";
import { expect, vi } from "vitest";
import { translateCurrent } from "../i18n";

// Resolve the host-owned picker through this composer's public action. Its DOM
// can live outside a replaceable presenter; a global input query would silently
// choose another conversation's picker in split-pane tests.
export function openComposerAttachmentPicker(owner: Element): HTMLInputElement {
  const clicked: HTMLInputElement[] = [];
  const originalClick = HTMLInputElement.prototype.click;
  const click = vi.spyOn(HTMLInputElement.prototype, "click").mockImplementation(function (this: HTMLInputElement) {
    if (this.type === "file") clicked.push(this);
    originalClick.call(this);
  });
  try {
    const plus = owner.querySelector<HTMLButtonElement>(".composer-plus-button");
    if (plus) {
      expect(document.querySelector('[data-floating-menu-owner="composer-plus"]')).toBeNull();
      act(() => plus.click());
      const menu = document.querySelector('[data-floating-menu-owner="composer-plus"]');
      const attachment = Array.from(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])
        .find((button) => button.textContent?.includes(translateCurrent("composer.addAttachment")));
      expect(attachment).toBeDefined();
      act(() => attachment!.click());
    } else {
      const attachment = owner.querySelector<HTMLButtonElement>(".composer-attach-button");
      expect(attachment).not.toBeNull();
      act(() => attachment!.click());
    }
    expect(clicked).toHaveLength(1);
    expect(clicked[0].isConnected).toBe(true);
    return clicked[0];
  } finally {
    click.mockRestore();
  }
}
