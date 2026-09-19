import { act, createRef, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { InitializeResult } from "../shared/protocol";
import { EnvironmentPanel } from "./EnvironmentPanel";
import { unhoverTooltip } from "./tooltipTestUtils";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
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
});

function initialized(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "test",
    model: "test-model",
    workspace_root: "/repo",
  };
}

function renderPanel(pluginSections?: ReactNode): void {
  act(() => {
    root = createRoot(container);
    root.render(
      <EnvironmentPanel
        panelRef={createRef<HTMLDivElement>()}
        motionState="open"
        initialized={initialized()}
        activeMenu={null}
        running={false}
        pullRequestDisabledReason=""
        onSetActiveMenu={() => {}}
        onClose={() => {}}
        onSelectBranch={() => {}}
        onCreateBranch={() => Promise.resolve()}
        onOpenReview={() => {}}
        onOpenCommit={() => {}}
        onOpenPullRequest={() => {}}
        pluginSections={pluginSections}
      />,
    );
  });
}

describe("EnvironmentPanel floating close", () => {
  it("renders plugin inspector sections above the body so the floating close can clear them", () => {
    renderPanel(
      <div className="plugin-inspector-sections">
        <section className="plugin-inspector-section">
          <h2>TODO</h2>
        </section>
      </div>,
    );

    const panel = container.querySelector(".environment-panel");
    expect(panel).not.toBeNull();
    const children = [...(panel?.children ?? [])];
    const headerIndex = children.findIndex((node) =>
      node.classList.contains("environment-panel-header"),
    );
    const sectionsIndex = children.findIndex((node) =>
      node.classList.contains("plugin-inspector-sections"),
    );
    const bodyIndex = children.findIndex((node) =>
      node.classList.contains("environment-panel-body"),
    );

    expect(panel?.querySelector(".environment-panel-header.floating")).not.toBeNull();
    expect(headerIndex).toBeGreaterThanOrEqual(0);
    expect(sectionsIndex).toBe(headerIndex + 1);
    expect(bodyIndex).toBe(sectionsIndex + 1);
  });

  it("renders the floating close as an icon-button wrapping the X glyph", () => {
    renderPanel();

    const close = container.querySelector(
      ".environment-panel-header.floating .environment-panel-actions > .icon-button",
    );
    expect(close).not.toBeNull();
    expect(close?.querySelector("svg.icon")).not.toBeNull();
  });
});
