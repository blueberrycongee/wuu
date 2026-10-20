import { useCallback, useEffect, useRef, useState } from "react";
import { WEB_WORKSPACE_RIBBON_ID, type WebWorkspaceApi, type WebWorkspaceState } from "../shared/webWorkspace";
import { readNavigationRibbonPreferences, saveNavigationRibbonPreferences } from "./NavigationRibbonPreferences";
import { showActionErrorToast } from "./Toast";

/** User browser tabs share native lifecycle, while the ribbon owns mixed app order. */
export function useRibbonBrowserTabs(enabled = true) {
  const api = (window.wuu as typeof window.wuu & { webWorkspace?: WebWorkspaceApi }).webWorkspace;
  const [state, setState] = useState<WebWorkspaceState>();
  const stateRef = useRef(state);
  stateRef.current = state;
  useEffect(() => {
    if (!enabled || !api?.initializeTabs) return;
    let disposed = false;
    let initializing = true;
    let latest: WebWorkspaceState | undefined;
    const off = api.onState((next) => {
      if (next.id !== WEB_WORKSPACE_RIBBON_ID) return;
      if (initializing) latest = next;
      else setState(next);
    });
    const preferences = readNavigationRibbonPreferences();
    const positions = new Map(preferences.order.map((id, index) => [id, index]));
    const websites = [...preferences.websites].sort((a, b) => (positions.get(a.id) ?? Infinity) - (positions.get(b.id) ?? Infinity));
    // The host migrates saved child tabs once. Keeping the old shortcut input
    // intact makes a failed migration retryable without risking session data.
    void api.initializeTabs(websites).then((next) => {
      if (disposed) return;
      const current = readNavigationRibbonPreferences();
      if (!current.browserTabsMigrated) {
        const groups = next.migratedTabGroups ?? {};
        const childIDs = new Set(Object.values(groups).flat());
        // Expand each old website slot in place, retaining child order and all
        // fixed destinations between groups. Save the marker atomically so a
        // retry cannot regroup tabs after the user has reordered them.
        const order = current.order.flatMap((id) => groups[id] ?? (childIDs.has(id) ? [] : [id]));
        const missing = (next.tabs ?? []).map((tab) => tab.id).filter((id) => !order.includes(id));
        saveNavigationRibbonPreferences({ ...current, order: [...new Set([...order, ...missing])], browserTabsMigrated: true });
      }
      initializing = false;
      setState(latest ?? next);
    }).catch((error: unknown) => { if (!disposed) showActionErrorToast(error); });
    return () => { disposed = true; off(); };
  }, [api, enabled]);

  const update = useCallback(async (operation: (browser: WebWorkspaceApi) => Promise<WebWorkspaceState>): Promise<WebWorkspaceState | undefined> => {
    if (!enabled || !api) return;
    try {
      const next = await operation(api);
      setState(next);
      return next;
    } catch (error) { showActionErrorToast(error); }
  }, [api, enabled]);
  const createTab = useCallback(() => update((browser) => browser.createTab(WEB_WORKSPACE_RIBBON_ID)), [update]);
  const reopenTab = useCallback(() => update((browser) => browser.reopenTab(WEB_WORKSPACE_RIBBON_ID)), [update]);
  const selectTab = useCallback((id: string) => update((browser) => browser.selectTab(WEB_WORKSPACE_RIBBON_ID, id)), [update]);
  const closeTab = useCallback((id: string) => { void update((browser) => browser.closeTab(WEB_WORKSPACE_RIBBON_ID, id)); }, [update]);
  const pinTab = useCallback((id: string, pinned: boolean) => { void update((browser) => browser.setTabPinned(WEB_WORKSPACE_RIBBON_ID, id, pinned)); }, [update]);
  const muteTab = useCallback((id: string, muted: boolean) => { void update((browser) => browser.setTabMuted(WEB_WORKSPACE_RIBBON_ID, id, muted)); }, [update]);
  const reorderTabs = useCallback((ids: string[]) => {
    const tabs = stateRef.current?.tabs ?? [];
    if (ids.length !== tabs.length || tabs.every((tab, index) => tab.id === ids[index])) return;
    void update((browser) => browser.reorderTabs(WEB_WORKSPACE_RIBBON_ID, ids));
  }, [update]);
  return { state, createTab, reopenTab, selectTab, closeTab, pinTab, muteTab, reorderTabs };
}
