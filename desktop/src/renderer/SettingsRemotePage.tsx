/**
 * Remote-control settings page: enable the machine-global remote host,
 * show the pairing QR for a phone to scan, and manage paired devices.
 *
 * Self-contained and props-driven: all data and actions arrive from the
 * shell wiring (main-process RemoteHostManager over IPC), so the component
 * renders and tests in isolation. The markup mirrors the shared settings
 * primitives (section/card/row/switch) by class name.
 */
import { useEffect, useState, type ReactNode } from "react";
import { AccountPanel } from "./AccountPanel";
import QRCode from "qrcode";
import { useI18n } from "./i18n";

export type RemoteDeviceView = {
  pub: string;
  fingerprint: string;
  name?: string;
  added_at: string;
};

export type RemoteStatusView = {
  fingerprint: string;
  account_server?: string;
  host_name?: string;
  relay_url?: string;
  store: string;
  devices: RemoteDeviceView[];
};

export type SettingsRemotePageProps = {
  status: RemoteStatusView | null;
  statusError: string;
  hostRunning: boolean;
  hostEnabled?: boolean;
  pairUri: string | null;
  webUrl?: string | null;
  busy: boolean;
  onToggleHost: (enabled: boolean) => void;
  onOpenPairing: () => void;
  onRemoveDevice: (device: RemoteDeviceView) => void;
};

export function SettingsRemotePage({
  status,
  statusError,
  hostRunning,
  hostEnabled = hostRunning,
  pairUri,
  webUrl,
  busy,
  onToggleHost,
  onOpenPairing,
  onRemoveDevice
}: SettingsRemotePageProps): JSX.Element {
  const { t, formatDate } = useI18n();

  return (
    <div className="settings-remote-page" data-testid="settings-remote-page">
      {window.wuu?.remoteAccount && <AccountPanel driver={window.wuu.remoteAccount} />}
      {statusError ? <div className="settings-error">{statusError}</div> : null}

      <RemoteSection title={t("remote.access")} description={t(status?.account_server ? "remote.accountDescription" : "remote.lanDescription")}>
        <div className="settings-group">
          <RemoteRow title={hostRunning ? t("remote.hostRunning") : t("remote.hostStopped")}>
            <button className="settings-switch" type="button" role="switch" aria-checked={hostEnabled}
              disabled={busy} onClick={() => onToggleHost(!hostEnabled)}>
              <span className="settings-switch-thumb" aria-hidden="true" />
              <span className="sr-only">{hostEnabled ? t("remote.disableAccess") : t("remote.enableAccess")}</span>
            </button>
          </RemoteRow>
          {hostRunning && webUrl && !status?.account_server ? <RemoteRow title={t("remote.webAddress")}>
            <code>{webUrl}</code>
          </RemoteRow> : null}
        </div>
      </RemoteSection>

      {!status?.account_server && <><RemoteSection title={t("remote.pairSection")} description={t("remote.pairSectionDescription")}>
        <div className="settings-group">
          {pairUri ? (
            <div className="settings-remote-pairing" data-testid="remote-pair-panel">
              <PairQRCode uri={pairUri} />
              <p className="settings-remote-pair-hint">{t("remote.pairHint")}</p>
              <button className="settings-button" type="button" onClick={() => void navigator.clipboard.writeText(pairUri)}>{t("remote.copyLink")}</button>
              <button className="settings-button" type="button" disabled={busy} onClick={onOpenPairing}>{t("remote.refreshPairQr")}</button>
            </div>
          ) : (
            <RemoteRow
              title={t("remote.pairNewPhone")}
              description={hostRunning ? t("remote.openPairWindow") : t("remote.enableAccessFirst")}
            >
              <button
                className="settings-button"
                type="button"
                disabled={busy || !hostRunning}
                onClick={onOpenPairing}
              >
                {t("remote.showPairQr")}
              </button>
            </RemoteRow>
          )}
        </div>
      </RemoteSection>

      <RemoteSection title={t("remote.devicesSection")} description={t("remote.devicesSectionDescription")}>
        <div className="settings-group">
          {!status || status.devices.length === 0 ? (
            <div className="settings-empty">{t("remote.noDevices")}</div>
          ) : (
            status.devices.map((device) => (
              <RemoteRow
                key={device.pub}
                title={device.name && device.name.trim() !== "" ? device.name : t("remote.unnamedDevice")}
                description={t("remote.pairedAt", { fingerprint: device.fingerprint, date: formatPairedAt(device.added_at, formatDate) })}
              >
                <button
                  className="settings-button settings-button-danger"
                  type="button"
                  disabled={busy}
                  onClick={() => onRemoveDevice(device)}
                >
                  {t("remote.revoke")}
                </button>
              </RemoteRow>
            ))
          )}
        </div>
      </RemoteSection>
      </>}
    </div>
  );
}

/** Renders the pairing URI as an inline SVG QR code. SVG keeps the module
 *  free of canvas dependencies, so it renders identically in tests. */
export function PairQRCode({ uri }: { uri: string }): JSX.Element {
  const { t } = useI18n();
  const [svg, setSvg] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    let cancelled = false;
    setSvg("");
    setError("");
    QRCode.toString(uri, { type: "svg", errorCorrectionLevel: "M", margin: 1 })
      .then((rendered) => {
        if (!cancelled) setSvg(rendered);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [uri]);
  if (error) {
    return <div className="settings-error">{t("remote.qrFailed", { error })}</div>;
  }
  return (
    <div
      className="settings-remote-qr"
      data-testid="remote-pair-qr"
      role="img"
      aria-label={t("remote.pairQr")}
      // The SVG comes from the qrcode encoder over our own URI, not from
      // remote input.
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}

function formatPairedAt(
  addedAt: string,
  formatter: (value: Date | number | string, options?: Intl.DateTimeFormatOptions) => string,
): string {
  const date = new Date(addedAt);
  if (Number.isNaN(date.getTime())) {
    return addedAt;
  }
  return formatter(date, { year: "numeric", month: "short", day: "numeric" });
}

/* Local copies of the settings primitives' markup: the originals live inside
 * SettingsView.tsx unexported; the wiring commit can consolidate them. */

function RemoteSection({
  title,
  description,
  children
}: {
  title: string;
  description: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <section className="settings-section">
      <header className="settings-section-header">
        <h2 className="settings-section-title">{title}</h2>
        <p className="settings-section-description">{description}</p>
      </header>
      {children}
    </section>
  );
}

function RemoteRow({
  title,
  description,
  block = false,
  children
}: {
  title: string;
  description?: string;
  block?: boolean;
  children: ReactNode;
}): JSX.Element {
  return (
    <div className="settings-row">
      <div className="settings-row-label">
        <span className="settings-row-label-title">{title}</span>
        {description ? <span className="settings-row-label-description">{description}</span> : null}
      </div>
      <div className={block ? "settings-row-control-block" : "settings-row-control"}>{children}</div>
    </div>
  );
}
