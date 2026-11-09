import { useComposerServices } from "./ComposerServicesContext";
import { useSyncExternalStore } from "react";
import type { TurnContextUsage } from "./AppState";
import { ComposerContextMeter } from "./ComposerContextMeter";
import { turnTelemetryStore } from "./TurnTelemetryStore";

function isExternalEngine(engine?: string): boolean {
  return Boolean(engine && engine !== "wuu");
}

export function ComposerRuntimeMeters({
  inToolRail = false,
  compact = inToolRail,
  turnID,
  fallbackContextUsage,
  activeEngine,
}: {
  inToolRail?: boolean;
  compact?: boolean;
  turnID?: string;
  fallbackContextUsage?: TurnContextUsage | null;
  // External engines (Codex/Claude) currently report usage against a
  // mismatched window, so the composer ring is hidden until that path is
  // trustworthy. The built-in Wuu engine keeps the meter.
  activeEngine?: string;
}): JSX.Element | null {
  const services = useComposerServices();
  const telemetry = useSyncExternalStore(
    services?.presentation.subscribeTelemetry ?? turnTelemetryStore.subscribe,
    () => services ? services.presentation.getTelemetry(turnID) : turnTelemetryStore.getSnapshot(turnID),
    () => services ? services.presentation.getTelemetry(turnID) : turnTelemetryStore.getSnapshot(turnID),
  );
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

  return <ComposerContextMeter usage={contextUsage} inToolRail={inToolRail} compact={compact} />;
}
