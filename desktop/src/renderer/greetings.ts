import { useEffect, useState } from "react";
import { translateCurrent } from "./i18n";

// Context for the empty new-conversation greeting. We keep it as a
// discriminated union so the helper can't accidentally mix the
// workspace-name and wuu fallbacks.
export type GreetingContext =
  | { kind: "workspace"; workspaceName: string }
  | { kind: "wuu" };

// Five time-of-day buckets in the user's local time. Boundaries are
// chosen for a coding tool: we want a clear "morning / noon / afternoon /
// evening / late night" feel without splitting the day into too many
// thin slices that would feel jittery.
export function greetingFor(hour: number, ctx: GreetingContext): string {
  const workspace = ctx.kind === "workspace" ? ctx.workspaceName : null;

  if (hour >= 5 && hour < 11) {
    return workspace
      ? translateCurrent("greeting.workspace.morning", { workspace })
      : translateCurrent("greeting.wuu.morning");
  }
  if (hour >= 11 && hour < 14) {
    return workspace
      ? translateCurrent("greeting.workspace.noon", { workspace })
      : translateCurrent("greeting.wuu.noon");
  }
  if (hour >= 14 && hour < 18) {
    return workspace
      ? translateCurrent("greeting.workspace.afternoon", { workspace })
      : translateCurrent("greeting.wuu.afternoon");
  }
  if (hour >= 18 && hour < 22) {
    return workspace
      ? translateCurrent("greeting.workspace.evening", { workspace })
      : translateCurrent("greeting.wuu.evening");
  }
  // 22:00 – 04:59 late night.
  return workspace
    ? translateCurrent("greeting.workspace.lateNight", { workspace })
    : translateCurrent("greeting.wuu.lateNight");
}

// Re-render once a minute so the greeting updates when the user crosses
// an hour boundary while the app sits idle on the empty screen. React
// bails out of the setState call when the hour is unchanged, so a
// no-op tick is free.
export function useCurrentHour(intervalMs: number = 60_000): number {
  const [hour, setHour] = useState<number>(() => new Date().getHours());
  useEffect(() => {
    const id = setInterval(() => {
      setHour(new Date().getHours());
    }, intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return hour;
}
