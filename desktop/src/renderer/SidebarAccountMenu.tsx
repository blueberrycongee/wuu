import { useEffect, useId, useRef, useState } from "react";
import { BarChart3, ChevronsUpDown, LogOut, Settings, UserRound } from "lucide-react";
import type { AccountView } from "./AccountPanel";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { hostSupports } from "./HostCapabilities";
import { useI18n } from "./i18n";
import "./SidebarAccountMenu.css";

export function SidebarAccountMenu({ disabled, onOpenSettings }: {
  disabled: boolean;
  onOpenSettings: (page: "providers" | "remote" | "usage") => void;
}): React.JSX.Element {
  const { t } = useI18n();
  const id = useId();
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const generation = useRef(0);
  const [open, setOpen] = useState(false);
  const [account, setAccount] = useState<AccountView>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState(false);
  const driver = hostSupports("remoteAccount") && hostSupports("getRemoteControlSnapshot")
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

  useEffect(() => {
    if (!open) return;
    panel.current?.querySelector<HTMLButtonElement>("[role=menuitem]")?.focus();
    const outside = (event: PointerEvent) => {
      if (!panel.current?.contains(event.target as Node) && !anchor.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside, true);
    return () => document.removeEventListener("pointerdown", outside, true);
  }, [open]);

  const close = () => { setOpen(false); anchor.current?.focus(); };
  const navigate = (page: "providers" | "remote" | "usage") => { close(); onOpenSettings(page); };
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
  const avatar = <span className="sidebar-account-avatar" aria-hidden="true">{account.username ? account.username.slice(0, 2).toUpperCase() : <UserRound size={18} />}</span>;

  return <>
    <button ref={anchor} className="sidebar-account-trigger" type="button" disabled={disabled}
      aria-label={t("account.menu")} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? id : undefined}
      onClick={() => setOpen(!open)} onKeyDown={event => {
        if (event.key === "ArrowUp" || event.key === "ArrowDown") { event.preventDefault(); setOpen(true); }
      }}>
      {avatar}<span className="sidebar-account-name">{account.username || (driver ? t("account.connect") : "Wuu")}</span><ChevronsUpDown size={14} aria-hidden="true" />
    </button>
    {open && <FloatingMenuPortal anchorRef={anchor} owner="sidebar-account" placement="above" align="left" width={280}>
      <div ref={panel} id={id} role="menu" aria-label={t("account.menu")} className="select-menu-panel sidebar-account-menu"
        onBlur={event => { if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget as Node) && event.relatedTarget !== anchor.current) setOpen(false); }}
        onKeyDown={event => {
          if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); }
          if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            const items = Array.from(panel.current?.querySelectorAll<HTMLButtonElement>("[role=menuitem]:not(:disabled)") ?? []);
            const index = items.indexOf(document.activeElement as HTMLButtonElement);
            const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
            items[next]?.focus();
          }
        }}>
        <div className="sidebar-account-profile">{avatar}<div><strong>{account.username || "Wuu"}</strong><span>{account.username ? account.server : t(driver ? "account.signedOut" : "account.local")}</span></div></div>
        <div className="sidebar-account-divider" role="separator" />
        <button role="menuitem" className="select-menu-item" onClick={() => navigate("usage")}><BarChart3 size={18} aria-hidden="true" /><span>{t("settings.usage")}</span></button>
        {driver && <button role="menuitem" className="select-menu-item" onClick={() => navigate("remote")}><UserRound size={18} aria-hidden="true" /><span>{t(account.username ? "account.manage" : "account.connect")}</span></button>}
        <button role="menuitem" data-settings-page="providers" className="select-menu-item" onClick={() => navigate("providers")}><Settings size={18} aria-hidden="true" /><span>{t("sidebar.settings")}</span></button>
        {account.username && <button role="menuitem" className="select-menu-item" disabled={busy} onClick={() => void logout()}><LogOut size={18} aria-hidden="true" /><span>{t(busy ? "account.busy" : "account.logout")}</span></button>}
        {error && <p className="sidebar-account-message settings-error" role="alert">{error}</p>}
        {notice && <p className="sidebar-account-message" role="status">{t("account.localLogout")}</p>}
      </div>
    </FloatingMenuPortal>}
  </>;
}
