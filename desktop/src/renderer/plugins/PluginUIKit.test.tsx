import * as React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { createPluginUIKit } from "../../shared/workbench";

describe("PluginUIKit public theme anchors", () => {
  it("exposes the published selectors on rendered plugin controls", () => {
    const ui = createPluginUIKit(React);
    const container = document.createElement("div");
    container.innerHTML = renderToStaticMarkup(
      <ui.Page>
        <ui.Panel>
          <ui.Card>
            <ui.Section title="Settings">
              <ui.Stack>
                <ui.Row><ui.Button>Save</ui.Button></ui.Row>
                <ui.TextInput label="Name" />
                <ui.EmptyState title="No items" />
              </ui.Stack>
            </ui.Section>
          </ui.Card>
        </ui.Panel>
      </ui.Page>,
    );

    // Theme plugins address these public selectors; element tags, CSS
    // classes, component source files, and wrapper order are unrestricted.
    for (const anchor of [
      "plugin-ui-page", "plugin-ui-panel", "plugin-ui-card",
      "plugin-ui-section", "plugin-ui-stack", "plugin-ui-row",
      "plugin-ui-button", "plugin-ui-field", "plugin-ui-input",
      "plugin-ui-empty-state",
    ]) {
      expect(container.querySelector(`[data-wuu-component="${anchor}"]`), anchor).not.toBeNull();
    }
  });
});

describe("ComposerDrawer interaction", () => {
  it("keeps actions independent and restores focus after keyboard dismissal", async () => {
    const { act } = React;
    const { createRoot } = await import("react-dom/client");
    const ui = createPluginUIKit(React);
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    let actions = 0;
    function Harness() {
      const [expanded, setExpanded] = React.useState(false);
      return <ui.ComposerDrawer expanded={expanded} onExpandedChange={setExpanded}
        toggleLabel="Toggle details" summary="Task" notice={<p role="alert">Retry needed</p>}
        actions={<button onClick={() => actions++}>Pause</button>}>
        <input aria-label="Objective" />
      </ui.ComposerDrawer>;
    }
    try {
      act(() => root.render(<Harness />));
      const toggle = container.querySelector<HTMLButtonElement>("button[aria-expanded]")!;
      act(() => container.querySelectorAll<HTMLButtonElement>("button")[1].click());
      expect(actions).toBe(1);
      expect(toggle.getAttribute("aria-expanded")).toBe("false");
      expect(container.querySelector('[role="alert"]')?.textContent).toBe("Retry needed");
      act(() => toggle.click());
      const input = container.querySelector("input")!;
      expect(document.getElementById(toggle.getAttribute("aria-controls")!)?.contains(input)).toBe(true);
      input.focus();
      act(() => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
      expect(container.querySelector("input")).toBeNull();
      expect(document.activeElement).toBe(toggle);
      expect(toggle.getAttribute("aria-expanded")).toBe("false");
    } finally {
      act(() => root.unmount());
      container.remove();
    }
  });
});
