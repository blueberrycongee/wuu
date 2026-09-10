import { isTouchWebShell } from "./ComposerFocus";
import { ExternalLink, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import type {
  VoiceInputLanguage,
  VoicePermissionStatus,
} from "../shared/protocol";
import { useI18n } from "./i18n";
import { useVoiceInputSettings } from "./VoiceInputSettingsState";

export function VoiceInputSettingsSection({
  polishAvailable,
}: {
  polishAvailable: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const { settings, updateSettings } = useVoiceInputSettings();
  const [microphonePermission, setMicrophonePermission] =
    useState<VoicePermissionStatus>("unknown");
  const [speechPermission, setSpeechPermission] =
    useState<VoicePermissionStatus>("unknown");
  const [permissionLoading, setPermissionLoading] = useState(false);
  const [error, setError] = useState("");
  const supported = window.wuu?.platform === "darwin";

  const refreshPermissions = useCallback(async (): Promise<void> => {
    const api = window.wuu as Partial<typeof window.wuu>;
    if (typeof api.getVoiceInputSettings !== "function") return;
    setPermissionLoading(true);
    try {
      const snapshot = await api.getVoiceInputSettings();
      setMicrophonePermission(snapshot.microphone_permission);
      setSpeechPermission(snapshot.speech_permission);
    } catch {
      setMicrophonePermission("unavailable");
      setSpeechPermission("unavailable");
    } finally {
      setPermissionLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshPermissions();
  }, [refreshPermissions]);

  async function save(
    next: Parameters<typeof updateSettings>[0],
  ): Promise<void> {
    setError("");
    try {
      await updateSettings(next);
    } catch {
      setError(t("settings.voice.saveFailed"));
    }
  }

  function openPrivacySettings(permission: "microphone" | "speech"): void {
    setError("");
    void window.wuu
      .openVoicePrivacySettings(permission)
      .catch(() => setError(t("settings.voice.openSettingsFailed")));
  }

  const languageOptions: Array<{
    value: VoiceInputLanguage;
    label: string;
  }> = [
    { value: "system", label: t("settings.voice.followApp") },
    { value: "zh-CN", label: t("common.chinese") },
    { value: "en-US", label: t("common.english") },
  ];

  return (
    <section
      className="settings-section"
      data-testid="settings-voice-input"
    >
      <header className="settings-section-header">
        <h2 className="settings-section-title">
          {t("settings.voice.title")}
        </h2>
        {!isTouchWebShell() && <p className="settings-section-description">
          {t("settings.voice.description")}
        </p>}
      </header>
      <div className="settings-group">
        {!supported ? (
          <VoiceSettingsRow
            title={t("settings.voice.platform")}
            description={t("settings.voice.platformDescription")}
          >
            <span className="settings-inline-flag">
              {t("settings.voice.macOnly")}
            </span>
          </VoiceSettingsRow>
        ) : (
          <>
            <VoiceSettingsRow
              title={t("settings.voice.defaultPolish")}
              description={
                polishAvailable
                  ? t("settings.voice.defaultPolishDescription")
                  : t("settings.voice.defaultPolishUnavailable")
              }
            >
              <button
                className="settings-switch"
                type="button"
                role="switch"
                aria-checked={settings.polish_enabled}
                data-testid="settings-voice-polish"
                disabled={!polishAvailable}
                onClick={() =>
                  void save({
                    ...settings,
                    polish_enabled: !settings.polish_enabled,
                  })
                }
              >
                <span className="settings-switch-thumb" aria-hidden="true" />
                <span className="sr-only">
                  {t("settings.voice.defaultPolish")}
                </span>
              </button>
            </VoiceSettingsRow>
            <VoiceSettingsRow
              title={t("settings.voice.language")}
              description={t("settings.voice.languageDescription")}
              block
            >
              <div
                className="theme-segmented"
                role="group"
                aria-label={t("settings.voice.language")}
              >
                {languageOptions.map((option) => (
                  <button
                    key={option.value}
                    type="button"
                    aria-pressed={settings.language === option.value}
                    data-testid={`settings-voice-language-${option.value}`}
                    onClick={() =>
                      void save({ ...settings, language: option.value })
                    }
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </VoiceSettingsRow>
            <VoicePermissionRow
              title={t("settings.voice.microphonePermission")}
              status={microphonePermission}
              loading={permissionLoading}
              onOpen={() => openPrivacySettings("microphone")}
            />
            <VoicePermissionRow
              title={t("settings.voice.speechPermission")}
              status={speechPermission}
              loading={permissionLoading}
              onOpen={() => openPrivacySettings("speech")}
            />
            <div className="settings-row settings-row-footer">
              {error ? <div className="settings-error">{error}</div> : null}
              <button
                className="settings-button settings-button-secondary"
                type="button"
                disabled={permissionLoading}
                onClick={() => void refreshPermissions()}
              >
                <RefreshCw
                  size={14}
                  className={permissionLoading ? "settings-spin" : undefined}
                  aria-hidden="true"
                />
                {t("settings.voice.refreshPermissions")}
              </button>
            </div>
          </>
        )}
      </div>
    </section>
  );
}

function VoicePermissionRow({
  title,
  status,
  loading,
  onOpen,
}: {
  title: string;
  status: VoicePermissionStatus;
  loading: boolean;
  onOpen: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <VoiceSettingsRow title={title}>
      <span
        className={`settings-voice-permission status-${status}`}
        data-testid={`settings-voice-permission-${title}`}
      >
        {loading
          ? t("settings.loading")
          : permissionStatusLabel(status, t)}
      </span>
      <button
        className="settings-button settings-button-secondary"
        type="button"
        onClick={onOpen}
      >
        <ExternalLink size={13} aria-hidden="true" />
        {t("settings.voice.openSystemSettings")}
      </button>
    </VoiceSettingsRow>
  );
}

function VoiceSettingsRow({
  title,
  description,
  block = false,
  children,
}: {
  title: string;
  description?: string;
  block?: boolean;
  children: ReactNode;
}): JSX.Element {
  return (
    <div className={`settings-row${block ? " settings-row-block" : ""}`}>
      <div className="settings-row-label">
        <span className="settings-row-label-title">{title}</span>
        {description && !isTouchWebShell() ? (
          <span className="settings-row-label-description">{description}</span>
        ) : null}
      </div>
      <div
        className={
          block ? "settings-row-control-block" : "settings-row-control"
        }
      >
        {children}
      </div>
    </div>
  );
}

function permissionStatusLabel(
  status: VoicePermissionStatus,
  t: ReturnType<typeof useI18n>["t"],
): string {
  switch (status) {
    case "granted":
      return t("settings.voice.permissionGranted");
    case "denied":
      return t("settings.voice.permissionDenied");
    case "restricted":
      return t("settings.voice.permissionRestricted");
    case "not_determined":
      return t("settings.voice.permissionNotRequested");
    default:
      return t("settings.voice.permissionUnavailable");
  }
}
