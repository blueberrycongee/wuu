import { useEffect, useState } from "react";
import { desktopPlatform } from "./platform";
import { useI18n } from "./i18n";

function usesCustomLinuxChrome(): boolean {
  return (
    desktopPlatform() === "linux" &&
    (window.wuu?.hostKind ?? "desktop") === "desktop" &&
    typeof window.wuu?.windowMinimize === "function"
  );
}

/** Double-click a drag strip to toggle maximize on Linux frameless windows. */
export function startLinuxTitlebarMaximizeGesture(): void {
  if (!usesCustomLinuxChrome()) return;
  const onDoubleClick = (event: MouseEvent): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    if (
      target.closest(
        "button, a, input, textarea, select, [role='button'], [role='menuitem'], [role='option'], .linux-window-controls",
      )
    ) {
      return;
    }
    if (
      !target.closest(
        ".titlebar, .settings-titlebar, .account-screen-titlebar, .channel-room-header, .onboarding-chrome",
      )
    ) {
      return;
    }
    void window.wuu.windowToggleMaximize?.();
  };
  document.addEventListener("dblclick", onDoubleClick);
}

function CaptionIcon({ kind }: { kind: "minimize" | "maximize" | "restore" | "close" }): JSX.Element {
  if (kind === "minimize") {
    return (
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <path d="M1 5h8" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
      </svg>
    );
  }
  if (kind === "maximize") {
    return (
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <rect x="1.25" y="1.25" width="7.5" height="7.5" rx="0.6" fill="none" stroke="currentColor" strokeWidth="1.2" />
      </svg>
    );
  }
  if (kind === "restore") {
    return (
      <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
        <path
          d="M3 3.2h4.3v4.3H3zM2.2 2.4V1.5h5.8v5.8H7.1"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.2"
          strokeLinejoin="round"
        />
      </svg>
    );
  }
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
      <path d="M2.2 2.2l5.6 5.6M7.8 2.2L2.2 7.8" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}

/** Frameless Linux caption buttons that share the white app header. */
export function LinuxWindowControls(): JSX.Element | null {
  const { t } = useI18n();
  const enabled = usesCustomLinuxChrome();
  const [maximized, setMaximized] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    void window.wuu.windowIsMaximized?.().then((value) => {
      if (!cancelled) setMaximized(Boolean(value));
    });
    const dispose = window.wuu.onWindowMaximizedChange?.((value) => {
      setMaximized(Boolean(value));
    });
    return () => {
      cancelled = true;
      dispose?.();
    };
  }, [enabled]);

  if (!enabled) return null;

  return (
    <div className="linux-window-controls" role="group" aria-label={t("shell.windowControls")}>
      <button
        type="button"
        className="linux-window-control"
        aria-label={t("shell.minimizeWindow")}
        title={t("shell.minimizeWindow")}
        onClick={() => {
          void window.wuu.windowMinimize?.();
        }}
      >
        <CaptionIcon kind="minimize" />
      </button>
      <button
        type="button"
        className="linux-window-control"
        aria-label={t(maximized ? "shell.restoreWindow" : "shell.maximizeWindow")}
        title={t(maximized ? "shell.restoreWindow" : "shell.maximizeWindow")}
        aria-pressed={maximized}
        onClick={() => {
          void window.wuu.windowToggleMaximize?.().then((value) => {
            if (typeof value === "boolean") setMaximized(value);
          });
        }}
      >
        <CaptionIcon kind={maximized ? "restore" : "maximize"} />
      </button>
      <button
        type="button"
        className="linux-window-control linux-window-control-close"
        aria-label={t("shell.closeWindow")}
        title={t("shell.closeWindow")}
        onClick={() => {
          void window.wuu.windowClose?.();
        }}
      >
        <CaptionIcon kind="close" />
      </button>
    </div>
  );
}
