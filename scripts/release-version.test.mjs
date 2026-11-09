import { test } from "node:test";
import assert from "node:assert/strict";
import { nextVersion, requireCalver, nativeBuildNumber } from "./release-version.mjs";

test("release dates follow UTC across month and year boundaries", () => {
  assert.equal(nextVersion("2026.902.4", new Date("2026-09-21T10:00:00Z")), "2026.921.0");
  assert.equal(nextVersion("2026.930.9", new Date("2026-10-01T00:30:00Z")), "2026.1001.0");
  assert.equal(nextVersion("2026.1231.3", new Date("2027-01-01T01:00:00+01:00")), "2027.101.0");
  assert.throws(() => nextVersion("2026.922.0", new Date("2026-09-21T23:00:00Z")));
});

test("same-day releases advance the release index", () => {
  const day = new Date("2026-11-08T10:00:00Z");
  assert.equal(nextVersion("2026.1108.0", day), "2026.1108.1");
  assert.equal(nextVersion("2026.1108.8", day), "2026.1108.9");
  assert.throws(() => nextVersion("2026.1108.9", day));
});

test("legacy YYYY.M.D versions migrate to the packed form", () => {
  const day = new Date("2026-11-08T10:00:00Z");
  assert.equal(nextVersion("2026.11.8", day), "2026.1108.1");
  assert.equal(nextVersion("2026.11.8-rc.1", day), "2026.1108.0");
  assert.equal(nextVersion("2026.11.8", new Date("2026-11-09T00:30:00Z")), "2026.1109.0");
});

test("calendar validation rejects nonexistent dates and padded components", () => {
  assert.equal(requireCalver("v2028.2.29-rc.1"), "2028.2.29-rc.1");
  assert.equal(requireCalver("v2028.229.0-rc.1"), "2028.229.0-rc.1");
  for (const value of ["2026.229.0", "2026.431.0", "2026.1108.10", "2026.1301.0", "2026.1100.0", "2026.09.21", "2026.9.01"]) {
    assert.throws(() => requireCalver(value));
  }
});

test("candidate promotion keeps native build numbers increasing", () => {
  const candidate = "2026.1108.0-rc.1";
  const final = nextVersion(candidate, new Date("2026-11-08T10:00:00Z"));
  assert.equal(final, "2026.1108.0");
  assert.ok(Number(nativeBuildNumber(candidate)) < Number(nativeBuildNumber(final)));
  assert.ok(Number(nativeBuildNumber("2026.11.8")) < Number(nativeBuildNumber(final)));
  assert.ok(Number(nativeBuildNumber(final)) < Number(nativeBuildNumber("2026.1108.1")));
});
