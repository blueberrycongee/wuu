import { useEffect } from "react";

// The empty home's idle play. After a quiet stretch the greeting mascot sends
// out a small copy of itself that bounces across the usage heatmap toward
// today, landing on the busiest day of each week it passes and pressing that
// cell down. It turns around on today's cell and leaps home, and the mascot
// catches it with a happy hop. Any input ends the play and hands the gaze
// back to the pointer.

const IDLE_MS = 20_000;
const REPEAT_MS = 60_000;
const LEAD_MS = 320;
const SIT_MS = 320;
const CHEER_MS = 620;
// Heatmap pixels per ms²: a hop four cells high lasts about 0.4 s.
const GRAVITY = 0.0024;
// Exact quadratic easings, so every hop traces a parabola.
const RISE = "cubic-bezier(0.333, 0.667, 0.667, 1)";
const FALL = "cubic-bezier(0.333, 0, 0.667, 0.333)";

type Point = { x: number; y: number };
type Play = { stop(): void };

/** Plays on the mounted usage card; reduced motion and hidden windows skip it. */
export function useEmptyHomePlay(card: HTMLElement | null): void {
  useEffect(() => {
    if (!card) return;
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    let lastInput = performance.now();
    let playedAt = -Infinity;
    let play: Play | undefined;
    let timer: number | undefined;
    let timerAt = Infinity;
    // Without input since the last play, wait the longer repeat interval.
    const dueAt = () => (lastInput >= playedAt ? lastInput + IDLE_MS : playedAt + REPEAT_MS);
    const arm = () => {
      window.clearTimeout(timer);
      timerAt = Infinity;
      if (play || document.hidden || reduced?.matches) return;
      timerAt = dueAt();
      timer = window.setTimeout(fire, timerAt - performance.now());
    };
    const played = () => {
      play = undefined;
      playedAt = performance.now();
      arm();
    };
    const fire = () => {
      if (performance.now() < dueAt()) {
        arm();
        return;
      }
      play = startPlay(card, played);
      if (!play) played();
    };
    // Pointer moves are frequent; the timer re-arms itself for the remaining
    // wait, so only input that shortens the wait resets it here.
    const input = () => {
      lastInput = performance.now();
      play?.stop();
      play = undefined;
      if (timerAt > lastInput + IDLE_MS) arm();
    };
    const restart = () => {
      play?.stop();
      play = undefined;
      lastInput = performance.now();
      arm();
    };
    const inputs = ["pointermove", "pointerdown", "keydown", "wheel", "resize"] as const;
    for (const type of inputs) window.addEventListener(type, input, { capture: true, passive: true });
    document.addEventListener("visibilitychange", restart);
    reduced?.addEventListener("change", restart);
    arm();
    return () => {
      window.clearTimeout(timer);
      play?.stop();
      for (const type of inputs) window.removeEventListener(type, input, { capture: true });
      document.removeEventListener("visibilitychange", restart);
      reduced?.removeEventListener("change", restart);
    };
  }, [card]);
}

function startPlay(card: HTMLElement, done: () => void): Play | undefined {
  const mascot = card.closest(".empty-home")?.querySelector<SVGSVGElement>(".empty-home-mascot");
  const eyes = mascot?.querySelector<SVGGElement>(".mo-eyes");
  const heatmap = card.querySelector<HTMLElement>(".empty-home-heatmap-weeks");
  const face = mascot?.getBoundingClientRect();
  if (!mascot || !eyes || !heatmap || !face?.width) return undefined;

  // Positions are relative to the card's padding box, where the play layer sits.
  const frame = card.getBoundingClientRect();
  const local = (element: Element): Point => {
    const rect = element.getBoundingClientRect();
    return {
      x: rect.left + rect.width / 2 - frame.left - card.clientLeft,
      y: rect.top + rect.height / 2 - frame.top - card.clientTop,
    };
  };
  // Weeks are newest first and laid out right to left. Older weeks that
  // wrapped onto the clipped second line are out of sight.
  const bottom = heatmap.getBoundingClientRect().bottom;
  const weeks = [...heatmap.children]
    .filter((week) => week.getBoundingClientRect().top < bottom - 1)
    .map((week) => [...week.querySelectorAll<HTMLElement>("i")])
    .reverse();
  const today = weeks.at(-1)?.at(-1);
  const target = today?.getBoundingClientRect();
  // Narrow layouts can push the heatmap below the fold; the mascot would
  // watch a play nobody can see.
  const hit = target && document.elementFromPoint(target.x + target.width / 2, target.y + target.height / 2);
  if (!today || !target || hit !== today) return undefined;
  const cellSize = target.width;
  const body = local(mascot);
  // Blobatar draws the body at radius 40 of its 100-unit frame.
  const radius = face.width * 0.4;
  const source = { x: body.x + radius * 0.7, y: body.y + radius * 0.6 };

  // The little one drops into the first week right of the mascot, then
  // bounces like a dropped ball: each hop is lower and, at a steady
  // horizontal speed, shorter, so a hop covers columns in proportion to
  // sqrt(height).
  const first = Math.max(0, weeks.findIndex((week) => local(week[0]).x >= body.x + radius));
  const span = weeks.length - 1 - first;
  const heights = Array.from(
    { length: Math.min(10, Math.round(span / 4)) },
    (_, hop) => Math.max(cellSize / 2, cellSize * 4 * 0.78 ** hop),
  );
  const airtime = heights.reduce((total, height) => total + Math.sqrt(height), 0);
  const columns = [first];
  let covered = 0;
  for (const height of heights) {
    covered += Math.sqrt(height);
    columns.push(first + Math.round((span * covered) / airtime));
  }
  const cells = columns.map((column, landing) => {
    if (landing === columns.length - 1) return today;
    // Land on the busiest day. Between equals, wander around the middle rows
    // so an empty year still gets a lively path.
    const week = weeks[column];
    const row = 3 + Math.round(2 * Math.sin(landing * 2.1));
    const score = (index: number) => Number(week[index].dataset.level) * 8 - Math.abs(index - row);
    let best = 0;
    week.forEach((_, index) => {
      if (score(index) > score(best)) best = index;
    });
    return week[best];
  });

  // Build the path in milliseconds; offsets become fractions once the total
  // is known.
  const xs: Keyframe[] = [];
  const ys: Keyframe[] = [];
  const landings: number[] = [];
  let clock = 0;
  const hop = (from: Point, to: Point, height: number): Point => {
    const top = Math.min(from.y, to.y) - height;
    const rise = Math.sqrt((2 * (from.y - top)) / GRAVITY);
    const fall = Math.sqrt((2 * (to.y - top)) / GRAVITY);
    xs.push({ offset: clock, translate: `${from.x}px 0` });
    ys.push(
      { offset: clock, translate: `0 ${from.y}px`, easing: RISE },
      { offset: clock + rise, translate: `0 ${top}px`, easing: FALL },
    );
    clock += rise + fall;
    xs.push({ offset: clock, translate: `${to.x}px 0` });
    ys.push({ offset: clock, translate: `0 ${to.y}px` });
    return { x: from.x + ((to.x - from.x) * rise) / (rise + fall), y: top };
  };
  const points = cells.map(local);
  points.forEach((to, landing) => {
    hop(landing === 0 ? source : points[landing - 1], to, landing === 0 ? cellSize : heights[landing - 1]);
    landings.push(clock);
  });
  const arrived = clock;
  const todayPoint = points.at(-1)!;
  clock += SIT_MS;
  const departed = clock;
  const homeApex = hop(todayPoint, { x: body.x, y: body.y - radius * 0.3 }, cellSize * 4);
  const caught = clock;
  const inBall = (keyframe: Keyframe) => ({ ...keyframe, offset: (keyframe.offset ?? 0) / caught });
  // Negative x scale turns it to face home; passing through zero reads as
  // turning around.
  const squash: Keyframe[] = [
    { offset: 0, scale: "0.3", opacity: 0 },
    { offset: 140, scale: "1", opacity: 1 },
    ...landings.flatMap((at) => [
      { offset: at - 30, scale: "1" },
      { offset: at, scale: "1.3 0.72" },
      { offset: at + 60, scale: "1" },
    ]),
    { offset: arrived + 120, scale: "1" },
    { offset: arrived + 240, scale: "-1 1" },
    { offset: departed - 50, scale: "-1.2 0.8" },
    { offset: departed + 70, scale: "-0.9 1.15" },
    { offset: departed + 200, scale: "-1 1", opacity: 1 },
    { offset: caught - 110, scale: "-1 1", opacity: 1 },
    { offset: caught, scale: "-0.3 0.3", opacity: 0 },
  ];

  const mascotStyle = getComputedStyle(mascot);
  const layer = card.appendChild(document.createElement("div"));
  layer.className = "empty-home-play";
  layer.setAttribute("aria-hidden", "true");
  layer.style.setProperty("--empty-home-play-body", mascotStyle.getPropertyValue("--mo-head"));
  layer.style.setProperty("--empty-home-play-eye", mascotStyle.getPropertyValue("--mo-eye"));
  layer.style.setProperty("--empty-home-play-cell", `${cellSize}px`);
  const append = (parent: Element, className: string) =>
    parent.appendChild(Object.assign(document.createElement("span"), { className }));
  const track = append(layer, "empty-home-play-ball");
  const ball = append(track, "");
  const ripple = append(layer, "empty-home-play-ripple");
  ripple.style.translate = `${todayPoint.x}px ${todayPoint.y}px`;
  const ballTiming = { duration: caught, delay: LEAD_MS };
  const layerMotion = [
    track.animate(xs.map(inBall), ballTiming),
    ball.animate(ys.map(inBall), ballTiming),
    ball.animate(squash.map(inBall), ballTiming),
    ripple.animate(
      [{ scale: "0.6", opacity: 0.7 }, { scale: "2.6", opacity: 0 }],
      { duration: 720, delay: LEAD_MS + arrived, easing: "cubic-bezier(0.22, 1, 0.36, 1)" },
    ),
  ];
  // Each landing presses its day down; today is pressed again by the takeoff.
  const presses = [...cells.map((cell, landing) => [cell, landings[landing]] as const), [today, departed - 50] as const]
    .map(([cell, at]) =>
      cell.animate([{ scale: "1" }, { offset: 0.25, scale: "0.62" }, { offset: 0.65, scale: "1.06" }, { scale: "1" }], {
        duration: 380,
        delay: LEAD_MS + at,
      }),
    );

  // Gaze uses the pointer channels the greeting already follows. While these
  // animations run they override the pointer; the implicit end keyframe
  // returns the face to it.
  const look = ({ x, y }: Point): Keyframe => {
    const dx = x - body.x;
    const dy = y - body.y;
    const distance = Math.hypot(dx, dy) || 1;
    const strength = Math.min(1, distance / 200);
    return {
      "--mo-pointer-yaw": ((20 * dx) / distance) * strength,
      "--mo-pointer-pitch": ((-16 * dy) / distance) * strength,
    };
  };
  const catchAt = LEAD_MS + caught;
  const mascotMs = catchAt + CHEER_MS;
  const inMascot = (ms: number) => ms / mascotMs;
  const pose = (ms: number, transform: string): Keyframe => ({ offset: inMascot(ms), transform, easing: "ease-in-out" });
  const mascotMotion = [
    mascot.animate([
      pose(0, "none"),
      pose(190, "scale(1.06, 0.92)"),
      pose(LEAD_MS, "translateY(-4px) scale(0.96, 1.05)"),
      pose(LEAD_MS + 170, "none"),
      pose(catchAt - 40, "none"),
      pose(catchAt + 60, "scale(1.08, 0.9)"),
      pose(catchAt + 180, "none"),
      pose(catchAt + 320, "translateY(-8px) scale(0.97, 1.04)"),
      pose(catchAt + 450, "scale(1.05, 0.94)"),
      pose(catchAt + 540, "translateY(-3px)"),
      pose(mascotMs, "none"),
    ], mascotMs),
    mascot.animate([
      { offset: inMascot(190), ...look(source) },
      ...landings.map((at, landing) => ({ offset: inMascot(LEAD_MS + at), ...look(points[landing]) })),
      { offset: inMascot(LEAD_MS + departed), ...look(todayPoint) },
      { offset: inMascot(catchAt - 200), ...look(homeApex) },
    ], mascotMs),
    // A contented squint through the hop.
    eyes.animate([{ offset: 0.2, "--mo-esy": 0.42 }, { offset: 0.8, "--mo-esy": 0.42 }], {
      duration: CHEER_MS - 120,
      delay: catchAt + 120,
    }),
  ];

  let ended = false;
  mascotMotion[0].finished.then(() => {
    if (ended) return;
    ended = true;
    layer.remove();
    done();
  }, () => undefined);

  return {
    stop() {
      if (ended) return;
      ended = true;
      // Ease the mascot back from its current pose rather than snapping.
      const current = getComputedStyle(mascot);
      const settle: Keyframe = {
        offset: 0,
        transform: current.transform,
        "--mo-pointer-yaw": current.getPropertyValue("--mo-pointer-yaw"),
        "--mo-pointer-pitch": current.getPropertyValue("--mo-pointer-pitch"),
      };
      const lids: Keyframe = { offset: 0, "--mo-esy": getComputedStyle(eyes).getPropertyValue("--mo-esy") };
      for (const animation of [...mascotMotion, ...presses]) animation.cancel();
      mascot.animate([settle], { duration: 160, easing: "ease-out" });
      eyes.animate([lids], { duration: 160, easing: "ease-out" });
      for (const animation of layerMotion) animation.pause();
      layer.animate([{ opacity: 1 }, { opacity: 0 }], 140).onfinish = () => layer.remove();
    },
  };
}
