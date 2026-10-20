import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type Ref } from "react";
import { closestCenter, DndContext, useSensor, useSensors } from "@dnd-kit/core";
import { restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { arrayMove, SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { Globe, Pin, PinOff, Plus, RotateCw, Volume2, VolumeX, X } from "./WuuIcons";
import { SIDEBAR_RIBBON_WIDTH } from "./AppLayoutState";
import { readFocusModality } from "./FocusModality";
import { HoverRevealScopeContext } from "./HoverReveal";
import {
  readNavigationRibbonPreferences,
  saveNavigationRibbonPreferences,
  saveRibbonOrder,
  useNavigationRibbonPreferences,
  type RibbonWebsite,
} from "./NavigationRibbonPreferences";
import { SidebarPointerSensor } from "./SidebarPointerSensor";
import { focusAfterRibbonClose, RibbonDestination } from "./NavigationRibbonDestination";
import { chromeOverlayApi, NativeRibbonFooterContext, useChromeOverlay } from "./ChromeOverlay";
import type { ChromeAccountTrigger, ChromeDesktopUpdate, ChromeOverlayRect, ChromeRibbonFocus, ChromeRibbonModel, RibbonIconModel } from "../shared/chromeOverlay";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { showActionErrorToast } from "./Toast";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";
import { WebsiteIcon } from "./WebsiteIcon";

export interface NavigationRibbonItem {
  id: string;
  title: string;
  icon: ReactNode;
  nativeIcon: RibbonIconModel;
  active?: boolean;
  disabled?: boolean;
  pinned?: boolean;
  audible?: boolean;
  muted?: boolean;
  component?: string;
  pluginId?: string;
  onSelect: () => void;
  onClose?: () => void;
  onMute?: () => void;
}

export interface RibbonBrowserActions {
  websites?: RibbonWebsite[];
  onNewTab?: () => void;
  onCloseTab?: (id: string) => void;
  onPinTab?: (id: string, pinned: boolean) => void;
  onMuteTab?: (id: string, muted: boolean) => void;
  onReorderTabs?: (ids: string[]) => void;
}


export function NavigationRibbon({ items, websites = [], activeWebsiteId, onSelectWebsite, onNewTab, onCloseTab, onPinTab, onMuteTab, onReorderTabs, footer, ribbonRef, footerSettings, onExpandedChange }: {
  items: NavigationRibbonItem[];
  activeWebsiteId?: string | null;
  onSelectWebsite?: (website: RibbonWebsite) => void;
  footer?: ReactNode;
  ribbonRef?: Ref<HTMLElement>;
  onExpandedChange?: (expanded: boolean) => void;
  footerSettings?: { title: string; disabled: boolean; active: boolean; onSelect: () => void };
} & RibbonBrowserActions): JSX.Element {
  const { t } = useI18n();
  const preferences = useNavigationRibbonPreferences();
  const navRef = useRef<HTMLElement | null>(null);
  const [nativeOpen, setNativeOpen] = useState(false);
  const [nativeClosing, setNativeClosing] = useState(false);
  const [nativeBounds, setNativeBounds] = useState<ChromeOverlayRect | null>(null);
  const [accountTrigger, setAccountTrigger] = useState<ChromeAccountTrigger>();
  const accountOpen = useRef<((bounds: ChromeOverlayRect) => void) | undefined>(undefined);
  const [updateModel, setUpdateModel] = useState<ChromeDesktopUpdate>();
  const updateActivate = useRef<(() => void) | undefined>(undefined);
  const scrollTop = useRef(0);
  const dismissedUnderPointer = useRef(false);
  // A destination change must not replace the surface under the pointer.
  // Keep this opening until the native host observes leave or dismissal.
  const nativeAvailable = Boolean((activeWebsiteId || nativeOpen) && chromeOverlayApi());
  const accountContext = useMemo(() => ({ publishAccount: (trigger: ChromeAccountTrigger | undefined, open?: (bounds: ChromeOverlayRect) => void) => {
    setAccountTrigger(current => JSON.stringify(current) === JSON.stringify(trigger) ? current : trigger);
    accountOpen.current = open;
  }, publishUpdate: (model: ChromeDesktopUpdate | undefined, activate?: () => void) => {
    setUpdateModel(current => JSON.stringify(current) === JSON.stringify(model) ? current : model);
    updateActivate.current = activate;
  } }), []);
  const [announcement, setAnnouncement] = useState("");
  const [menu, setMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  const [pointerInside, setPointerInside] = useState(false);
  const enterTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(enterTimer.current), []);
  const [keyboardFocusWithin, setKeyboardFocusWithin] = useState(false);
  const [nativeFocusTarget, setNativeFocusTarget] = useState<ChromeRibbonFocus>();
  const rememberFocus = (target: EventTarget | null) => {
    if (!(target instanceof Element)) return;
    const button = target.closest<HTMLButtonElement>("button");
    const next: ChromeRibbonFocus | undefined = button?.dataset.ribbonId ? { kind: "item", id: button.dataset.ribbonId }
      : button?.dataset.ribbonCloseId ? { kind: "close", id: button.dataset.ribbonCloseId }
      : button?.matches(".desktop-update-button") ? { kind: "desktop-update" }
      : button?.matches(".sidebar-account-trigger") ? { kind: "account" }
      : button?.matches(".sidebar-ribbon-add") ? { kind: "new-tab" }
      : button?.closest(".sidebar-ribbon-footer") && button.matches(".sidebar-ribbon-button") ? { kind: "settings" } : undefined;
    setNativeFocusTarget(current => JSON.stringify(current) === JSON.stringify(next) ? current : next);
  };
  const [dragging, setDragging] = useState(false);
  const [retainedLayers, setRetainedLayers] = useState(0);
  const retainLayer = useCallback(() => {
    setRetainedLayers((count) => count + 1);
    return () => setRetainedLayers((count) => count - 1);
  }, []);
  const sensors = useSensors(useSensor(SidebarPointerSensor, { activationConstraint: { distance: 6 } }));
  const destinations: NavigationRibbonItem[] = [
    ...items,
    ...websites.map((website): NavigationRibbonItem => ({
      id: website.id,
      title: website.url === "about:blank" && website.startPage !== false ? t("webWorkspace.newTab") : website.title || website.url,
      icon: website.loading ? <RotateCw className="sidebar-ribbon-tab-loading" aria-hidden="true" /> : <WebsiteIcon url={website.url} favicon={website.favicon} fallback={<span className="sidebar-ribbon-website-icon" aria-hidden="true">{website.url === "about:blank" && website.startPage !== false ? <Globe /> : Array.from(website.title)[0]?.toLocaleUpperCase() || <Globe />}</span>} />,
      nativeIcon: { kind: "website", ...website },
      active: activeWebsiteId === website.id,
      disabled: !onSelectWebsite,
      pinned: website.pinned,
      audible: website.audible,
      muted: website.muted,
      component: "website-navigation-item",
      onSelect: () => onSelectWebsite?.(website),
      onClose: onCloseTab ? () => closeWebsite(website.id) : undefined,
      onMute: onMuteTab ? () => onMuteTab(website.id, !website.muted) : undefined,
    })),
  ];
  const availableIDs = JSON.stringify(destinations.map((item) => item.id));
  useEffect(() => {
    const ids = JSON.parse(availableIDs) as string[];
    const current = readNavigationRibbonPreferences();
    const additions = ids.filter((id) => !current.order.includes(id));
    if (!additions.length) return;
    try { saveNavigationRibbonPreferences({ ...current, order: [...current.order, ...additions] }); }
    catch (saveError) { showActionErrorToast(saveError, t("ribbon.saveFailed")); }
  }, [availableIDs, t]);
  const destinationByID = new Map(destinations.map((item) => [item.id, item]));
  const orderedIDs = [...preferences.order.filter((id) => destinationByID.has(id)),
    ...destinations.map((item) => item.id).filter((id) => !preferences.order.includes(id))];
  const ordered = orderedIDs.map((id) => destinationByID.get(id)!);
  const tabStop = (ordered.find(item => item.active && !item.disabled) ?? ordered.find(item => !item.disabled))?.id;
  const websiteIDs = new Set(websites.map((website) => website.id));
  const browserOrder = JSON.stringify(orderedIDs.filter((id) => websiteIDs.has(id)));
  useEffect(() => {
    // Native Ctrl+Tab and number shortcuts follow the same order as the ribbon.
    const ids = JSON.parse(browserOrder) as string[];
    if (ids.length) onReorderTabs?.(ids);
  }, [browserOrder, onReorderTabs]);
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
  const expanded = nativeOpen || pointerInside || keyboardFocusWithin || dragging || Boolean(menuWebsite) || retainedLayers > 0;
  useLayoutEffect(() => {
    onExpandedChange?.(expanded);
  }, [expanded, onExpandedChange]);
  useLayoutEffect(() => () => onExpandedChange?.(false), [onExpandedChange]);
  useLayoutEffect(() => {
    if (!nativeAvailable || !expanded || !navRef.current) { setNativeOpen(false); return; }
    const nav = navRef.current, rect = nav.getBoundingClientRect();
    const top = Number.parseFloat(getComputedStyle(nav).paddingTop);
    scrollTop.current = nav.querySelector(".sidebar-ribbon-main")?.scrollTop ?? 0;
    setNativeBounds({ x: rect.x, y: rect.y + top, width: rect.width, height: rect.height - top });
    setNativeOpen(true);
  }, [nativeAvailable, expanded]);
  const nativeModel: ChromeRibbonModel = { kind: "ribbon", collapsedWidth: SIDEBAR_RIBBON_WIDTH, items: ordered.map(item => ({ id: item.id, title: item.title, icon: item.nativeIcon,
    active: item.active, disabled: item.disabled, pinned: item.pinned, audible: item.audible, muted: item.muted, canMute: Boolean(item.onMute), closable: Boolean(item.onClose), website: websiteIDs.has(item.id), component: item.component, pluginId: item.pluginId })),
    label: t("sidebar.mainNavigation"), newTabLabel: t("webWorkspace.newTab"), canCreateTab: Boolean(onNewTab), closeTabLabel: t("webWorkspace.closeCurrentTab"),
    pinTabLabel: t("webWorkspace.pinTab"), unpinTabLabel: t("webWorkspace.unpinTab"), reorderHint: t("ribbon.reorderHint"),
    muteTabLabel: t("webWorkspace.muteTab"), unmuteTabLabel: t("webWorkspace.unmuteTab"),
    paddingTop: 0,
    paddingBottom: navRef.current ? Number.parseFloat(getComputedStyle(navRef.current).paddingBottom) : 8,
    scrollTop: scrollTop.current, focusTarget: nativeFocusTarget,
    footer: footerSettings ? { settings: { id: "settings", title: footerSettings.title, icon: { kind: "settings" }, active: footerSettings.active, disabled: footerSettings.disabled }, account: accountTrigger, update: updateModel } : undefined,
  };
  const closeNative = () => { clearTimeout(enterTimer.current); enterTimer.current = undefined; setNativeClosing(false); setNativeOpen(false); setPointerInside(false); setKeyboardFocusWithin(false); setDragging(false); };
  const nativePresented = useChromeOverlay(nativeAvailable && nativeOpen, nativeBounds, nativeModel, command => {
    if (command.type === "pointer") {
      setNativeClosing(!command.inside);
      if (command.inside) { setPointerInside(true); setNativeOpen(true); }
    }
    else if (command.type === "dismiss") dismissedUnderPointer.current = command.reason === "escape" || command.reason === "tab";
    else if (command.type === "select") destinationByID.get(command.id)?.onSelect();
    // The native ribbon owns its focus; focusing its duplicate shell button
    // would steal keyboard input while the native surface is still open.
    else if (command.type === "close-tab") onCloseTab?.(command.id);
    else if (command.type === "pin-tab") onPinTab?.(command.id, command.pinned);
    else if (command.type === "mute-tab") onMuteTab?.(command.id, command.muted);
    else if (command.type === "reorder") reorder(command.id, command.target);
    else if (command.type === "new-tab") onNewTab?.();
    else if (command.type === "desktop-update") updateActivate.current?.();
    else if (command.type === "settings") footerSettings?.onSelect();
    else if (command.type === "account-open") accountOpen.current?.(command.anchor);
    else if (command.type === "scroll") { scrollTop.current = command.top; const main = navRef.current?.querySelector(".sidebar-ribbon-main"); if (main) main.scrollTop = command.top; }
  }, closeNative, keyboardFocusWithin, navRef.current);
  const enterRibbon = () => {
    if (dismissedUnderPointer.current || pointerInside || enterTimer.current) return;
    enterTimer.current = setTimeout(() => { enterTimer.current = undefined; setPointerInside(true); }, 120);
  };
  return <>
    <NativeRibbonFooterContext.Provider value={accountContext}>
    <HoverRevealScopeContext.Provider value={{ disabled: false, retain: retainLayer }}>
    <nav ref={node => { navRef.current = node; if (typeof ribbonRef === "function") ribbonRef(node); else if (ribbonRef) ribbonRef.current = node; }} className="sidebar-ribbon" aria-label={t("sidebar.mainNavigation")}
      data-expanded={(expanded && !nativeClosing) || undefined}
      data-native-supported={nativeAvailable || undefined}
      data-wuu-layer={expanded && !nativeAvailable ? "popover" : undefined}
      data-native-overlay={nativePresented || undefined}
      onPointerOver={enterRibbon}
      onPointerMove={enterRibbon}
      onPointerOut={(event) => {
        if (event.currentTarget.contains(event.target as Node)
          && (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget))) { clearTimeout(enterTimer.current); enterTimer.current = undefined; setPointerInside(false); dismissedUnderPointer.current = false; }
      }}
      onPointerDownCapture={() => setKeyboardFocusWithin(false)}
      onFocus={event => { rememberFocus(event.target); setKeyboardFocusWithin(readFocusModality() === "keyboard"); }}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setKeyboardFocusWithin(false);
      }}
      onKeyDownCapture={event => { rememberFocus(event.target); setKeyboardFocusWithin(true); }}>
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVerticalAxis]}
        accessibility={{ screenReaderInstructions: { draggable: t("ribbon.reorderHint") } }}
        onDragStart={() => setDragging(true)}
        onDragCancel={() => setDragging(false)}
        onDragEnd={({ active, over }) => { setDragging(false); if (over) reorder(String(active.id), String(over.id)); }}>
        <div className="sidebar-ribbon-main scrollbar-hidden">
          <SortableContext items={orderedIDs} strategy={verticalListSortingStrategy}>
            {ordered.map((item) => <RibbonDestination key={item.id} item={item} tabStop={item.id === tabStop} boundedNative={nativePresented} nativeTooltip={Boolean(activeWebsiteId)}
              onMove={(direction) => move(item.id, direction)}
              onMenu={websiteIDs.has(item.id) ? (x, y) => setMenu({ id: item.id, x, y }) : undefined} />)}
          </SortableContext>
          <Tooltip content={t("webWorkspace.newTab")} disabled={nativePresented} native={Boolean(activeWebsiteId)}>
            <button type="button" className="sidebar-ribbon-button sidebar-ribbon-add" aria-label={t("webWorkspace.newTab")}
              disabled={!onNewTab} onClick={onNewTab}>
              <Plus aria-hidden="true" /><span className="sidebar-ribbon-label">{t("webWorkspace.newTab")}</span>
            </button>
          </Tooltip>
        </div>
      </DndContext>
      {footer ? <div className="sidebar-ribbon-footer">{footer}</div> : null}
      <span className="sr-only" role="status" aria-live="polite">{announcement}</span>
    </nav>
    </HoverRevealScopeContext.Provider>
    </NativeRibbonFooterContext.Provider>
    {menu && menuWebsite ? <ThreadContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
      { label: t(menuWebsite.pinned ? "webWorkspace.unpinTab" : "webWorkspace.pinTab"), icon: menuWebsite.pinned ? <PinOff /> : <Pin />, onSelect: () => { focusRibbonItem(menuWebsite.id); onPinTab?.(menuWebsite.id, !menuWebsite.pinned); } },
      { label: t(menuWebsite.muted ? "webWorkspace.unmuteTab" : "webWorkspace.muteTab"), icon: menuWebsite.muted ? <Volume2 /> : <VolumeX />, disabled: !onMuteTab, onSelect: () => onMuteTab?.(menuWebsite.id, !menuWebsite.muted) },
      { label: t("webWorkspace.closeCurrentTab"), icon: <X />, onSelect: () => closeWebsite(menuWebsite.id) },
    ]} /> : null}
  </>;
}
