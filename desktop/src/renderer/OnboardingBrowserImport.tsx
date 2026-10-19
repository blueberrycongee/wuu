import { useEffect, useRef, useState } from "react";
import type { BrowserCookieImportResult, BrowserProfile } from "../shared/protocol";
import { useI18n } from "./i18n";
import { PREVIEW_BROWSER_PROFILES } from "./onboardingPreview";
import { SettingsGroup } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { LoaderCircle, RefreshCw } from "./WuuIcons";

export function OnboardingBrowserImport({ preview, onBusyChange, onImported }: {
  preview: boolean;
  onBusyChange: (busy: boolean) => void;
  onImported: () => void;
}): JSX.Element {
  const { t, locale } = useI18n();
  const [profiles, setProfiles] = useState<BrowserProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [results, setResults] = useState<Record<string, BrowserCookieImportResult>>({});
  const importing = useRef(false);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void (async () => {
      try {
        const found = preview ? PREVIEW_BROWSER_PROFILES : await window.wuu.listBrowserProfiles!();
        if (!cancelled) setProfiles(found);
      } catch (reason) {
        if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [preview, attempt]);

  async function importCookies(profile: BrowserProfile): Promise<void> {
    if (importing.current) return;
    importing.current = true;
    setBusy(profile.profilePath);
    onBusyChange(true);
    setError("");
    setResults((current) => {
      const next = { ...current };
      delete next[profile.profilePath];
      return next;
    });
    try {
      // Preview never reads a local profile or invokes the production importer.
      const result: BrowserCookieImportResult = preview
        ? { ...profile, discovered: 24, imported: 22, skippedExpired: 2, skippedInvalid: 0, failed: 0 }
        : await window.wuu.importBrowserCookies!(profile.profilePath);
      if (mounted.current) {
        setResults((current) => ({ ...current, [profile.profilePath]: result }));
        if (result.imported > 0) onImported();
      }
    } catch (reason) {
      if (mounted.current) setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      importing.current = false;
      if (mounted.current) {
        setBusy("");
        onBusyChange(false);
      }
    }
  }

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
              <button className="settings-button" type="button" disabled={Boolean(busy) || !profile.hasCookies} aria-label={`${t("settings.browserImportAction")} · ${label}`} onClick={() => void importCookies(profile)}>
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
