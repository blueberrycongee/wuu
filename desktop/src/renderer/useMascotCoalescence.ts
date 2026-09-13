import { useEffect, useId, useRef } from "react";

const SVG_NS = "http://www.w3.org/2000/svg";

/** Liquid motion borrows the existing silhouette and rejoins before changing state. */
export function useMascotCoalescence(svg: SVGSVGElement | null, mode: "idle" | "off", identity: string): void {
  const filterID = `mascot-liquid-${useId().replace(/:/g, "")}`;
  const currentMode = useRef(mode);
  const changeMode = useRef(() => {});
  currentMode.current = mode;
  useEffect(() => {
    if (!svg) return;
    const body = svg.querySelector<SVGPathElement>(".mo-bob > g:not(.mo-eyes) > path");
    if (!body || typeof body.getTotalLength !== "function") return;
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    let timer: number | undefined;
    let frame: number | undefined;
    let liquid: SVGGElement | null = null;
    let definitions: SVGDefsElement | null = null;
    let returnAt: number | null = null;
    let amount = 0;
    let returnFrom = 0;
    const originalVisibility = body.style.visibility;
    const paintable = () => !document.hidden && !reduced?.matches
      && !svg.closest(':root[data-renderer-hidden], .cached-conversation-pane[data-active="false"]');
    const clearArt = () => {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
      frame = undefined;
      liquid?.remove();
      definitions?.remove();
      liquid = null;
      definitions = null;
      body.style.visibility = originalVisibility;
      returnAt = null;
    };
    const schedule = () => {
      window.clearTimeout(timer);
      if (paintable() && currentMode.current !== "off") {
        timer = window.setTimeout(play, 20_000 + Math.random() * 20_000);
      }
    };
    const element = <K extends keyof SVGElementTagNameMap>(name: K, attrs: Record<string, string>) => {
      const node = document.createElementNS(SVG_NS, name);
      for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value);
      return node;
    };
    const play = () => {
      if (!paintable() || currentMode.current === "off" || liquid) return;
      const bounds = body.getBBox();
      const cx = bounds.x + bounds.width / 2, cy = bounds.y + bounds.height / 2;
      const length = body.getTotalLength();
      // Sample the actual contour so polygonal identities also emit from their rim.
      const anchors = [0.12, 0.48, 0.8].map(fraction => body.getPointAtLength(length * fraction));
      definitions = element("defs", {});
      const filter = element("filter", { id: filterID, x: "-25%", y: "-25%", width: "150%", height: "150%", "color-interpolation-filters": "sRGB" });
      filter.append(
        element("feGaussianBlur", { in: "SourceGraphic", stdDeviation: "2.1" }),
        element("feColorMatrix", { type: "matrix", values: "1 0 0 0 0 0 1 0 0 0 0 0 1 0 0 0 0 0 20 -9", result: "silhouette" }),
        element("feFlood", { "flood-color": "var(--mo-head)" }),
        element("feComposite", { in2: "silhouette", operator: "in" }),
      );
      definitions.append(filter);
      liquid = element("g", { filter: `url(#${filterID})`, fill: "var(--mo-head)", "data-wuu-liquid": "" });
      const silhouette = body.cloneNode(false) as SVGPathElement;
      silhouette.removeAttribute("id");
      liquid.append(silhouette);
      const drops = Array.from({ length: 3 }, () => element("circle", { r: "0" }));
      liquid.append(...drops);
      svg.append(definitions);
      body.after(liquid);
      body.style.visibility = "hidden";
      const started = performance.now();
      const draw = (now: number) => {
        if (!paintable()) { clearArt(); return; }
        const elapsed = now - started;
        const progress = Math.min(1, elapsed / 1800);
        const returning = returnAt === null ? 0 : Math.min(1, (now - returnAt) / 160);
        amount = returnAt === null
          ? Math.sin(Math.PI * progress) ** 2
          : returnFrom * (1 - returning * returning * (3 - 2 * returning));
        if ((returnAt === null && progress === 1) || returning === 1) { clearArt(); schedule(); return; }
        const scale = 1 - amount * 0.035;
        silhouette.setAttribute("transform", `translate(${cx} ${cy}) scale(${scale}) translate(${-cx} ${-cy})`);
        drops.forEach((drop, index) => {
          const anchor = anchors[index];
          const dx = anchor.x - cx, dy = anchor.y - cy;
          const distance = Math.hypot(dx, dy) || 1;
          const reach = -5 + amount * 14;
          const drift = Math.sin(progress * Math.PI * 2 + index) * amount * 3;
          drop.setAttribute("cx", `${anchor.x + dx / distance * reach - dy / distance * drift}`);
          drop.setAttribute("cy", `${anchor.y + dy / distance * reach + dx / distance * drift}`);
          drop.setAttribute("r", `${(4.2 - index * 0.6) * amount}`);
        });
        frame = window.requestAnimationFrame(draw);
      };
      frame = window.requestAnimationFrame(draw);
    };
    const rejoin = () => {
      window.clearTimeout(timer);
      if (liquid && returnAt === null) { returnAt = performance.now(); returnFrom = amount; }
      else if (!liquid) schedule();
    };
    changeMode.current = rejoin;
    const interact = () => {
      if (currentMode.current !== "idle") return;
      if (liquid && returnAt === null) { returnAt = performance.now(); returnFrom = amount; }
      else if (!liquid) schedule();
    };
    const reset = () => { clearArt(); schedule(); };
    const observer = new MutationObserver(() => { if (!paintable()) clearArt(); else schedule(); });
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-renderer-hidden"] });
    const pane = svg.closest('.cached-conversation-pane');
    if (pane) observer.observe(pane, { attributes: true, attributeFilter: ["data-active"] });
    schedule();
    document.addEventListener("pointerdown", interact, true);
    document.addEventListener("keydown", interact, true);
    svg.addEventListener("pointerenter", interact);
    document.addEventListener("visibilitychange", reset);
    reduced?.addEventListener("change", reset);
    return () => {
      changeMode.current = () => {};
      window.clearTimeout(timer);
      clearArt();
      observer.disconnect();
      document.removeEventListener("pointerdown", interact, true);
      document.removeEventListener("keydown", interact, true);
      svg.removeEventListener("pointerenter", interact);
      document.removeEventListener("visibilitychange", reset);
      reduced?.removeEventListener("change", reset);
    };
  }, [svg, identity, filterID]);
  useEffect(() => { changeMode.current(); }, [mode]);
}
