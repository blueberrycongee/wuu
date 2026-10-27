import { useEffect, useState } from "react";
import type { BrowserCookieImportResult, BrowserProfile } from "../shared/protocol";
import { useSettingsControls } from "./SettingsControlsContext";
import { useI18n } from "./i18n";
import { PREVIEW_BROWSER_PROFILES } from "./onboardingPreview";
import { SettingsGroup } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { LoaderCircle, RefreshCw } from "./WuuIcons";

export function OnboardingBrowserImport({ preview, busy, importError, results, onImport }: {
  preview: boolean;
  busy: string;
  importError: string;
  results: Record<string, BrowserCookieImportResult>;
  onImport: (profile: BrowserProfile) => Promise<void>;
}): JSX.Element {
  const { t, locale } = useI18n();
  const controls = useSettingsControls();
  const [profiles, setProfiles] = useState<BrowserProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [discoveryError, setDiscoveryError] = useState("");
  const error = importError || discoveryError;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setDiscoveryError("");
    void (async () => {
      try {
        const found = preview ? PREVIEW_BROWSER_PROFILES : await controls.listBrowserProfiles();
        if (!cancelled) setProfiles(found);
      } catch (reason) {
        if (!cancelled) setDiscoveryError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [preview, attempt, controls]);

  const mostRecent = profiles.find((profile) => profile.hasCookies && profile.lastUsedAt);
  return (
    <div className="onboarding-body onboarding-browser-import" data-scroll-fade="">
      <button className="settings-button settings-button-ghost onboarding-browser-refresh" type="button" disabled={loading || Boolean(busy)} onClick={() => setAttempt((value) => value + 1)}>
        <RefreshCw className="icon" aria-hidden="true" />{t("settings.browserRefresh")}
      </button>
      {loading ? <p className="onboarding-status" role="status"><LoaderCircle className="icon settings-spin" aria-hidden="true" />{t("settings.browserLoading")}</p> : null}
      {!loading && !error && profiles.length === 0 ? <p className="onboarding-status" role="status">{t("settings.browserNoProfiles")}</p> : null}
      {!loading && profiles.length > 0 ? <SettingsGroup>
        {profiles.map((profile) => {
          const result = results[profile.profilePath];
          const label = `${profile.appName} · ${profile.profileName}`;
          return <SettingsRow
              key={profile.profilePath}
              title={label}
              description={result ? <span role="status">{t("settings.browserImportResult", {
                imported: result.imported, discovered: result.discovered,
                skipped: result.skippedExpired + result.skippedInvalid, failed: result.failed,
              })}</span> : profile.userName}
              hint={!profile.hasCookies ? t("settings.browserNoCookies") : profile === mostRecent ? t("onboarding.browserMostRecent", { date: new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(profile.lastUsedAt) }) : undefined}
            >
              <button className="settings-button" type="button" disabled={Boolean(busy) || !profile.hasCookies} aria-label={`${t("settings.browserImportAction")} · ${label}`} onClick={() => void onImport(profile)}>
                {busy === profile.profilePath ? t("settings.browserImporting") : t("settings.browserImportAction")}
              </button>
            </SettingsRow>;
        })}
      </SettingsGroup> : null}
      {error ? <p className="onboarding-error" role="alert">{error}</p> : null}
      {profiles.some((profile) => profile.hasCookies) ? <p className="onboarding-lead">{t("onboarding.browserImportHint")}</p> : null}
    </div>
  );
}
