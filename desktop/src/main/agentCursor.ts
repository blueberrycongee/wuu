import { iconSVG } from "../shared/iconArtwork";

// Shared pointer rendering for the workspace page and the preview overlay.
// Motion uses page coordinates; viewport mapping keeps the pointer legible
// at a constant display size when the page is scaled down.
//
// The trajectory, cursor artwork and effects follow the signature_arc style of
// Cua Cursor Motion (MIT, Copyright (c) 2025 Cua AI, Inc.;
// https://github.com/trycua/cua/blob/main/libs/typescript/cursor-motion/LICENSE).
// Timing differs: browser input waits for arrival, so every move must arrive
// within CURSOR_ARRIVE_CAP_MS.

export type AgentCursorFeedback = {
  kind: "click" | "scroll" | "type";
  x: number;
  y: number;
  direction?: string;
};

export const CURSOR_ARRIVE_CAP_MS = 700;

// The cursor body on its 128-unit canvas, hotspot (the tip) at (55, 30).
const CURSOR_PATH = "M55 30 C48 28 42 33 43 41 C43 41 64 98 64 98 C67 106 73 106 77 99 C77 99 86 79 86 79 C88 75 91 72 95 70 C95 70 108 63 108 63 C115 59 114 53 107 50 C107 50 55 30 55 30 Z";
const CURSOR_CANVAS = 128;
const CURSOR_DISPLAY = 42;
const CURSOR_SCALE = CURSOR_DISPLAY / CURSOR_CANVAS;
const TIP_X = 55 * CURSOR_SCALE;
const TIP_Y = 30 * CURSOR_SCALE;
const CURSOR_FILL = "#5ec0e8";
// The speed glow uses the fill lifted 45% toward white. Click and input
// feedback keep the fill itself so the ring stays visible on light pages.
const EFFECT_RGB = "166,220,242";
const GLOW_LAYERS: Array<[number, number]> = [
  [44, 0.02], [36, 0.024], [29, 0.03], [23, 0.038],
  [18, 0.048], [14, 0.06], [10, 0.075], [7, 0.095],
];

// Inline styles keep page stylesheets from restyling the artwork.
const AGENT_CURSOR_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="${CURSOR_DISPLAY}" height="${CURSOR_DISPLAY}" viewBox="0 0 ${CURSOR_CANVAS} ${CURSOR_CANVAS}" style="display:block;overflow:visible">${
  GLOW_LAYERS.map(([width, opacity]) => `<path d="${CURSOR_PATH}" style="fill:none;stroke:${CURSOR_FILL};stroke-opacity:${opacity};stroke-width:${width};stroke-linecap:round;stroke-linejoin:round"/>`).join("")
}<path d="${CURSOR_PATH}" style="fill:${CURSOR_FILL};stroke:#fff;stroke-width:5;stroke-linejoin:round"/></svg>`;

// The idle float, one 4 s loop sampled from the reference curve and offset so
// it starts from the rest pose.
const FLOAT_KEYFRAMES = Array.from({ length: 17 }, (_, index) => {
  const angle = (index / 16) * Math.PI * 2;
  const x = Math.sin(angle) * 5 * CURSOR_SCALE;
  const y = (6 * Math.cos(angle) - 6) * CURSOR_SCALE;
  const rotation = 2.5 * (Math.cos(angle) - 1);
  return { transform: `translate(${x.toFixed(3)}px, ${y.toFixed(3)}px) rotate(${rotation.toFixed(3)}deg)` };
});

export function agentCursorCommandScript(x: number, y: number): string {
  const px = Math.round(x);
  const py = Math.round(y);
  return `(() => {
    const runtime = window.__wuuAgentCursor || (window.__wuuAgentCursor = (${cursorRuntimeSource})());
    return runtime.moveTo(${px}, ${py});
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
  var CAP = ${CURSOR_ARRIVE_CAP_MS};
  var TIP_X = ${TIP_X};
  var TIP_Y = ${TIP_Y};
  var EFFECT = ${JSON.stringify(EFFECT_RGB)};
  var FILL = ${JSON.stringify(CURSOR_FILL)};
  var FLOAT = ${JSON.stringify(FLOAT_KEYFRAMES)};
  // Heading is the direction from the tip to the body; the arrow rests
  // pointing up and to the left.
  var REST = Math.PI / 4;
  var TIP_ANGLE = -0.75 * Math.PI;
  var BODY_OFFSET = 16;
  var DT = 1000 / 120;
  var GLOW_BOX = 44;
  var CLICK_SECS = 0.55;
  var host = null;
  var body = null;
  var glow = null;
  var effect = null;
  var effectAnimation = null;
  var floatAnimation = null;
  var viewport = { x: 0, y: 0, scale: 1 };
  var point = null;
  var heading = REST;
  var opacity = 1;
  var travel = null;
  var pending = null;
  var clickAt = -1;
  var raf = 0;
  var arrivalTimer = 0;
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function now() {
    return (typeof performance !== "undefined" && performance.now) ? performance.now() : Date.now();
  }

  function hypot(dx, dy) {
    return Math.sqrt(dx * dx + dy * dy);
  }

  function clamp(value, min, max) {
    return Math.max(min, Math.min(max, value));
  }

  function wrap(angle) {
    var turn = Math.PI * 2;
    var r = angle % turn;
    if (r > Math.PI) r -= turn;
    if (r < -Math.PI) r += turn;
    return r;
  }

  function ensureHost() {
    if (host && host.isConnected) return host;
    var root = document.documentElement || document.body;
    if (glow && glow.parentNode) glow.parentNode.removeChild(glow);
    glow = document.createElement("div");
    glow.setAttribute("aria-hidden", "true");
    glow.style.cssText = "position:fixed;left:0;top:0;width:" + (GLOW_BOX * 2) + "px;height:" + (GLOW_BOX * 2) + "px;border-radius:50%;pointer-events:none;z-index:2147483646;opacity:0;background:radial-gradient(closest-side,rgba(" + EFFECT + ",1),rgba(" + EFFECT + ",0));";
    root.appendChild(glow);
    host = document.createElement("div");
    host.id = "__wuu_agent_cursor";
    host.setAttribute("aria-hidden", "true");
    host.style.cssText = "position:fixed;left:0;top:0;width:${CURSOR_DISPLAY}px;height:${CURSOR_DISPLAY}px;pointer-events:none;z-index:2147483647;transform-origin:" + TIP_X + "px " + TIP_Y + "px;filter:drop-shadow(0 1px 1.5px rgba(10,30,48,.28));";
    body = document.createElement("div");
    body.style.cssText = "width:100%;height:100%;transform-origin:" + TIP_X + "px " + TIP_Y + "px;";
    body.innerHTML = ${JSON.stringify(AGENT_CURSOR_SVG)};
    host.appendChild(body);
    root.appendChild(host);
    return host;
  }

  // Signature arc: a cubic bezier bent to the natural side (up for horizontal
  // moves) with its peak shifted toward the destination, walked by arc length.
  function arcPath(a, b) {
    var dx = b.x - a.x;
    var dy = b.y - a.y;
    var len = Math.max(hypot(dx, dy), 1);
    var side = dx >= 0 ? -1 : 1;
    var px = -dy / len;
    var py = dx / len;
    var bend = len * 0.16 * side;
    var flow = (0.15 + 1) / 2;
    var d1 = bend * (1 - 0.5 * flow);
    var d2 = bend * (1 - 0.5 * (1 - flow));
    var c1 = { x: a.x + dx * 0.3 + px * d1, y: a.y + dy * 0.3 + py * d1 };
    var c2 = { x: b.x - dx * 0.3 + px * d2, y: b.y - dy * 0.3 + py * d2 };
    function at(u) {
      var v = 1 - u;
      var k0 = v * v * v, k1 = 3 * v * v * u, k2 = 3 * v * u * u, k3 = u * u * u;
      return { x: k0 * a.x + k1 * c1.x + k2 * c2.x + k3 * b.x, y: k0 * a.y + k1 * c1.y + k2 * c2.y + k3 * b.y };
    }
    var steps = 256;
    var lengths = [0];
    var prev = a;
    for (var i = 1; i <= steps; i++) {
      var p = at(i / steps);
      lengths.push(lengths[i - 1] + hypot(p.x - prev.x, p.y - prev.y));
      prev = p;
    }
    var total = lengths[steps];
    var endDir = at(1 - 1e-3);
    var ex = b.x - endDir.x, ey = b.y - endDir.y, el = hypot(ex, ey) || 1;
    return function (fraction) {
      if (total < 1e-9) return { x: b.x, y: b.y };
      // The follow-through runs briefly past the end along the arrival tangent.
      if (fraction > 1) return { x: b.x + ex / el * (fraction - 1) * total, y: b.y + ey / el * (fraction - 1) * total };
      var target = Math.max(0, fraction) * total;
      var lo = 0, hi = steps;
      while (hi - lo > 1) {
        var mid = (lo + hi) >> 1;
        if (lengths[mid] < target) lo = mid; else hi = mid;
      }
      var span = lengths[hi] - lengths[lo] || 1;
      return at((lo + (target - lengths[lo]) / span) / steps);
    };
  }

  // Minimum-jerk speed with a small push past the target that returns.
  function profile(distance) {
    var over = Math.min(0.018, 8 / Math.max(distance, 1));
    var a = 8.2, b = 1.8;
    var peak = Math.pow(a / (a + b), a) * Math.pow(b / (a + b), b);
    return function (t) {
      var base = t * t * t * (10 - 15 * t + 6 * t * t);
      return base + over * (Math.pow(t, a) * Math.pow(1 - t, b)) / peak;
    };
  }

  // Fitts timing for an unknown 24 pt target: longer moves take longer, but
  // short hops stay quick.
  function durationMs(distance) {
    return clamp(120 + 110 * Math.log(distance / 24 + 1) / Math.LN2, 260, 900);
  }

  function plan(start, end, fromHeading) {
    var distance = hypot(end.x - start.x, end.y - start.y);
    var path = arcPath(start, end);
    var ease = profile(distance);
    var ms = durationMs(distance);
    var count = Math.max(2, Math.ceil(ms / DT));
    var raw = [];
    for (var i = 0; i <= count; i++) {
      var tau = i / count;
      var at = path(ease(tau));
      raw.push({ t: tau * ms, x: at.x, y: at.y });
    }
    raw[0].x = start.x; raw[0].y = start.y;
    raw[count].x = end.x; raw[count].y = end.y;
    var arrival = ms;
    for (var j = 0; j < raw.length; j++) {
      if (hypot(raw[j].x - end.x, raw[j].y - end.y) <= 1) { arrival = raw[j].t; break; }
    }
    // Input waits for arrival, so compress any move that would arrive late.
    var latest = CAP * 0.95;
    if (arrival > latest) {
      var k = latest / arrival;
      for (var r = 0; r < raw.length; r++) raw[r].t *= k;
      arrival = latest;
    }
    // The tip leads the motion once it is moving, and the turn is damped.
    var rotation = wrap((fromHeading == null ? REST : fromHeading) - REST);
    var samples = [];
    for (var n = 0; n < raw.length; n++) {
      var before = raw[Math.max(n - 2, 0)];
      var after = raw[Math.min(n + 2, raw.length - 1)];
      var span = Math.max((after.t - before.t) / 1000, 1e-3);
      var vx = (after.x - before.x) / span;
      var vy = (after.y - before.y) / span;
      var weight = clamp((hypot(vx, vy) - 40) / 260, 0, 1);
      var want = wrap(Math.atan2(vy, vx) - TIP_ANGLE) * weight;
      var step = n > 0 ? (raw[n].t - raw[n - 1].t) / 1000 : 0;
      rotation += wrap(want - rotation) * (1 - Math.exp(-step * 22));
      samples.push({ t: raw[n].t / 1000, x: raw[n].x, y: raw[n].y, heading: REST + rotation });
    }
    var last = samples[samples.length - 1];
    var t = last.t;
    for (var s = 0; s < 36 && Math.abs(rotation) >= 0.002; s++) {
      t += DT / 1000;
      rotation -= rotation * (1 - Math.exp(-DT / 1000 * 22));
      samples.push({ t: t, x: last.x, y: last.y, heading: REST + rotation });
    }
    samples[samples.length - 1].heading = REST;
    return { start: start, end: end, samples: samples, arrivalT: arrival / 1000, duration: samples[samples.length - 1].t };
  }

  function sampleAt(move, t) {
    var list = move.samples;
    if (t <= list[0].t) return list[0];
    var last = list[list.length - 1];
    if (t >= last.t) return last;
    var lo = 0, hi = list.length;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      if (list[mid].t <= t) lo = mid + 1; else hi = mid;
    }
    var a = list[lo - 1], b = list[lo];
    var f = (t - a.t) / (b.t - a.t);
    return { t: t, x: a.x + (b.x - a.x) * f, y: a.y + (b.y - a.y) * f, heading: a.heading + wrap(b.heading - a.heading) * f };
  }

  function squish(age) {
    if (age < 0 || age >= CLICK_SECS) return 0;
    if (age < 0.09) return 0.12 * Math.min(age / 0.05, 1);
    var after = age - 0.09;
    return 0.12 * Math.max(Math.cos(Math.min(after / 0.22, 1) * Math.PI * 1.5), 0) * Math.max(1 - after / 0.22, 0);
  }

  function screenPoint(at) {
    return { x: viewport.x + at.x * viewport.scale, y: viewport.y + at.y * viewport.scale };
  }

  function paint(velocity) {
    if (!point) return;
    ensureHost();
    var at = screenPoint(point);
    var press = clickAt >= 0 && !reduced ? squish((now() - clickAt) / 1000) : 0;
    host.style.opacity = String(opacity);
    host.style.transform = "translate(" + (at.x - TIP_X) + "px, " + (at.y - TIP_Y) + "px) rotate(" + ((heading - REST) * 180 / Math.PI) + "deg) scale(" + (1 - press) + ")";
    var speed = velocity ? hypot(velocity.x, velocity.y) : 0;
    var alpha = Math.min(speed * 0.00014, 0.42);
    if (reduced || alpha <= 0.02) {
      glow.style.opacity = "0";
      return;
    }
    // A soft halo trails the body in proportion to speed.
    var lag = Math.min(speed * 0.009, 18);
    var radius = 30 * (1 + Math.min(speed * 0.00024, 0.44));
    var cx = at.x + Math.cos(heading) * BODY_OFFSET - velocity.x / speed * lag;
    var cy = at.y + Math.sin(heading) * BODY_OFFSET - velocity.y / speed * lag;
    glow.style.opacity = String(alpha * opacity);
    glow.style.transform = "translate(" + (cx - GLOW_BOX) + "px, " + (cy - GLOW_BOX) + "px) scale(" + (radius / GLOW_BOX) + ")";
  }

  function stopFloat() {
    if (floatAnimation) floatAnimation.cancel();
    floatAnimation = null;
  }

  function startFloat() {
    stopFloat();
    if (reduced) return;
    floatAnimation = body.animate(FLOAT, { duration: 4000, iterations: Infinity });
  }

  function settleArrival() {
    window.clearTimeout(arrivalTimer);
    arrivalTimer = 0;
    if (!pending) return;
    var resolve = pending;
    pending = null;
    resolve();
  }

  function rest() {
    var move = travel.move;
    travel = null;
    point = { x: move.end.x, y: move.end.y };
    heading = REST;
    opacity = 1;
    paint(null);
    startFloat();
  }

  function schedule() {
    if (!raf) raf = window.requestAnimationFrame(frame);
  }

  function frame() {
    raf = 0;
    var time = now();
    var velocity = null;
    if (travel) {
      var elapsed = (time - travel.started) / 1000;
      var at = sampleAt(travel.move, elapsed);
      point = { x: at.x, y: at.y };
      heading = at.heading;
      opacity = Math.min(1, travel.baseOpacity + elapsed * 1000 / 140);
      if (elapsed >= travel.move.arrivalT) settleArrival();
      if (elapsed >= travel.move.duration) {
        rest();
      } else {
        var ahead = sampleAt(travel.move, elapsed + 0.008);
        var behind = sampleAt(travel.move, elapsed - 0.008);
        velocity = { x: (ahead.x - behind.x) / 0.016, y: (ahead.y - behind.y) / 0.016 };
        paint(velocity);
      }
    } else {
      paint(null);
    }
    if (clickAt >= 0 && (time - clickAt) / 1000 >= CLICK_SECS) clickAt = -1;
    if (travel || clickAt >= 0) schedule();
  }

  function cancelMotion() {
    if (raf) window.cancelAnimationFrame(raf);
    raf = 0;
    travel = null;
    settleArrival();
  }

  return {
    setReducedMotion: function (value) {
      reduced = value;
      if (!reduced) return;
      if (travel) rest();
      cancelMotion();
      stopFloat();
      clickAt = -1;
      if (host) paint(null);
      if (effectAnimation) effectAnimation.cancel();
      if (effect) { effect.remove(); effect = null; }
    },
    moveTo: function (x, y) {
      cancelMotion();
      stopFloat();
      var end = { x: x, y: y };
      var start = point || (function () {
        var width = (window.innerWidth - viewport.x * 2) / viewport.scale;
        var height = (window.innerHeight - viewport.y * 2) / viewport.scale;
        return { x: Math.round(width * 0.58), y: Math.round(height * 0.55) };
      })();
      var appearing = !point;
      return new Promise(function (resolve) {
        pending = resolve;
        if (reduced || hypot(end.x - start.x, end.y - start.y) < 0.5) {
          point = end;
          heading = REST;
          opacity = 1;
          paint(null);
          settleArrival();
          startFloat();
          return;
        }
        travel = { move: plan(start, end, heading), started: now(), baseOpacity: appearing ? 0 : 1 };
        if (!point) point = start;
        schedule();
        // Hidden or stalled pages may never run a frame; arrive on time anyway.
        arrivalTimer = window.setTimeout(function () {
          if (pending !== resolve) return;
          if (travel) rest();
          settleArrival();
        }, CAP);
      });
    },
    setViewport: function (next) {
      viewport = next;
      if (point && host) paint(null);
      if (effect) { effect.remove(); effect = null; }
    },
    feedback: function (hint) {
      if (!point || !host) return;
      if (effectAnimation) effectAnimation.cancel();
      if (effect) effect.remove();
      effect = document.createElement("div");
      effect.setAttribute("aria-hidden", "true");
      var at = screenPoint(hint);
      effect.style.cssText = "position:fixed;left:" + at.x + "px;top:" + at.y + "px;pointer-events:none;z-index:2147483647;color:" + FILL + ";";
      var transform = "translate(-50%,-50%)";
      var frames;
      var timing = { duration: 420, easing: "ease-out", fill: "forwards" };
      if (hint.kind === "click") {
        effect.style.cssText += "box-sizing:border-box;border:solid currentColor;border-radius:50%;transform:" + transform + ";";
        if (reduced) {
          frames = [{ width: "28px", height: "28px", borderWidth: "2px", opacity: .75 }, { width: "28px", height: "28px", borderWidth: "2px", opacity: 0 }];
        } else {
          // Size eases out while opacity and stroke fall linearly.
          frames = [];
          for (var i = 0; i <= 12; i++) {
            var k = i / 12;
            var size = 16 + 88 * (1 - Math.pow(1 - k, 3));
            frames.push({ offset: k, width: size + "px", height: size + "px", borderWidth: (5 - 4 * k) + "px", opacity: .75 * (1 - k) });
          }
          timing = { duration: 520, easing: "linear", fill: "forwards" };
          clickAt = now();
          schedule();
        }
      } else {
        if (hint.kind === "type") {
          effect.style.cssText += "width:3px;height:18px;background:currentColor;border-radius:2px;";
          effect.style.left = (at.x + 13) + "px";
        } else {
          var angle = { down: 0, left: 90, up: 180, right: 270 }[hint.direction] || 0;
          transform += " rotate(" + angle + "deg)";
          effect.style.left = (at.x + 15) + "px";
          effect.innerHTML = ${JSON.stringify(iconSVG("ChevronsDown", 16))};
        }
        frames = [
          { opacity: .9, transform: transform + (reduced ? "" : " scale(.65)") },
          { opacity: 0, transform: transform + (reduced ? "" : " scale(1.15)") }
        ];
      }
      document.documentElement.appendChild(effect);
      effectAnimation = effect.animate(frames, timing);
    },
    hide: function () {
      cancelMotion();
      stopFloat();
      clickAt = -1;
      if (effectAnimation) effectAnimation.cancel();
      effectAnimation = null;
      if (effect) effect.remove();
      effect = null;
      point = null;
      heading = REST;
      if (glow && glow.parentNode) glow.parentNode.removeChild(glow);
      if (host && host.parentNode) host.parentNode.removeChild(host);
      host = null;
      body = null;
      glow = null;
    },
    plan: plan,
    sampleAt: sampleAt
  };
}`;
