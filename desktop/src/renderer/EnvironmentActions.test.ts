import { describe, expect, it, vi } from "vitest";
import { createEnvironmentPanelActions } from "./EnvironmentActions";
import type { EnvironmentPanelMenu } from "./EnvironmentPanel";

function buildActions({ environmentPanelVisible = false, panelContainsFocus = false } = {}) {
  let activeMenu: EnvironmentPanelMenu = null;
  let panelOpen = false;
  let panelDismissed = false;
  const closeRuntimeMenus = vi.fn();
  const focusEnvironmentToggle = vi.fn();
  const actions = createEnvironmentPanelActions({
    setEnvironmentPanelOpen: open => { panelOpen = open; },
    setEnvironmentPanelDismissed: dismissed => { panelDismissed = dismissed; },
    setEnvironmentPanelMenu: menu => { activeMenu = menu; },
    closeRuntimeMenus,
    getEnvironmentPanelVisible: () => environmentPanelVisible,
    environmentPanelContainsActiveElement: () => panelContainsFocus,
    focusEnvironmentToggle,
  });
  return { actions, getPanelState: () => ({ activeMenu, panelOpen, panelDismissed }), closeRuntimeMenus, focusEnvironmentToggle };
}

describe("createEnvironmentPanelActions", () => {
  it("opens and closes the environment panel with focus restoration", () => {
    const harness = buildActions({
      environmentPanelVisible: true,
      panelContainsFocus: true,
    });

    harness.actions.openEnvironmentPanel();
    expect(harness.getPanelState().panelOpen).toBe(true);
    expect(harness.getPanelState().panelDismissed).toBe(false);
    expect(harness.closeRuntimeMenus).toHaveBeenCalled();

    harness.actions.toggleEnvironmentPanel();
    expect(harness.getPanelState()).toEqual({
      activeMenu: null,
      panelOpen: false,
      panelDismissed: true,
    });
    expect(harness.focusEnvironmentToggle).toHaveBeenCalled();
  });
});
