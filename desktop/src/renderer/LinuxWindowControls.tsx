import { Minus, Square, Copy, X } from "./WuuIcons";
import { useEffect, useState } from "react";
import type { ApplicationDesktopControlsV1 } from "../../../packages/plugin-sdk/src/ui-desktop";
import { useI18n } from "./i18n";

function usesCustomLinuxChrome(desktop: ApplicationDesktopControlsV1): boolean {
  return (
    desktop.platform === "linux" &&
    desktop.hostKind === "desktop" &&
    typeof desktop.minimize === "function"
  );
}

/** Double-click a drag strip to toggle maximize on Linux frameless windows. */
export function startLinuxTitlebarMaximizeGesture(desktop: ApplicationDesktopControlsV1): () => void {
  if (!usesCustomLinuxChrome(desktop)) return () => {};
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
        ".titlebar, .settings-titlebar, .account-screen-titlebar, .onboarding-chrome",
      )
    ) {
      return;
    }
    void desktop.toggleMaximize?.().catch(() => {});
  };
  document.addEventListener("dblclick", onDoubleClick);
  return () => document.removeEventListener("dblclick", onDoubleClick);
}

function CaptionIcon({ kind }: { kind: "minimize" | "maximize" | "restore" | "close" }): JSX.Element {
  const Icon = { minimize: Minus, maximize: Square, restore: Copy, close: X }[kind];
  return <Icon size={14} />;
}

/** Frameless Linux caption buttons; retired scopes cancel pending native calls. */
export function LinuxWindowControls({ desktop }: { desktop: ApplicationDesktopControlsV1 }): JSX.Element | null {
  const { t } = useI18n();
  const enabled = usesCustomLinuxChrome(desktop);
  useEffect(() => startLinuxTitlebarMaximizeGesture(desktop), [desktop]);
  const [maximized, setMaximized] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    void desktop.isMaximized?.().then((value) => {
      if (!cancelled) setMaximized(Boolean(value));
    }, () => {});
    const dispose = desktop.onMaximizedChange?.((value) => {
      setMaximized(Boolean(value));
    });
    return () => {
      cancelled = true;
      dispose?.();
    };
  }, [enabled, desktop]);

  if (!enabled) return null;

  return (
    <div className="linux-window-controls" role="group" aria-label={t("shell.windowControls")}>
      <button
        type="button"
        className="linux-window-control"
        aria-label={t("shell.minimizeWindow")}
        title={t("shell.minimizeWindow")}
        onClick={() => {
          void desktop.minimize?.().catch(() => {});
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
          void desktop.toggleMaximize?.().then((value) => {
            if (typeof value === "boolean") setMaximized(value);
          }, () => {});
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
          void desktop.close?.().catch(() => {});
        }}
      >
        <CaptionIcon kind="close" />
      </button>
    </div>
  );
}
