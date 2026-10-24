import type { EnvironmentPanelMenu } from "./EnvironmentPanel";

/** Local presentation only; Git operations belong to application.environment. */
export function createEnvironmentPanelActions(deps: {
  setEnvironmentPanelOpen: (open: boolean) => void;
  setEnvironmentPanelDismissed: (dismissed: boolean) => void;
  setEnvironmentPanelMenu: (menu: EnvironmentPanelMenu) => void;
  closeRuntimeMenus: () => void;
  getEnvironmentPanelVisible: () => boolean;
  environmentPanelContainsActiveElement: () => boolean;
  focusEnvironmentToggle: () => void;
}) {
  function toggleEnvironmentPanel(): void {
    if (deps.getEnvironmentPanelVisible()) {
      closeEnvironmentPanel({ dismissed: true });
      return;
    }
    openEnvironmentPanel();
  }

  function openEnvironmentPanel(): void {
    deps.setEnvironmentPanelOpen(true);
    deps.setEnvironmentPanelDismissed(false);
    deps.closeRuntimeMenus();
  }

  function closeEnvironmentPanel({
    dismissed = false,
  }: { dismissed?: boolean } = {}): void {
    restoreEnvironmentPanelFocus();
    deps.setEnvironmentPanelOpen(false);
    if (dismissed) {
      deps.setEnvironmentPanelDismissed(true);
    }
    deps.setEnvironmentPanelMenu(null);
  }

  function restoreEnvironmentPanelFocus(): void {
    if (deps.environmentPanelContainsActiveElement()) {
      deps.focusEnvironmentToggle();
    }
  }

  return { toggleEnvironmentPanel, openEnvironmentPanel, closeEnvironmentPanel };
}
