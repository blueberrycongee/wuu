import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import { useComposerHeight } from "./useComposerHeight";

it("keeps the collapsed dock footprint when a restored draft adds a toolbar row", () => {
  const host = document.createElement("div");
  host.innerHTML = '<div class="composer-stack"><div class="composer"><textarea style="min-height:36px;max-height:330px;border:0"></textarea></div></div>';
  document.body.append(host);
  const stack = host.querySelector<HTMLElement>(".composer-stack")!;
  const composer = host.querySelector<HTMLElement>(".composer")!;
  const input = host.querySelector("textarea")!;
  // Browser measurements from the compact and full-width multiline layouts.
  // The extra toolbar row must grow upward with the text, not enlarge the dock.
  Object.defineProperties(input, {
    offsetWidth: { get: () => 736 },
    offsetHeight: { get: () => Math.max(36, parseFloat(input.style.height) || 0) },
    scrollHeight: { get: () => 36 + (input.value.split("\n").length - 1) * 24 },
    checkVisibility: { value: () => true },
  });
  Object.defineProperty(composer, "offsetHeight", {
    get: () => input.offsetHeight + (stack.dataset.multiline ? 48 : 8),
  });
  const ref = { current: input };
  function Probe({ value }: { value: string }) { useComposerHeight(ref, value); return null; }
  const mount = document.createElement("div");
  host.append(mount);
  const root = createRoot(mount);
  const render = (value: string) => {
    input.value = value;
    act(() => root.render(<Probe value={value} />));
  };
  const footprint = () => composer.offsetHeight - (parseFloat(stack.style.getPropertyValue("--composer-input-growth")) || 0);
  try {
    render("");
    const collapsed = footprint();
    render("First line\nSecond line\nThird line");
    expect(input.offsetHeight).toBeGreaterThan(36);
    expect(footprint()).toBe(collapsed);
    render("First line\nSecond line\nThird line\nFourth line");
    expect(footprint()).toBe(collapsed);
    render("");
    expect(footprint()).toBe(collapsed);
  } finally {
    act(() => root.unmount());
    host.remove();
  }
});
