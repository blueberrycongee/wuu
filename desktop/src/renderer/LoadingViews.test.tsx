import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { ViewSwitchLoading } from "./LoadingViews";

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function render(view: JSX.Element): HTMLDivElement {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => root?.render(view));
  return container;
}

afterEach(() => {
  act(() => root?.unmount());
  root = undefined;
  container?.remove();
  container = undefined;
});

describe("ViewSwitchLoading", () => {

  it("shows view switching outside the calling pane and removes it on unmount", () => {
    const view = render(<ViewSwitchLoading />);
    const status = document.querySelector('[role="status"]');

    expect(status).not.toBeNull();
    expect(status?.parentElement).toBe(document.body);
    expect(view.contains(status)).toBe(false);
    act(() => root?.unmount());
    root = undefined;
    expect(document.querySelector('[role="status"]')).toBeNull();
  });

  it("can embed connection progress beside recovery controls", () => {
    const view = render(<section><ViewSwitchLoading inline /><button>Reconnect</button></section>);
    expect(view.querySelector('[role="status"]')?.parentElement).toBe(view.querySelector("section"));
    expect(view.querySelector("button")).not.toBeNull();
  });
});
