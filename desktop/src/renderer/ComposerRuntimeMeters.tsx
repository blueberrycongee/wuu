import { useSyncExternalStore } from "react";
import type { TurnContextUsage } from "./AppState";
import { ComposerContextMeter } from "./ComposerContextMeter";
// Temporarily hidden: the token-speed gauge is removed from the composer
// toolbar while we evaluate its cost/benefit. Restore by un-commenting this
// import and the <ComposerTokenGauge> block in the JSX below.
// import { ComposerTokenGauge } from "./ComposerTokenGauge";
import { turnTelemetryStore } from "./TurnTelemetryStore";

function isExternalEngine(engine?: string): boolean {
  return Boolean(engine && engine !== "wuu");
}

export function ComposerRuntimeMeters({
  running,
  turnID,
  fallbackTokensPerSecond = 0,
  fallbackSampledAt,
  fallbackSource = "none",
  fallbackContextUsage,
  activeEngine,
}: {
  running: boolean;
  turnID?: string;
  fallbackTokensPerSecond?: number;
  fallbackSampledAt?: number;
  fallbackSource?: "real" | "estimated" | "none";
  fallbackContextUsage?: TurnContextUsage | null;
  // External engines (Codex/Claude) currently report usage against a
  // mismatched window, so the composer ring is hidden until that path is
  // trustworthy. The built-in Wuu engine keeps the meter.
  activeEngine?: string;
}): JSX.Element | null {
  const telemetry = useSyncExternalStore(
    turnTelemetryStore.subscribe,
    () => turnTelemetryStore.getSnapshot(turnID),
    () => turnTelemetryStore.getSnapshot(turnID),
  );
  const useLiveTelemetry = Boolean(turnID && telemetry.source !== "none");
  const contextUsage = telemetry.contextUsage
    ? {
        ...telemetry.contextUsage,
        ...(fallbackContextUsage?.window
          ? { window: fallbackContextUsage.window }
          : {}),
        requestContext: fallbackContextUsage?.requestContext,
      }
    : fallbackContextUsage ?? undefined;

  if (isExternalEngine(activeEngine)) {
    return null;
  }

  return (
    <>
      {/* Temporarily hidden — token-speed gauge, see note at the import.
          <ComposerTokenGauge
            running={running}
            tokensPerSecond={
              useLiveTelemetry ? telemetry.tokensPerSecond : fallbackTokensPerSecond
            }
            sampledAt={useLiveTelemetry ? telemetry.sampledAt : fallbackSampledAt}
            source={useLiveTelemetry ? telemetry.source : fallbackSource}
          /> */}
      <ComposerContextMeter usage={contextUsage} />
    </>
  );
}
