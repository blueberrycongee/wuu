import { ChevronLeft, X } from "lucide-react";
import { useEffect } from "react";
import { SidebarNameDialog, type SidebarNameDialogProps } from "./SidebarNameDialog";
import { useI18n } from "./i18n";
import "./styles/channel-agent-settings.css";

// Conversation settings stay in a floating card over the chat. Outside a
// conversation, the same editor still uses the shared modal contract.
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
      if (props.closeOnEscape === false || event.key !== "Escape" || event.defaultPrevented || props.busy) return;
      event.preventDefault();
      props.onClose();
    };
    const dismiss = (event: PointerEvent) => {
      if (props.busy || props.backgrounded) return;
      const target = event.target;
      if (!(target instanceof Node)) return;
      if (target instanceof Element && target.closest(".channel-settings-panel, .channel-room-settings-trigger, .floating-menu-layer")) return;
      props.onClose();
    };
    window.addEventListener("keydown", escape);
    window.addEventListener("pointerdown", dismiss);
    return () => {
      window.removeEventListener("keydown", escape);
      window.removeEventListener("pointerdown", dismiss);
    };
  }, [props.inline, props.open, props.onClose, props.busy, props.closeOnEscape, props.backgrounded]);
  if (!props.inline) return <SidebarNameDialog {...props} />;
  if (!props.open) return null;
  return <aside
    id="channel-conversation-settings"
    className={`channel-settings-panel${props.dialogClassName ? ` ${props.dialogClassName}` : ""}${props.backgrounded ? " backgrounded" : ""}`}
    aria-labelledby={props.dialogTitleId}
    aria-hidden={props.backgrounded || undefined}
  >
    <header className="channel-settings-header">
      {props.onBack ? <button className="icon-button" type="button" disabled={props.busy} onClick={props.onBack} aria-label={t("common.back")}><ChevronLeft /></button> : null}
      <h2 id={props.dialogTitleId}>{props.dialogTitle}</h2>
      <button className="icon-button" type="button" disabled={props.busy} onClick={props.onClose} aria-label={t("common.close")}><X /></button>
    </header>
    <form className="channel-settings-form" aria-busy={props.busy || undefined} onSubmit={(event) => { event.preventDefault(); props.onSubmit(); }}>
      <fieldset disabled={props.busy} className="channel-settings-fields">{props.content}</fieldset>
      {props.error ? <div className="channel-error" role="alert">{props.error}</div> : null}
      {props.hideActions ? null : <div className="sidebar-name-dialog-actions channel-settings-actions">
        <button type="button" disabled={props.busy} onClick={props.onClose}>{props.cancelLabel}</button>
        <button type="submit" disabled={props.busy || props.submitDisabled}>{props.busy ? t("channels.saving") : props.submitLabel}</button>
      </div>}
    </form>
  </aside>;
}
