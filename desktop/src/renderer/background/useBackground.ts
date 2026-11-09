import { useEffect, useState } from "react";
import { observeBackground, readBackground, type BackgroundPreferences } from "./preferences";

const hostBackground = { readBackground, observeBackground };

export function useBackground(source: Pick<import("../../../../packages/plugin-sdk/src/ui-settings-controls").ApplicationSettingsControlsV1, "readBackground" | "observeBackground"> = hostBackground) {
  const [preferences, setPreferences] = useState<BackgroundPreferences | null>(null);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let generation = 0;
    const refresh = () => {
      const current = ++generation;
      void source.readBackground().then(value => {
        if (generation === current) {
          // Focus refreshes clone blobs; retain image identity to avoid reprocessing.
          setPreferences(previous => value && previous?.imageID === value.imageID ? { ...value, image: previous.image } : value);
          setError(false); setLoading(false);
        }
      }, () => { if (generation === current) { setError(true); setLoading(false); } });
    };
    const stop = source.observeBackground(refresh);
    refresh();
    return () => { generation++; stop(); };
  }, [source]);
  return { preferences, error, loading };
}
