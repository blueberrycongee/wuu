import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type Ref } from "react";
import { closestCenter, DndContext, useSensor, useSensors } from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { arrayMove, SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { Globe, Pin, PinOff, Plus, RotateCw, Volume2, VolumeX, X } from "./WuuIcons";
import { HoverRevealScopeContext } from "./HoverReveal";
import {
  readNavigationRibbonPreferences,
  saveNavigationRibbonPreferences,
  saveRibbonOrder,
  useNavigationRibbonPreferences,
  type RibbonWebsite,
} from "./NavigationRibbonPreferences";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { RIBBON_COMPACT_WIDTH } from "../shared/navigationRibbon";
import { RibbonResizeHandle } from "./RibbonResizeHandle";
import { RibbonVisibilityToggle } from "./RibbonVisibilityToggle";
import { focusAfterRibbonClose, RibbonDestination } from "./NavigationRibbonDestination";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { showActionErrorToast } from "./Toast";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";
import { WebsiteIcon } from "./WebsiteIcon";
import mascotIcon from "../../../assets/app-icon-source.svg";

export interface NavigationRibbonItem {
  id: string;
  title: string;
  icon: ReactNode;
  active?: boolean;
  disabled?: boolean;
  pinned?: boolean;
  audible?: boolean;
  muted?: boolean;
  component?: string;
  pluginId?: string;
  onSelect: () => void;
  onClose?: () => void;
}

export interface RibbonBrowserActions {
  websites?: RibbonWebsite[];
  onNewTab?: () => void;
  onCloseTab?: (id: string) => void;
  onPinTab?: (id: string, pinned: boolean) => void;
  onMuteTab?: (id: string, muted: boolean) => void;
  onReorderTabs?: (ids: string[]) => void;
}


function restoreReopenedOrder(order: readonly string[], tabs: readonly Pick<RibbonWebsite, "id" | "reopenedFrom">[]): string[] {
  const present = new Set(order);
  const replacements = new Map(tabs.filter(tab => tab.reopenedFrom && !present.has(tab.id)).map(tab => [tab.reopenedFrom!, tab.id]));
  return order.map(id => replacements.get(id) ?? id);
}

export function NavigationRibbon({ onOpenExternal, items, websites = [], activeWebsiteId, onSelectWebsite, onNewTab, onCloseTab, onPinTab, onMuteTab, onReorderTabs, footer, ribbonRef }: {
  onOpenExternal?: (url: string) => Promise<void>;
  items: NavigationRibbonItem[];
  activeWebsiteId?: string | null;
  onSelectWebsite?: (website: RibbonWebsite) => void;
  footer?: ReactNode;
  ribbonRef?: Ref<HTMLElement>;
} & RibbonBrowserActions): JSX.Element {
  const { t } = useI18n();
  const preferences = useNavigationRibbonPreferences();
  const navRef = useRef<HTMLElement | null>(null);
  const [draft, setDraft] = useState<{ width: number; collapsed: boolean } | null>(null);
  const width = draft?.width ?? preferences.width ?? RIBBON_COMPACT_WIDTH;
  const collapsed = draft?.collapsed ?? preferences.collapsed === true;
  const resizing = draft !== null;
  useLayoutEffect(() => {
    const nav = navRef.current!;
    const shell = nav.closest<HTMLElement>(".app-shell") ?? nav.parentElement!;
    shell.style.setProperty("--sidebar-ribbon-width", collapsed ? "var(--conversation-pane-inset)" : `${width}px`);
    shell.style.setProperty("--sidebar-ribbon-extra", `calc(var(--sidebar-ribbon-width) - ${RIBBON_COMPACT_WIDTH}px)`);
    shell.toggleAttribute("data-ribbon-resizing", resizing);
    return () => { shell.removeAttribute("data-ribbon-resizing"); };
  }, [width, resizing, collapsed]);

  function resizeRibbon(next: number, finished: boolean, nextCollapsed = false): void {
    if (!finished) { setDraft({ width: next, collapsed: nextCollapsed }); return; }
    try { saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), width: next, collapsed: nextCollapsed }); }
    catch (error) { showActionErrorToast(error, t("ribbon.saveFailed")); }
    setDraft(null);
  }
  const [announcement, setAnnouncement] = useState("");
  const [menu, setMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  const [dragging, setDragging] = useState(false);
  const sensors = useSensors(useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }));
  const destinations: NavigationRibbonItem[] = [
    ...items,
    ...websites.map((website): NavigationRibbonItem => ({
      id: website.id,
      title: website.url === "about:blank" && website.startPage !== false ? t("webWorkspace.newTab") : website.title || website.url,
      icon: website.loading ? <RotateCw className="sidebar-ribbon-tab-loading" aria-hidden="true" /> : <WebsiteIcon url={website.url} favicon={website.favicon} fallback={<span className="sidebar-ribbon-website-icon" aria-hidden="true">{website.url === "about:blank" && website.startPage !== false ? <Globe /> : Array.from(website.title)[0]?.toLocaleUpperCase() || <Globe />}</span>} />,
      active: activeWebsiteId === website.id,
      disabled: !onSelectWebsite,
      pinned: website.pinned,
      audible: website.audible,
      muted: website.muted,
      component: "website-navigation-item",
      onSelect: () => onSelectWebsite?.(website),
      onClose: onCloseTab ? () => closeWebsite(website.id) : undefined,
    })),
  ];
  const availableIDs = JSON.stringify(destinations.map((item) => item.id));
  const reopenedTabs = JSON.stringify(websites.filter(tab => tab.reopenedFrom).map(({ id, reopenedFrom }) => ({ id, reopenedFrom })));
  // Resolve the slot during render, before the reorder effect can publish an
  // appended UUID back to the host. A later manual reorder stays authoritative.
  const restoredOrder = restoreReopenedOrder(preferences.order, websites);
  useEffect(() => {
    const ids = JSON.parse(availableIDs) as string[];
    const current = readNavigationRibbonPreferences();
    const restored = restoreReopenedOrder(current.order, JSON.parse(reopenedTabs));
    const additions = ids.filter((id) => !restored.includes(id));
    if (!additions.length && restored.every((id, index) => id === current.order[index])) return;
    try { saveNavigationRibbonPreferences({ ...current, order: [...restored, ...additions] }); }
    catch (saveError) { showActionErrorToast(saveError, t("ribbon.saveFailed")); }
  }, [availableIDs, reopenedTabs, t]);
  const destinationByID = new Map(destinations.map((item) => [item.id, item]));
  const orderedIDs = [...restoredOrder.filter((id) => destinationByID.has(id)),
    ...destinations.map((item) => item.id).filter((id) => !restoredOrder.includes(id))];
  const ordered = orderedIDs.map((id) => destinationByID.get(id)!);
  const tabStop = (ordered.find(item => item.active && !item.disabled) ?? ordered.find(item => !item.disabled))?.id;
  const websiteIDs = new Set(websites.map((website) => website.id));
  const browserOrder = JSON.stringify(orderedIDs.filter((id) => websiteIDs.has(id)));
  const nativeBrowserOrder = JSON.stringify(websites.map(website => website.id));
  useEffect(() => {
    // Native Ctrl+Tab and number shortcuts follow the same order as the ribbon.
    // A native state reply rerenders the shell with fresh callbacks. Once its
    // order agrees, another reorder would create an IPC/render feedback loop.
    if (browserOrder === nativeBrowserOrder) return;
    const ids = JSON.parse(browserOrder) as string[];
    if (ids.length) onReorderTabs?.(ids);
  }, [browserOrder, nativeBrowserOrder, onReorderTabs]);
  useLayoutEffect(() => {
    if (!activeWebsiteId) return;
    [...document.querySelectorAll<HTMLButtonElement>(".sidebar-ribbon-button[data-ribbon-id]")]
      .find((button) => button.dataset.ribbonId === activeWebsiteId)?.scrollIntoView({ block: "nearest" });
  }, [activeWebsiteId]);

  function reorder(id: string, destination: string): void {
    const from = orderedIDs.indexOf(id);
    const to = orderedIDs.indexOf(destination);
    if (from < 0 || to < 0 || from === to) return;
    try {
      saveRibbonOrder(arrayMove(orderedIDs, from, to));
      setAnnouncement(t("ribbon.moved", { name: destinationByID.get(id)!.title, position: to + 1, count: orderedIDs.length }));
    } catch (saveError) {
      showActionErrorToast(saveError, t("ribbon.saveFailed"));
    }
  }

  function move(id: string, direction: -1 | 1): void {
    const target = orderedIDs[orderedIDs.indexOf(id) + direction];
    if (target) reorder(id, target);
  }

  function focusRibbonItem(id: string | undefined): void {
    [...document.querySelectorAll<HTMLButtonElement>(".sidebar-ribbon-button[data-ribbon-id]")]
      .find((button) => button.dataset.ribbonId === id)?.focus();
  }

  function closeWebsite(id: string): void {
    focusAfterRibbonClose(id);
    onCloseTab?.(id);
  }

  const menuWebsite = menu && websites.find((website) => website.id === menu.id);
  return <>
    <HoverRevealScopeContext.Provider value={{ disabled: dragging || resizing || collapsed, retain: () => () => {} }}>
    <nav ref={node => { navRef.current = node; if (typeof ribbonRef === "function") ribbonRef(node); else if (ribbonRef) ribbonRef.current = node; }} className="sidebar-ribbon" hidden={collapsed} data-expanded={width > RIBBON_COMPACT_WIDTH || undefined} aria-label={t("sidebar.mainNavigation")}>
      <HoverRevealScopeContext.Provider value={null}>
        <Tooltip content={t("ribbon.github")} side="bottom" native={Boolean(activeWebsiteId)} disabled={collapsed}>
          <a className="sidebar-ribbon-brand"
            href="https://github.com/blueberrycongee/wuu" target="_blank" rel="noreferrer"
            aria-label={t("ribbon.github")}
            onClick={event => {
              event.preventDefault();
              void (onOpenExternal ?? window.wuu.openExternal)(event.currentTarget.href).catch(error => showActionErrorToast(error));
            }}>
            <img src={mascotIcon} alt="" draggable={false} />
          </a>
        </Tooltip>
      </HoverRevealScopeContext.Provider>
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis]}
        accessibility={{ screenReaderInstructions: { draggable: t("ribbon.reorderHint") } }}
        onDragStart={() => setDragging(true)}
        onDragCancel={() => setDragging(false)}
        onDragEnd={({ active, over }) => { setDragging(false); if (over) reorder(String(active.id), String(over.id)); }}>
        <div className="sidebar-ribbon-main scrollbar-hidden">
          <SortableContext items={orderedIDs} strategy={verticalListSortingStrategy}>
            {ordered.map((item) => <RibbonDestination key={item.id} item={item} tabStop={item.id === tabStop} nativeTooltip={Boolean(activeWebsiteId)}
              onMove={(direction) => move(item.id, direction)}
              onMenu={websiteIDs.has(item.id) ? (x, y) => setMenu({ id: item.id, x, y }) : undefined} />)}
          </SortableContext>
          <Tooltip content={t("webWorkspace.newTab")} native={Boolean(activeWebsiteId)}>
            <button type="button" className="sidebar-ribbon-button sidebar-ribbon-add" aria-label={t("webWorkspace.newTab")}
              disabled={!onNewTab} onClick={onNewTab}>
              <Plus aria-hidden="true" /><span className="sidebar-ribbon-label">{t("webWorkspace.newTab")}</span>
            </button>
          </Tooltip>
        </div>
      </DndContext>
      <div className="sidebar-ribbon-footer">
        {!collapsed ? <RibbonVisibilityToggle collapsed={false} nativeTooltip={Boolean(activeWebsiteId)} /> : null}
        {footer}
      </div>
      <span className="sr-only" role="status" aria-live="polite">{announcement}</span>
    </nav>
    {/* Keep the captured handle mounted while collapse is only a drag preview. */}
    {!preferences.collapsed ? <RibbonResizeHandle width={width} label={t("ribbon.resize")} onResize={resizeRibbon} onCancel={() => setDraft(null)} /> : null}
    </HoverRevealScopeContext.Provider>
    {menu && menuWebsite ? <ThreadContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
      { label: t(menuWebsite.pinned ? "webWorkspace.unpinTab" : "webWorkspace.pinTab"), icon: menuWebsite.pinned ? <PinOff /> : <Pin />, onSelect: () => { focusRibbonItem(menuWebsite.id); onPinTab?.(menuWebsite.id, !menuWebsite.pinned); } },
      { label: t(menuWebsite.muted ? "webWorkspace.unmuteTab" : "webWorkspace.muteTab"), icon: menuWebsite.muted ? <Volume2 /> : <VolumeX />, disabled: !onMuteTab, onSelect: () => onMuteTab?.(menuWebsite.id, !menuWebsite.muted) },
      { label: t("webWorkspace.closeCurrentTab"), icon: <X />, onSelect: () => closeWebsite(menuWebsite.id) },
    ]} /> : null}
  </>;
}
