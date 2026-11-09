import { useEffect, useMemo, useSyncExternalStore } from "react";
import { WEB_WORKSPACE_RIBBON_ID } from "../shared/webWorkspace";
import type { ApplicationWebWorkspaceV1 as WebWorkspaceApi, WebWorkspaceStateV1 as WebWorkspaceState } from "../../../packages/plugin-sdk/src/ui-web-workspace";
import { useWebWorkspaceControls } from "./WebWorkspaceControlsContext";
import { readNavigationRibbonPreferences, saveNavigationRibbonPreferences } from "./NavigationRibbonPreferences";
import { showActionErrorToast } from "./Toast";

/** One owner supports both a mounted ribbon and an application-owned navigation service. */
export function createRibbonBrowserState(getControls: () => WebWorkspaceApi) {
  let state: WebWorkspaceState | undefined;
  const listeners = new Set<() => void>();
  const publish = (next: WebWorkspaceState) => { state = next; for (const listener of listeners) listener(); return next; };
  async function update(operation: (browser: WebWorkspaceApi) => Promise<WebWorkspaceState>) {
    const browser = getControls();
    if (!browser.capabilities.available) throw new Error("Browser tabs are unavailable");
    return publish(await operation(browser));
  }
  return {
    getSnapshot: () => state,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    start(onError: (error: unknown) => void) {
      const browser = getControls();
      if (!browser.capabilities.available) return () => {};
      let disposed = false;
      let initializing = true;
      let latest: WebWorkspaceState | undefined;
      const off = browser.onState(next => {
        if (next.id !== WEB_WORKSPACE_RIBBON_ID) return;
        if (initializing) latest = next;
        else if (!disposed) publish(next);
      });
      const preferences = readNavigationRibbonPreferences();
      const positions = new Map(preferences.order.map((id, index) => [id, index]));
      const websites = [...preferences.websites].sort((a, b) => (positions.get(a.id) ?? Infinity) - (positions.get(b.id) ?? Infinity));
      void browser.initializeTabs(websites).then(next => {
        if (disposed) return;
        const current = readNavigationRibbonPreferences();
        if (!current.browserTabsMigrated) {
          const groups = next.migratedTabGroups ?? {};
          const childIDs = new Set(Object.values(groups).flat());
          const order = current.order.flatMap(id => groups[id] ?? (childIDs.has(id) ? [] : [id]));
          const missing = (next.tabs ?? []).map(tab => tab.id).filter(id => !order.includes(id));
          saveNavigationRibbonPreferences({ ...current, order: [...new Set([...order, ...missing])], browserTabsMigrated: true });
        }
        initializing = false;
        publish(latest ?? next);
      }).catch(error => { if (!disposed) onError(error); });
      return () => { disposed = true; off(); };
    },
    createTab: () => update(browser => browser.createTab(WEB_WORKSPACE_RIBBON_ID)),
    reopenTab: () => update(browser => browser.reopenTab(WEB_WORKSPACE_RIBBON_ID)),
    selectTab: (id: string) => update(browser => browser.selectTab(WEB_WORKSPACE_RIBBON_ID, id)),
    closeTab: (id: string) => update(browser => browser.closeTab(WEB_WORKSPACE_RIBBON_ID, id)),
    pinTab: (id: string, pinned: boolean) => update(browser => browser.setTabPinned(WEB_WORKSPACE_RIBBON_ID, id, pinned)),
    muteTab: (id: string, muted: boolean) => update(browser => browser.setTabMuted(WEB_WORKSPACE_RIBBON_ID, id, muted)),
    reorderTabs: (ids: string[]) => update(browser => browser.reorderTabs(WEB_WORKSPACE_RIBBON_ID, ids)),
  };
}

export function useRibbonBrowserTabs(enabled = true) {
  const controls = useWebWorkspaceControls();
  const owner = useMemo(() => createRibbonBrowserState(() => controls), [controls]);
  const state = useSyncExternalStore(owner.subscribe, owner.getSnapshot);
  useEffect(() => enabled ? owner.start(showActionErrorToast) : undefined, [enabled, owner]);
  const operations = useMemo(() => {
    const perform = async (operation: () => Promise<WebWorkspaceState>) => {
      if (!enabled) return;
      try { return await operation(); } catch (error) { showActionErrorToast(error); }
    };
    return { createTab: () => perform(owner.createTab), reopenTab: () => perform(owner.reopenTab),
      selectTab: (id: string) => perform(() => owner.selectTab(id)),
      closeTab: (id: string) => { void perform(() => owner.closeTab(id)); },
      pinTab: (id: string, pinned: boolean) => { void perform(() => owner.pinTab(id, pinned)); },
      muteTab: (id: string, muted: boolean) => { void perform(() => owner.muteTab(id, muted)); },
      reorderTabs: (ids: string[]) => {
        const tabs = owner.getSnapshot()?.tabs;
        if (ids.length === tabs?.length && !tabs.every((tab, index) => tab.id === ids[index])) void perform(() => owner.reorderTabs(ids));
      },
    };
  }, [enabled, owner]);
  return { state, ...operations };
}
