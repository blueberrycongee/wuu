import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { FieldError } from "./FieldError";

describe("FieldError", () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(() => {
    act(() => root.unmount());
    container.remove();
  });

  it("renders an addressable alert with a non-color cue", () => {
    act(() => root.render(<FieldError className="avatar-error" id="avatar-error">Choose a smaller image.</FieldError>));

    const alert = container.querySelector<HTMLElement>("[role=alert]");
    expect(alert?.id).toBe("avatar-error");
    expect(alert?.textContent).toBe("Choose a smaller image.");
    expect(alert?.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("does not reserve space without an error", () => {
    act(() => root.render(<FieldError />));

    expect(container.childElementCount).toBe(0);
  });
});
