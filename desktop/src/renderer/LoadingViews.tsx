import { useEffect, useState } from "react";
import { useI18n } from "./i18n";
import { WuuMascot, type WuuMascotActivity } from "./WuuMascot";

type EmptyHomeMascotActivity = Extract<WuuMascotActivity, "idle" | "compose">;

function randomDuration([minimum, maximum]: readonly [number, number]): number {
  return minimum + Math.random() * (maximum - minimum);
}

function emptyHomeMascotDwellTime(
  primary: EmptyHomeMascotActivity,
  current: EmptyHomeMascotActivity,
): number {
  if (current === primary) {
    return randomDuration(primary === "idle" ? [3_200, 6_800] : [4_200, 7_600]);
  }
  return randomDuration(primary === "idle" ? [800, 1_800] : [500, 1_100]);
}

function useAutonomousMascotActivity(primary: WuuMascotActivity): WuuMascotActivity {
  const [state, setState] = useState<{ source: WuuMascotActivity; current: WuuMascotActivity }>(
    { source: primary, current: primary },
  );

  useEffect(() => {
    setState({ source: primary, current: primary });
    if (primary !== "idle" && primary !== "compose") return;

    const reducedMotion = typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-reduced-motion: reduce)")
      : null;
    let current: EmptyHomeMascotActivity = primary;
    let timer: number | undefined;

    const schedule = () => {
      if (reducedMotion?.matches) return;
      timer = window.setTimeout(() => {
        current = current === "idle" ? "compose" : "idle";
        setState({ source: primary, current });
        schedule();
      }, emptyHomeMascotDwellTime(primary, current));
    };
    const handleMotionPreference = () => {
      if (timer !== undefined) window.clearTimeout(timer);
      current = primary;
      setState({ source: primary, current: primary });
      schedule();
    };

    schedule();
    reducedMotion?.addEventListener("change", handleMotionPreference);
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      reducedMotion?.removeEventListener("change", handleMotionPreference);
    };
  }, [primary]);

  // A changed interaction state wins immediately, before the effect above has
  // restarted its autonomous cycle for the new primary pose.
  return state.source === primary ? state.current : primary;
}

export function RuntimeLoading({
  status
}: {
  status: string;
}): JSX.Element {
  const { t } = useI18n();
  const isStarting = status === "connecting" || status === "opening";
  return (
    <div className="project-empty-pane" data-wuu-component="launch-view">
      {isStarting ? (
        <div className="wuu-launch" role="status" aria-label={t("loading.starting")}>
          <div className="wuu-launch-glass" aria-hidden="true">
            <WuuMascot
              className="wuu-launch-mascot"
              accessory="none"
            />
          </div>
        </div>
      ) : (
        <div className="project-empty-content">
          <h2>{status}</h2>
        </div>
      )}
    </div>
  );
}

export function ViewSwitchLoading({ compact = false }: { compact?: boolean }): JSX.Element {
  const { t } = useI18n();
  return (
    <div className={`view-switch-loading${compact ? " view-switch-loading-compact" : ""}`} role="status" aria-label={t("loading.switching")}>
      <div className="wuu-launch-mark view-switch-mark" aria-hidden="true">
        <span>w</span>
        <span>u</span>
        <span>u</span>
      </div>
      <div className="wuu-launch-rail view-switch-rail" aria-hidden="true" />
    </div>
  );
}

export function EmptyConversationHome({
  title,
  belowTitle,
  // The caller passes "compose" once a draft exists. The empty-home mascot
  // maps that interaction signal to a lowered primary gaze because the draft
  // and composer sit below it; without a draft it primarily faces the user.
  activity = "idle",
  children
}: {
  title: string;
  // Optional element rendered directly under the title in the same
  // grid cell so it can sit a few pixels below the greeting without
  // inheriting the very large row-gap reserved for the hero composer.
  belowTitle?: JSX.Element;
  activity?: WuuMascotActivity;
  children?: JSX.Element | null;
}): JSX.Element {
  const primaryMascotActivity = activity === "idle"
    ? "compose"
    : activity === "compose"
      ? "idle"
      : activity;
  const mascotActivity = useAutonomousMascotActivity(primaryMascotActivity);

  return (
    <section className="empty-home" data-wuu-component="empty-session">
      <div className="empty-home-inner session-flow">
        <div className="empty-home-header">
          {/* The wuu blobatar greeting faces the user while the composer is
              empty, lowers its primary gaze once a draft appears below it,
              occasionally visits the other pose on an irregular rhythm, and
              keeps pointer gaze layered over both. Hue
              is pinned the same way default avatars are, so the mark matches
              the pairing hero on the phone companion. Shape is pinned to
              "round" (the first silhouette, shape < 0.28) with the aspect
              ratio locked to 1, and the plate is off (none), so the mascot is
              a pure round ball floating on the paper. */}
          <WuuMascot
            className="empty-home-mascot"
            activity={mascotActivity}
            followPointer
            animate="hover"
            aria-hidden="true"
          />
          <h2>{title}</h2>
          {belowTitle ?? null}
        </div>
        {children}
      </div>
    </section>
  );
}
