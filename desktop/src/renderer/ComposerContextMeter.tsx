// Composer-toolbar retained-history meter.
//
// Sits next to the live token-speed gauge and shows retained conversation
// history against the active provider/model input ceiling. The current
// in-flight request can be larger than this when the user attaches large
// files or images; /context is the surface for inspecting that full request.
//
// The progress stroke reuses the token-speed gauge's color palette so the
// two meters read as a coordinated pair: idle gray when the window is empty,
// warm amber while context is filling, accent-warm red once usage crosses
// the high-water mark. The 0.7 threshold mirrors the token-speed gauge's
// 70/100 tps break, so both meters shift into "high" at the same fraction
// of their scale.

import type { TurnContextUsage } from "./AppState";
import { useId, useRef, useState } from "react";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { ContextUsageRing } from "./ContextUsageRing";
import { formatCurrentNumber, translateCurrent as translate, useI18n } from "./i18n";

type ComposerContextMeterProps = {
  // Pass the latest per-turn usage snapshot from AppState. The component
  // hides entirely when no context ceiling is known yet (e.g. before the
  // first turn finishes, or for an unknown model with no limit data).
  usage: TurnContextUsage | undefined;
};

const TOOLTIP_WIDTH = 212;

// Fill-level thresholds mirror the token-speed gauge's idle/mid/high
// tiers so the two meters step into "high" at the same fraction of their
// own scale. The progress stroke inherits currentColor from the SVG; the
// container keeps --ink-strong so the inline label is unaffected.
const FILL_HIGH_THRESHOLD = 0.7;

type FillLevel = "idle" | "mid" | "high";

function fillLevel(ratio: number): FillLevel {
  if (ratio <= 0) return "idle";
  if (ratio < FILL_HIGH_THRESHOLD) return "mid";
  return "high";
}

function fillColor(level: FillLevel): string {
  if (level === "idle") return "var(--token-gauge-idle)";
  if (level === "mid") return "var(--token-gauge-mid)";
  return "var(--token-gauge-high)";
}

export function ComposerContextMeter({
  usage,
}: ComposerContextMeterProps): JSX.Element | null {
  const { t } = useI18n();
  const tooltipID = useId();
  const anchorRef = useRef<HTMLDivElement>(null);
  const [tooltipOpen, setTooltipOpen] = useState(false);

  if (!usage || !Number.isFinite(usage.window) || usage.window <= 0) {
    return null;
  }
  const used = Math.max(0, usage.used);
  const ratio = Math.min(1, Math.max(0, used / usage.window));
  const percent = Math.round(ratio * 100);
  // Color tier mirrors the token-speed gauge so the two meters read as a
  // coordinated pair: gray when empty, amber while filling, warm red as
  // the window approaches its limit. The 0.7 cutoff matches the gauge's
  // 70/100 tps break so both step into "high" at the same fraction.
  const level = fillLevel(ratio);
  const ringColor = fillColor(level);
  const percentLabel = `${percent}%`;
  const valueLabel = `${formatTokenCount(used)} / ${formatTokenCount(
    usage.window,
  )}`;
  const requestContext = usage.requestContext;
  const ariaLabel = t("contextMeter.ariaLabel", {
    limit: formatTokenCount(usage.window),
    used: formatTokenCount(used),
    percent,
  });
  return (
    <div
      ref={anchorRef}
      className="composer-context-meter"
      tabIndex={0}
      role="status"
      aria-label={ariaLabel}
      aria-describedby={tooltipOpen ? tooltipID : undefined}
      onBlur={() => setTooltipOpen(false)}
      onFocus={() => setTooltipOpen(true)}
      onMouseEnter={() => setTooltipOpen(true)}
      onMouseLeave={() => setTooltipOpen(false)}
    >
      <ContextUsageRing
        ratio={ratio}
        className="composer-context-meter-svg"
        data-fill={level}
        style={{ color: ringColor }}
      />
      <span className="composer-context-meter-label">{valueLabel}</span>
      {tooltipOpen ? (
        <FloatingMenuPortal
          anchorRef={anchorRef}
          owner="composer-context-meter"
          layer="tooltip"
          placement="above"
          align="right"
          offset={8}
          width={TOOLTIP_WIDTH}
        >
          <div
            id={tooltipID}
            className="composer-context-meter-tooltip"
            role="tooltip"
          >
            <div className="composer-context-meter-tooltip-headline">
              <span className="composer-context-meter-tooltip-label">
                {t("contextMeter.retainedHistory")}
              </span>
              <span className="composer-context-meter-tooltip-value">
                {percentLabel}
              </span>
            </div>
            <div className="composer-context-meter-tooltip-row">
              <span className="composer-context-meter-tooltip-label">{t("contextMeter.history")}</span>
              <span className="composer-context-meter-tooltip-value">
                {valueLabel}
              </span>
            </div>
            {requestContext ? (
              <>
                <div className="composer-context-meter-tooltip-divider" />
                <div className="composer-context-meter-tooltip-row">
                  <span className="composer-context-meter-tooltip-label">
                    {t("contextMeter.stablePrefix")}
                  </span>
                  <span className="composer-context-meter-tooltip-value">
                    {formatMessageShare(
                      requestContext.stablePrefix,
                      requestContext.messageCount,
                    )}
                  </span>
                </div>
                <div className="composer-context-meter-tooltip-row">
                  <span className="composer-context-meter-tooltip-label">
                    {t("contextMeter.turnPrefix")}
                  </span>
                  <span className="composer-context-meter-tooltip-value">
                    {formatMessageShare(
                      requestContext.turnPrefix,
                      requestContext.messageCount,
                    )}
                  </span>
                </div>
                <div className="composer-context-meter-tooltip-row">
                  <span className="composer-context-meter-tooltip-label">
                    {t("contextMeter.transientContext")}
                  </span>
                  <span className="composer-context-meter-tooltip-value">
                    {formatTransientContext(requestContext)}
                  </span>
                </div>
                <div className="composer-context-meter-tooltip-row">
                  <span className="composer-context-meter-tooltip-label">
                    {t("contextMeter.toolSurface")}
                  </span>
                  <span className="composer-context-meter-tooltip-value">
                    {formatToolSurface(requestContext)}
                  </span>
                </div>
              </>
            ) : null}
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}

function formatMessageShare(value: number, total: number): string {
  const safeValue = Math.max(0, Math.round(value));
  const safeTotal = Math.max(0, Math.round(total));
  return translate(safeTotal === 1 ? "contextMeter.messageShareOne" : "contextMeter.messageShare", {
    value: formatCurrentNumber(safeValue),
    total: formatCurrentNumber(safeTotal),
  });
}

function formatTransientContext(
  context: NonNullable<TurnContextUsage["requestContext"]>,
): string {
  const messageCount = Math.max(
    0,
    Math.round(context.transientMessages || context.hiddenMessages || 0),
  );
  const byteLabel = formatByteCount(context.dynamicBytes);
  if (messageCount <= 0) {
    return byteLabel ? byteLabel : translate("contextMeter.messageCount", { count: 0 });
  }
  const countLabel = translate(messageCount === 1 ? "contextMeter.messageCountOne" : "contextMeter.messageCount", {
    count: formatCurrentNumber(messageCount),
  });
  return byteLabel ? `${countLabel} · ${byteLabel}` : countLabel;
}

function formatToolSurface(
  context: NonNullable<TurnContextUsage["requestContext"]>,
): string {
  const toolCount = Math.max(0, Math.round(context.toolCount));
  const byteLabel = formatByteCount(context.toolSchemaBytes);
  if (toolCount <= 0) {
    return byteLabel ? byteLabel : translate("contextMeter.toolCount", { count: 0 });
  }
  const countLabel = translate(toolCount === 1 ? "contextMeter.toolCountOne" : "contextMeter.toolCount", {
    count: formatCurrentNumber(toolCount),
  });
  return byteLabel ? `${countLabel} · ${byteLabel}` : countLabel;
}

function formatByteCount(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return "";
  }
  if (value >= 1_000_000) {
    return `${trimNumber(value / 1_000_000)}MB`;
  }
  if (value >= 1_000) {
    return `${trimNumber(value / 1_000)}kB`;
  }
  return `${formatCurrentNumber(Math.round(value))}B`;
}

function formatTokenCount(value: number): string {
  if (!Number.isFinite(value) || value <= 0) {
    return "0";
  }
  if (value >= 1_000_000) {
    const scaled = value / 1_000_000;
    return `${trimNumber(scaled)}M`;
  }
  if (value >= 1_000) {
    const scaled = value / 1_000;
    return `${trimNumber(scaled)}k`;
  }
  return formatCurrentNumber(Math.round(value));
}

function trimNumber(value: number): string {
  return formatCurrentNumber(value, {
    maximumFractionDigits: value >= 100 ? 0 : 1,
    minimumFractionDigits: 0,
  });
}
