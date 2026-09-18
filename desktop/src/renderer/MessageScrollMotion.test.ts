import { expect, it } from "vitest";
import { createMessageScrollMotion } from "./MessageScrollMotion";

it("does not jump or change velocity abruptly when a late layout change moves the target", () => {
  const original = createMessageScrollMotion(100, 1100, 360);
  const changed = createMessageScrollMotion(100, 1100, 360);
  original(0, 1100); changed(0, 1100);
  expect(changed(300, 700).position).toBe(original(300, 1100).position);
  expect(Math.abs(changed(300.01, 700).position - original(300.01, 1100).position)).toBeLessThan(0.001);
  expect(changed(360, 700)).toEqual({ position: 700, done: true });
});

it("keeps the same trajectory at different display refresh rates", () => {
  const run = (step: number) => {
    const sample = createMessageScrollMotion(0, 1000, 360);
    sample(0, 1000);
    for (let now = step; now < 200; now += step) sample(now, 1000);
    return sample(200, 1000);
  };
  expect(run(1000 / 60)).toEqual(run(1000 / 120));
});

it("settles short moves earlier and never overshoots a fixed destination", () => {
  const short = createMessageScrollMotion(0, 16, 360);
  const long = createMessageScrollMotion(0, 1000, 360);
  short(0, 16); long(0, 1000);
  expect(short(200, 16).done).toBe(true);
  expect(long(200, 1000).done).toBe(false);
  for (let now = 208; now <= 400; now += 8) {
    const { position } = long(now, 1000);
    expect(position).toBeGreaterThanOrEqual(0);
    expect(position).toBeLessThanOrEqual(1000);
  }
});

it("retargets within the original deadline instead of appending correction animations", () => {
  const sample = createMessageScrollMotion(0, 1000, 360);
  sample(0, 1000);
  for (let now = 16; now < 360; now += 16) sample(now, 1000 + now);
  expect(sample(360, 1360)).toEqual({ position: 1360, done: true });
  expect(createMessageScrollMotion(0, 200, 0)(0, 400)).toEqual({ position: 400, done: true });
});

it("has one deceleration phase rather than a slow start followed by another acceleration", () => {
  const sample = createMessageScrollMotion(600, 200, 360);
  let previous = sample(0, 200).position;
  let previousStep = Infinity;
  for (let now = 10; now <= 360; now += 10) {
    const position = sample(now, 200).position;
    const step = previous - position;
    expect(step).toBeGreaterThanOrEqual(-0.000001);
    expect(step).toBeLessThanOrEqual(previousStep + 0.000001);
    previousStep = step;
    previous = position;
  }
});
