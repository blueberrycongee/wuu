import type { Rectangle } from "electron";
import type { ActivitySession } from "../shared/protocol";
import type { BrowserInteractionHint } from "./browserHostWindows";
import type { BrowserPiPWindowHandle } from "./activityPiPWindow";
import type { ObservationPiPEventSink } from "./cuaActivityWindows";

// Internal desktop adapters own content only. Window ownership, visibility,
// geometry and task controls stay in the host and are never extension APIs.
export interface ActivityPiPContent {
  start(): void;
  setVisible(visible: boolean): void;
  setLive?(live: boolean): void;
  updateActivity?(activity: ActivitySession): void;
  refresh?(): void;
  layout(bounds: Rectangle): void;
  // A data URL of the content as currently displayed, for appearance motion.
  snapshot?(): Promise<string | undefined>;
  stop(onStopped: () => void): void;
}

export type ActivityPiPContentContext = ObservationPiPEventSink & {
  window: BrowserPiPWindowHandle;
  setViewport(viewport: { width: number; height: number }): void;
  setMounted(mounted: boolean): void;
  setLabel(label: string): void;
  onInteraction(hint: BrowserInteractionHint): Promise<unknown> | undefined;
};

export type ActivityPiPContentFactory = (
  activity: ActivitySession,
  context: ActivityPiPContentContext,
) => ActivityPiPContent | undefined;

export function pipContainRect(
  contentW: number, contentH: number, boxW: number, boxH: number,
): { x: number; y: number; width: number; height: number; scale: number } {
  if (contentW <= 0 || contentH <= 0 || boxW <= 0 || boxH <= 0) {
    return { x: 0, y: 0, width: 0, height: 0, scale: 0 };
  }
  const scale = Math.min(1, boxW / contentW, boxH / contentH);
  const width = contentW * scale, height = contentH * scale;
  return { x: (boxW - width) / 2, y: (boxH - height) / 2, width, height, scale };
}

export function activityPreviewIsLive(activity: ActivitySession): boolean {
  return activity.state !== "stopped" && activity.controller !== "none";
}
