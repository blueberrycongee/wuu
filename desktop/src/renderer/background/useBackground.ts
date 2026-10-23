import { useEffect, useState } from "react";
import { observeBackground, readBackground, type BackgroundPreferences } from "./preferences";

export function useBackground() {
  const [preferences, setPreferences] = useState<BackgroundPreferences | null>(null);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let generation = 0;
    const refresh = () => {
      const current = ++generation;
      void readBackground().then(value => {
        if (generation === current) {
          // Focus refreshes clone blobs; retain image identity to avoid reprocessing.
          setPreferences(previous => value && previous?.imageID === value.imageID ? { ...value, image: previous.image } : value);
          setError(false); setLoading(false);
        }
      }, () => { if (generation === current) { setError(true); setLoading(false); } });
    };
    const stop = observeBackground(refresh);
    refresh();
    return () => { generation++; stop(); };
  }, []);
  return { preferences, error, loading };
}
