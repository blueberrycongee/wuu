import { useEffect, useState } from "react";
import type { WuuMascotActivity } from "./wuu-mascot-spec";

/** Decorative attention never changes the caller's semantic activity. */
export function useMascotAttention(activity: WuuMascotActivity, enabled: boolean): WuuMascotActivity {
  const [glance, setGlance] = useState<{ source: WuuMascotActivity; pose: WuuMascotActivity } | null>(null);
  useEffect(() => {
    setGlance(null);
    if (!enabled || (activity !== "idle" && activity !== "compose")) return;
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    let timer: number | undefined;
    let away = false;
    const schedule = () => {
      if (reduced?.matches || document.hidden) return;
      timer = window.setTimeout(() => {
        away = !away;
        setGlance(away ? { source: activity, pose: activity === "idle" ? "compose" : "idle" } : null);
        schedule();
      }, away ? 600 + Math.random() * 700 : 3_200 + Math.random() * 3_600);
    };
    const reset = () => {
      window.clearTimeout(timer);
      away = false;
      setGlance(null);
      schedule();
    };
    schedule();
    reduced?.addEventListener("change", reset);
    document.addEventListener("visibilitychange", reset);
    return () => {
      window.clearTimeout(timer);
      reduced?.removeEventListener("change", reset);
      document.removeEventListener("visibilitychange", reset);
    };
  }, [activity, enabled]);
  return enabled && glance?.source === activity ? glance.pose : activity;
}

export const MASCOT_EXIT_MS = 180;

/** Keep the same instance through a cancelled exit; ignore child animation events. */
export function useMascotPresence(visible: boolean): boolean {
  const [retained, setRetained] = useState(visible);
  useEffect(() => {
    if (visible) {
      setRetained(true);
      return;
    }
    if (!retained) return;
    const timer = window.setTimeout(() => setRetained(false), MASCOT_EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [visible, retained]);
  return visible || retained;
}
