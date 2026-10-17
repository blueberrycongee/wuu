import { useEffect, useLayoutEffect, useRef, useState, type Ref } from "react";
import type { AppLocale, SystemPermission, SystemPermissionPresentation, SystemPermissionsState } from "../shared/protocol";
import { AlertCircle, Check, Hand, LoaderCircle, Monitor, RefreshCw } from "./WuuIcons";
import { SettingsSection } from "./SettingsSection";
import { translate, useI18n } from "./i18n";
import { prefersReducedMotion, subscribeReducedMotion } from "./motion";

export function SystemPermissionRow({ permission, locale, granted, pending = false, disabled = false, onEnable, rowRef }: {
  permission: SystemPermission;
  locale: AppLocale;
  granted: boolean;
  pending?: boolean;
  disabled?: boolean;
  onEnable?: () => void;
  rowRef?: Ref<HTMLDivElement>;
}): JSX.Element {
  const Icon = permission === "screen" ? Monitor : Hand;
  const t = (key: Parameters<typeof translate>[1], values?: Record<string, string>) => translate(locale, key, values);
  return <div ref={rowRef} className="system-permission-row" data-permission={permission} data-granted={granted || undefined}>
    <span className="system-permission-symbol"><Icon className="system-permission-icon" aria-hidden="true" /></span>
    <div className="system-permission-copy">
      <span className="system-permission-title">{t(`permissions.${permission}`)}</span>
      <span className="system-permission-description">{t(`permissions.${permission}Description`)}</span>
    </div>
    <div className="system-permission-action">
      {granted ? <span className="system-permission-granted" role="status"><Check className="icon" aria-hidden="true" />{t("permissions.granted")}</span>
        : <button className="settings-button system-permission-enable" type="button" disabled={disabled} onClick={onEnable}
            aria-label={t("permissions.enableNamed", { permission: t(`permissions.${permission}`) })} aria-busy={pending || undefined} tabIndex={onEnable ? undefined : -1}>
            {pending ? <LoaderCircle className="icon settings-spin" aria-hidden="true" /> : t("permissions.enable")}
          </button>}
    </div>
  </div>;
}

function sourceRect(row: HTMLElement): SystemPermissionPresentation["sourceRect"] {
  const rect = row.getBoundingClientRect();
  // clientWidth/clientHeight round to whole pixels; rects retain fractional layout.
  const tolerance = 1;
  if (!row.isConnected || rect.width <= 0 || rect.height <= 0 || rect.left < 0 || rect.top < 0
    || rect.right > window.innerWidth || rect.bottom > window.innerHeight) return null;
  // A partially clipped row cannot be a faithful destination for the native window.
  for (let parent = row.parentElement; parent; parent = parent.parentElement) {
    const style = getComputedStyle(parent);
    const bounds = parent.getBoundingClientRect();
    const left = bounds.left + parent.clientLeft;
    const top = bounds.top + parent.clientTop;
    if ((/(auto|scroll|hidden|clip)/.test(style.overflowX) && (rect.left < left - tolerance || rect.right > left + parent.clientWidth + tolerance))
      || (/(auto|scroll|hidden|clip)/.test(style.overflowY) && (rect.top < top - tolerance || rect.bottom > top + parent.clientHeight + tolerance))) return null;
  }
  return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
}

function sourcePresentation(requestId: string, row: HTMLElement): SystemPermissionPresentation {
  return {
    requestId, sourceRect: sourceRect(row), reducedMotion: prefersReducedMotion(),
    theme: document.documentElement.dataset.theme === "dark" ? "dark" : "light",
  };
}

type ActiveGuide = { requestId: string; permission: SystemPermission; row: HTMLElement };

export function SystemPermissionsPanel({ preview = false }: { preview?: boolean }): JSX.Element {
  const { t, locale } = useI18n();
  const [state, setState] = useState<SystemPermissionsState>();
  const [pending, setPending] = useState<{ requestId: string; permission: SystemPermission }>();
  const [floating, setFloating] = useState<SystemPermission>();
  const [error, setError] = useState<"read" | "request">();
  const [attempt, setAttempt] = useState(0);
  const panel = useRef<HTMLDivElement>(null);
  const rows = useRef<Partial<Record<SystemPermission, HTMLDivElement>>>({});
  const activeGuide = useRef<ActiveGuide | undefined>(undefined);
  const mounted = useRef(false);
  const refreshGeometry = useRef<() => void>(() => undefined);

  useEffect(() => {
    mounted.current = true;
    const stopGuide = preview ? undefined : window.wuu.onSystemPermissionGuideChanged?.(next => {
      const current = activeGuide.current;
      if (!current || next.requestId !== current.requestId) return;
      if (next.phase === "closed") {
        activeGuide.current = undefined;
        setFloating(undefined);
        if (next.granted) setState(value => value && { ...value, [current.permission]: true });
      } else {
        setFloating(current.permission);
      }
    });
    let frame = 0;
    let lastPresentation = "";
    const publish = (): void => {
      const current = activeGuide.current;
      if (!current) return;
      const presentation = sourcePresentation(current.requestId, current.row);
      const serialized = JSON.stringify(presentation);
      if (serialized === lastPresentation) return;
      lastPresentation = serialized;
      window.wuu.updateSystemPermissionGuide?.(presentation);
    };
    const schedule = (): void => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(publish);
    };
    refreshGeometry.current = schedule;
    const observer = new ResizeObserver(schedule);
    for (const row of Object.values(rows.current)) observer.observe(row);
    for (let element: HTMLElement | null = panel.current; element; element = element.parentElement) observer.observe(element);
    window.addEventListener("scroll", schedule, true);
    window.addEventListener("resize", schedule);
    const stopMotion = subscribeReducedMotion(publish);
    const appearanceObserver = new MutationObserver(schedule);
    appearanceObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => {
      mounted.current = false;
      stopGuide?.();
      stopMotion();
      observer.disconnect();
      appearanceObserver.disconnect();
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", schedule, true);
      window.removeEventListener("resize", schedule);
      const current = activeGuide.current;
      activeGuide.current = undefined;
      if (current) void window.wuu.closeSystemPermissionGuide?.(current.requestId).catch(() => undefined);
    };
  }, [preview]);

  useLayoutEffect(() => refreshGeometry.current());

  useEffect(() => {
    if (preview) {
      setState({ available: true, screen: false, accessibility: false });
      return;
    }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    let checking = false;
    const refresh = async (): Promise<void> => {
      if (checking || !active) return;
      checking = true;
      clearTimeout(timer);
      try {
        const next = await window.wuu.getSystemPermissions!();
        if (active) { setState(next); setError(current => current === "read" ? undefined : current); }
      } catch { if (active) setError("read"); }
      finally {
        checking = false;
        if (active) timer = setTimeout(() => void refresh(), 1_500);
      }
    };
    const onFocus = () => void refresh();
    window.addEventListener("focus", onFocus);
    void refresh();
    return () => {
      active = false;
      clearTimeout(timer);
      window.removeEventListener("focus", onFocus);
    };
  }, [preview, attempt]);

  async function request(permission: SystemPermission): Promise<void> {
    if (preview) {
      setState(current => current && { ...current, [permission]: true });
      return;
    }
    if (activeGuide.current || pending) return;
    const row = rows.current[permission]!;
    const requestId = crypto.randomUUID();
    const presentation = sourcePresentation(requestId, row);
    activeGuide.current = { requestId, permission, row };
    setPending({ requestId, permission });
    setError(undefined);
    try { await window.wuu.requestSystemPermission!(permission, presentation); }
    catch {
      if (mounted.current) setError("request");
      if (activeGuide.current?.requestId === requestId) {
        activeGuide.current = undefined;
        setFloating(undefined);
        void window.wuu.closeSystemPermissionGuide?.(requestId).catch(() => undefined);
      }
    } finally {
      if (mounted.current) setPending(current => current?.requestId === requestId ? undefined : current);
    }
  }

  return (
    <div ref={panel} className="system-permissions" data-testid="system-permissions">
      <div className="system-permission-list">
        {(["screen", "accessibility"] as const).map(permission => <div className="system-permission-slot" key={permission}
          data-floating={floating === permission || undefined} aria-hidden={floating === permission || undefined}>
          <SystemPermissionRow permission={permission} locale={locale} granted={state?.[permission] === true && error !== "read"}
            pending={pending?.permission === permission} disabled={!state?.available || pending !== undefined || floating !== undefined}
            rowRef={element => { if (element) rows.current[permission] = element; else delete rows.current[permission]; }}
            onEnable={() => void request(permission)} />
        </div>)}
      </div>
      {!state && !error ? <p className="system-permissions-note" role="status">{t("permissions.checking")}</p> : null}
      {state && !state.available ? <p className="system-permissions-note" role="status">{t("permissions.unavailable")}</p> : null}
      {error ? <div className="system-permissions-error" role="alert">
        <p className="system-permission-feedback"><AlertCircle className="icon" aria-hidden="true" /><span>{t("permissions.error")}</span></p>
        <button type="button" className="settings-button settings-button-ghost" onClick={() => { setError(undefined); setAttempt(value => value + 1); }}>
          <RefreshCw className="icon" aria-hidden="true" />{t("permissions.retry")}
        </button>
      </div> : null}
    </div>
  );
}

export function SystemPermissionsSection(): JSX.Element | null {
  const { t } = useI18n();
  if (window.wuu?.platform !== "darwin" || !window.wuu.getSystemPermissions) return null;
  return <SettingsSection title={t("settings.appContext.permissions")} testID="settings-system-permissions"><SystemPermissionsPanel /></SettingsSection>;
}
