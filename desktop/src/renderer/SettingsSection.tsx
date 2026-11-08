import { createContext, type ReactNode } from "react";
import { isTouchWebShell } from "./ComposerFocus";

// Settings pages share one hierarchy: a page header, titled sections, and
// clean divided rows. Page components render their own header at the top of
// the reading column so titles, descriptions, and page-level actions stay
// aligned on the same reading axis (Linear / Notion layout).

export const SettingsTitlebarSlot = createContext<HTMLElement | null>(null);

export function SettingsPageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
}): JSX.Element {
  const showDescription = Boolean(description) && !isTouchWebShell();
  return (
    <header className="settings-page-header">
      <div className="settings-page-heading">
        <h1 className="settings-page-title">{title}</h1>
        {showDescription ? <p className="settings-page-description">{description}</p> : null}
      </div>
      {actions ? <div className="settings-page-actions">{actions}</div> : null}
    </header>
  );
}

export function SettingsSection({
  title,
  description,
  actions,
  testID,
  ariaBusy,
  children,
}: {
  title?: string;
  description?: string;
  actions?: ReactNode;
  testID?: string;
  ariaBusy?: boolean;
  children: ReactNode;
}): JSX.Element {
  const showDescription = Boolean(description) && !isTouchWebShell();
  return (
    <section
      className="settings-section"
      data-wuu-component="settings-section"
      {...(testID ? { "data-testid": testID } : {})}
      {...(ariaBusy === undefined ? {} : { "aria-busy": ariaBusy })}
    >
      {title || showDescription || actions ? (
        <header className="settings-section-header">
          <div className="settings-section-heading">
            {title ? <h2 className="settings-section-title">{title}</h2> : null}
            {showDescription ? <p className="settings-section-description">{description}</p> : null}
          </div>
          {actions ? <div className="settings-section-actions">{actions}</div> : null}
        </header>
      ) : null}
      <div className="settings-section-body">{children}</div>
    </section>
  );
}

export function SettingsGroup({ children, className }: { children: ReactNode; className?: string }): JSX.Element {
  return <div className={className ? `settings-group ${className}` : "settings-group"} data-wuu-component="settings-group">{children}</div>;
}

/** Stands in for a list that has nothing to show: what would appear here,
 * optionally how it gets here, and the one action that fills it. */
export function SettingsEmptyState({
  title,
  description,
  action,
  testID,
}: {
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  testID?: string;
}): JSX.Element {
  const showDescription = Boolean(description) && !isTouchWebShell();
  return (
    <div
      className="settings-empty-state"
      data-wuu-component="settings-empty-state"
      {...(testID ? { "data-testid": testID } : {})}
    >
      <p className="settings-empty-state-title">{title}</p>
      {showDescription ? <p className="settings-empty-state-description">{description}</p> : null}
      {action ? <div className="settings-empty-state-action">{action}</div> : null}
    </div>
  );
}

export type SettingsStatusTone = "success" | "warning" | "danger" | "neutral";
