import { useState, type ReactNode } from "react";
import { Pencil } from "./WuuIcons";
import { useI18n } from "./i18n";
import { useConnectionDisplayPreferences } from "./ConnectionDisplayPreferences";

/** A local display alias never changes the connection's configuration identity. */
export function ConnectionTitle({ connectionKey, fallback, disabled = false, suffix }: {
  connectionKey: string; fallback: string; disabled?: boolean; suffix?: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const preferences = useConnectionDisplayPreferences();
  const label = preferences.label(connectionKey, fallback);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(label);
  const [error, setError] = useState("");
  function save(): void { try { preferences.setAlias(connectionKey, draft.trim()); setError(""); setEditing(false); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } }
  return <span className="connection-title-edit">
    {editing ? <span className="connection-title-form">
      <input className="settings-input" aria-label={t("connection.displayName")} value={draft} autoFocus disabled={disabled}
        onChange={event => setDraft(event.target.value)} onKeyDown={event => {
          if (event.key === "Escape") { event.preventDefault(); setEditing(false); }
          if (event.key === "Enter") { event.preventDefault(); save(); }
        }} />
      <button type="button" className="settings-button" disabled={disabled} onClick={() => { save(); }}>{t("common.save")}</button>
      <button type="button" className="settings-button settings-button-ghost" onClick={() => setEditing(false)}>{t("common.cancel")}</button>
    </span> : <><span>{label}</span><button type="button" className="settings-button settings-button-ghost settings-icon-button" aria-label={t("connection.editName")} disabled={disabled} onClick={() => { setDraft(label); setEditing(true); }}><Pencil className="icon" /></button></>}
    {error ? <span className="settings-error" role="alert">{error}</span> : null}
    {suffix}
  </span>;
}
