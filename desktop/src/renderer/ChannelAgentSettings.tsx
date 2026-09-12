import { ChevronLeft, X } from "lucide-react";
import { useEffect } from "react";
import { SidebarNameDialog, type SidebarNameDialogProps } from "./SidebarNameDialog";
import { useI18n } from "./i18n";
import "./styles/channel-agent-settings.css";

// Keep the existing editor and save contract, but put conversation settings in
// normal layout flow rather than a modal portal.
export function ChannelAgentSettings(props: SidebarNameDialogProps & {
  inline: boolean;
  onBack?: () => void;
  busy?: boolean;
  error?: string;
}) {
  const { t } = useI18n();
  useEffect(() => {
    if (!props.inline || !props.open) return;
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || props.busy) return;
      event.preventDefault();
      props.onClose();
    };
    window.addEventListener("keydown", escape);
    return () => window.removeEventListener("keydown", escape);
  }, [props.inline, props.open, props.onClose, props.busy]);
  if (!props.inline) return <SidebarNameDialog {...props} />;
  if (!props.open) return null;
  return <aside id="channel-conversation-settings" className="channel-settings-panel" aria-labelledby={props.dialogTitleId}>
    <header className="channel-settings-header">
      {props.onBack ? <button className="icon-button" type="button" disabled={props.busy} onClick={props.onBack} aria-label={t("common.back")}><ChevronLeft /></button> : null}
      <h2 id={props.dialogTitleId}>{props.dialogTitle}</h2>
      <button className="icon-button" type="button" disabled={props.busy} onClick={props.onClose} aria-label={t("common.close")}><X /></button>
    </header>
    <form className="channel-settings-form" aria-busy={props.busy || undefined} onSubmit={(event) => { event.preventDefault(); props.onSubmit(); }}>
      <fieldset disabled={props.busy} className="channel-settings-fields">{props.content}</fieldset>
      {props.error ? <div className="channel-error" role="alert">{props.error}</div> : null}
      <div className="sidebar-name-dialog-actions channel-settings-actions">
        <button type="button" disabled={props.busy} onClick={props.onClose}>{props.cancelLabel}</button>
        <button type="submit" disabled={props.busy || props.submitDisabled}>{props.busy ? t("channels.saving") : props.submitLabel}</button>
      </div>
    </form>
  </aside>;
}
