import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

import { build } from "esbuild";

const bundled = await build({
  entryPoints: [fileURLToPath(new URL("../src/schedule.ts", import.meta.url))],
  bundle: true,
  format: "esm",
  platform: "node",
  target: "node22.7",
  write: false,
});
const scheduleURL = `data:text/javascript;base64,${Buffer.from(bundled.outputFiles[0].text).toString("base64")}`;
const { parseCronExpression } = await import(scheduleURL);

const weekdayCases = [
  ["0", [20, 27, 34]],
  ["7", [20, 27, 34]],
  ["7-7", [20, 27, 34]],
  ["7/2", [20, 27, 34]],
  ["6-7", [20, 26, 27, 33, 34]],
  ["6,7", [20, 26, 27, 33, 34]],
  ["0,6", [20, 26, 27, 33, 34]],
  ["5-7/2", [20, 25, 27, 32, 34]],
  ["1-7/2", [20, 21, 23, 25, 27, 28, 30]],
  ["3-7/4", [20, 23, 27, 30]],
  ["6-7/2", [26, 33, 40]],
  ["5-7/3", [25, 32, 39]],
  ["0,7", [20, 27, 34]],
  ["0-7", [20, 21, 22, 23, 24, 25, 26, 27, 28]],
  ["0-7/2", [20, 22, 24, 26, 27, 29]],
  ["*", [20, 21, 22, 23, 24, 25, 26, 27, 28]],
  ["?", [20, 21, 22, 23, 24, 25, 26, 27, 28]],
  ["*/2", [20, 22, 24, 26, 27, 29]],
  ["*/7", [20, 27, 34]],
];

for (const [dayOfWeek, days] of weekdayCases) {
  test(`nextRun preserves weekday selection ${dayOfWeek}`, () => {
    const expression = parseCronExpression(`0 9 * * ${dayOfWeek}`);
    let anchor = new Date("2026-09-19T12:00:00Z");
    for (const [index, day] of days.entries()) {
      const got = expression.nextRun(anchor, "UTC");
      const want = new Date(Date.UTC(2026, 8, day, 9, 0, 0));
      assert.equal(got.toISOString(), want.toISOString(), `occurrence ${index} after ${anchor.toISOString()}`);
      anchor = got;
    }
  });
}

const boundaryCases = [
  ["before-minute", "UTC", "2026-09-20T08:59:59Z", "2026-09-20T09:00:00Z"],
  ["at-minute", "UTC", "2026-09-20T09:00:00Z", "2026-09-27T09:00:00Z"],
  ["within-minute", "UTC", "2026-09-20T09:00:00.001Z", "2026-09-27T09:00:00Z"],
  ["local-sunday", "Asia/Shanghai", "2026-09-19T23:59:59Z", "2026-09-20T01:00:00Z"],
  ["spring-DST", "America/New_York", "2026-03-07T12:00:00Z", "2026-03-08T13:00:00Z"],
  ["autumn-DST", "America/New_York", "2026-10-31T12:00:00Z", "2026-11-01T14:00:00Z"],
];

for (const [name, timezone, after, want] of boundaryCases) {
  for (const dayOfWeek of ["0", "7"]) {
    test(`nextRun handles ${name} with Sunday alias ${dayOfWeek}`, () => {
      const expression = parseCronExpression(`0 9 * * ${dayOfWeek}`);
      const got = expression.nextRun(new Date(after), timezone);
      assert.equal(got.toISOString(), new Date(want).toISOString());
    });
  }
}

for (const dayOfWeek of ["0", "7"]) {
  test(`nextRun unions day-of-month and Sunday alias ${dayOfWeek}`, () => {
    const expression = parseCronExpression(`0 9 21 * ${dayOfWeek}`);
    let anchor = new Date("2026-09-19T12:00:00Z");
    for (const day of [20, 21, 27]) {
      const got = expression.nextRun(anchor, "UTC");
      const want = new Date(Date.UTC(2026, 8, day, 9, 0, 0));
      assert.equal(got.toISOString(), want.toISOString(), `after ${anchor.toISOString()}`);
      anchor = got;
    }
  });
}

test("parseCronExpression rejects invalid weekdays", () => {
  for (const dayOfWeek of ["8", "14", "-1", "6-8", "7-0", "7/0", "5-7/0", "0,8", "7,", "5-7/-2"]) {
    assert.throws(() => parseCronExpression(`0 9 * * ${dayOfWeek}`), undefined, dayOfWeek);
  }
});
