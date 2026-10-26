import { Tooltip } from "./Tooltip";
import { useSettingsOperationScope } from "./SettingsOperationScope";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { useCallback, useEffect, useRef, useState } from "react";
import type {
  BrowserCookieImportResult,
  BrowserExtensionRecord,
  BrowserProfile,
} from "../shared/protocol";
import { ChevronDown, Folder, Globe, Loader2, RefreshCw, Settings, Trash2 } from "./WuuIcons";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";
import { SettingsRow } from "./SettingsRow";
import { useI18n } from "./i18n";

export function BrowserSettingsSection(): JSX.Element | null {
  const scope = useSettingsOperationScope();
  const controls = scope.controls;
  const { t } = useI18n();
  const supported = controls.capabilities.browser;
  const platform = controls.capabilities.platform;
  const [profiles, setProfiles] = useState<BrowserProfile[]>([]);
  const [loadedScope, setLoadedScope] = useState<typeof scope>();
  const [extensions, setExtensions] = useState<BrowserExtensionRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [profileError, setProfileError] = useState("");
  const [extensionError, setExtensionError] = useState("");
  const [busy, setBusy] = useState("");
  const [expandedExtension, setExpandedExtension] = useState("");
  const [result, setResult] = useState<BrowserCookieImportResult>();
  const revision = useRef(0);

  const refresh = useCallback(async (): Promise<void> => {
    if (!scope.active || !supported) return;
    const current = ++revision.current;
    setLoading(true); setProfileError(""); setExtensionError("");
    const [profileResult, extensionResult] = await Promise.allSettled([
      Promise.resolve().then(() => controls.listBrowserProfiles()),
      Promise.resolve().then(() => controls.listBrowserExtensions()),
    ]);
    if (!scope.active || current !== revision.current) return;
    if (profileResult.status === "fulfilled") setProfiles(profileResult.value);
    else if (!isPluginLifecycleCancellation(profileResult.reason)) setProfileError(messageOf(profileResult.reason));
    if (extensionResult.status === "fulfilled") setExtensions(extensionResult.value);
    else if (!isPluginLifecycleCancellation(extensionResult.reason)) setExtensionError(messageOf(extensionResult.reason));
    setLoadedScope(scope);
    setLoading(false);
  }, [scope, supported]);

  useEffect(() => {
    setLoadedScope(undefined); setProfiles([]); setExtensions([]); setBusy(""); setResult(undefined); setExpandedExtension("");
    setProfileError(""); setExtensionError("");
    if (supported) void refresh();
    else setLoading(false);
  }, [scope, supported, refresh]);

  if (!supported) return null;
  const unavailable = loadedScope !== scope || loading;

  const importCookies = async (profile: BrowserProfile): Promise<void> => {
    if (!scope.active || unavailable || busy) return;
    setBusy(`import:${profile.profilePath}`);
    setProfileError("");
    setResult(undefined);
    try {
      const next = await controls.importBrowserCookies!(profile.profilePath);
      if (scope.active) setResult(next);
    } catch (reason) {
      if (scope.active && !isPluginLifecycleCancellation(reason)) setProfileError(messageOf(reason));
    } finally {
      if (scope.active) setBusy("");
    }
  };

  const addExtension = async (): Promise<void> => {
    if (!scope.active || unavailable || busy) return;
    setBusy("add");
    setExtensionError("");
    try {
      const next = await controls.addBrowserExtension!();
      if (scope.active) setExtensions(next);
    } catch (reason) {
      if (scope.active && !isPluginLifecycleCancellation(reason)) setExtensionError(messageOf(reason));
    } finally {
      if (scope.active) setBusy("");
    }
  };

  const toggleExtension = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (!scope.active || unavailable || busy) return;
    setBusy(`toggle:${extension.id}`);
    setExtensionError("");
    try {
      const next = await controls.setBrowserExtensionEnabled!(
        extension.id,
        !extension.enabled,
      );
      if (scope.active) setExtensions(next);
    } catch (reason) {
      if (scope.active && !isPluginLifecycleCancellation(reason)) setExtensionError(messageOf(reason));
    } finally {
      if (scope.active) setBusy("");
    }
  };

  const reloadExtension = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (!scope.active || unavailable || busy || !controls.capabilities.browserReload) return;
    setBusy(`reload:${extension.id}`);
    setExtensionError("");
    try {
      const next = await controls.reloadBrowserExtension(extension.id);
      if (scope.active) setExtensions(next);
    } catch (reason) {
      if (!scope.active || isPluginLifecycleCancellation(reason)) return;
      setExtensionError(messageOf(reason));
      try {
        const next = await controls.listBrowserExtensions();
        if (scope.active) setExtensions(next);
      } catch (refreshError) {
        if (scope.active && !isPluginLifecycleCancellation(refreshError)) setExtensionError(messageOf(refreshError));
      }
    } finally {
      if (scope.active) setBusy("");
    }
  };

  const removeExtension = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (!scope.active || unavailable || busy) return;
    setBusy(`remove:${extension.id}`);
    setExtensionError("");
    try {
      const next = await controls.removeBrowserExtension!(extension.id);
      if (scope.active) setExtensions(next);
    } catch (reason) {
      if (scope.active && !isPluginLifecycleCancellation(reason)) setExtensionError(messageOf(reason));
    } finally {
      if (scope.active) setBusy("");
    }
  };

  const openOptions = async (extension: BrowserExtensionRecord): Promise<void> => {
    if (!scope.active || unavailable || busy || !controls.capabilities.browserOptions) return;
    setBusy(`options:${extension.id}`);
    setExtensionError("");
    try {
      await controls.openBrowserExtensionOptions(extension.id);
    } catch (reason) {
      if (scope.active && !isPluginLifecycleCancellation(reason)) setExtensionError(messageOf(reason));
    } finally {
      if (scope.active) setBusy("");
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
            disabled={unavailable || Boolean(busy)}
            onClick={() => void refresh()}
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
                      unavailable || Boolean(busy) || platform !== "darwin" || !profile.hasCookies
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
            disabled={unavailable || Boolean(busy)}
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
              <div className="browser-extension-entry" key={extension.id}>
                <SettingsRow
                  title={extension.name}
                  error={extension.error}
                  description={[
                    extension.version,
                    extension.needsReview ? t("settings.browserExtensionsNeedsReview")
                      : extension.loaded ? t("settings.browserExtensionsLoaded")
                        : extension.enabled ? t("settings.browserExtensionsNotLoaded")
                          : t("settings.browserExtensionsDisabled"),
                  ].filter(Boolean).join(" · ")}
                >
                  <div className="browser-settings-actions">
                    {extension.hasOptions && controls.capabilities.browserOptions ? <Tooltip asChild content={t("settings.browserExtensionsOptions", { name: extension.name })}><button
                      className="settings-button settings-button-ghost settings-icon-button"
                      type="button"
                      aria-label={t("settings.browserExtensionsOptions", { name: extension.name })}
                      disabled={unavailable || Boolean(busy) || !extension.loaded}
                      onClick={() => void openOptions(extension)}
                    >
                      <Settings size={16} aria-hidden="true" />
                    </button></Tooltip> : null}
                    <button
                      className="settings-button settings-button-ghost settings-icon-button"
                      type="button"
                      aria-label={t("settings.browserExtensionsDetails", { name: extension.name })}
                      aria-expanded={expandedExtension === extension.id}
                      aria-controls={`browser-extension-${extension.id}`}
                      onClick={() => setExpandedExtension(expandedExtension === extension.id ? "" : extension.id)}
                    >
                      <ChevronDown size={16} aria-hidden="true" />
                    </button>
                    {controls.capabilities.browserReload ? <Tooltip asChild content={extension.enabled ? t("settings.browserExtensionsReload", { name: extension.name }) : t("settings.browserExtensionsEnableToReload")}><button
                      className="settings-button settings-button-ghost settings-icon-button"
                      type="button"
                      aria-label={t("settings.browserExtensionsReload", { name: extension.name })}
                      disabled={unavailable || Boolean(busy) || !extension.enabled}
                      onClick={() => void reloadExtension(extension)}
                    >
                      <RefreshCw size={16} className={busy === `reload:${extension.id}` ? "settings-spin" : undefined} aria-hidden="true" />
                    </button></Tooltip> : null}
                    <button
                      className="settings-switch"
                      type="button"
                      role="switch"
                      aria-label={extension.enabled
                        ? t("settings.browserExtensionsDisable", { name: extension.name })
                        : t("settings.browserExtensionsEnable", { name: extension.name })}
                      aria-checked={extension.enabled}
                      disabled={unavailable || Boolean(busy)}
                      onClick={() => void toggleExtension(extension)}
                    >
                      <span className="settings-switch-thumb" aria-hidden="true" />
                    </button>
                    <button
                      className="settings-button settings-button-ghost settings-icon-button"
                      type="button"
                      aria-label={t("settings.browserExtensionsRemove", { name: extension.name })}
                      disabled={unavailable || Boolean(busy)}
                      onClick={() => void removeExtension(extension)}
                    >
                      {busy === `remove:${extension.id}`
                        ? <Loader2 className="icon settings-spin" aria-hidden="true" />
                        : <Trash2 size={16} aria-hidden="true" />}
                    </button>
                  </div>
                </SettingsRow>
                {expandedExtension === extension.id ? <div className="browser-extension-details" id={`browser-extension-${extension.id}`}>
                  {extension.manifest?.description ? <p>{extension.manifest.description}</p> : null}
                  <dl>
                    <dt>{t("settings.browserExtensionsSource")}</dt><dd>{extension.path}</dd>
                    <dt>{t("settings.browserExtensionsID")}</dt><dd>{extension.id}</dd>
                    {extension.manifest ? <>
                      <dt>{t("settings.browserExtensionsManifest")}</dt><dd>{extension.manifest.manifestVersion}</dd>
                      <dt>{t("settings.browserExtensionsPermissions")}</dt><dd>{extension.manifest.permissions.join(", ") || t("settings.browserExtensionsNone")}</dd>
                      <dt>{t("settings.browserExtensionsSites")}</dt><dd>{extension.manifest.sites.join(", ") || t("settings.browserExtensionsNone")}</dd>
                      <dt>{t("settings.browserExtensionsOptional")}</dt><dd>{[...extension.manifest.optionalPermissions, ...extension.manifest.optionalSites].join(", ") || t("settings.browserExtensionsNone")}</dd>
                    </> : null}
                  </dl>
                  <p>{t("settings.browserExtensionsSharedProfile")}</p>
                  <p>{t("settings.browserExtensionsFileAccess")}</p>
                  {extension.manifest?.unsupportedPermissions.length ? <p>{t("settings.browserExtensionsUnsupported", { permissions: extension.manifest.unsupportedPermissions.join(", ") })}</p> : null}
                  {extension.manifest?.compatibility.map(warning => <p key={warning}>{t(`settings.browserExtensionsWarning.${warning}`)}</p>)}
                  <p>{t("settings.browserExtensionsReloadHint")}</p>
                </div> : null}
              </div>
            ))
          )}
        </SettingsGroup>
        <p className="settings-section-note">{t("settings.browserExtensionsLocalOnly")}</p>
        {extensionError ? <p className="settings-error" role="alert">{extensionError}</p> : null}
      </SettingsSection>
    </>
  );
}

function messageOf(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason);
}
