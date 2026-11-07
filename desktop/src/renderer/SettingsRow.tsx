import type { ReactNode } from "react";
import { isTouchWebShell } from './ComposerFocus';

export function SettingsRow({
  title,
  description,
  hint,
  error,
  detail,
  children,
  block = false,
  hideDescriptionWhenNarrow = false,
}: {
  title: string;
  description?: ReactNode;
  hint?: string;
  error?: string;
  detail?: ReactNode;
  children: ReactNode;
  block?: boolean;
  hideDescriptionWhenNarrow?: boolean;
}): JSX.Element {
  return (
    <div className={`settings-row${block ? " settings-row-block" : ""}`} data-wuu-component="settings-row" data-hide-description-when-narrow={hideDescriptionWhenNarrow || undefined}>
      <div className="settings-row-label">
        <span className="settings-row-label-title">{title}</span>
        {description ? <span className="settings-row-label-description">{description}</span> : null}
        {hint && !isTouchWebShell() ? <span className="settings-row-label-description">{hint}</span> : null}
      </div>
      <div className={block ? "settings-row-control-block" : "settings-row-control"}>{children}</div>
      {error ? <div className="settings-row-error settings-error" role="alert">{error}</div> : null}
      {detail ? <div className="settings-row-detail">{detail}</div> : null}
    </div>
  );
}

/** A short fact beside a row title, such as the setting's reach or when it applies. */
export function SettingsBadge({ children, tone = "neutral" }: { children: ReactNode; tone?: "neutral" | "warning" }): JSX.Element {
  return <span className="settings-badge" data-tone={tone}>{children}</span>;
}
