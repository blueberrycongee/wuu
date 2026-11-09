import type { ChromeDesktopUpdate } from "../shared/chromeOverlay";
import { Download, RefreshCw, RotateCw } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import "./DesktopUpdateButton.css";

/** Shared presentation only; update/restart authority remains in the shell. */
export function DesktopUpdateButtonSurface({ model, onActivate, boundedNative = false }: {
  model: ChromeDesktopUpdate; onActivate: () => void; boundedNative?: boolean;
}): React.JSX.Element {
  const downloading = model.state === "downloading";
  return <div className="desktop-update" data-state={model.state}>
    <Tooltip content={model.label} disabled={boundedNative}>
      <button type="button" className="desktop-update-button" aria-label={model.label}
        title={boundedNative ? model.label : undefined} aria-disabled={model.busy} onClick={() => { if (!model.busy) onActivate(); }}>
        <span className="desktop-update-glyph" key={model.state} aria-hidden="true">
          {downloading ? <>
            <svg className="desktop-update-ring" viewBox="0 0 28 28" fill="none">
              <circle className="desktop-update-track" cx="14" cy="14" r="11" />
              <circle className="desktop-update-progress" cx="14" cy="14" r="11" pathLength="100"
                strokeDasharray="100" strokeDashoffset={100 - (model.percent ?? 0)} />
            </svg>
            <Download className="desktop-update-download" />
          </> : model.state === "ready" || model.state === "installing" ? <RotateCw />
            : model.state === "checking" ? <RefreshCw /> : <Download />}
        </span>
      </button>
    </Tooltip>
    {downloading && <span className="sr-only" role="progressbar" aria-label={model.progressLabel}
      aria-valuemin={0} aria-valuemax={100} aria-valuenow={model.percent} />}
    <span className="sr-only" role="status">{downloading ? model.progressLabel : model.label}</span>
  </div>;
}
