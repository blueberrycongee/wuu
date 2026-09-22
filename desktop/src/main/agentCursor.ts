// Synthetic pointer drawn inside the page the user is watching. The page view
// is composited above the workspace, so a panel overlay cannot sit on top of
// it. The pointer does not receive clicks. Short travels ease in a straight
// line with a small lean; longer travels arc off the straight line and the
// tip leads. A press plays as the pointer arrives, and the pointing action
// waits for that arrival so the click lands with the pointer.

export const CURSOR_SCOOT_DISTANCE = 196;
export const CURSOR_ARRIVE_CAP_MS = 700;

const TIP_X = 1.2;
const TIP_Y = 1.1;

export function agentCursorCommandScript(x: number, y: number, press: boolean): string {
  const px = Math.round(x);
  const py = Math.round(y);
  return `(() => {
    const runtime = window.__wuuAgentCursor || (window.__wuuAgentCursor = (${cursorRuntimeSource})());
    return runtime.moveTo(${px}, ${py}, ${press ? "true" : "false"});
  })()`;
}

export function clearAgentCursorScript(): string {
  return `(() => {
    const runtime = window.__wuuAgentCursor;
    if (runtime) runtime.hide();
    delete window.__wuuAgentCursor;
  })()`;
}

// The runtime is plain script text so it can run inside the page without
// calling eval. It is also what the motion tests execute.
export const cursorRuntimeSource = `function () {
  var SCOOT = ${CURSOR_SCOOT_DISTANCE};
  var CAP = ${CURSOR_ARRIVE_CAP_MS};
  var TIP_X = ${TIP_X};
  var TIP_Y = ${TIP_Y};
  var host = null;
  var point = null;
  var travel = null;
  var pressing = 0;
  var pressTimer = 0;
  var arrivalTimer = 0;
  var restingAt = 0;
  var restPose = null;
  var raf = 0;
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function now() {
    return (typeof performance !== "undefined" && performance.now) ? performance.now() : Date.now();
  }

  function ensureHost() {
    if (host && host.isConnected) return host;
    host = document.createElement("div");
    host.id = "__wuu_agent_cursor";
    host.setAttribute("aria-hidden", "true");
    host.style.cssText = "position:fixed;left:0;top:0;width:22px;height:22px;pointer-events:none;z-index:2147483647;transform-origin:" + TIP_X + "px " + TIP_Y + "px;filter:drop-shadow(0 1px 1px rgba(15,23,42,.38)) drop-shadow(0 0 7px rgba(37,99,235,.62));";
    host.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 22 22"><path d="M1.2 1.1 L1.4 15.6 L6.2 11.2 L9.4 18.4 L12.1 17.1 L8.8 10.2 L15.2 9.6 Z" fill="#f8fbff" stroke="#2563eb" stroke-width="1.15" stroke-linejoin="round"/></svg>';
    (document.documentElement || document.body).appendChild(host);
    return host;
  }

  function paint(pose) {
    var node = ensureHost();
    var pressScale = pose.press > 0 ? (1 - pose.press * 0.16) : 1;
    node.style.opacity = String(pose.opacity);
    node.style.transform = "translate(" + (pose.x - TIP_X) + "px, " + (pose.y - TIP_Y) + "px) rotate(" + pose.rotation + "deg) scale(" + (pose.stretch * pressScale) + ", " + (pose.squash * pressScale) + ")";
  }

  function anchor() {
    var width = window.innerWidth || 800;
    var height = window.innerHeight || 600;
    return { x: Math.round(width * 0.58), y: Math.round(height * 0.55) };
  }

  function hypot(dx, dy) {
    return Math.sqrt(dx * dx + dy * dy);
  }

  function clamp(value, min, max) {
    return Math.max(min, Math.min(max, value));
  }

  function lerp(a, b, t) {
    return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
  }

  function bow(start, end) {
    var dx = end.x - start.x;
    var dy = end.y - start.y;
    var len = hypot(dx, dy) || 1;
    var mag = Math.min(72, len * 0.18);
    return {
      x: (start.x + end.x) / 2 + (-dy / len) * mag,
      y: (start.y + end.y) / 2 + (dx / len) * mag
    };
  }

  function quad(a, control, b, t) {
    var u = 1 - t;
    return {
      x: u * u * a.x + 2 * u * t * control.x + t * t * b.x,
      y: u * u * a.y + 2 * u * t * control.y + t * t * b.y
    };
  }

  function heading(from, to) {
    return Math.atan2(to.y - from.y, to.x - from.x) * (180 / Math.PI);
  }

  function poseAt(move, t, opacity) {
    var along = clamp(t, 0, 1);
    var settle = along < 0.8 ? 0 : (along - 0.8) / 0.2;
    if (move.mode === "snap") {
      return { x: move.end.x, y: move.end.y, rotation: 0, stretch: 1, squash: 1, opacity: opacity, press: pressing };
    }
    if (move.mode === "scoot") {
      var ease = 1 - Math.pow(1 - along, 3);
      var at = lerp(move.start, move.end, ease);
      var lean = Math.sin(along * Math.PI) * clamp(move.lean, -22, 22);
      return {
        x: at.x,
        y: at.y,
        rotation: lean * (1 - settle),
        stretch: 1,
        squash: 1 - Math.sin(along * Math.PI) * 0.14,
        opacity: opacity,
        press: pressing
      };
    }
    var atArc = quad(move.start, move.control, move.end, along);
    var ahead = quad(move.start, move.control, move.end, Math.min(1, along + 0.04));
    var lead = heading(atArc, ahead) + 135;
    lead = ((lead + 180) % 360 + 360) % 360 - 180;
    return {
      x: atArc.x,
      y: atArc.y,
      rotation: lead * (1 - settle),
      stretch: 1 + Math.sin(along * Math.PI) * 0.22,
      squash: 1,
      opacity: opacity,
      press: pressing
    };
  }

  function plan(start, end) {
    var dx = end.x - start.x;
    var dy = end.y - start.y;
    var distance = hypot(dx, dy);
    if (reduced || distance < 0.5) {
      return { mode: "snap", start: start, end: end, duration: 0, lean: 0 };
    }
    if (distance <= SCOOT) {
      var lean = clamp(heading(start, end) * 0.08, -22, 22);
      return {
        mode: "scoot",
        start: start,
        end: end,
        duration: clamp(0.16 + distance / 1400, 0.16, 0.42),
        lean: lean
      };
    }
    return {
      mode: "arc",
      start: start,
      end: end,
      control: bow(start, end),
      duration: clamp(0.28 + distance / 1800, 0.28, 0.62),
      lean: 0
    };
  }

  function finish(resolve) {
    if (raf) window.cancelAnimationFrame(raf);
    raf = 0;
    var move = travel.move;
    var shouldPress = travel.press;
    window.clearTimeout(arrivalTimer);
    arrivalTimer = 0;
    travel = null;
    point = { x: move.end.x, y: move.end.y };
    restPose = poseAt(move, 1, 1);
    restingAt = now();
    if (shouldPress) {
      pressing = 1;
      restPose.press = pressing;
      window.clearTimeout(pressTimer);
      pressTimer = window.setTimeout(function () {
        pressing = 0;
        if (!travel && host && restPose) {
          restPose.press = 0;
          paint(restPose);
        }
      }, 90);
    }
    paint(restPose);
    resolve();
    if (!reduced) raf = window.requestAnimationFrame(frame);
  }

  function frame() {
    raf = 0;
    if (!travel) {
      if (!restPose || !host) return;
      var elapsed = now() - restingAt;
      var progress = Math.min(1, elapsed / 1100);
      var tilt = Math.sin(progress * Math.PI * 4) * Math.sin(progress * Math.PI) * 9;
      paint(Object.assign({}, restPose, { rotation: tilt, press: pressing }));
      if (progress < 1) raf = window.requestAnimationFrame(frame);
      return;
    }
    var elapsed = now() - travel.started;
    var durationMs = travel.move.duration * 1000;
    var t = durationMs <= 0 ? 1 : Math.min(1, elapsed / durationMs);
    var opacity = Math.min(1, travel.baseOpacity + elapsed / 140);
    var pose = poseAt(travel.move, t, opacity);
    paint(pose);
    // Retargeting starts from the painted point, including mid-flight moves.
    point = { x: pose.x, y: pose.y };
    if (t >= 1 || elapsed >= CAP) {
      finish(travel.resolve);
      return;
    }
    raf = window.requestAnimationFrame(frame);
  }

  function cancelTravel() {
    if (raf) window.cancelAnimationFrame(raf);
    raf = 0;
    window.clearTimeout(arrivalTimer);
    arrivalTimer = 0;
    if (travel) {
      var resolve = travel.resolve;
      travel = null;
      resolve();
    }
  }

  return {
    moveTo: function (x, y, press) {
      cancelTravel();
      window.clearTimeout(pressTimer);
      pressing = 0;
      restPose = null;
      var end = { x: x, y: y };
      var start = point || anchor();
      var move = plan(start, end);
      return new Promise(function (resolve) {
        travel = {
          move: move,
          press: press === true,
          started: now(),
          baseOpacity: point ? 1 : 0,
          resolve: resolve
        };
        if (move.mode === "snap") {
          point = end;
          paint(poseAt(move, 1, 1));
          finish(resolve);
          return;
        }
        if (raf) window.cancelAnimationFrame(raf);
        raf = window.requestAnimationFrame(frame);
        arrivalTimer = window.setTimeout(function () {
          if (travel && travel.resolve === resolve) {
            point = end;
            paint(poseAt(travel.move, 1, 1));
            finish(resolve);
          }
        }, CAP);
      });
    },
    hide: function () {
      cancelTravel();
      window.clearTimeout(pressTimer);
      restPose = null;
      point = null;
      pressing = 0;
      if (host && host.parentNode) host.parentNode.removeChild(host);
      host = null;
    },
    plan: plan,
    poseAt: poseAt
  };
}`;
