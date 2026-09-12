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
