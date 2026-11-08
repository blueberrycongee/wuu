import { useEffect, useState } from "react";
import type { DesktopPreferences } from "../shared/protocol";

let current: DesktopPreferences | undefined;
let loading: Promise<void> | undefined;
const listeners = new Set<(preferences: DesktopPreferences) => void>();

function publish(preferences: DesktopPreferences): void {
  current = preferences;
  for (const listener of listeners) listener(preferences);
}

function ensureLoaded(): void {
  const api = window.wuu;
  if (loading || !api?.getDesktopPreferences) return;
  api.onDesktopPreferencesChanged?.(publish);
  loading = api.getDesktopPreferences().then(publish, error => {
    loading = undefined;
    console.error("Could not read desktop preferences", error);
  });
}

/** Undefined on hosts without desktop preferences and until the first read. */
export function useDesktopPreferences(): DesktopPreferences | undefined {
  const [preferences, setPreferences] = useState(current);
  useEffect(() => {
    listeners.add(setPreferences);
    ensureLoaded();
    if (current) setPreferences(current);
    return () => { listeners.delete(setPreferences); };
  }, []);
  return preferences;
}

export async function updateDesktopPreferences(patch: Partial<DesktopPreferences>): Promise<void> {
  const api = window.wuu;
  if (!api?.updateDesktopPreferences) throw new Error("Desktop preferences are unavailable");
  publish(await api.updateDesktopPreferences(patch));
}
