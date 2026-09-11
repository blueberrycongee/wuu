import { useEffect, useRef } from "react";
import { ChevronRight } from "lucide-react";
import { useI18n } from "./i18n";
import "./AccountConnectionOnboarding.css";

/** Optional device-linking onboarding, separate from account forms and management. */
export function AccountConnectionOnboarding({ onContinue, error, onError }: {
  onContinue: () => void;
  error: string;
  onError: (message: string) => void;
}): React.JSX.Element {
  const { t } = useI18n();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { heading.current?.focus(); }, []);
  return <section className="account-panel account-onboarding" aria-labelledby="account-onboarding-heading">
    <header className="account-onboarding-heading">
      <h2 id="account-onboarding-heading" ref={heading} tabIndex={-1}>{t("account.linkDevices")}</h2>
      <p>{t("account.linkIntro")}</p>
    </header>
    <div className="account-device-animation" aria-hidden="true">
      <svg viewBox="0 0 560 208" fill="none" focusable="false">
        <g transform="translate(27 0)">
        <path className="account-transfer-track" d="M194 88H372M194 124H372" strokeDasharray="3 7" />
        <g className="account-transfer-packet account-transfer-out">
          <rect x="198" y="76" width="32" height="24" rx="7" />
          <path d="M207 85h14m-14 6h9" />
        </g>
        <g className="account-transfer-packet account-transfer-in">
          <rect x="340" y="112" width="32" height="24" rx="7" />
          <path d="m349 124 4 4 9-9" />
        </g>
        <g className="account-device-outline">
          <rect x="56" y="54" width="132" height="90" rx="10" />
          <path d="M57 126h130m-65 18v17m-24 0h48" />
          <rect x="378" y="36" width="72" height="130" rx="14" />
          <path d="M403 46h22m-18 108h14" />
        </g>
        <g className="account-device-screen">
          <rect x="70" y="68" width="30" height="42" rx="4" />
          <path d="M111 76h57m-57 12h42m-42 12h49" />
          <rect x="390" y="66" width="48" height="26" rx="5" />
          <path d="M399 76h29m-29 7h18m-18 23h29m-29 10h22m-22 10h26" />
        </g>
        <circle className="account-device-signal account-device-signal-computer" cx="176" cy="57" r="5" />
        <circle className="account-device-signal account-device-signal-phone" cx="446" cy="39" r="5" />
        </g>
      </svg>
    </div>
    <ol className="account-link-steps">
      <li><strong>{t("account.linkServer")}</strong><p>{t("account.linkServerDetail")}</p></li>
      <li><strong>{t("account.linkAccount")}</strong><p>{t("account.linkAccountDetail")}</p></li>
    </ol>
    {error && <p role="alert" className="settings-error">{error}</p>}
    <button className="account-primary account-onboarding-continue" type="button" onClick={onContinue}>{t("account.linkReady")}<ChevronRight size={18} aria-hidden="true" /></button>
    <details className="account-deploy-guide">
      <summary>{t("account.linkDeploy")}</summary>
      <p>{t("account.linkDeployDetail")}</p>
      <a href="https://github.com/blueberrycongee/wuu/blob/main/deploy/remote/README.md" target="_blank" rel="noreferrer" onClick={event => {
        if (window.wuu?.openExternal) {
          event.preventDefault();
          void window.wuu.openExternal(event.currentTarget.href).catch(cause => onError(String(cause)));
        }
      }}>{t("account.linkGuide")}</a>
    </details>
  </section>;
}
