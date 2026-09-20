import { SESSION_SWITCH_PERF_ENABLED } from "./SessionSwitchPerformance";

/**
 * Development-only settle trace for a session switch.
 *
 * The restore effect positions the incoming conversation inside the switch
 * commit, so anything the reader perceives as a jump afterwards is a *later*
 * change to the same geometry: a queued scroll write, content that arrives with
 * the resumed snapshot, or a bottom-clearance token being re-measured. This
 * samples those values per frame and logs the deltas with the frame they landed
 * on, so a report names the writer instead of guessing at it.
 *
 * Console-only and gated by `import.meta.env.DEV`; it never ships in a
 * production bundle and adds no UI. Drop it once switch settling is stable.
 */

const SETTLE_FRAME_LIMIT = 72;
const SETTLE_QUIET_FRAMES = 15;

type SettleSample = {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
  contentHeight: number;
  tailSpace: number;
  composerHeight: number;
  statusSpace: number;
};

export function observeSessionSwitchSettle({
  threadID,
  pane,
}: {
  threadID: string;
  pane: HTMLElement;
}): void {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return;
  }
  const viewport = pane.closest<HTMLElement>(".scroll-region");
  if (!viewport) {
    return;
  }

  const read = (): SettleSample => ({
    scrollTop: Math.round(viewport.scrollTop),
    scrollHeight: Math.round(viewport.scrollHeight),
    clientHeight: Math.round(viewport.clientHeight),
    contentHeight: Math.round(pane.getBoundingClientRect().height),
    tailSpace: cssPixels(pane, "--session-tail-space"),
    composerHeight: cssPixels(pane, "--dock-composer-height"),
    statusSpace: cssPixels(pane, "--conversation-status-space"),
  });

  const first = read();
  const events: string[] = [];
  let previous = first;
  let frame = 0;
  let quiet = 0;

  const step = (): void => {
    frame += 1;
    const current = read();
    const moved = describeMove(previous, current);
    if (moved) {
      events.push(`${moved}@f${frame}`);
      quiet = 0;
    } else {
      quiet += 1;
    }
    previous = current;
    if (frame < SETTLE_FRAME_LIMIT && quiet < SETTLE_QUIET_FRAMES) {
      window.requestAnimationFrame(step);
      return;
    }
    console.info(
      `[session-settle] ${threadID} frames=${frame} ` +
        `start=${JSON.stringify(first)} end=${JSON.stringify(current)} ` +
        `events=${events.length > 0 ? events.join(" ") : "none"}`,
    );
  };

  window.requestAnimationFrame(step);
}

function cssPixels(node: HTMLElement, property: string): number {
  const declared = node.style.getPropertyValue(property);
  if (!declared) {
    return 0;
  }
  const parsed = Number.parseFloat(declared);
  return Number.isFinite(parsed) ? Math.round(parsed) : 0;
}

function describeMove(previous: SettleSample, current: SettleSample): string | undefined {
  const parts: string[] = [];
  for (const key of Object.keys(previous) as Array<keyof SettleSample>) {
    const delta = current[key] - previous[key];
    if (delta !== 0) {
      parts.push(`${key}${delta > 0 ? "+" : ""}${delta}`);
    }
  }
  return parts.length > 0 ? parts.join(",") : undefined;
}
