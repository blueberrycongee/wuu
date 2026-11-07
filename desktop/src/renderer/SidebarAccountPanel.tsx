import type { Ref } from "react";
import type { ApplicationDesktopChromeAccountActionV1 as ChromeAccountAction, ApplicationDesktopChromeAccountPanelV1 as ChromeAccountPanel } from "../../../packages/plugin-sdk/src/ui-desktop";
import { BarChart3, LogOut, Settings, Smartphone, UserRound } from "./WuuIcons";
import { handleFloatingMenuKeyDown } from "./ComposerFloatingMenu";
import "./SidebarAccount.css";

const accountIcons = { devices: UserRound, usage: BarChart3, account: Smartphone, settings: Settings, providers: Settings, logout: LogOut };
export function SidebarAccountPanel({ model, panelRef, id, onAction, onClose, trigger, onBlur }: {
  model: ChromeAccountPanel; panelRef?: Ref<HTMLDivElement>; id?: string; onAction: (action: ChromeAccountAction) => void;
  onClose: () => void; trigger?: HTMLElement | null; onBlur?: React.FocusEventHandler<HTMLDivElement>;
}): JSX.Element {
  return <div ref={panelRef} id={id} role="menu" aria-label={model.label} className="select-menu-panel sidebar-account-menu"
    onBlur={onBlur} onKeyDown={event => { if (!event.nativeEvent.isComposing) handleFloatingMenuKeyDown(event, onClose, trigger); }}>
    {model.profile ? <>
      <div className="sidebar-account-profile"><span className="sidebar-account-avatar" aria-hidden="true">{model.profile.initials}</span><div><strong>{model.profile.title}</strong><span>{model.profile.server}</span></div></div>
      <div className="sidebar-account-divider" role="separator" />
    </> : null}
    {model.rows.map(row => {
      const Icon = accountIcons[row.action];
      return <button key={row.action} role="menuitem" className="select-menu-item"
        disabled={row.disabled} onClick={() => onAction(row.action)}><Icon size={18} aria-hidden="true" /><span>{row.label}</span></button>;
    })}
    {model.error && <p className="sidebar-account-message settings-error" role="alert">{model.error}</p>}
    {model.notice && <p className="sidebar-account-message" role="status">{model.notice}</p>}
  </div>;
}
