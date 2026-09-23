import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ImagePreviewProvider,
  useImagePreview,
  type ImagePreviewContextValue
} from "./ImagePreview";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(800);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(600);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 800, height: 600 } as DOMRect);
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  vi.restoreAllMocks();
});

function overlayImage(): HTMLImageElement | null {
  return container.querySelector(".image-preview-image");
}

function overlayRoot(): HTMLElement | null {
  return container.querySelector(".image-preview-overlay");
}

function renderWithProbe(): { getAPI: () => ImagePreviewContextValue | null } {
  const ref: { current: ImagePreviewContextValue | null } = { current: null };

  function Probe(): null {
    ref.current = useImagePreview();
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(
      <ImagePreviewProvider>
        <Probe />
      </ImagePreviewProvider>
    );
  });

  return {
    getAPI: () => ref.current
  };
}

describe("ImagePreviewProvider", () => {
  it("does not render the overlay when nothing is open", () => {
    renderWithProbe();
    expect(overlayRoot()).toBeNull();
  });

  it("renders the image when openPreview is called and hides it when closePreview is called", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({
        src: "data:image/png;base64,iVBORw0KGgo=",
        alt: "Sample",
        title: "Sample title"
      });
    });
    const previewImage = overlayImage();
    expect(previewImage).not.toBeNull();
    expect(previewImage?.getAttribute("src")).toContain("data:image/png");
    expect(previewImage?.getAttribute("alt")).toBe("Sample");

    act(() => {
      probe.getAPI()?.closePreview();
    });
    expect(overlayRoot()).toBeNull();
  });

  it("closes when the visible X icon is clicked", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,AAA" });
    });
    const icon = container.querySelector(".image-preview-toolbar-button .wuu-icon-x");
    expect(icon).not.toBeNull();
    act(() => {
      icon!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(overlayRoot()).toBeNull();
  });

  it("closes when the close button itself is clicked", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,AAA" });
    });
    const button = container.querySelector(".wuu-icon-x")?.closest("button");
    expect(button).not.toBeNull();
    act(() => {
      button!.click();
    });
    expect(overlayRoot()).toBeNull();
  });

  it("resets the visible image when openPreview receives a new source", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,AAA" });
    });
    expect(overlayImage()?.getAttribute("src")).toContain("AAA");
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,BBB" });
    });
    expect(overlayImage()?.getAttribute("src")).toContain("BBB");
  });

  it("closes the preview when the non-image stage area is clicked", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,AAA" });
    });
    const stage = container.querySelector(".image-preview-stage");
    expect(stage).not.toBeNull();
    act(() => {
      (stage as HTMLElement).click();
    });
    expect(overlayRoot()).toBeNull();
  });

  it("does not close the preview when the image itself is clicked", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({ src: "data:image/png;base64,AAA" });
    });
    const image = overlayImage();
    expect(image).not.toBeNull();
    act(() => {
      image?.click();
    });
    expect(overlayRoot()).not.toBeNull();
  });

  it("renders raw SVG markup directly when openPreview receives svg", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({
        svg: "<svg><text>Diagram</text></svg>",
        alt: "Diagram",
      });
    });
    const svg = container.querySelector(".image-preview-svg svg");
    expect(svg).not.toBeNull();
    expect(svg?.textContent).toBe("Diagram");
    expect(container.querySelector("img.image-preview-image")).toBeNull();
    expect(container.querySelector(".image-preview-status")).toBeNull();
  });

  it("does not render the title or alt text in the toolbar", () => {
    const probe = renderWithProbe();
    act(() => {
      probe.getAPI()?.openPreview({
        src: "data:image/png;base64,AAA",
        alt: "Should not show",
        title: "Should not show"
      });
    });
    expect(container.querySelector(".image-preview-title")).toBeNull();
    expect(overlayRoot()?.textContent ?? "").not.toContain("Should not show");
  });
});

describe("useImagePreview", () => {
  it("throws when used outside an ImagePreviewProvider", () => {
    function Naked(): null {
      useImagePreview();
      return null;
    }
    expect(() => {
      act(() => {
        root = createRoot(container);
        root.render(<Naked />);
      });
    }).toThrow(/ImagePreviewProvider/);
  });
});

it("shares the original image through the native host and reports a failed save", async () => {
  const previous = window.wuu;
  const saveArtifactFile = vi.fn().mockRejectedValue(new Error("Storage full"));
  window.wuu = { ...previous, saveArtifactFile };
  try {
    const probe = renderWithProbe();
    act(() => probe.getAPI()!.openPreview({src:"data:image/png;base64,aGVsbG8=",title:"test.png"}));
    loadImage();
    const button = container.querySelector<HTMLButtonElement>('.image-preview-toolbar-button')!;
    await act(async () => { button.click(); });
    expect(saveArtifactFile).toHaveBeenCalledWith("test.png","data:image/png;base64,aGVsbG8=");
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Storage full");
  } finally { window.wuu = previous; }
});

function loadImage(): void {
  const image = overlayImage()!;
  Object.defineProperties(image, { naturalWidth: { configurable: true, value: 1600 }, naturalHeight: { configurable: true, value: 1200 } });
  act(() => image.dispatchEvent(new Event("load")));
}

function key(key: string, options: KeyboardEventInit = {}): void {
  act(() => document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...options })));
}

function transform(): { x: number; y: number; scale: number } {
  const values = overlayImage()!.style.transform.match(/translate\(([-.\d]+)px, ([-.\d]+)px\) scale\(([-.\d]+)\)/)!;
  return { x: Number(values[1]), y: Number(values[2]), scale: Number(values[3]) };
}

it("zooms smoothly around the pinch position, pans with two-finger scrolling, and fits back into view", () => {
  const probe = renderWithProbe();
  act(() => probe.getAPI()!.openPreview({ src: "data:image/png;base64,AAA" }));
  loadImage();
  key("1");
  const stage = container.querySelector(".image-preview-stage")!;
  const wheel = new WheelEvent("wheel", { ctrlKey: true, deltaY: -10, clientX: 600, clientY: 400, cancelable: true });
  act(() => stage.dispatchEvent(wheel));
  expect(wheel.defaultPrevented).toBe(true);
  expect(transform().scale).toBeCloseTo(Math.exp(0.1));
  expect(transform().x).toBeCloseTo(200 * (1 - Math.exp(0.1)));
  expect(transform().y).toBeCloseTo(100 * (1 - Math.exp(0.1)));
  const before = transform();
  act(() => stage.dispatchEvent(new WheelEvent("wheel", { deltaX: 30, deltaY: 20, cancelable: true })));
  expect(transform().scale).toBe(before.scale);
  expect(transform().x).toBeCloseTo(before.x - 30);
  key("0");
  expect(transform()).toEqual({ x: 0, y: 0, scale: 568 / 1200 });
});

it("keeps the image reachable after extreme panning and zooming out, and resets for the next image", () => {
  const probe = renderWithProbe();
  act(() => probe.getAPI()!.openPreview({ src: "data:image/png;base64,AAA" }));
  loadImage();
  key("1");
  const stage = container.querySelector(".image-preview-stage")!;
  act(() => stage.dispatchEvent(new WheelEvent("wheel", { deltaX: 10000, deltaY: 10000 })));
  expect(transform()).toEqual({ x: -416, y: -316, scale: 1 });
  act(() => stage.dispatchEvent(new WheelEvent("wheel", { ctrlKey: true, deltaY: 10000 })));
  expect(transform()).toEqual({ x: 0, y: 0, scale: 0.05 });
  key("r");
  expect(overlayImage()!.style.transform).toContain("rotate(90deg)");
  expect(transform().scale).toBeCloseTo(568 / 1600);
  act(() => probe.getAPI()!.openPreview({ src: "data:image/png;base64,BBB" }));
  loadImage();
  expect(overlayImage()!.style.transform).toContain("rotate(0deg)");
  expect(transform()).toEqual({ x: 0, y: 0, scale: 568 / 1200 });
});

it("does not close when a captured drag releases on the stage", () => {
  const probe = renderWithProbe();
  act(() => probe.getAPI()!.openPreview({ src: "data:image/png;base64,AAA" }));
  loadImage();
  key("1");
  const stage = container.querySelector<HTMLElement>(".image-preview-stage")!;
  stage.setPointerCapture = vi.fn();
  stage.hasPointerCapture = () => true;
  stage.releasePointerCapture = vi.fn();
  function pointer(type: string, x: number): void {
    const event = new MouseEvent(type, { bubbles: true, button: 0, clientX: x, clientY: 200 });
    Object.defineProperty(event, "pointerId", { value: 1 });
    act(() => (type === "pointerdown" ? overlayImage()! : stage).dispatchEvent(event));
  }
  pointer("pointerdown", 200);
  pointer("pointermove", 260);
  const transfer = new Event("lostpointercapture", { bubbles: true });
  Object.defineProperty(transfer, "pointerId", { value: 1 });
  act(() => overlayImage()!.dispatchEvent(transfer));
  pointer("pointermove", 320);
  pointer("pointerup", 320);
  act(() => stage.click());
  expect(overlayRoot()).not.toBeNull();
  expect(transform().x).toBe(120);
  act(() => stage.click());
  expect(overlayRoot()).toBeNull();
});

it("contains keyboard focus and Escape without triggering conversation shortcuts", () => {
  const opener = document.createElement("button");
  document.body.append(opener);
  opener.focus();
  const probe = renderWithProbe();
  const underlying = vi.fn();
  window.addEventListener("keydown", underlying);
  try {
    act(() => probe.getAPI()!.openPreview({ src: "data:image/png;base64,AAA" }));
    loadImage();
    expect(document.activeElement).toBe(overlayRoot());
    key("Tab", { shiftKey: true });
    expect(document.activeElement).toBe(container.querySelector(".wuu-icon-x")!.closest("button"));
    key("Tab");
    expect(overlayRoot()!.contains(document.activeElement)).toBe(true);
    key("Escape");
    expect(overlayRoot()).toBeNull();
    expect(document.activeElement).toBe(opener);
    expect(underlying).not.toHaveBeenCalled();
  } finally {
    window.removeEventListener("keydown", underlying);
    opener.remove();
  }
});
