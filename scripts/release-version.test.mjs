import { test } from "node:test";
import assert from "node:assert/strict";
import { nextVersion, requireCalver, nativeBuildNumber } from "./release-version.mjs";

test("release dates follow UTC across month and year boundaries", () => {
  assert.equal(nextVersion("2026.9.2", new Date("2026-09-21T10:00:00Z")), "2026.9.21");
  assert.equal(nextVersion("2026.9.30", new Date("2026-10-01T00:30:00Z")), "2026.10.1");
  assert.equal(nextVersion("2026.12.31", new Date("2027-01-01T01:00:00+01:00")), "2027.1.1");
  assert.throws(() => nextVersion("2026.9.22", new Date("2026-09-21T23:00:00Z")));
  assert.throws(() => nextVersion("2026.9.21", new Date("2026-09-21T10:00:00Z")));
});

test("calendar validation rejects nonexistent dates and padded components", () => {
  assert.equal(requireCalver("v2028.2.29-rc.1"), "2028.2.29-rc.1");
  for (const value of ["2026.2.29", "2026.4.31", "2026.9.100", "2026.09.21", "2026.9.01"]) {
    assert.throws(() => requireCalver(value));
  }
});

test("candidate promotion keeps native build numbers increasing", () => {
  const candidate = "2026.9.21-rc.1";
  const final = nextVersion(candidate, new Date("2026-09-21T10:00:00Z"));
  assert.equal(final, "2026.9.21");
  assert.ok(Number(nativeBuildNumber(candidate)) < Number(nativeBuildNumber(final)));
  assert.ok(Number(nativeBuildNumber("2026.9.2")) < Number(nativeBuildNumber(final)));
});
