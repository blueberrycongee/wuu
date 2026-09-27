import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InstructionFilesCard, type InstructionFilesEntry } from "./InstructionFilesCard";
import { hoverTooltipText, unhoverTooltip } from "./tooltipTestUtils";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  container = document.createElement("div");
  document.body.appendChild(container);
});

afterEach(() => {
  unhoverTooltip();
  act(() => {
    root?.unmount();
  });
  root = null;
  container.remove();
  vi.useRealTimers();
});

function renderCard(entry: InstructionFilesEntry, onDismiss?: (id: string) => void): void {
  act(() => {
    root = createRoot(container);
    root.render(<InstructionFilesCard entry={entry} onDismiss={onDismiss} />);
  });
}

function click(element: Element | null): void {
  if (!element) {
    throw new Error("element not found");
  }
  act(() => {
    element.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
}

describe("InstructionFilesCard", () => {
  it("shows an error state", () => {
    renderCard({ id: "e1", threadID: "t1", loading: false, error: "读取失败" });
    expect(container.textContent).toContain("读取失败");
  });

  it("groups files by scope and hides preview until expanded", async () => {
    renderCard({
      id: "e1",
      threadID: "t1",
      loading: false,
      result: {
        files: [
          {
            path: "/home/u/.config/wuu/AGENTS.md",
            name: "AGENTS.md",
            source: "user",
            scope: "global",
            bytes: 12,
            content: "global rules",
          },
          {
            path: "/repo/AGENTS.md",
            name: "AGENTS.md",
            source: "project",
            scope: "project",
            bytes: 20,
            content: "project instructions",
          },
        ],
      },
    });

    // Both scope groups render.
    expect(container.textContent).toContain("全局");
    expect(container.textContent).toContain("工作区");
    const toggles = Array.from(container.querySelectorAll(".instruction-file-toggle"));
    expect(await hoverTooltipText(toggles[0])).toBe("/home/u/.config/wuu/AGENTS.md");
    expect(await hoverTooltipText(toggles[1])).toBe("/repo/AGENTS.md");
    // Content is collapsed by default.
    expect(container.querySelector(".instruction-file-preview")).toBeNull();

    // Expanding the first file reveals its content.
    const firstToggle = container.querySelector(".instruction-file-toggle");
    click(firstToggle);
    const preview = container.querySelector(".instruction-file-preview");
    expect(preview).not.toBeNull();
    expect(preview?.textContent).toContain("global rules");
  });

  it("invokes onDismiss when the dismiss button is clicked", () => {
    const onDismiss = vi.fn();
    renderCard(
      {
        id: "entry-42",
        threadID: "t1",
        loading: false,
        result: { files: [] },
      },
      onDismiss,
    );
    click(container.querySelector(".instruction-files-dismiss"));
    expect(onDismiss).toHaveBeenCalledWith("entry-42");
  });
});
