import { useEffect, useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import { currentAppliedTheme, observeAppliedTheme } from "../Theme";
import { processBackground } from "./image";
import { useBackground } from "./useBackground";
import { useShellSkins, useSkinImage } from "./ShellSkin";

export function AppBackground(): JSX.Element | null {
  const { preferences } = useBackground();
  const { skins, selected } = useShellSkins();
  const [mode, setMode] = useState(currentAppliedTheme);
  const skin = skins.find(candidate => candidate.key === selected);
  const variant = skin?.[mode];
  const pluginImage = useSkinImage(skin, mode);
  const [localURL, setLocalURL] = useState("");
  useEffect(() => {
    setMode(currentAppliedTheme());
    return observeAppliedTheme(setMode);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    let objectURL = "";
    setLocalURL("");
    if (selected === "custom" && preferences) void processBackground(preferences.image, mode, controller.signal).then(async blob => {
      if (controller.signal.aborted) return;
      const url = window.wuu?.cacheShellImage
        ? await window.wuu.cacheShellImage(await blob.arrayBuffer())
        : (objectURL = URL.createObjectURL(blob));
      if (!controller.signal.aborted) setLocalURL(url);
    }).catch(() => {});
    return () => { controller.abort(); if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [selected, preferences?.image, mode]);
  const url = skin ? pluginImage.url : selected === "custom" ? localURL : "";
  const enabled = Boolean(skin || url);
  // Canvas placement moves the artwork behind the main pane; the custom image
  // and every skin without a placement keep decorating only the frame.
  const placement = skin?.placement === "canvas" ? "canvas" : "frame";
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
      if (url) variables["--shell-image"] = `url(${JSON.stringify(url)})`;
      variables["--shell-image-ratio"] = "1";
      const position = variant?.position ?? "center";
      variables["--shell-image-align-x"] = position === "left" ? "0" : position === "right" ? "1" : "0.5";
      variables["--shell-image-align-y"] = position === "top" ? "0" : position === "bottom" ? "1" : "0.5";
      if (placement === "canvas") {
        variables["--shell-veil"] = String(variant?.veil ?? 0.86);
        if (variant?.dots) {
          variables["--shell-dots-pitch"] = `${variant.dots.pitch}px`;
          variables["--shell-dots-size"] = `${variant.dots.size}px`;
        }
      }
    }
    for (const [name, value] of Object.entries(variables)) root.style.setProperty(name, value);
    root.toggleAttribute("data-shell-skin", enabled);
    if (enabled) root.dataset.shellPlacement = placement;
    else delete root.dataset.shellPlacement;
    root.toggleAttribute("data-shell-dots", enabled && Boolean(variant?.dots));
    // The chrome overlay receives this URL separately from CSS values. Canvas
    // artwork lives inside the pane, so the frame keeps a solid fill there.
    if (url && placement === "frame") root.dataset.shellImage = url;
    else delete root.dataset.shellImage;
    const image = new Image();
    image.onload = () => root.style.setProperty("--shell-image-ratio", String(image.naturalWidth / image.naturalHeight));
    if (url) image.src = url;
    return () => {
      image.onload = null;
      for (const name of Object.keys(variables)) root.style.removeProperty(name);
      root.removeAttribute("data-shell-skin");
      root.removeAttribute("data-shell-dots");
      delete root.dataset.shellPlacement;
      delete root.dataset.shellImage;
    };
  }, [enabled, url, variant, mode, placement]);
  return enabled && placement === "frame"
    ? createPortal(<div className="app-background" aria-hidden="true" />, document.body)
    : null;
}
