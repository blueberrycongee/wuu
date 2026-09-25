import type { ReactNode } from "react";
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
