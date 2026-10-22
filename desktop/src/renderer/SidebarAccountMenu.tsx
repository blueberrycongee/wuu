import { useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { PhoneNavigationContext } from './PhoneNavigationContext';
import { SidebarAccountPanel, SidebarAccountTrigger } from "./SidebarAccountParts";
import { chromeOverlayApi, NativeRibbonFooterContext, useChromeOverlay } from "./ChromeOverlay";
import type { ChromeAccountAction, ChromeAccountPanel, ChromeAccountTrigger, ChromeOverlayRect } from "../shared/chromeOverlay";
import type { AccountView } from "./AccountPanel";
import { hostSupports } from "./HostCapabilities";
import { ENABLE_ACCOUNT } from "./FeatureFlags";
import { useRetainHoverOwner } from "./HoverReveal";
import { useI18n } from "./i18n";
import { composerMenuWidth, FloatingMenuPortal, useFloatingMenuFocus } from "./ComposerFloatingMenu";
import "./SidebarAccountMenu.css";

export function SidebarAccountMenu({ disabled, localOnly = false, matchTriggerWidth = true, onOpenSettings, onOpenAccount }: {
  disabled: boolean;
  localOnly?: boolean;
  matchTriggerWidth?: boolean;
  onOpenAccount?: () => void;
  onOpenSettings: (page: "providers" | "usage") => void;
}): React.JSX.Element {
  const { t } = useI18n();
  const phoneNavigation = useContext(PhoneNavigationContext);
  const nativeRibbon = useContext(NativeRibbonFooterContext);
  const [nativeAnchor, setNativeAnchor] = useState<ChromeOverlayRect | null>(null);
  const [nativeBounds, setNativeBounds] = useState<ChromeOverlayRect | null>(null);
  const useNative = Boolean(chromeOverlayApi());
  const id = useId();
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const generation = useRef(0);
  const [open, setOpen] = useState(false);
  useRetainHoverOwner(open);
  const [account, setAccount] = useState<AccountView>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState(false);
  const accountEnabled = ENABLE_ACCOUNT && !localOnly;
  const driver = accountEnabled && hostSupports("remoteAccount") && hostSupports("getRemoteControlSnapshot")
    ? window.wuu?.remoteAccount : undefined;

  useEffect(() => {
    if (!driver || disabled) return;
    let active = true;
    const refresh = async () => {
      const request = ++generation.current;
      try {
        const next = await driver("status");
        if (active && request === generation.current) { setAccount(next); setError(""); }
      } catch (cause) {
        if (active && request === generation.current) setError(cause instanceof Error ? cause.message : String(cause));
      }
    };
    void refresh();
    window.addEventListener("focus", refresh);
    window.addEventListener("wuu:account-changed", refresh);
    return () => {
      active = false;
      window.removeEventListener("focus", refresh);
      window.removeEventListener("wuu:account-changed", refresh);
    };
  }, [driver, disabled, open]);

  useFloatingMenuFocus(panel, "account", open && !useNative);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => {
      if (!panel.current?.contains(event.target as Node) && !anchor.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [open]);

  const close = () => { setOpen(false); setNativeAnchor(null); anchor.current?.focus(); };
  const navigate = (page: "providers" | "usage") => { close(); onOpenSettings(page); };
  const logout = async () => {
    if (!driver || busy) return;
    generation.current++;
    setBusy(true); setError(""); setNotice(false);
    try {
      const result: AccountView = await driver("logout");
      setAccount({}); setNotice(result.localLogoutOnly === true);
      window.dispatchEvent(new Event("wuu:account-changed"));
      if (!result.localLogoutOnly) close();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally { setBusy(false); }
  };
  const name = account.display_name || account.username || t("account.localMode");
  const initials = account.username ? name.slice(0, 2).toUpperCase() : undefined;
  const triggerModel: ChromeAccountTrigger = { title: name, initials, disabled, open, label: t("account.menu") };
  const panelModel: ChromeAccountPanel = { label: t("account.menu"),
    profile: account.username ? { title: name, initials: initials!, server: account.server } : undefined,
    rows: [
      ...(accountEnabled && phoneNavigation ? [{ action: "devices" as const, label: t("account.computersAndAccount") }] : []),
      { action: "usage", label: t("settings.usage") },
      ...(driver && onOpenAccount ? [{ action: "account" as const, label: t(account.username ? "account.manage" : "account.linkDevices") }] : []),
      { action: "providers", label: t("sidebar.settings") },
      ...(account.username ? [{ action: "logout" as const, label: t(busy ? "account.busy" : "account.logout"), disabled: busy }] : []),
    ], error, notice: notice ? t("account.localLogout") : undefined,
  };
  const action = (value: ChromeAccountAction) => {
    if (value === "logout") void logout();
    else if (value === "usage" || value === "providers") navigate(value);
    else if (value === "devices") { close(); phoneNavigation?.openDevices(); }
    else if (value === "account") { close(); onOpenAccount?.(); }
  };
  useLayoutEffect(() => {
    nativeRibbon?.publishAccount(triggerModel, bounds => { setNativeAnchor(bounds); setOpen(value => !value); });
    return () => nativeRibbon?.publishAccount(undefined);
  }, [nativeRibbon, JSON.stringify(triggerModel)]);
  useLayoutEffect(() => {
    if (!open || !useNative || !panel.current || !anchor.current) return;
    const measure = () => {
      const source = nativeAnchor ?? anchor.current!.getBoundingClientRect();
      const width = Math.min(window.innerWidth - 16, Math.max(composerMenuWidth(240), matchTriggerWidth ? source.width : 0));
      const height = Math.min(window.innerHeight - 16, panel.current!.scrollHeight);
      setNativeBounds({ x: Math.max(8, Math.min(window.innerWidth - width - 8, source.x)),
        y: Math.max(8, source.y - height - 6), width, height });
    };
    measure();
    const observer = new ResizeObserver(measure); observer.observe(panel.current);
    return () => observer.disconnect();
  }, [open, useNative, nativeAnchor, matchTriggerWidth, JSON.stringify(panelModel)]);
  useChromeOverlay(open && useNative, nativeBounds, { kind: "account", panel: panelModel }, command => {
    if (command.type === "account-action") action(command.action);
    if (command.type === "dismiss" && (command.reason === "escape" || command.reason === "tab")) anchor.current?.focus();
  }, () => { setOpen(false); setNativeAnchor(null); }, true);

  const content = <SidebarAccountPanel panelRef={panel} id={id} model={panelModel} onAction={action} onClose={() => setOpen(false)} trigger={anchor.current}
    onBlur={event => { if (!useNative && event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node) && event.relatedTarget !== anchor.current) setOpen(false); }} />;
  return <div className="sidebar-account">
    <SidebarAccountTrigger model={triggerModel} anchorRef={anchor} controls={open ? id : undefined} onOpen={() => { setNativeAnchor(null); setOpen(!open); }} />
    {open && (useNative ? <div aria-hidden="true" style={{ position: "fixed", visibility: "hidden", pointerEvents: "none", width: composerMenuWidth(240),
      "--floating-menu-available-width": `${composerMenuWidth(240)}px`, "--floating-menu-available-height": "400px" } as React.CSSProperties}>{content}</div>
      : <FloatingMenuPortal anchorRef={anchor} owner="sidebar-account" placement="above" align="left"
        width={composerMenuWidth(240)} matchAnchorWidth={matchTriggerWidth} flip>{content}</FloatingMenuPortal>)}
  </div>;
}
