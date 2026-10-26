import { createContext, useContext, useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { isPluginLifecycleCancellation } from "../shared/PluginLifecycle";
import { flushSync } from "react-dom";
import { showApplicationActionError } from "./ApplicationActionError";

type Pending = { readonly requestId?: number; readonly intentId?: string; readonly contentReady?: boolean };
type Frame = { release(): void; full(): void };
const MAX_PRESENTATION_MS = 4000;
const MAX_CAPTURE_MS = 120;

function supportsCapture(): boolean {
  return document.visibilityState !== "hidden" && typeof window.wuu?.capturePresentationFrame === "function";
}

/** An inert host-owned bitmap only; no old DOM, callbacks, or business handles survive. */
function capture(scope: HTMLElement, pane: HTMLElement, changed: (held: boolean) => void, captured: () => void): Frame | undefined {
  if (!supportsCapture()) return;
  const region = pane.getBoundingClientRect();
  const width = window.innerWidth, height = window.innerHeight;
  const overlay = document.createElement("img");
  overlay.alt = ""; overlay.setAttribute("aria-hidden", "true"); overlay.inert = true;
  overlay.setAttribute("data-wuu-presentation-frame", "region");
  Object.assign(overlay.style, { position: "fixed", inset: "0", width: `${width}px`, height: `${height}px`,
    maxWidth: "none", maxHeight: "none", pointerEvents: "none", userSelect: "none", zIndex: "2147483646",
    clipPath: `inset(${Math.max(0, region.top)}px ${Math.max(0, width - region.right)}px ${Math.max(0, height - region.bottom)}px ${Math.max(0, region.left)}px)` });
  let released = false, url: string | undefined, timer: number | undefined;
  const release = (): void => {
    if (released) return;
    released = true; window.clearTimeout(captureTimer); window.clearTimeout(timer);
    window.removeEventListener("resize", resized); document.removeEventListener("visibilitychange", visibilityChanged);
    overlay.remove(); overlay.removeAttribute("src"); if (url) URL.revokeObjectURL(url);
    scope.inert = false; changed(false);
  };
  const fallback = () => { release(); captured(); };
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
    document.body.append(overlay); changed(true); captured();
  }).catch(fallback);
  return { release, full: () => { if (!released) { overlay.setAttribute("data-wuu-presentation-frame", "full"); overlay.style.clipPath = "none"; scope.inert = true; } } };
}

type HostState = { binding: number; retiring: boolean; failed: boolean };
type Observation = { owner: object; pending?: Pending; enabled: boolean; pane: HTMLElement | null };
export class ConversationPresentationCancellation extends Error {
  readonly code = "WUU_NAVIGATION_PRESENTATION_SUPERSEDED";
  constructor() { super("Navigation was superseded before it started"); this.name = "AbortError"; }
}
type Bridge = { update(observation: Observation): void; detach(owner: object): void; present<T>(action: () => Promise<T>): Promise<T> };
const PresentationBridge = createContext<Bridge | undefined>(undefined);

/** Stable host boundary survives only a verified, identity-preserving provider handoff. */
export function ConversationPresentationBoundary({ binding, retiring, failed, children }: HostState & { children: ReactNode }): ReactNode {
  const scope = useRef<HTMLDivElement>(null), host = useRef<HostState>({ binding, retiring, failed });
  host.current = { binding, retiring, failed };
  const [held, setHeld] = useState(false);
  const source = useRef<Observation | undefined>(undefined);
  const record = useRef<{ owner?: object; intentId?: string; binding: number; handoff: boolean; frame?: Frame; started: boolean; cancel(): void } | undefined>(undefined);
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
              if (record.current === current && !(current.handoff && current.intentId && isPluginLifecycleCancellation(error))) clear();
              reject(error);
            });
          };
          current.frame = capture(scope.current!, observation.pane!, value => {
            if (record.current === current) setHeld(value);
          }, execute);
          if (!current.frame) execute();
        });
      },
      update(observation) {
        const { owner, pending, enabled } = observation;
        source.current = observation;
        const current = record.current;
        if (!current) return;
        if (!enabled || !pending?.intentId) {
          if (current.owner === owner && (current.started || !enabled) || current.owner === undefined) clear();
          return;
        }
        if (!current.intentId && current.owner === owner) { current.intentId = pending.intentId; return; }
        if (current.intentId === pending.intentId) {
          if (current.owner === owner || current.handoff && current.binding !== host.current.binding) current.owner = owner;
          else clear();
          return;
        }
        clear();
      },
      detach(owner) {
        if (source.current?.owner === owner) source.current = undefined;
        const current = record.current;
        if (current?.owner !== owner) return;
        if (host.current.retiring && !host.current.failed && current.intentId) {
          current.owner = undefined; current.handoff = true; current.frame?.full();
        } else clear();
      },
    };
  }, []);
  useLayoutEffect(() => {
    const current = record.current;
    if (failed) { record.current = undefined; current?.cancel(); current?.frame?.release(); setHeld(false); }
    else if (retiring && current) { current.handoff = true; current.frame?.full(); }
  }, [retiring, failed]);
  useLayoutEffect(() => () => { const current = record.current; record.current = undefined; current?.cancel(); current?.frame?.release(); }, []);
  return <PresentationBridge.Provider value={bridge}><div ref={scope} style={{ position: "relative", width: "100%", height: "100%" }}>
    {retiring && held ? null : children}
  </div></PresentationBridge.Provider>;
}

/** Observe intent readiness; captures start only at the explicit navigation action boundary. */
export function useConversationPresentationTransition({ scopeRef, pending, enabled }: {
  scopeRef: RefObject<HTMLElement | null>; pending?: Pending; enabled: boolean;
}): void {
  const bridge = useContext(PresentationBridge), owner = useRef({});
  const requestId = pending?.requestId;
  useLayoutEffect(() => {
    bridge?.update({ owner: owner.current, pending, enabled, pane: scopeRef.current });
    // contentReady starts restoration, not completion. It must not release the
    // same request's frame before reveal removes pending.
  }, [bridge, enabled, requestId, pending?.intentId, scopeRef]);
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
