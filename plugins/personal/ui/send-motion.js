// Motionbook's dot-send study: https://github.com/blueberrycongee/motionbook/tree/main/examples/chatgpt-dot-send
const travelMS = 360;
const travelCurve = "cubic-bezier(.33,1,.68,1)";
const shapeMS = 190;
const shapeCurve = "cubic-bezier(.73,.42,.54,1)";
const reduced = () => document.documentElement.dataset.appearanceMotion === "reduce"
  || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

export const sendMotionStyles = `
  .mosaic [data-personal-send-source] { opacity:0; }
  .mosaic .mosaic-send-overlay { position:absolute!important;margin:0!important;pointer-events:none;z-index:12; }
  .mosaic .mosaic-send-overlay > .bubble-message-body { width:100%;max-width:none;transform:none;transition:none;margin:0; }
  .mosaic .mosaic-send-overlay .user-message[data-send-shaping] { position:relative;isolation:isolate;overflow:visible;background:transparent;border-color:transparent;box-shadow:none; }
  .mosaic .mosaic-send-overlay .user-message[data-send-shaping]::before { content:"";position:absolute;inset:var(--send-inset);z-index:-1;pointer-events:none;border-radius:inherit;background:var(--send-background);border:var(--send-border);box-shadow:var(--send-shadow); }
`;

// The plugin owns submission identity. History, retries and other views never
// infer a new send from a newly mounted message. The overlay crosses the scroll
// clip without changing the actual message's layout or the reading position.
export function useSendMotion(React, rootRef) {
  const flights = React.useRef(new Map());
  const className = requestID => requestID ? `mosaic-send-${requestID}` : undefined;
  const reconcile = flight => {
    const row = flight.view.querySelector(".plugin-conversation-flow")?.getElementsByClassName(className(flight.requestID))[0];
    const body = row?.querySelector(".bubble-message-body");
    if (body !== flight.source) {
      flight.source?.removeAttribute("data-personal-send-source");
      flight.source = body;
      body?.setAttribute("data-personal-send-source", "");
    }
    return row;
  };
  const start = flight => {
    const row = reconcile(flight), body = flight.source;
    if (!body || flight.stop) return;
    if (document.hidden || reduced() || flight.view.closest('[hidden],[inert],[aria-hidden="true"]')) {
      body.removeAttribute("data-personal-send-source"); flights.current.delete(flight.requestID); return;
    }
    const destination = body.getBoundingClientRect(), viewRect = flight.view.getBoundingClientRect();
    const viewport = flight.view.querySelector(".plugin-conversation-scroll").getBoundingClientRect();
    if (!destination.width || !body.animate || destination.bottom <= viewport.top || destination.top >= viewport.bottom) {
      body.removeAttribute("data-personal-send-source"); flights.current.delete(flight.requestID); return;
    }
    // offsetWidth rounds to whole pixels. That error can narrow a cloned bubble
    // just enough to wrap its last word, then unwrap when the real row returns.
    const zoom = flight.view.currentCSSZoom;
    const width = destination.width / zoom, height = destination.height / zoom;
    const x = (flight.origin.right - destination.right) / zoom;
    const y = (flight.origin.top - destination.top) / zoom;
    const overlay = row.cloneNode(false), copy = body.cloneNode(true);
    overlay.classList.add("mosaic-send-overlay");
    overlay.setAttribute("aria-hidden", "true"); overlay.inert = true;
    copy.removeAttribute("data-personal-send-source");
    overlay.append(copy);
    for (const node of overlay.querySelectorAll("[id]")) node.removeAttribute("id");
    overlay.style.width = `${width}px`;
    flight.view.append(overlay);
    let previousPosition;
    const place = () => {
      const target = flight.source?.getBoundingClientRect(), parent = flight.view.getBoundingClientRect();
      if (!target) return;
      const position = { left: (target.left - parent.left) / zoom, top: (target.top - parent.top) / zoom };
      overlay.style.left = `${position.left}px`;
      overlay.style.top = `${position.top}px`;
      const previous = previousPosition;
      previousPosition = position;
      return previous && { x: previous.left - position.left, y: previous.top - position.top };
    };
    place();
    const tracks = [];
    const play = (target, frames, options) => {
      const track = target.animate(frames, options);
      if (document.timeline.currentTime !== null) track.startTime = document.timeline.currentTime;
      tracks.push(track); return track;
    };
    let travel = play(overlay, [
      { translate: `${x}px ${y}px` }, { translate: "0px 0px" },
    ], { duration: travelMS, easing: travelCurve });
    const size = Math.min(1, Math.max(width, height) / 360);
    overlay.style.transformOrigin = "right top";
    const compression = play(overlay, [
      { offset: 0, scale: "1 1", easing: "cubic-bezier(.65,0,.35,1)" },
      { offset: 1 / 3, scale: `${1 - Math.min(.5, 18 * size / width)} ${1 - Math.min(.5, 4 * size / height)}`, easing: "cubic-bezier(.19,0,.48,1)" },
      { offset: .9, scale: "1 1" }, { offset: 1, scale: "1 1" },
    ], { duration: travelMS });
    const bubble = copy.querySelector(".user-message");
    const extra = bubble ? Math.max(0, (flight.origin.width - bubble.getBoundingClientRect().width) / zoom) : 0;
    if (bubble && extra > 0) {
      const style = getComputedStyle(bubble);
      for (const [name, value] of Object.entries({ background: style.background, border: style.border, shadow: style.boxShadow,
        inset: `-${style.borderTopWidth} -${style.borderRightWidth} -${style.borderBottomWidth} -${style.borderLeftWidth}` })) bubble.style.setProperty(`--send-${name}`, value);
      const borderLeft = style.borderLeftWidth;
      bubble.setAttribute("data-send-shaping", "");
      const shape = play(bubble, [{ left: `${-extra - parseFloat(borderLeft)}px` }, { left: `-${borderLeft}` }], { duration: shapeMS, easing: shapeCurve, pseudoElement: "::before" });
      // Preserve the final line wrapping while the surface narrows around it.
      for (const child of bubble.children) play(child, [{ translate: `${-extra}px 0` }, { translate: "0px 0px" }], { duration: shapeMS, easing: shapeCurve });
      shape.addEventListener("finish", () => bubble.removeAttribute("data-send-shaping"), { once: true });
    }
    let frame;
    const stop = () => {
      cancelAnimationFrame(frame);
      for (const track of tracks) track.cancel();
      overlay.remove(); flight.source?.removeAttribute("data-personal-send-source");
      for (const event of ["wheel", "pointerdown", "keydown"]) flight.view.removeEventListener(event, stop, true);
      flights.current.delete(flight.requestID);
    };
    flight.stop = stop;
    const follow = () => {
      if (document.hidden || reduced() || !flight.view.isConnected || flight.view.closest('[hidden],[inert],[aria-hidden="true"]') || Math.abs(flight.view.offsetWidth - viewRect.width / zoom) > 1) { stop(); return; }
      reconcile(flight);
      const delta = place();
      if (delta && Math.hypot(delta.x, delta.y) > .5) {
        // A reply or composer resize may move the destination during the send.
        // Rebase from the painted position, keeping the original landing clock.
        const current = getComputedStyle(overlay).translate.split(" ").map(parseFloat);
        const remaining = Math.max(1, travelMS - Number(compression.currentTime));
        const paused = travel.playState === "paused";
        travel.cancel();
        travel = play(overlay, [{ translate: `${(current[0] || 0) + delta.x}px ${(current[1] || 0) + delta.y}px` }, { translate: "0px 0px" }], { duration: remaining, easing: travelCurve });
        if (paused) travel.pause();
        travel.addEventListener("finish", stop, { once: true });
      }
      frame = requestAnimationFrame(follow);
    };
    for (const event of ["wheel", "pointerdown", "keydown"]) flight.view.addEventListener(event, stop, true);
    travel.addEventListener("finish", stop, { once: true });
    frame = requestAnimationFrame(follow);
  };
  React.useLayoutEffect(() => {
    for (const flight of flights.current.values()) {
      reconcile(flight);
      if (!flight.frame && !flight.stop) {
        // Let the conversation's resize/follow pass place the new row first.
        flight.frame = requestAnimationFrame(() => {
          flight.frame = requestAnimationFrame(() => { flight.frame = undefined; start(flight); });
        });
      }
    }
  });
  React.useEffect(() => () => {
    for (const flight of flights.current.values()) { cancelAnimationFrame(flight.frame); flight.stop?.(); flight.source?.removeAttribute("data-personal-send-source"); }
    flights.current.clear();
  }, []);
  return {
    className,
    begin(requestID, conversationID) {
      if (document.hidden || reduced()) return;
      const view = [...(rootRef.current?.querySelectorAll(".plugin-conversation") || [])].find(node => node.dataset.conversationId === conversationID);
      const composer = view?.querySelector(".composer-frame");
      if (!composer || view.closest('[hidden],[inert],[aria-hidden="true"]')) return;
      for (const flight of flights.current.values()) if (flight.view === view) {
        cancelAnimationFrame(flight.frame); flight.stop?.(); flight.source?.removeAttribute("data-personal-send-source"); flights.current.delete(flight.requestID);
      }
      flights.current.set(requestID, { requestID, view, origin: composer.getBoundingClientRect() });
    },
  };
}
