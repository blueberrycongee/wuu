import { type FormEvent, type ReactNode, useLayoutEffect, useId, useRef } from "react";
import { ArrowLeft } from "./WuuIcons";
import { useI18n } from "./i18n";

export type ConnectionPageProps = {
  title: ReactNode;
  ariaLabel?: string;
  icon?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  closeDisabled?: boolean;
  backTestId?: string;
  panelClassName?: string;
  asForm?: boolean;
  onSubmit?: (event: FormEvent<HTMLFormElement>) => void;
};

/** Page-contained connection settings with ordinary navigation and form behavior. */
export function ConnectionPage({
  title,
  ariaLabel,
  icon,
  subtitle,
  actions,
  children,
  footer,
  onClose,
  closeDisabled = false,
  backTestId = "settings-connection-back",
  panelClassName,
  asForm = false,
  onSubmit,
}: ConnectionPageProps): JSX.Element {
  const { t } = useI18n();
  const titleId = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  useLayoutEffect(() => {
    // Inline navigation shares the settings scrollport. Start the new page at
    // its Back control before focusing its heading, without browser scrolling.
    const scrollport = heading.current?.closest<HTMLElement>(".settings-scroll");
    if (scrollport) scrollport.scrollTop = 0;
    heading.current?.focus({ preventScroll: true });
  }, []);
  const content = <>
    <button
      type="button"
      className="settings-button settings-button-ghost connection-page-back"
      data-testid={backTestId}
      disabled={closeDisabled}
      onClick={onClose}
    >
      <ArrowLeft className="icon" />
      {t("common.back")}
    </button>
    <header className="connection-page-header">
      {icon ? <span className="connection-page-icon">{icon}</span> : null}
      <div className="connection-page-heading">
        <h2 ref={heading} tabIndex={-1} id={titleId} className="connection-page-title">{title}</h2>
        {subtitle ? <div className="connection-page-subtitle">{subtitle}</div> : null}
      </div>
      {actions ? <div className="connection-page-actions">{actions}</div> : null}
    </header>
    <div className="connection-page-body">{children}</div>
    {footer ? <div className="connection-page-footer">{footer}</div> : null}
  </>;
  return <section
    className={["connection-page", panelClassName].filter(Boolean).join(" ")}
    role="region"
    aria-label={ariaLabel}
    aria-labelledby={ariaLabel ? undefined : titleId}
  >
    {icon ? <div className="connection-page-watermark-clip" aria-hidden="true"><div className="connection-page-watermark">{icon}</div></div> : null}
    {asForm ? <form className="connection-page-form" onSubmit={event => {
      event.preventDefault();
      onSubmit?.(event);
    }}>{content}</form> : content}
  </section>;
}
