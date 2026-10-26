import { createContext, useContext, useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { MAX_PRESENTATION_FRAME_EDGE, MAX_PRESENTATION_FRAME_PIXELS } from "../shared/PresentationFrameLimits";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { flushSync } from "react-dom";
import { showApplicationActionError } from "./ApplicationActionError";
import { ViewSwitchLoading } from "./LoadingViews";

type Pending = { readonly requestId?: number; readonly intentId?: string; readonly contentReady?: boolean };
type Frame = { release(): void; full(): void; region(regions: readonly ConversationPresentationRegion[]): void };
export type ConversationPresentationRegion = { readonly element: HTMLElement; readonly excludeBelow?: readonly HTMLElement[] };
type RegionBounds = { left: number; top: number; width: number; height: number };

function measureRegions(regions: readonly ConversationPresentationRegion[]): RegionBounds[] {
  return regions.map(({ element, excludeBelow = [] }) => {
    const bounds = element.getBoundingClientRect();
    const bottom = Math.min(bounds.bottom, ...excludeBelow.map(node => node.getBoundingClientRect())
      .filter(rect => rect.width > 0 && rect.height > 0 && rect.bottom > bounds.top).map(rect => rect.top));
    return { left: bounds.left, top: bounds.top, width: bounds.width, height: Math.max(0, bottom - bounds.top) };
  });
}

/** The loading animation uses the same message-only geometry as the held frame. */
export function ConversationPresentationLoading({ scopeRef, getRegions, visible }: {
  scopeRef: RefObject<HTMLElement | null>; getRegions: () => readonly ConversationPresentationRegion[]; visible: boolean;
}): ReactNode {
  const [bounds, setBounds] = useState<RegionBounds[]>([]);
  useLayoutEffect(() => {
    if (!visible || !scopeRef.current) { setBounds(current => current.length ? [] : current); return; }
    const regions = getRegions();
    const measure = () => {
      const origin = scopeRef.current?.getBoundingClientRect();
      if (!origin) return;
      const next = measureRegions(regions).map(rect => ({ ...rect, left: rect.left - origin.left, top: rect.top - origin.top }));
      setBounds(current => current.length === next.length && current.every((rect, index) =>
        Object.keys(rect).every(key => rect[key as keyof RegionBounds] === next[index][key as keyof RegionBounds])) ? current : next);
    };
    measure();
    const observer = new ResizeObserver(measure);
    for (const element of [scopeRef.current, ...regions.flatMap(region => [region.element, ...region.excludeBelow ?? []])]) observer.observe(element);
    window.addEventListener("resize", measure);
    return () => { observer.disconnect(); window.removeEventListener("resize", measure); };
  }, [visible, scopeRef, getRegions]);
  return visible ? bounds.map((rect, index) => <ViewSwitchLoading key={index} placement="conversation"
    style={{ position: "absolute", ...rect }} />) : null;
}
const MAX_PRESENTATION_MS = 4000;
const MAX_CAPTURE_MS = 120;

function supportsCapture(): boolean {
  return document.visibilityState !== "hidden" && (typeof document.startViewTransition === "function"
    || typeof window.wuu?.capturePresentationFrame === "function");
}

function regionClip(rectangles: readonly RegionBounds[], width: number, height: number): string {
  return rectangles.length ? `path("${rectangles.map(rect => {
    const left = Math.max(0, rect.left), top = Math.max(0, rect.top);
    const right = Math.min(width, rect.left + rect.width), bottom = Math.min(height, rect.top + rect.height);
    return `M ${left} ${top} H ${right} V ${bottom} H ${left} Z`;
  }).join(" ")}")` : "inset(100%)";
}

function intersectRegions(source: readonly RegionBounds[], target: readonly RegionBounds[]): RegionBounds[] {
  return source.flatMap(from => target.map(to => {
    const left = Math.max(from.left, to.left), top = Math.max(from.top, to.top);
    return { left, top, width: Math.max(0, Math.min(from.left + from.width, to.left + to.width) - left),
      height: Math.max(0, Math.min(from.top + from.height, to.top + to.height) - top) };
  })).filter(rect => rect.width > 0 && rect.height > 0);
}

/** Only old compositor pixels are retained. The synchronous update never pauses
 * rendering during navigation, and excluding the new root keeps live controls
 * hit-testable. No old DOM, callbacks, or business handles survive retirement. */
function captureCompositor(scope: HTMLElement, regions: readonly ConversationPresentationRegion[], changed: (held: boolean) => void, captured: () => void): Frame | undefined {
  const pixelWidth = Math.ceil(window.innerWidth * window.devicePixelRatio);
  const pixelHeight = Math.ceil(window.innerHeight * window.devicePixelRatio);
  if (!Number.isSafeInteger(pixelWidth) || !Number.isSafeInteger(pixelHeight) || pixelWidth < 1 || pixelHeight < 1
    || pixelWidth > MAX_PRESENTATION_FRAME_EDGE || pixelHeight > MAX_PRESENTATION_FRAME_EDGE
    || pixelWidth * pixelHeight > MAX_PRESENTATION_FRAME_PIXELS) return;
  const root = document.documentElement, bounds = measureRegions(regions);
  const width = window.innerWidth, height = window.innerHeight;
  const originalName = root.style.getPropertyValue("view-transition-name");
  const originalPriority = root.style.getPropertyPriority("view-transition-name");
  const originalClip = root.style.getPropertyValue("--wuu-presentation-clip");
  const originalClipPriority = root.style.getPropertyPriority("--wuu-presentation-clip");
  const style = document.createElement("style");
  style.textContent = `
    :root[data-wuu-compositor-frame]::view-transition { pointer-events: none; }
    :root[data-wuu-compositor-frame]::view-transition-group(wuu-presentation) { animation: none; }
    :root[data-wuu-compositor-frame]::view-transition-old(wuu-presentation) {
      animation: wuu-presentation-hold 4s linear both paused;
      opacity: 1; mix-blend-mode: normal; pointer-events: none;
      clip-path: var(--wuu-presentation-clip);
    }
    @keyframes wuu-presentation-hold { from { opacity: 1; } to { opacity: 1; } }
  `;
  document.head.append(style);
  root.setAttribute("data-wuu-compositor-frame", "region");
  root.style.setProperty("view-transition-name", "wuu-presentation");
  root.style.setProperty("--wuu-presentation-clip", regionClip(bounds, width, height));
  let released = false, transition: ViewTransition | undefined, timer: number | undefined;
  let onCaptured: (() => void) | undefined = captured;
  const release = (): void => {
    if (released) return;
    released = true; onCaptured = undefined; window.clearTimeout(captureTimer); window.clearTimeout(timer);
    window.removeEventListener("resize", fallback); document.removeEventListener("visibilitychange", visibilityChanged);
    transition?.skipTransition();
    root.style.setProperty("view-transition-name", originalName, originalPriority);
    root.style.setProperty("--wuu-presentation-clip", originalClip, originalClipPriority); root.removeAttribute("data-wuu-compositor-frame"); style.remove();
    scope.inert = false; changed(false);
  };
  const fallback = () => { const dispatch = onCaptured; release(); dispatch?.(); };
  const visibilityChanged = () => { if (document.visibilityState === "hidden") fallback(); };
  const captureTimer = window.setTimeout(fallback, MAX_CAPTURE_MS);
  window.addEventListener("resize", fallback); document.addEventListener("visibilitychange", visibilityChanged);
  try {
    transition = document.startViewTransition(() => {
      if (released) return;
      // Capturing a new root would redirect hit testing away from its controls.
      root.style.setProperty("view-transition-name", "none");
      window.clearTimeout(captureTimer); timer = window.setTimeout(release, MAX_PRESENTATION_MS);
      const dispatch = onCaptured; onCaptured = undefined; changed(true); dispatch?.();
    });
    void transition.ready.catch(fallback);
  } catch { fallback(); }
  return {
    release,
    full: () => { if (!released) { root.setAttribute("data-wuu-compositor-frame", "full"); root.style.setProperty("--wuu-presentation-clip", "none"); scope.inert = true; } },
    region: next => {
      if (released) return;
      root.setAttribute("data-wuu-compositor-frame", "region");
      root.style.setProperty("--wuu-presentation-clip", regionClip(intersectRegions(bounds, measureRegions(next)), width, height));
      scope.inert = false;
    },
  };
}

/** An inert host-owned bitmap fallback; no old DOM or business handles survive. */
function captureRaster(scope: HTMLElement, regions: readonly ConversationPresentationRegion[], changed: (held: boolean) => void, captured: () => void): Frame | undefined {
  if (typeof window.wuu?.capturePresentationFrame !== "function") return;
  const bounds = measureRegions(regions);
  const width = window.innerWidth, height = window.innerHeight;
  // Disjoint split message bodies must not include either input dock. An empty
  // region captures no visible overlay until a verified provider retirement.
  const clipPath = regionClip(bounds, width, height);
  const overlay = document.createElement("img");
  overlay.alt = ""; overlay.setAttribute("aria-hidden", "true"); overlay.inert = true;
  overlay.setAttribute("data-wuu-presentation-frame", "region");
  Object.assign(overlay.style, { position: "fixed", inset: "0", width: "100vw", height: "100vh",
    maxWidth: "none", maxHeight: "none", pointerEvents: "none", userSelect: "none", zIndex: "2147483646",
    clipPath });
  let released = false, url: string | undefined, timer: number | undefined;
  let onCaptured: (() => void) | undefined = captured;
  const release = (): void => {
    if (released) return;
    released = true; onCaptured = undefined; window.clearTimeout(captureTimer); window.clearTimeout(timer);
    window.removeEventListener("resize", resized); document.removeEventListener("visibilitychange", visibilityChanged);
    overlay.remove(); overlay.removeAttribute("src"); if (url) URL.revokeObjectURL(url);
    scope.inert = false; changed(false);
  };
  const fallback = () => { const dispatch = onCaptured; release(); dispatch?.(); };
  const resized = () => fallback();
  const visibilityChanged = () => { if (document.visibilityState === "hidden") fallback(); };
  const captureTimer = window.setTimeout(fallback, MAX_CAPTURE_MS);
  window.addEventListener("resize", resized); document.addEventListener("visibilitychange", visibilityChanged);
  void Promise.resolve().then(() => window.wuu.capturePresentationFrame!()).then(async frame => {
    if (released) return;
    if (!frame || !frame.data.byteLength || frame.data.byteLength > 16 * 1024 * 1024 || !Number.isFinite(frame.scaleFactor)
      || frame.scaleFactor <= 0 || frame.width <= 0 || frame.height <= 0) { fallback(); return; }
    // BrowserWindow capture dimensions include device scale. Never stretch a frame
    // from a resized window or another geometry into the current workbench.
    if (Math.abs(frame.width / frame.scaleFactor - width) > 1 || Math.abs(frame.height / frame.scaleFactor - height) > 1) { fallback(); return; }
    const bytes = new Uint8Array(frame.data);
    url = URL.createObjectURL(new Blob([bytes], { type: "image/png" })); overlay.src = url;
    await overlay.decode();
    if (released) return;
    window.clearTimeout(captureTimer); timer = window.setTimeout(release, MAX_PRESENTATION_MS);
    document.body.append(overlay); const dispatch = onCaptured; onCaptured = undefined; changed(true); dispatch?.();
  }).catch(fallback);
  return {
    release,
    full: () => { if (!released) { overlay.setAttribute("data-wuu-presentation-frame", "full"); overlay.style.clipPath = "none"; scope.inert = true; } },
    region: nextRegions => {
      if (released) return;
      // A successor can have a taller input or leave split mode. Never cover
      // its live controls with pixels from the source message rectangle.
      overlay.setAttribute("data-wuu-presentation-frame", "region");
      overlay.style.clipPath = regionClip(intersectRegions(bounds, measureRegions(nextRegions)), width, height); scope.inert = false;
    },
  };
}

type HostState = { binding: number; retiring: boolean; failed: boolean };
type Observation = { owner: object; pending?: Pending; enabled: boolean; pane: HTMLElement | null; getRegions?: () => readonly ConversationPresentationRegion[] };
export class ConversationPresentationCancellation extends Error {
  readonly code = "WUU_NAVIGATION_PRESENTATION_SUPERSEDED";
  constructor() { super("Navigation was superseded before it started"); this.name = "AbortError"; }
}
type Bridge = { update(observation: Observation): void; detach(owner: object): void; present<T>(action: () => Promise<T>): Promise<T> };
const PresentationBridge = createContext<Bridge | undefined>(undefined);

/** Stable host boundary survives only a verified, identity-preserving provider handoff. */
export function ConversationPresentationBoundary({ binding, retiring, failed, getRetirement, children }: HostState & {
  getRetirement?: () => { readonly binding: number } | undefined; children: ReactNode;
}): ReactNode {
  const scope = useRef<HTMLDivElement>(null), host = useRef<HostState>({ binding, retiring, failed });
  host.current = { binding, retiring, failed };
  const retirementReader = useRef(getRetirement); retirementReader.current = getRetirement;
  const retirement = getRetirement?.();
  const [held, setHeld] = useState(false);
  const source = useRef<Observation | undefined>(undefined);
  const record = useRef<{ owner?: object; intentId?: string; binding: number; handoff: boolean; frame?: Frame; started: boolean; cancel(): void } | undefined>(undefined);
  const markRetirement = useCallback(() => {
    const current = record.current;
    if (!current?.intentId || host.current.failed) return false;
    const verified = retirementReader.current
      ? retirementReader.current()?.binding === current.binding : host.current.retiring;
    if (!verified) return false;
    current.handoff = true; current.frame?.full(); return true;
  }, []);
  const bridge = useMemo<Bridge>(() => {
    const clear = () => { const current = record.current; record.current = undefined; current?.cancel(); current?.frame?.release(); setHeld(false); };
    return {
      present<T>(action: () => Promise<T>): Promise<T> {
        const observation = source.current;
        if (!scope.current || !observation?.enabled || !observation.pane || !supportsCapture()) return action();
        clear();
        return new Promise<T>((resolve, reject) => {
          let cancelled = false;
          const current: NonNullable<typeof record.current> = {
            owner: observation.owner, binding: host.current.binding, handoff: false, started: false,
            cancel() { if (!current.started && !cancelled) { cancelled = true; reject(new ConversationPresentationCancellation()); } },
          };
          record.current = current;
          const execute = () => {
            if (cancelled || current.started) return;
            current.started = true;
            let operation!: Promise<T>;
            try { flushSync(() => { operation = action(); }); }
            catch (error) { if (record.current === current) clear(); reject(error); return; }
            void operation.then(value => {
              if (record.current === current && !current.intentId) clear();
              resolve(value);
            }, error => {
              // The source promise retires with its writer, but a verified
              // successor may still own the same presentation intent.
              if (record.current === current) {
                if (isPluginLifecycleCancellation(error)) markRetirement();
                const pendingIntent = source.current?.owner === current.owner
                  && source.current?.pending?.intentId === current.intentId;
                // The selected provider may fence its action while a committed
                // successor still prepares. This explicit marker retains only
                // message pixels; full retention still requires verified retirement.
                if (!(current.intentId && (current.handoff || pendingIntent) && isPluginLifecycleCancellation(error))) clear();
              }
              reject(error);
            });
          };
          const changed = (value: boolean) => { if (record.current === current) setHeld(value); };
          current.frame = typeof document.startViewTransition === "function"
            ? captureCompositor(scope.current!, observation.getRegions?.() ?? [{ element: observation.pane! }], changed, execute)
            : captureRaster(scope.current!, observation.getRegions?.() ?? [{ element: observation.pane! }], changed, execute);
          if (!current.frame) execute();
        });
      },
      update(observation) {
        const { owner, pending, enabled } = observation;
        source.current = observation;
        const current = record.current;
        if (!current) return;
        const retiring = markRetirement();
        if (!enabled || !pending?.intentId) {
          // Consumer abort notifications precede the React host commit. Only an
          // explicit replacement of this binding can bridge that ordering gap.
          if (retiring) return;
          if (current.owner === owner && (current.started || !enabled) || current.owner === undefined) clear();
          return;
        }
        if (!current.intentId && current.owner === owner) { current.intentId = pending.intentId; return; }
        if (current.intentId === pending.intentId) {
          if (current.owner === owner || current.handoff && current.binding !== host.current.binding) {
            const successor = current.owner !== owner;
            current.owner = owner; if (successor) current.binding = host.current.binding;
            if (!host.current.retiring && (!retiring || successor)) current.frame?.region(observation.getRegions?.() ?? (observation.pane ? [{ element: observation.pane }] : []));
          }
          else clear();
          return;
        }
        clear();
      },
      detach(owner) {
        if (source.current?.owner === owner) source.current = undefined;
        const current = record.current;
        if (current?.owner !== owner) return;
        if (markRetirement()) {
          current.owner = undefined;
        } else clear();
      },
    };
  }, [markRetirement]);
  useLayoutEffect(() => {
    const current = record.current;
    if (failed || getRetirement && !retirement && current?.handoff && current.owner === undefined) {
      record.current = undefined; current?.cancel(); current?.frame?.release(); setHeld(false);
    }
    else if (retiring && current) markRetirement();
  }, [retiring, failed, getRetirement, retirement, markRetirement]);
  useLayoutEffect(() => () => { const current = record.current; record.current = undefined; current?.cancel(); current?.frame?.release(); }, []);
  return <PresentationBridge.Provider value={bridge}><div ref={scope} style={{ position: "relative", width: "100%", height: "100%" }}>
    {retiring && held ? null : children}
  </div></PresentationBridge.Provider>;
}

/** Observe intent readiness; captures start only at the explicit navigation action boundary. */
export function useConversationPresentationTransition({ scopeRef, pending, enabled, getRegions }: {
  scopeRef: RefObject<HTMLElement | null>; pending?: Pending; enabled: boolean; getRegions?: () => readonly ConversationPresentationRegion[];
}): void {
  const bridge = useContext(PresentationBridge), owner = useRef({});
  const requestId = pending?.requestId;
  useLayoutEffect(() => {
    bridge?.update({ owner: owner.current, pending, enabled, pane: scopeRef.current, getRegions });
    // Content readiness starts restoration, not reveal. Retain message pixels
    // until pending clears, while successor chrome is immediately live.
  }, [bridge, enabled, requestId, pending?.intentId, scopeRef, getRegions]);
  useLayoutEffect(() => () => bridge?.detach(owner.current), [bridge]);
}

/** User navigation starts after a bounded own-window capture; async business work is never awaited by it. */
export function usePresentedNavigation(): <T>(action: () => Promise<T>) => Promise<T> {
  const bridge = useContext(PresentationBridge);
  return useCallback(<T,>(action: () => Promise<T>) => bridge ? bridge.present(action) : action(), [bridge]);
}

export function reportPresentedNavigationError(error: unknown): void {
  if (!(error instanceof ConversationPresentationCancellation)) showApplicationActionError(error);
}
