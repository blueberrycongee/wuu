import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { isTouchWebShell } from "./ComposerFocus";

// Settings pages share one hierarchy: a page header, titled sections, and
// bordered groups of rows. Page components render their own header so page
// actions (add, refresh) sit beside the title instead of on a row.

/** The settings window's titlebar slot. Inside it a page's title follows the
 * "Settings /" crumb and its actions sit at the strip's trailing edge, so the
 * page itself opens on its content. */
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
  const slot = useContext(SettingsTitlebarSlot);
  const showDescription = Boolean(description) && !isTouchWebShell();
  if (slot) {
    return (
      <>
        {createPortal(
          <>
            <h1 className="settings-page-title">{title}</h1>
            {actions ? <div className="settings-page-actions">{actions}</div> : null}
          </>,
          slot,
        )}
        {showDescription ? <p className="settings-page-description">{description}</p> : null}
      </>
    );
  }
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
  children,
}: {
  title?: string;
  description?: string;
  actions?: ReactNode;
  testID?: string;
  children: ReactNode;
}): JSX.Element {
  const showDescription = Boolean(description) && !isTouchWebShell();
  return (
    <section
      className="settings-section"
      data-wuu-component="settings-section"
      {...(testID ? { "data-testid": testID } : {})}
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
      {children}
    </section>
  );
}

export function SettingsGroup({ children }: { children: ReactNode }): JSX.Element {
  return <div className="settings-group" data-wuu-component="settings-group">{children}</div>;
}

export type SettingsStatusTone = "success" | "warning" | "danger" | "neutral";
