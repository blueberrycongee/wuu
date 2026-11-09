import { useEffect, useLayoutEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { currentAppliedTheme, observeAppliedTheme } from "../Theme";
import { processBackground } from "./image";
import { useBackground } from "./useBackground";
import { publishCanvasArtwork } from "./ShellCanvasArtwork";
import { showsUserImage, useShellSkins, useSkinImage } from "./ShellSkin";

export function AppBackground(): JSX.Element | null {
  const { preferences } = useBackground();
  const { skins, selected } = useShellSkins();
  const [mode, setMode] = useState(currentAppliedTheme);
  const skin = skins.find(candidate => candidate.key === selected);
  const variant = skin?.[mode];
  const placement = skin?.placement === "canvas" ? "canvas" : "frame";
  const picture = showsUserImage(skin) && preferences ? preferences.image : null;
  const pluginImage = useSkinImage(picture ? undefined : skin, mode);
  const [processed, setProcessed] = useState<{ blob: Blob; url: string } | null>(null);
  useEffect(() => {
    setMode(currentAppliedTheme());
    return observeAppliedTheme(setMode);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    setProcessed(null);
    if (picture) void processBackground(picture, placement === "canvas" ? `canvas-${mode}` : mode, controller.signal).then(async blob => {
      if (controller.signal.aborted) return;
      // Canvas artwork is painted from the blob; frame artwork is a CSS URL
      // that the native chrome overlay must also be able to load.
      const url = placement === "canvas" ? ""
        : window.wuu?.cacheShellImage ? await window.wuu.cacheShellImage(await blob.arrayBuffer())
        : (objectURL = URL.createObjectURL(blob));
      if (!controller.signal.aborted) setProcessed({ blob, url });
    }).catch(() => {});
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [picture, mode, placement]);
  const frameURL = placement !== "frame" ? "" : picture ? processed?.url ?? "" : pluginImage.url;
  const canvasSource = placement !== "canvas" ? undefined : picture ? processed?.blob : pluginImage.url || undefined;
  const enabled = Boolean(skin || frameURL);
  const position = variant?.position ?? "center";
  const align = useMemo(() => [
    position === "left" ? 0 : position === "right" ? 1 : 0.5,
    position === "top" ? 0 : position === "bottom" ? 1 : 0.5,
  ] as const, [position]);
  useEffect(() => {
    publishCanvasArtwork(canvasSource ? { source: canvasSource, dots: variant?.dots, overlay: variant?.overlay, align, onError: pluginImage.retry } : null);
    return () => publishCanvasArtwork(null);
  }, [canvasSource, variant, align, pluginImage.retry]);
  useLayoutEffect(() => {
    const root = document.documentElement;
    const variables: Record<string, string> = {};
    if (enabled) {
      variables["--shell-background"] = variant?.background ?? "var(--sidebar-material-fill)";
      variables["--shell-foreground"] = variant?.foreground ?? (mode === "light" ? "#171719" : "#f5f5f7");
      variables["--shell-overlay"] = variant?.overlay ?? "transparent";
      if (variant?.hover) variables["--shell-hover"] = variant.hover;
      else if (!skin) variables["--shell-hover"] = mode === "light" ? "#ffffff70" : "#18181970";
      if (variant?.active) variables["--shell-active"] = variant.active;
      else if (!skin) variables["--shell-active"] = mode === "light" ? "#ffffffcc" : "#181819cc";
      if (placement === "canvas") variables["--shell-veil"] = String(variant?.veil ?? 0.86);
      if (frameURL) {
        variables["--shell-image"] = `url(${JSON.stringify(frameURL)})`;
        variables["--shell-image-ratio"] = "1";
        variables["--shell-image-align-x"] = String(align[0]);
        variables["--shell-image-align-y"] = String(align[1]);
      }
    }
    for (const [name, value] of Object.entries(variables)) root.style.setProperty(name, value);
    root.toggleAttribute("data-shell-skin", enabled);
    if (enabled) root.dataset.shellPlacement = placement;
    else delete root.dataset.shellPlacement;
    // The chrome overlay receives this URL separately from CSS values.
    if (frameURL) root.dataset.shellImage = frameURL;
    else delete root.dataset.shellImage;
    const image = new Image();
    image.onload = () => root.style.setProperty("--shell-image-ratio", String(image.naturalWidth / image.naturalHeight));
    image.onerror = () => pluginImage.retry();
    if (frameURL) image.src = frameURL;
    return () => {
      image.onload = null;
      image.onerror = null;
      for (const name of Object.keys(variables)) root.style.removeProperty(name);
      root.removeAttribute("data-shell-skin");
      delete root.dataset.shellPlacement;
      delete root.dataset.shellImage;
    };
  }, [enabled, frameURL, variant, mode, placement, skin, align, pluginImage.retry]);
  return enabled && placement === "frame"
    ? createPortal(<div className="app-background" aria-hidden="true" />, document.body)
    : null;
}
