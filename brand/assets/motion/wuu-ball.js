// Wuu ball pose and motion reference. Plain script (UMD) so it runs from file://
// pages and from the Node asset generator; both draw eyes with the same function.
(function (root, factory) {
  if (typeof module === "object" && module.exports) module.exports = factory();
  else root.WuuBall = factory();
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  // Optical eye sets in the ball's 100-unit frame. Mirrors tokens.json → ball.optical;
  // the generator checks that the two stay equal.
  var OPTICAL = {
    display: { eyeWidth: 10.5, eyeHeight: 23, gap: 20, lean: 8, gazeX: 12, gazeY: -10, foreshorten: 0.35 },
    small: { eyeWidth: 13.5, eyeHeight: 28, gap: 25, lean: 6, gazeX: 9, gazeY: -8, foreshorten: 0.2 },
    // Micro lands each eye on whole pixels at 16 px (3 × 5 px).
    micro: { eyeWidth: 18.75, eyeHeight: 31.25, gap: 25, lean: 0, gazeX: 9.375, gazeY: -9.375, foreshorten: 0 },
  };
  var BREAKPOINTS = { small: 40, micro: 20 };

  // States change only the eyes. Offsets are added to the optical base pose.
  var STATES = {
    rest: { label: "Rest", dx: 0, dy: 0, sy: 1 },
    listen: { label: "Listening", dx: -16, dy: 16, sy: 1 },
    think: { label: "Thinking", dx: -20, dy: -6, sy: 0.86, loop: "drift" },
    work: { label: "Working", dx: -2, dy: 20, sy: 0.9, loop: "scan" },
    // Facing the user, with extra head tilt so the pair never reads as a pause glyph.
    wait: { label: "Needs you", dx: -12, dy: 4, sy: 1.12, roll: 8 },
    done: { label: "Done", dx: 0, dy: 0, sy: 1, blinkOnEnter: true },
    failed: { label: "Failed", dx: -16, dy: 22, sy: 0.45, roll: -6 },
    paused: { label: "Paused", dx: -12, dy: 16, sy: 0.3 },
  };


  var MOTION = { base: 180, fast: 120, slow: 280, blink: { close: 60, hold: 40, open: 90, min: 3800, max: 8200 }, loop: 1600 };

  function opticalSize(px) {
    return px <= BREAKPOINTS.micro ? "micro" : px <= BREAKPOINTS.small ? "small" : "display";
  }

  function pose(size, state) {
    var base = OPTICAL[size], s = STATES[state || "rest"];
    // Small sizes move less so the face never leaves the disc.
    var k = size === "display" ? 1 : size === "small" ? 0.7 : 0.45;
    return { gazeX: base.gazeX + s.dx * k, gazeY: base.gazeY + s.dy * k, scaleY: s.sy, roll: s.roll || 0 };
  }

  /** Eye capsules for a pose. Each: centre, width, height, rotation in degrees. */
  function eyeRects(size, p) {
    var e = {}, key, base = OPTICAL[size || "display"];
    for (key in base) e[key] = base[key];
    for (key in p || {}) e[key] = p[key];
    var fx = 50 + e.gazeX, fy = 50 + e.gazeY, axis = (e.lean * Math.PI) / 180;
    return [-1, 1].map(function (side) {
      var cx = fx + (side * e.gap * Math.cos(axis)) / 2;
      var cy = fy + (side * e.gap * Math.sin(axis)) / 2;
      var off = Math.max(0, ((e.gazeX >= 0 ? 1 : -1) * (cx - 50)) / 50);
      return {
        cx: cx, cy: cy,
        width: e.eyeWidth * (1 - e.foreshorten * off) * (e.scaleX || 1),
        height: e.eyeHeight * (e.scaleY == null ? 1 : e.scaleY),
        rotate: e.lean + (e.roll || 0),
      };
    });
  }

  function eyeAttrs(r) {
    var h = Math.max(r.height, r.width * 0.9), w = r.width;
    return { x: -w / 2, y: -h / 2, width: w, height: h, rx: w / 2, transform: "translate(" + r.cx + " " + r.cy + ") rotate(" + r.rotate + ")" };
  }

  var ease = function (t) { return 1 - Math.pow(1 - t, 3); };
  var lerp = function (a, b, t) { return a + (b - a) * t; };

  /** Eye openness (1 open → 0.1 closed) at t ms into a blink. */
  function blink(t) {
    var b = MOTION.blink;
    if (t <= 0 || t >= b.close + b.hold + b.open) return 1;
    if (t < b.close) return 1 - 0.9 * ease(t / b.close);
    if (t < b.close + b.hold) return 0.1;
    return 0.1 + 0.9 * ease((t - b.close - b.hold) / b.open);
  }

  /** Squash amount at t ms into the completion settle; the body widens by it and shortens by it. */
  function settle(t) {
    if (t <= 0 || t >= MOTION.slow) return 0;
    return 0.06 * Math.sin((t / MOTION.slow) * Math.PI);
  }

  /**
   * Mount an animated ball in a container. Returns { set(state), destroy() }.
   * Respects prefers-reduced-motion: state changes jump, no blinks, no loops.
   */
  function mount(el, opts) {
    opts = opts || {};
    var px = opts.px || 96, size = opts.size || opticalSize(px);
    var body = opts.body || "#141411", eye = opts.eye || "#F7F7F4";
    var ns = "http://www.w3.org/2000/svg";
    var svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 100 100");
    svg.setAttribute("width", px); svg.setAttribute("height", px);
    svg.setAttribute("role", "img");
    var g = document.createElementNS(ns, "g");
    var disc = document.createElementNS(ns, "circle");
    disc.setAttribute("cx", 50); disc.setAttribute("cy", 50); disc.setAttribute("r", 50); disc.setAttribute("fill", body);
    g.appendChild(disc);
    var eyes = [0, 1].map(function () { var r = document.createElementNS(ns, "rect"); r.setAttribute("fill", eye); g.appendChild(r); return r; });
    svg.appendChild(g); el.appendChild(svg);

    var reduced = opts.reducedMotion != null ? { matches: opts.reducedMotion } : (window.matchMedia ? matchMedia("(prefers-reduced-motion: reduce)") : { matches: false });
    var state = opts.state || "rest", from = pose(size, state), to = from, t0 = 0, raf = 0, nextBlink = 0, blinkAt = -1, settleAt = -1, alive = true;

    function schedule(now) { nextBlink = now + MOTION.blink.min + Math.random() * (MOTION.blink.max - MOTION.blink.min); }
    function lid(now) {
      if (blinkAt < 0) return 1;
      var k = blink(now - blinkAt);
      if (k === 1 && now - blinkAt > 0) blinkAt = -1;
      return k;
    }
    function frame(now) {
      raf = 0; if (!alive) return;
      var k = reduced.matches ? 1 : Math.min(1, (now - t0) / MOTION.base), e = ease(k);
      var p = { gazeX: lerp(from.gazeX, to.gazeX, e), gazeY: lerp(from.gazeY, to.gazeY, e), scaleY: lerp(from.scaleY, to.scaleY, e), roll: lerp(from.roll, to.roll, e) };
      var s = STATES[state], animated = !reduced.matches;
      if (animated && s.loop) {
        var ph = ((now % MOTION.loop) / MOTION.loop) * Math.PI * 2;
        var amp = size === "display" ? 1 : 0.6;
        if (s.loop === "drift") { p.gazeX += 2 * amp * Math.sin(ph); p.gazeY += 1.2 * amp * Math.cos(ph); }
        if (s.loop === "scan") { p.gazeX += 5 * amp * Math.sin(ph); }
      }
      if (animated && (state === "rest" || state === "listen" || state === "wait") && now > nextBlink && blinkAt < 0) { blinkAt = now; schedule(now); }
      if (animated) p.scaleY *= lid(now);
      var rects = eyeRects(size, p);
      rects.forEach(function (r, i) { var a = eyeAttrs(r); for (var n in a) eyes[i].setAttribute(n, a[n]); });
      // Settle: one small squash on completion, anchored at the bottom of the disc.
      var sq = 0;
      if (animated && settleAt >= 0) { sq = settle(now - settleAt); if (now - settleAt >= MOTION.slow) settleAt = -1; }
      g.setAttribute("transform", sq ? "translate(50 100) scale(" + (1 + sq) + " " + (1 - sq) + ") translate(-50 -100)" : "");
      // Idle blinks need a running clock; reduced motion stops after the jump.
      if (animated) raf = requestAnimationFrame(frame);
    }
    function set(next) {
      if (!STATES[next]) return;
      var now = performance.now();
      from = to; to = pose(size, next); state = next; t0 = now;
      svg.setAttribute("aria-label", "Wuu · " + STATES[next].label);
      if (STATES[next].blinkOnEnter && !reduced.matches) { blinkAt = now; settleAt = now; }
      if (!raf) raf = requestAnimationFrame(frame);
    }
    schedule(performance.now());
    set(state);
    if (reduced.addEventListener) reduced.addEventListener("change", function () { if (!raf) raf = requestAnimationFrame(frame); });
    return { set: set, destroy: function () { alive = false; if (raf) cancelAnimationFrame(raf); el.removeChild(svg); }, get state() { return state; } };
  }

  /** Static SVG markup for a state (used for storyboards and reduced-motion stills). */
  function staticSVG(opts) {
    opts = opts || {};
    var px = opts.px || 96, size = opts.size || opticalSize(px);
    var p = opts.pose || pose(size, opts.state || "rest");
    var rects = eyeRects(size, p);
    var eyes = rects.map(function (r) { var a = eyeAttrs(r); return '<rect x="' + a.x.toFixed(2) + '" y="' + a.y.toFixed(2) + '" width="' + a.width.toFixed(2) + '" height="' + a.height.toFixed(2) + '" rx="' + a.rx.toFixed(2) + '" transform="' + a.transform.replace(/(\d+\.\d{2})\d+/g, "$1") + '"/>'; }).join("");
    var sq = opts.squash || 0;
    var g = sq ? '<g transform="translate(50 100) scale(' + (1 + sq) + " " + (1 - sq) + ') translate(-50 -100)">' : "<g>";
    return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="' + px + '" height="' + px + '" overflow="visible" role="img" aria-label="Wuu · ' + STATES[opts.state || "rest"].label + '">' + g + '<circle cx="50" cy="50" r="50" fill="' + (opts.body || "#141411") + '"/><g fill="' + (opts.eye || "#F7F7F4") + '">' + eyes + "</g></g></svg>";
  }

  return { OPTICAL: OPTICAL, BREAKPOINTS: BREAKPOINTS, STATES: STATES, MOTION: MOTION, opticalSize: opticalSize, pose: pose, eyeRects: eyeRects, blink: blink, settle: settle, mount: mount, staticSVG: staticSVG };
});
