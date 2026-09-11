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
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
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
    const icon = container.querySelector(".image-preview-toolbar-button .lucide-x");
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
    const button = container.querySelector(".lucide-x")?.closest("button");
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
    const button = container.querySelector<HTMLButtonElement>('.image-preview-toolbar-button')!;
    await act(async () => { button.click(); });
    expect(saveArtifactFile).toHaveBeenCalledWith("test.png","data:image/png;base64,aGVsbG8=");
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Storage full");
  } finally { window.wuu = previous; }
});
