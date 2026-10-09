import { act, createElement, createRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  SideThreadPanel,
  type SideThreadPanelHandle,
} from "./SideThreadPanel";
import { ArtifactPreviewContext } from "./ArtifactPreviewContext";
import type { SideThreadEntryState } from "./SideThreadState";

vi.mock("./WorkspacePdfPreview", () => ({ WorkspacePdfPreview: () => <div>Side PDF</div> }));

let mountedRoots: Root[] = [];
let mountedContainers: HTMLElement[] = [];

function mount(element: React.ReactElement): HTMLElement {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => root.render(element));
  mountedRoots.push(root);
  mountedContainers.push(container);
  return container;
}

afterEach(() => {
  for (const root of mountedRoots) {
    act(() => root.unmount());
  }
  for (const container of mountedContainers) {
    container.remove();
  }
  mountedRoots = [];
  mountedContainers = [];
  vi.restoreAllMocks();
});

function makeEntry(
  overrides: Partial<SideThreadEntryState> = {},
): SideThreadEntryState {
  return {
    open: true,
    summary: null,
    messages: [],
    draft: "",
    streaming: false,
    ...overrides,
  };
}

function renderPanel(
  entry: SideThreadEntryState,
): HTMLElement {
  return mount(
    createElement(SideThreadPanel, {
      entry,
      mainThreadId: "main-1",
      active: true,
      title: "Main conversation",
      composer: createElement("textarea", { "aria-label": "side composer" }),
    }),
  );
}

describe("SideThreadPanel", () => {
  it("keeps side-thread PDFs in their own preview instead of sending them to the main conversation", async () => {
    const openMainPreview = vi.fn();
    const entry = makeEntry({
      summary: { side_thread_id: "side-1", main_thread_id: "main-1", status: "completed", revision: 1, created_at: "", updated_at: "" },
      messages: [{ id: "side-answer", side_thread_id: "side-1", role: "assistant", text: "", status: "completed", created_at: "",
        items: [{ id: "side-pdf", type: "tool_call", status: "completed", name: "present_artifact", result_detail: { content: [{
          type: "file", mime_type: "application/pdf", name: "side.pdf", uri: "wuu-artifact://workspace/side-1/digest/side.pdf",
          artifact: { placement: "turn_end", sha256: "digest" },
        }] } }],
      }],
    });
    const container = mount(<ArtifactPreviewContext.Provider value={openMainPreview}>
      <SideThreadPanel entry={entry} mainThreadId="main-1" active title="Main conversation" composer={<textarea aria-label="side composer" />}
        />
    </ArtifactPreviewContext.Provider>);
    await act(async () => container.querySelector<HTMLButtonElement>('.turn-edit-summary-overview')!.click());
    expect(openMainPreview).not.toHaveBeenCalled();
    expect(container.querySelector('.artifact-preview-overlay')).not.toBeNull();
  });

  it("reserves the measured floating footer height in the scroll flow", () => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      function getBoundingClientRect(this: HTMLElement) {
        const height = this.classList.contains("side-thread-panel__footer") ? 144 : 0;
        return {
          x: 0,
          y: 0,
          top: 0,
          right: 0,
          bottom: height,
          left: 0,
          width: 0,
          height,
          toJSON: () => ({}),
        };
      },
    );

    const container = renderPanel(makeEntry());
    expect(
      container
        .querySelector<HTMLElement>(".side-thread-panel")
        ?.style.getPropertyValue("--side-thread-footer-height"),
    ).toBe("144px");
  });

  it("exposes safe focus control for the embedded composer", () => {
    const ref = createRef<SideThreadPanelHandle>();
    const container = mount(
      createElement(SideThreadPanel, {
        ref,
        entry: makeEntry(),
        mainThreadId: "main-1",
        active: true,
        title: "Main conversation",
        composer: createElement("textarea", { "aria-label": "side composer" }),
      }),
    );
    act(() => ref.current?.focusComposer());
    expect(document.activeElement).toBe(container.querySelector("textarea"));
  });
});
