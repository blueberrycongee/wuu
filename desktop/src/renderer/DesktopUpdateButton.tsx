import type { SidebarNavigationService } from "./AppSidebar";
import { useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { DesktopUpdateState } from "../shared/protocol";
import { DesktopUpdateButtonSurface } from "./DesktopUpdateButtonSurface";
import { NativeRibbonFooterContext } from "./ChromeOverlay";
import type { ChromeDesktopUpdate } from "../shared/chromeOverlay";
import { useI18n } from "./i18n";

export function DesktopUpdateButton({ navigation }: { navigation?: SidebarNavigationService } = {}): React.JSX.Element | null {
  const { t } = useI18n();
  const nativeRibbon = useContext(NativeRibbonFooterContext);
  const [legacyState, setState] = useState<DesktopUpdateState>({ kind: "unsupported" });
  const state = navigation?.view.update ?? legacyState;
  const [pending, setPending] = useState(false);
  const [requestFailed, setRequestFailed] = useState(false);
  useEffect(() => {
    if (navigation) return;
    const api = window.wuu;
    if (!api?.getDesktopUpdateState || !api.onDesktopUpdateChanged) return;
    let active = true;
    let receivedEvent = false;
    const unsubscribe = api.onDesktopUpdateChanged(next => {
      receivedEvent = true;
      if (active) { setState(next); setRequestFailed(false); }
    });
    void api.getDesktopUpdateState().then(next => {
      // An in-flight snapshot must not overwrite a newer progress event.
      if (active && !receivedEvent) setState(next);
    }).catch(error => console.error("Could not read desktop update state", error));
    return () => { active = false; unsubscribe(); };
  }, [Boolean(navigation)]);

  const version = "version" in state ? state.version : "";
  const downloading = state.kind === "downloading";
  const busy = pending || downloading || state.kind === "checking" || state.kind === "prompting" || state.kind === "installing";
  const label = requestFailed ? t("desktopUpdate.actionFailed")
    : state.kind === "available" ? t("desktopUpdate.available", { version })
    : state.kind === "ready" ? t("desktopUpdate.ready", { version })
    : state.kind === "error" ? t("desktopUpdate.failed")
    : downloading ? t("desktopUpdate.downloading", { percent: state.percent })
    : state.kind === "installing" ? t("desktopUpdate.installing")
    : state.kind === "prompting" ? t("desktopUpdate.prompting")
    : t("desktopUpdate.checking");
  const activate = async () => {
    if (busy) return;
    setPending(true);
    setRequestFailed(false);
    try {
      if (navigation) await navigation.invoke("navigation.install-update", {});
      else if (state.kind === "ready") await window.wuu.restartDesktopUpdate!();
      else await window.wuu.downloadDesktopUpdate!();
    } catch (error) {
      console.error("Desktop update action failed", error);
      setRequestFailed(true);
    } finally { setPending(false); }
  };

  const model: ChromeDesktopUpdate | undefined = state.kind === "unsupported" || state.kind === "idle" ? undefined
    : { state: state.kind, label, progressLabel: t("desktopUpdate.progress"), busy, percent: downloading ? state.percent : undefined };
  const activateRef = useRef(activate); activateRef.current = activate;
  useLayoutEffect(() => {
    nativeRibbon?.publishUpdate(model, () => void activateRef.current());
    return () => nativeRibbon?.publishUpdate(undefined);
  }, [nativeRibbon, JSON.stringify(model)]);
  return model ? <DesktopUpdateButtonSurface model={model} onActivate={() => void activate()} /> : null;
}
