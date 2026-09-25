import { useEffect } from "react";
import { prefersReducedMotion, subscribeReducedMotion } from "./motion";

// Short, autonomous idle scenes, never games that capture keyboard input.
// Heatmap effects are temporary animations; usage data remains untouched.
// The mascot throws the ball that opens every scene and then only watches:
// each game ends on the heatmap in its own way, with its own reaction.

const IDLE_MS = 20_000;
const REPEAT_MS = 60_000;
const LEAD_MS = 320;
// Heatmap pixels per ms²: a hop four cells high lasts about 0.6 s.
const GRAVITY = 0.0012;
// Exact quadratic easings, so every hop traces a parabola.
const RISE = "cubic-bezier(0.333, 0.667, 0.667, 1)";
const FALL = "cubic-bezier(0.333, 0, 0.667, 0.333)";
// Grid games stay a local vignette, even on a full year of visible activity.
const VIGNETTE_WEEKS = 13;
const SNAKE_STEP_MS = 130;

// Mascot gestures as [ms from their start, transform]. The throw opens every
// scene; each ending has its own reaction.
const THROW: Pose[] = [
  [0, "none"],
  [190, "scale(1.06, 0.92)"],
  [LEAD_MS, "translateY(-4px) scale(0.96, 1.05)"],
  [LEAD_MS + 170, "none"],
];
const TILT: Pose[] = [[0, "none"], [240, "rotate(9deg) scale(1.02, 0.97)"], [760, "rotate(9deg)"], [1_000, "none"]];
const STARTLE: Pose[] = [[0, "none"], [70, "translateY(-5px) scale(0.93, 1.08)"], [260, "none"]];
const GIGGLE: Pose[] = [
  [0, "none"],
  [90, "translateY(-2px)"],
  [180, "scale(1.03, 0.97)"],
  [270, "translateY(-2px)"],
  [360, "scale(1.03, 0.97)"],
  [450, "translateY(-1px)"],
  [560, "none"],
];
const CHEER: Pose[] = [
  [0, "none"],
  [100, "scale(1.08, 0.9)"],
  [240, "translateY(-8px) scale(0.97, 1.04)"],
  [370, "scale(1.05, 0.94)"],
  [460, "translateY(-3px)"],
  [580, "none"],
];
const SMILE: Keyframe = { "--mo-esy": 0.42 };
const WIDE: Keyframe = { "--mo-esx": 1.5, "--mo-esy": 1.95 };
// Each landing presses its day down.
const PRESS: Keyframe[] = [{ scale: "1" }, { offset: 0.25, scale: "0.62" }, { offset: 0.65, scale: "1.06" }, { scale: "1" }];

type Point = { x: number; y: number };
type TimedPoint = Point & { at: number };
// A keyframe on its scene's clock; `at` becomes the offset.
type Frame = Keyframe & { at: number };
type Pose = [at: number, transform: string];
type Play = { stop(): void };
type PlayKind = "bounce" | "snake" | "breakout";
type Scene = {
  mascot: SVGSVGElement;
  eyes: SVGGElement;
  layer: HTMLDivElement;
  weeks: HTMLElement[][];
  local(element: Element): Point;
  body: Point;
  source: Point;
  radius: number;
  cellSize: number;
};
type Motion = { layerMotion: Animation[]; presses: Animation[]; mascotMotion: Animation[] };
type Sprite = { track: HTMLSpanElement; body: HTMLSpanElement };
type Trajectory = ReturnType<typeof trajectory>;

/** Plays on the mounted usage card; reduced motion and hidden windows skip it. */
export function useEmptyHomePlay(card: HTMLElement | null): void {
  useEffect(() => {
    if (!card) return;
    let lastInput = performance.now();
    let playedAt = -Infinity;
    let play: Play | undefined;
    let timer: number | undefined;
    let timerAt = Infinity;
    let remaining: PlayKind[] = [];
    let previous: PlayKind | undefined;
    // Without input since the last play, wait the longer repeat interval.
    const dueAt = () => (lastInput >= playedAt ? lastInput + IDLE_MS : playedAt + REPEAT_MS);
    const arm = () => {
      window.clearTimeout(timer);
      timerAt = Infinity;
      if (play || document.hidden || prefersReducedMotion()) return;
      timerAt = dueAt();
      timer = window.setTimeout(fire, timerAt - performance.now());
    };
    const played = () => {
      play = undefined;
      playedAt = performance.now();
      arm();
    };
    const fire = () => {
      // This timer has fired. Input that interrupts play must arm a new
      // idle wait instead of relying on the expired deadline.
      timerAt = Infinity;
      if (performance.now() < dueAt()) {
        arm();
        return;
      }
      // A shuffled bag shows every scene before repeating; the bag boundary
      // must not repeat the last scene either.
      if (!remaining.length) remaining = ["bounce", "snake", "breakout"];
      const choices = remaining.filter((kind) => kind !== previous);
      const kind = choices[Math.floor(Math.random() * choices.length)];
      play = startEmptyHomePlay(card, kind, played);
      if (play) {
        remaining.splice(remaining.indexOf(kind), 1);
        previous = kind;
      }
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
    const inputs = ["pointermove", "pointerdown", "keydown", "wheel", "scroll", "resize"] as const;
    for (const type of inputs) window.addEventListener(type, input, { capture: true, passive: true });
    document.addEventListener("visibilitychange", restart);
    const stopReducedMotion = subscribeReducedMotion(restart);
    const observer = new ResizeObserver(restart);
    observer.observe(card);
    arm();
    return () => {
      window.clearTimeout(timer);
      play?.stop();
      for (const type of inputs) window.removeEventListener(type, input, { capture: true });
      document.removeEventListener("visibilitychange", restart);
      stopReducedMotion();
      observer.disconnect();
    };
  }, [card]);
}

export function startEmptyHomePlay(card: HTMLElement, kind: PlayKind, done: () => void): Play | undefined {
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

  const mascotStyle = getComputedStyle(mascot);
  const layer = card.appendChild(document.createElement("div"));
  layer.className = "empty-home-play";
  layer.setAttribute("aria-hidden", "true");
  layer.style.setProperty("--empty-home-play-body", mascotStyle.getPropertyValue("--mo-head"));
  layer.style.setProperty("--empty-home-play-eye", mascotStyle.getPropertyValue("--mo-eye"));
  layer.style.setProperty("--empty-home-play-cell", `${cellSize}px`);
  const scene = { mascot, eyes, layer, weeks, local, body, source, radius, cellSize };
  // The last week ends today and may have fewer than seven cells. Grid games
  // only use full columns. An exceptionally narrow card, or a snake without
  // enough to eat, falls back to hops.
  const fullWeeks = weeks.filter((week) => week.length === 7);
  const grid = fullWeeks.length >= 4 ? { ...scene, weeks: fullWeeks } : undefined;
  const { layerMotion, presses, mascotMotion } =
    (grid && kind === "snake" && animateSnake(grid)) ||
    (grid && kind === "breakout" && animateBreakout(grid)) ||
    animateBounce(scene);

  let ended = false;
  mascotMotion[0].finished.then(() => {
    if (ended) return;
    ended = true;
    for (const animation of [...layerMotion, ...presses, ...mascotMotion]) animation.cancel();
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
      const shape = getComputedStyle(eyes);
      const lids: Keyframe = {
        offset: 0,
        "--mo-esx": shape.getPropertyValue("--mo-esx"),
        "--mo-esy": shape.getPropertyValue("--mo-esy"),
      };
      for (const animation of [...mascotMotion, ...presses]) animation.cancel();
      mascot.animate([settle], { duration: 160, easing: "ease-out" });
      eyes.animate([lids], { duration: 160, easing: "ease-out" });
      for (const animation of layerMotion) animation.pause();
      void layer.animate([{ opacity: 1 }, { opacity: 0 }], 140).finished.then(() => {
        for (const animation of layerMotion) animation.cancel();
        layer.remove();
      });
    },
  };
}

// The ball drops into the first week right of the mascot and bounces lower
// and shorter toward today. Two small bounces settle it there; it turns to
// face the mascot, closes its eyes, and tucks into today's cell.
function animateBounce(scene: Scene): Motion {
  const { layer, weeks, local, body, source, radius, cellSize } = scene;
  layer.dataset.play = "bounce";
  const today = weeks.at(-1)!.at(-1)!;

  // At a steady horizontal speed, a hop covers columns in proportion to
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

  const points = cells.map(local);
  const todayPoint = points.at(-1)!;
  const path = trajectory(LEAD_MS, source);
  const landings = points.map((to, landing) => {
    path.hop(to, landing === 0 ? cellSize : heights[landing - 1]);
    return { at: path.clock, point: to, squash: 1 };
  });
  // The settling bounces keep a pixel floor so they stay visible, and long
  // enough for their own squash, on the smallest cells.
  const settled = [Math.max(3, cellSize * 0.3), 1.5].map((height, bounce) => {
    path.hop(todayPoint, height);
    return { at: path.clock, point: todayPoint, squash: 0.5 / (bounce + 1) };
  });
  const rested = path.clock;
  const turned = rested + 280;
  const sleeping = turned + 420;
  const tucked = sleeping + 380;
  const end = tucked + 700;

  const ball = sprite(layer, "empty-home-play-ball");
  // Negative x scale turns it to face the mascot; passing through zero reads
  // as turning around.
  const looks: Frame[] = [
    { at: LEAD_MS, scale: "0.3", opacity: 0 },
    { at: LEAD_MS + 140, scale: "1", opacity: 1 },
    ...[...landings, ...settled].flatMap(({ at, squash }) => [
      { at: at - 20, scale: "1" },
      { at, scale: `${1 + 0.3 * squash} ${1 - 0.28 * squash}` },
      { at: at + 50, scale: "1" },
    ]),
    { at: rested + 100, scale: "1" },
    { at: turned, scale: "-1 1" },
    { at: sleeping + 120, scale: "-1 1", opacity: 1 },
    { at: sleeping + 260, scale: "-1.18 0.82", opacity: 1 },
    { at: tucked, scale: "-0.3 0.3", opacity: 0 },
  ];
  const lids = ["::before", "::after"].map((pseudoElement) =>
    ball.body.animate([{ scale: "1 0.15" }], { duration: 140, delay: sleeping, fill: "forwards", pseudoElement }),
  );
  const layerMotion = [...fly(ball, path, looks, end), ...lids, ripple(layer, todayPoint, tucked - 120)];
  const presses = [
    ...landings.map(({ at }, landing) => cells[landing].animate(PRESS, { duration: 380, delay: at })),
    // Today takes the ball in with a soft swell.
    today.animate([{ scale: "1" }, { offset: 0.4, scale: "1.3" }, { scale: "1" }], {
      duration: 420,
      delay: tucked - 140,
      easing: "ease-out",
    }),
  ];

  const mascotMotion = animateMascot(scene, end, [
    { at: 190, ...source },
    ...[...landings, ...settled].map(({ at, point }) => ({ at, ...point })),
    { at: end - 300, ...todayPoint },
  ], [...THROW, ...gesture(sleeping, TILT)], [[sleeping - 40, 900, SMILE]]);
  return { layerMotion, presses, mascotMotion };
}

// The snake grazes rows 2–4 in a serpentine, growing a segment for each day
// with activity; empty days are bare ground. It then U-turns under its own
// neck and bites its body, which takes at least three segments. Game over:
// the body blinks, the head hops off the board, and the pasture grows back.
function animateSnake(scene: Scene): Motion | undefined {
  const { layer, weeks, local, source, cellSize } = scene;
  const width = Math.min(VIGNETTE_WEEKS, weeks.length);
  const isFood = (cell: HTMLElement) => Number(cell.dataset.level) > 0;
  const routeOf = (columns: HTMLElement[][]) => [
    ...[2, 3, 4].flatMap((row, turn) =>
      columns.map((_, index) => columns[turn % 2 ? width - 1 - index : index][row]),
    ),
    columns[width - 1][5],
    columns[width - 2][5],
  ];
  const starts = weeks
    .slice(0, weeks.length - width + 1)
    .flatMap((_, first) => (routeOf(weeks.slice(first, first + width)).filter(isFood).length >= 3 ? [first] : []));
  if (!starts.length) return undefined;
  layer.dataset.play = "snake";
  const first = starts[Math.floor(Math.random() * starts.length)];
  const columns = weeks.slice(first, first + width);
  const route = routeOf(columns);
  const points = route.map(local);
  const last = route.length - 1;

  const path = trajectory(LEAD_MS, source);
  path.hop(points[0], cellSize * 2);
  const landed = path.clock;
  const stepAt = (index: number) => landed + index * SNAKE_STEP_MS;
  for (const point of points.slice(1)) path.go(point, SNAKE_STEP_MS);
  // Lunge at the body, recoil, and stay stunned for a beat.
  const cornered = points[last];
  const contact = { x: cornered.x, y: (cornered.y + points[last - 3].y) / 2 };
  path.go({ x: cornered.x, y: cornered.y + (contact.y - cornered.y) * 0.9 }, 80);
  const biteAt = path.clock;
  path.go(cornered, 140);
  path.go(cornered, 280);
  const knockedAt = path.clock;
  const floor = local(columns[width - 2][6]).y;
  const apex = path.hop({ x: cornered.x + cellSize * 0.6, y: floor + cellSize * 3 }, cellSize * 2.5);
  const offBoard = apex.at + Math.sqrt((2 * (floor - apex.y)) / GRAVITY);

  // Segment k joins at the tail when the k-th day is eaten and trails the
  // head by k steps; the body tapers toward the tail. When the game ends, the
  // body blinks, then one wave runs back along the route: each segment pops
  // where it lies and the eaten days grow back right behind it.
  const eaten = route.flatMap((cell, index) => (isFood(cell) ? [index] : []));
  const unzipAt = knockedAt + 150;
  const wave = Math.min(35, 1_000 / last);
  const regrowAt = (index: number) => unzipAt + (last - 1 - index) * wave + 60;
  const giggleAt = knockedAt + 350;
  const end = Math.max(regrowAt(0) + 260, giggleAt + 720, path.clock) + 80;

  // Segments come first so the head stays on top of its body.
  const layerMotion = eaten.flatMap((eatenIndex, index) => {
    const lag = index + 1;
    const start = Math.max(0, eatenIndex - lag);
    const trail = trajectory(stepAt(eatenIndex), points[start]);
    for (let step = start + 1; step <= last - lag; step++) trail.go(points[step], stepAt(step + lag) - trail.clock);
    const size = 0.85 - (0.3 * index) / Math.max(1, eaten.length - 1);
    const pop = unzipAt + index * wave;
    return fly(sprite(layer, "empty-home-play-segment"), trail, [
      { at: stepAt(eatenIndex), scale: "0.3", opacity: 0 },
      { at: stepAt(eatenIndex) + 120, scale: `${size}`, opacity: 1 },
      { at: biteAt + 60, opacity: 1 },
      { at: biteAt + 120, opacity: 0.2 },
      { at: biteAt + 220, opacity: 1 },
      { at: biteAt + 280, opacity: 0.2 },
      { at: biteAt + 380, opacity: 1 },
      { at: pop - 40, scale: `${size}`, opacity: 1 },
      { at: pop + 40, scale: `${size * 1.35}`, opacity: 1 },
      { at: pop + 130, scale: "0", opacity: 0 },
    ], end);
  });
  // The head faces along each row: left on the middle row and after the U-turn.
  layerMotion.push(...fly(sprite(layer, "empty-home-play-ball"), path, [
    { at: LEAD_MS, scale: "0.3", opacity: 0 },
    { at: LEAD_MS + 140, scale: "1", opacity: 1 },
    { at: landed - 20, scale: "1" },
    { at: landed, scale: "1.3 0.72" },
    { at: landed + 50, scale: "1" },
    { at: stepAt(width - 1), scale: "1" },
    { at: stepAt(width), scale: "-1 1" },
    { at: stepAt(2 * width - 1), scale: "-1 1" },
    { at: stepAt(2 * width), scale: "1" },
    { at: stepAt(last - 1), scale: "1" },
    { at: stepAt(last), scale: "-1 1", rotate: "0deg" },
    { at: biteAt, scale: "-1.2 0.8" },
    { at: biteAt + 120, scale: "-1 1", rotate: "-14deg" },
    { at: biteAt + 190, rotate: "12deg" },
    { at: biteAt + 260, rotate: "-8deg" },
    { at: knockedAt, rotate: "0deg", opacity: 1 },
    { at: offBoard - 60, opacity: 1 },
    { at: path.clock, scale: "-1 1", rotate: "180deg", opacity: 0 },
  ], end), ripple(layer, contact, biteAt - 10, 2));

  const presses = eaten.map((index) => {
    const eatenAt = stepAt(index);
    const back = regrowAt(index);
    return route[index].animate(timeline([
      { at: eatenAt, scale: "1", opacity: 1 },
      { at: eatenAt + 120, scale: "0.15", opacity: 0 },
      { at: back, scale: "0.15", opacity: 0 },
      { at: back + 140, scale: "1.15", opacity: 1 },
      { at: back + 260, scale: "1" },
    ], end), end);
  });

  const mascotMotion = animateMascot(scene, end, [
    { at: 190, ...source },
    ...points.map((point, index) => ({ at: stepAt(index), ...point })),
    { at: biteAt, ...contact },
    apex,
    { at: path.clock, ...path.point },
    { at: unzipAt, ...points[last - 1] },
    { at: regrowAt(0), ...points[0] },
  ], [...THROW, ...gesture(biteAt, STARTLE), ...gesture(giggleAt, GIGGLE)], [
    [biteAt - 30, 380, WIDE],
    [giggleAt, 700, SMILE],
  ]);
  return { layerMotion, presses, mascotMotion };
}

// The lower rows dim into a court under three rows of bricks, with the paddle
// on the bottom row. Rally shots break bricks in row 2, the last one mid-court.
// The final shot is charged and goes straight up that fresh gap, drills the
// two bricks above, and bursts over the board. The wall then rebuilds in one
// sweep, like the next level loading.
function animateBreakout(scene: Scene): Motion {
  const { layer, weeks, local, source, cellSize } = scene;
  layer.dataset.play = "breakout";
  const width = Math.min(VIGNETTE_WEEKS, weeks.length);
  const first = Math.floor((weeks.length - width) * (0.25 + Math.random() * 0.5));
  const columns = weeks.slice(first, first + width);
  const point = (column: number, row: number) => local(columns[column][row]);
  const paddle = sprite(layer, "empty-home-play-paddle");
  const ball = sprite(layer, "empty-home-play-ball");
  const ballRadius = ball.body.getBoundingClientRect().width / 2;
  const paddleY = point(0, 6).y;
  const floorY = paddleY - ballRadius;
  const under = (column: number, row: number) => ({
    x: point(column, row).x,
    y: point(column, row).y + cellSize / 2 + ballRadius,
  });
  const targets = [...new Set([0.15, 0.7, 0.35, 0.9, 0.5].map((share) => Math.round(share * (width - 1))))];

  const path = trajectory(LEAD_MS, source);
  let contactX = point(0, 6).x + cellSize * 1.5;
  path.hop({ x: contactX, y: floorY }, cellSize);
  const paddlePath = trajectory(LEAD_MS + 200, { x: contactX, y: paddleY });
  const gaze: TimedPoint[] = [{ at: 190, ...source }, { at: path.clock, ...path.point }];
  const broken = new Map<HTMLElement, number>();
  const ripples: Animation[] = [];
  targets.forEach((column, index) => {
    path.go(under(column, 2), 420);
    broken.set(columns[column][2], path.clock);
    ripples.push(ripple(layer, point(column, 2), path.clock, 2));
    gaze.push({ at: path.clock, ...path.point });
    // The paddle picks a contact point that sends the next shot toward
    // another brick, rather than tracking directly beneath the ball.
    const next = targets[index + 1];
    contactX = next === undefined ? point(column, 2).x : (point(column, 2).x + point(next, 2).x) / 2;
    paddlePath.go({ x: contactX, y: paddleY }, path.clock + 320 - paddlePath.clock);
    path.go({ x: contactX, y: floorY }, 380);
    gaze.push({ at: path.clock, ...path.point });
  });

  const column = targets.at(-1)!;
  path.go(path.point, 240);
  const charged = path.clock;
  const burst = path.hop({ x: contactX, y: point(column, 0).y - cellSize * 2.5 }, 0);
  const popAt = path.clock;
  // Solve the decelerating rise for the moment the ball meets each brick.
  const speed = Math.sqrt(2 * GRAVITY * (floorY - burst.y));
  const drilled = [1, 0].map((row) => {
    const distance = floorY - under(column, row).y;
    const at = charged + (speed - Math.sqrt(speed ** 2 - 2 * GRAVITY * distance)) / GRAVITY;
    broken.set(columns[column][row], at);
    return { cell: columns[column][row], at };
  });
  gaze.push(burst);
  paddlePath.go(paddlePath.point, popAt - paddlePath.clock);
  paddlePath.hop(paddlePath.point, cellSize * 0.8);
  const sweepAt = (index: number) => popAt + 220 + index * 40;
  const sweepEnd = sweepAt(width - 1) + 260;
  gaze.push({ at: sweepAt(0), ...point(0, 3) }, { at: sweepAt(width - 1), ...point(width - 1, 3) });
  const end = Math.max(sweepEnd + 40, popAt + 220 + 640) + 60;

  // Debris wears the colour of the brick it came from.
  const debris = drilled.flatMap(({ cell, at }, row) => [-1.5, -0.4, 1.3].flatMap((share) => {
    const from = local(cell);
    const direction = row ? -1 : 1;
    const trail = trajectory(at, from);
    trail.hop(
      { x: from.x + share * direction * cellSize, y: from.y + cellSize * (1.5 + Math.abs(share)) },
      cellSize * (1 + 0.4 * row),
    );
    const shard = sprite(layer, "empty-home-play-shard");
    shard.body.style.background = getComputedStyle(cell).backgroundColor;
    return fly(shard, trail, [
      { at, scale: "0.4", opacity: 0, rotate: "0deg" },
      { at: at + 60, scale: "1", opacity: 1 },
      { at: trail.clock - 160, opacity: 1 },
      { at: trail.clock, opacity: 0, rotate: `${share * direction * 160}deg` },
    ], end);
  }));
  const layerMotion = [
    ...fly(paddle, paddlePath, [
      { at: LEAD_MS + 200, scale: "0.3", opacity: 0 },
      { at: LEAD_MS + 340, scale: "1", opacity: 1 },
      { at: charged - 200, scale: "1" },
      { at: charged - 20, scale: "1.1 0.55" },
      { at: charged + 80, scale: "0.94 1.4" },
      { at: charged + 200, scale: "1" },
      { at: sweepEnd - 240, scale: "1", opacity: 1 },
      { at: sweepEnd, scale: "0.6 1", opacity: 0 },
    ], end),
    ...fly(ball, path, [
      { at: LEAD_MS, scale: "0.3", opacity: 0 },
      { at: LEAD_MS + 140, scale: "1", opacity: 1 },
      { at: charged - 200, scale: "1" },
      { at: charged - 20, scale: "1.25 0.75" },
      { at: charged + 60, scale: "0.8 1.3" },
      { at: popAt - 60, scale: "1", opacity: 1 },
      { at: popAt + 60, scale: "1.5", opacity: 1 },
      { at: popAt + 180, scale: "0.2", opacity: 0 },
    ], end),
    ...debris,
    ...ripples,
    ripple(layer, burst, popAt, 3.2),
  ];

  // Unbroken bricks shimmer as the sweep passes, broken ones pop back in,
  // and the court lights up again.
  const presses = columns.flatMap((week, index) => week.map((cell, row) => {
    const sweep = sweepAt(index);
    const hitAt = broken.get(cell);
    const frames: Frame[] = row > 2
      ? [
        { at: LEAD_MS, opacity: 1 },
        { at: LEAD_MS + 400, opacity: 0.16 },
        { at: sweep, opacity: 0.16 },
        { at: sweep + 240, opacity: 1 },
      ]
      : hitAt === undefined
        ? [{ at: sweep, scale: "1" }, { at: sweep + 90, scale: "0.8" }, { at: sweep + 220, scale: "1" }]
        : [
          { at: hitAt, scale: "1", opacity: 1 },
          { at: hitAt + 120, scale: "0.2", opacity: 0 },
          { at: sweep, scale: "0.2", opacity: 0 },
          { at: sweep + 150, scale: "1.15", opacity: 1 },
          { at: sweep + 260, scale: "1" },
        ];
    return cell.animate(timeline(frames, end), end);
  }));

  const mascotMotion = animateMascot(scene, end, gaze, [...THROW, ...gesture(popAt + 160, CHEER)], [
    [popAt - 80, 300, WIDE],
    [popAt + 220, 640, SMILE],
  ]);
  return { layerMotion, presses, mascotMotion };
}

function animateMascot(
  { mascot, eyes, body }: Scene,
  end: number,
  gaze: TimedPoint[],
  poses: Pose[],
  shapes: [at: number, duration: number, shape: Keyframe][],
): Animation[] {
  // Gaze uses the pointer channels the greeting already follows. While these
  // animations run they override the pointer; the implicit end keyframe
  // returns the face to it. Targets from several tracks are put in time order.
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
  return [
    mascot.animate(poses.map(([at, transform]) => ({ offset: at / end, transform, easing: "ease-in-out" })), end),
    mascot.animate([...gaze].sort((a, b) => a.at - b.at).map((point) => ({ offset: point.at / end, ...look(point) })), end),
    // Each eye shape eases in and out over the first and last fifth of its hold.
    ...shapes.map(([at, duration, shape]) =>
      eyes.animate([{ offset: 0.2, ...shape }, { offset: 0.8, ...shape }], { duration, delay: at }),
    ),
  ];
}

function gesture(start: number, poses: Pose[]): Pose[] {
  return poses.map(([at, transform]) => [start + at, transform]);
}

/**
 * A path on its scene's clock. X and y are separate tracks, so a hop's y
 * easing traces an exact parabola while x moves at a steady pace.
 */
function trajectory(start: number, from: Point) {
  const xs: Frame[] = [];
  const ys: Frame[] = [];
  let clock = start;
  let point = from;
  const mark = () => {
    xs.push({ at: clock, translate: `${point.x}px 0` });
    ys.push({ at: clock, translate: `0 ${point.y}px` });
  };
  mark();
  return {
    xs,
    ys,
    get clock() {
      return clock;
    },
    get point() {
      return point;
    },
    /** Straight moves; moving to the current point waits there. */
    go(to: Point, ms: number) {
      clock += ms;
      point = to;
      mark();
    },
    /** Hops `height` above the higher end, or rises to rest at `to` with 0. */
    hop(to: Point, height: number): TimedPoint {
      const top = Math.min(point.y, to.y) - height;
      const rise = Math.sqrt((2 * (point.y - top)) / GRAVITY);
      const fall = Math.sqrt((2 * (to.y - top)) / GRAVITY);
      ys.at(-1)!.easing = RISE;
      ys.push({ at: clock + rise, translate: `0 ${top}px`, easing: FALL });
      const apex = { at: clock + rise, x: point.x + ((to.x - point.x) * rise) / (rise + fall), y: top };
      clock += rise + fall;
      point = to;
      mark();
      return apex;
    },
  };
}

function timeline(frames: Frame[], end: number): Keyframe[] {
  return frames.map(({ at, ...frame }) => ({ ...frame, offset: at / end }));
}

function append(parent: Element, className: string): HTMLSpanElement {
  return parent.appendChild(Object.assign(document.createElement("span"), { className }));
}

// A sprite's track carries x and its body carries y and its looks.
function sprite(layer: Element, className: string): Sprite {
  const track = append(layer, className);
  return { track, body: append(track, "") };
}

/** Plays a sprite's path and looks, holding their first and last frames outside them. */
function fly({ track, body }: Sprite, path: Trajectory, looks: Frame[], end: number): Animation[] {
  const hold = (frames: Frame[]) => timeline([{ ...frames[0], at: 0 }, ...frames, { ...frames.at(-1)!, at: end }], end);
  return [track.animate(hold(path.xs), end), body.animate(hold(path.ys), end), body.animate(hold(looks), end)];
}

function ripple(layer: Element, { x, y }: Point, delay: number, size = 2.6): Animation {
  const ring = append(layer, "empty-home-play-ripple");
  ring.style.translate = `${x}px ${y}px`;
  return ring.animate([{ scale: "0.6", opacity: 0.7 }, { scale: String(size), opacity: 0 }], {
    duration: 640,
    delay,
    easing: "cubic-bezier(0.22, 1, 0.36, 1)",
  });
}
