import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { processBackground } from "./image";
import { useBackground } from "./useBackground";

export function AppBackground(): JSX.Element | null {
  const { preferences } = useBackground();
  const [light, setLight] = useState(document.documentElement.dataset.theme !== "dark");
  const [url, setURL] = useState("");
  useEffect(() => {
    const observer = new MutationObserver(() => setLight(document.documentElement.dataset.theme !== "dark"));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    setURL("");
    if (preferences) void processBackground(preferences.image, preferences.effect, light, controller.signal).then(blob => {
      if (controller.signal.aborted) return;
      objectURL = URL.createObjectURL(blob);
      setURL(objectURL);
    }, () => {});
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [preferences?.image, preferences?.effect, light]);
  useEffect(() => {
    document.documentElement.toggleAttribute("data-app-background", !!url);
    return () => document.documentElement.removeAttribute("data-app-background");
  }, [url]);
  return url ? createPortal(<div className="app-background" aria-hidden="true" style={{ backgroundImage: `url(${JSON.stringify(url)})`, opacity: preferences?.opacity ?? 0.15 }} />, document.body) : null;
}
