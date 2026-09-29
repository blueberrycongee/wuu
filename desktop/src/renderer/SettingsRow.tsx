import type { CSSProperties, ReactNode } from "react";
import { isTouchWebShell } from './ComposerFocus';

export function SettingsRow({
  title,
  description,
  hint,
  error,
  children,
  block = false,
}: {
  title: string;
  description?: ReactNode;
  hint?: string;
  error?: string;
  children: ReactNode;
  block?: boolean;
}): JSX.Element {
  return (
    <div className={`settings-row${block ? " settings-row-block" : ""}`} data-wuu-component="settings-row">
      <div className="settings-row-label">
        <span className="settings-row-label-title">{title}</span>
        {description ? <span className="settings-row-label-description">{description}</span> : null}
        {hint && !isTouchWebShell() ? <span className="settings-row-label-description">{hint}</span> : null}
      </div>
      <div className={block ? "settings-row-control-block" : "settings-row-control"}>{children}</div>
      {error ? <div className="settings-row-error settings-error" role="alert">{error}</div> : null}
    </div>
  );
}

/** Draws a numeric field's unit inside the field, after the number. A numeric
 * placeholder (a default such as "20,000") keeps its unit; a word ("Auto")
 * stands alone, since a unit would read as part of it. */
export function SettingsInputUnit({
  unit,
  placeholder,
  children,
}: {
  unit: string;
  placeholder?: string;
  children: ReactNode;
}): JSX.Element {
  // Wide (CJK) characters take two character cells.
  const cells = [...unit].reduce((width, char) => width + (/[\u2e80-\ua4cf\uac00-\ud7af\uf900-\ufaff\uff00-\uff60]/.test(char) ? 2 : 1), 0);
  return (
    <span
      className="settings-input-unit"
      data-unit={unit}
      data-unit-placeholder={/^[\d.,\s]+$/.test(placeholder ?? "") || undefined}
      style={{ "--settings-unit-chars": cells } as CSSProperties}
    >
      {children}
    </span>
  );
}
