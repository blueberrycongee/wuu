import { DURATION, renderFilm } from "./film";
import { artReady } from "./art";

// Preview: space plays/pauses, arrows step a frame (shift: one second), and
// ?t=12.5 opens at a time. The capture script drives `window.promo.render`.
const canvas = document.querySelector("canvas")!;
const ctx = canvas.getContext("2d")!;
const button = document.querySelector("button")!;
const scrubber = document.querySelector("input")!;
const clock = document.querySelector("output")!;
scrubber.max = String(DURATION);

let time = Number(new URLSearchParams(location.search).get("t") ?? 0);
let playing = false;
let last = 0;

function draw(t: number) {
  time = Math.max(0, Math.min(DURATION, t));
  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  renderFilm(ctx, time);
  scrubber.value = String(time);
  clock.value = `${time.toFixed(2)}s`;
}

function tick(now: number) {
  if (!playing) return;
  draw(time + (now - last) / 1000);
  last = now;
  if (time >= DURATION) toggle(false);
  else requestAnimationFrame(tick);
}

function toggle(play = !playing) {
  playing = play;
  button.textContent = playing ? "Pause" : "Play";
  if (playing) {
    if (time >= DURATION) time = 0;
    last = performance.now();
    requestAnimationFrame(tick);
  }
}

await artReady;
button.addEventListener("click", () => toggle());
scrubber.addEventListener("input", () => { toggle(false); draw(Number(scrubber.value)); });
addEventListener("keydown", (event) => {
  if (event.key === " ") { event.preventDefault(); toggle(); }
  if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
    toggle(false);
    draw(time + (event.key === "ArrowRight" ? 1 : -1) * (event.shiftKey ? 1 : 1 / 60));
  }
});

Object.assign(window, { promo: { duration: DURATION, canvas, render: draw } });
draw(time);
