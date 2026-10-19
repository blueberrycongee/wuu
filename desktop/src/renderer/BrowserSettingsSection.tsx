import { useEffect, useRef, useState } from "react";
import type {
  BrowserCookieImportResult,
  BrowserExtensionRecord,
  BrowserProfile,
} from "../shared/protocol";
import { Folder, Globe, Loader2, RefreshCw, Trash2 } from "./WuuIcons";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

function available(): boolean {
  const api = window.wuu;
  return Boolean(
    api?.listBrowserProfiles &&
      api.importBrowserCookies &&
      api.listBrowserExtensions &&
      api.addBrowserExtension &&
      api.setBrowserExtensionEnabled &&
      api.removeBrowserExtension,
  );
}

export function BrowserSettingsSection(): JSX.Element | null {
  const { t } = useI18n();
  const supported = available();
  const platform = window.wuu?.platform;
  const [profiles, setProfiles] = useState<BrowserProfile[]>([]);
  const [extensions, setExtensions] = useState<BrowserExtensionRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [profileError, setProfileError] = useState("");
  const [extensionError, setExtensionError] = useState("");
  const [busy, setBusy] = useState("");
  const [result, setResult] = useState<BrowserCookieImportResult>();
  const mounted = useRef(true);
  const revision = useRef(0);

  useEffect(() => {
    mounted.current = true;
    if (!supported) {
      setLoading(false);
      return () => {
        mounted.current = false;
      };
    }
    const current = ++revision.current;
    void (async () => {
      const [profileResult, extensionResult] = await Promise.allSettled([
        window.wuu.listBrowserProfiles!(),
        window.wuu.listBrowserExtensions!(),
      ]);
      if (!mounted.current || current !== revision.current) return;
      if (profileResult.status === "fulfilled") {
        setProfiles(profileResult.value);
        setProfileError("");
      } else {
        setProfileError(messageOf(profileResult.reason));
      }
      if (extensionResult.status === "fulfilled") {
        setExtensions(extensionResult.value);
        setExtensionError("");
      } else {
        setExtensionError(messageOf(extensionResult.reason));
      }
      setLoading(false);
    })();
    return () => {
      mounted.current = false;
    };
  }, [supported]);

  if (!supported) return null;

  const importCookies = async (profile: BrowserProfile): Promise<void> => {
    if (busy) return;
    setBusy(`import:${profile.profilePath}`);
    setProfileError("");
    setResult(undefined);
    try {
      const next = await window.wuu.importBrowserCookies!(profile.profilePath);
      if (mounted.current) setResult(next);
    } catch (reason) {
      if (mounted.current) setProfileError(messageOf(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  };

  const addExtension = async (): Promise<void> => {
    if (busy) return;
    setBusy("add");
    setExtensionError("");
    try {
      const next = await window.wuu.addBrowserExtension!();
      if (mounted.current) setExtensions(next);
    } catch (reason) {
      if (mounted.current) setExtensionError(messageOf(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  };

  const toggleExtension = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (busy) return;
    setBusy(`toggle:${extension.id}`);
    setExtensionError("");
    try {
      const next = await window.wuu.setBrowserExtensionEnabled!(
        extension.id,
        !extension.enabled,
      );
      if (mounted.current) setExtensions(next);
    } catch (reason) {
      if (mounted.current) setExtensionError(messageOf(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  };

  const removeExtension = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (busy) return;
    setBusy(`remove:${extension.id}`);
    setExtensionError("");
    try {
      const next = await window.wuu.removeBrowserExtension!(extension.id);
      if (mounted.current) setExtensions(next);
    } catch (reason) {
      if (mounted.current) setExtensionError(messageOf(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  };

  return (
    <>
      <SettingsPageHeader
        title={t("settings.browser")}
        actions={
          <button
            className="settings-button settings-button-ghost"
            type="button"
            disabled={loading}
            onClick={() => {
              setLoading(true);
              setProfileError("");
              setExtensionError("");
              ++revision.current;
              void Promise.allSettled([
                window.wuu.listBrowserProfiles!().then(setProfiles),
                window.wuu.listBrowserExtensions!().then(setExtensions),
              ]).then(([profileResult, extensionResult]) => {
                if (!mounted.current) return;
                if (profileResult.status === "rejected") {
                  setProfileError(messageOf(profileResult.reason));
                }
                if (extensionResult.status === "rejected") {
                  setExtensionError(messageOf(extensionResult.reason));
                }
                setLoading(false);
              });
            }}
          >
            <RefreshCw size={16} aria-hidden="true" />
            {t("settings.browserRefresh")}
          </button>
        }
      />

      <SettingsSection
        title={t("settings.browserImportTitle")}
        testID="settings-browser-import"
      >
        <SettingsGroup>
          {loading ? (
            <SettingsRow title={t("settings.browserLoading")}>
              <Loader2 className="icon settings-spin" aria-hidden="true" />
            </SettingsRow>
          ) : profiles.length === 0 ? (
            <div className="settings-group-empty" role="status">
              <Globe size={18} aria-hidden="true" />
              <span>{t("settings.browserNoProfiles")}</span>
            </div>
          ) : (
            profiles.map((profile) => (
              <SettingsRow
                key={profile.profilePath}
                title={`${profile.appName} · ${profile.profileName}`}
                hint={profile.hasCookies ? undefined : t("settings.browserNoCookies")}
              >
                <div className="browser-settings-actions">
                  <button
                    className="settings-button"
                    type="button"
                    disabled={
                      Boolean(busy) || platform !== "darwin" || !profile.hasCookies
                    }
                    onClick={() => void importCookies(profile)}
                  >
                    {busy === `import:${profile.profilePath}`
                      ? t("settings.browserImporting")
                      : t("settings.browserImportAction")}
                  </button>
                </div>
              </SettingsRow>
            ))
          )}
        </SettingsGroup>
        {platform !== "darwin" ? (
          <p className="settings-section-note">{t("settings.browserMacOnly")}</p>
        ) : null}
        {profileError && !result ? <p className="settings-error" role="alert">{profileError}</p> : null}
        {result ? (
          <p className="settings-section-note" role="status">
            {t("settings.browserImportResult", {
              imported: result.imported,
              discovered: result.discovered,
              skipped: result.skippedExpired + result.skippedInvalid,
              failed: result.failed,
            })}
          </p>
        ) : null}
      </SettingsSection>

      <SettingsSection
        title={t("settings.browserExtensionsTitle")}
        actions={
          <button
            className="settings-button settings-button-ghost"
            type="button"
            disabled={Boolean(busy)}
            onClick={() => void addExtension()}
          >
            <Folder size={16} aria-hidden="true" />
            {t("settings.browserExtensionsAdd")}
          </button>
        }
        testID="settings-browser-extensions"
      >
        <SettingsGroup>
          {extensions.length === 0 ? (
            <div className="settings-group-empty" role="status">
              <Globe size={18} aria-hidden="true" />
              <span>{t("settings.browserExtensionsEmpty")}</span>
            </div>
          ) : (
            extensions.map((extension) => (
              <SettingsRow
                key={extension.id}
                title={extension.name}
                error={extension.error}
                hint={extension.enabled && !extension.loaded ? t("settings.browserExtensionsNotLoaded") : undefined}
              >
                <div className="browser-settings-actions">
                  <button
                    className="settings-switch"
                    type="button"
                    role="switch"
                    aria-label={
                      extension.enabled
                        ? t("settings.browserExtensionsDisable", { name: extension.name })
                        : t("settings.browserExtensionsEnable", { name: extension.name })
                    }
                    aria-checked={extension.enabled}
                    disabled={Boolean(busy)}
                    onClick={() => void toggleExtension(extension)}
                  >
                    <span className="settings-switch-thumb" aria-hidden="true" />
                  </button>
                  <button
                    className="settings-button settings-button-ghost settings-icon-button"
                    type="button"
                    aria-label={t("settings.browserExtensionsRemove", { name: extension.name })}
                    disabled={Boolean(busy)}
                    onClick={() => void removeExtension(extension)}
                  >
                    {busy === `remove:${extension.id}` ? (
                      <Loader2 className="icon settings-spin" aria-hidden="true" />
                    ) : (
                      <Trash2 size={16} aria-hidden="true" />
                    )}
                  </button>
                </div>
              </SettingsRow>
            ))
          )}
        </SettingsGroup>
        {extensionError ? <p className="settings-error" role="alert">{extensionError}</p> : null}
      </SettingsSection>
    </>
  );
}

function messageOf(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason);
}
