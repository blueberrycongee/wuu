#!/usr/bin/env node

import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;
const nativeAndroid = "clients/native/android/app/build.gradle.kts";
const nativeIOS = "clients/native/ios/Wuu.xcodeproj/project.pbxproj";
// The same-day release index is a single digit because the native build
// number reserves two digits for it, leaving two for the build iteration.
const maxDailyIndex = 9;

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const [command = "check", rawVersion] = process.argv.slice(2);
  try {
    switch (command) {
      case "check":
        checkVersions(rawVersion);
        break;
      case "sync":
        syncVersions();
        break;
      case "next":
        console.log(nextVersion(currentVersion(), new Date()));
        break;
      case "prepare":
        prepare(rawVersion);
        break;
      case "notes":
        process.stdout.write(`${releaseNotes(requireCalver(rawVersion))}\n`);
        break;
      default:
        fail("usage: release-version.mjs check [v<version>] | sync | next | prepare [version] | notes <version>");
    }
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}

function checkVersions(expectedTag) {
  const version = requireCalver(currentVersion());
  const files = versionFiles(version);
  for (const [path, expected] of files) {
    if (read(path) !== expected) fail(`release version differs from VERSION=${version}: ${path}; run make version-sync`);
  }
  if (expectedTag) {
    const expected = requireCalver(expectedTag);
    if (version !== expected) fail(`release version ${version} does not match tag v${expected}`);
    releaseNotes(expected);
  }
  console.log(`release versions match: ${version}`);
}

function syncVersions() {
  const version = requireCalver(currentVersion());
  writeFiles(versionFiles(version));
  console.log(`synchronized product version ${version}`);
}

function prepare(rawNextVersion) {
  const current = requireCalver(currentVersion());
  const now = new Date();
  const parsed = parseProductVersion(rawNextVersion || nextVersion(current, now));
  if (parsed.legacy) {
    fail(`new releases use YYYY.MMDD.P (e.g. ${parsed.year}.${parsed.month * 100 + parsed.day}.0); got ${parsed.version}`);
  }
  const version = parsed.version;
  const currentParsed = parseProductVersion(current);
  const versionDay = Date.UTC(parsed.year, parsed.month - 1, parsed.day);
  const currentDay = Date.UTC(currentParsed.year, currentParsed.month - 1, currentParsed.day);
  if (versionDay < currentDay) fail(`new version ${version} is dated before current version ${current}`);
  if (compareVersions(version, current) <= 0) fail(`new version ${version} must be greater than current version ${current}`);

  releaseNotes(version);
  const files = versionFiles(version);
  files.set("VERSION", `${version}\n`);
  writeFiles(files);
  console.log(`prepared release ${version}`);
}

function releaseNotes(version) {
  const path = `docs/releases/${version}.md`;
  let body;
  try {
    body = read(path).trim();
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    fail(`release notes missing: ${path}; write and review the notes before preparing a release`);
  }
  if (!body) fail(`release notes are empty: ${path}`);
  return `# wuu v${version}\n\n${body}`;
}

function versionFiles(version) {
  const files = new Map();
  for (const path of ["desktop/package.json", "desktop/package-lock.json"]) {
    const json = JSON.parse(read(path));
    json.version = version;
    if (path.endsWith("package-lock.json")) {
      if (!json.packages?.[""]) fail(`${path} is missing its root package entry`);
      json.packages[""].version = version;
    }
    files.set(path, `${JSON.stringify(json, null, 2)}\n`);
  }

  const buildNumber = nativeBuildNumber(version);
  let android = read(nativeAndroid).replace(/(\bversionName\s*=\s*")[^"\n]+(")/, `$1${version}$2`);
  android = android.replace(/(\bversionCode\s*=\s*)\d+(\b)/, `$1${buildNumber}$2`);
  files.set(nativeAndroid, android);

  const appleVersion = version.split("-")[0];
  let ios = read(nativeIOS).replace(/(\bMARKETING_VERSION\s*=\s*)[^;]+(;)/g, `$1${appleVersion}$2`);
  ios = ios.replace(/(\bCURRENT_PROJECT_VERSION\s*=\s*)\d+(;)/g, `$1${buildNumber}$2`);
  files.set(nativeIOS, ios);
  return files;
}

function nativeBuildNumber(version) {
  const parsed = parseProductVersion(version);
  // Android limits versionCode to 2,100,000,000. The numeric layout encodes
  // the release date, then the same-day index, then the build iteration:
  // candidates use their numeric prerelease suffix (1-98) and the final build
  // uses 99, so promoting a candidate never reuses a store build number.
  // Legacy YYYY.M.D versions keep their original day*100+iteration layout so
  // already published builds keep their ordering.
  const candidateNumber = parsed.modifier?.match(/(?:^|\.)(\d+)$/)?.[1];
  const buildIteration = parsed.modifier ? Math.min(Number(candidateNumber || 1), 98) : 99;
  const year = parsed.year % 100;
  const value = parsed.legacy
    ? year * 10000000 + parsed.month * 100000 + parsed.day * 100 + buildIteration
    : year * 10000000 + parsed.month * 100000 + parsed.day * 1000 + parsed.index * 100 + buildIteration;
  if (!Number.isSafeInteger(value) || value < 1 || value > 2100000000) {
    fail(`version ${version} cannot be represented as an Android build number`);
  }
  return String(value);
}

function nextVersion(rawCurrent, now) {
  const current = parseProductVersion(rawCurrent);
  const year = now.getUTCFullYear();
  const month = now.getUTCMonth() + 1;
  const day = now.getUTCDate();
  const currentDay = Date.UTC(current.year, current.month - 1, current.day);
  const today = Date.UTC(year, month - 1, day);
  if (currentDay > today) fail(`current version ${current.version} is ahead of the UTC release date`);
  if (currentDay < today) return formatVersion(year, month, day, 0);
  // Same UTC day: a candidate promotes to the final build at its own index;
  // a finished release moves to the next same-day index.
  const index = current.modifier ? current.index : current.index + 1;
  if (index > maxDailyIndex) fail(`at most ${maxDailyIndex + 1} final releases are allowed per UTC day`);
  return formatVersion(current.year, current.month, current.day, index);
}

function formatVersion(year, month, day, index) {
  return `${year}.${month * 100 + day}.${index}`;
}

function currentVersion() {
  return read("VERSION").trim();
}

function requireSemver(rawVersion) {
  const version = String(rawVersion ?? "").trim().replace(/^v/, "");
  if (!semverPattern.test(version)) fail(`invalid version: ${rawVersion ?? ""}`);
  return version;
}

// Product versions are UTC CalVer `YYYY.MMDD.P`: the release year, the UTC
// month and day packed as month*100+day (1108 is November 8), and a same-day
// release index starting at 0. A minor below 100 is a legacy `YYYY.M.D`
// release (month.minor, day.patch); existing tags and `release_tag`
// dispatches for those releases still parse, but newly prepared releases
// always use the packed form.
function parseProductVersion(rawVersion) {
  const version = requireSemver(rawVersion);
  const [base, modifier] = version.split("-", 2);
  const [year, minor, patch] = base.split(".").map(Number);
  const parsed = { version, year, modifier, legacy: minor < 100 };
  if (parsed.legacy) {
    parsed.month = minor;
    parsed.day = patch;
    parsed.index = 0;
  } else {
    parsed.month = Math.floor(minor / 100);
    parsed.day = minor % 100;
    parsed.index = patch;
    if (parsed.index > maxDailyIndex) {
      fail(`invalid product version: ${version}; the same-day release index must be 0-${maxDailyIndex}`);
    }
  }
  if (
    parsed.month < 1 ||
    parsed.month > 12 ||
    parsed.day < 1 ||
    parsed.day > new Date(Date.UTC(parsed.year, parsed.month, 0)).getUTCDate()
  ) {
    fail(`invalid product version date: ${version}`);
  }
  return parsed;
}

function requireCalver(rawVersion) {
  return parseProductVersion(rawVersion).version;
}

function compareVersions(left, right) {
  const a = semverPattern.exec(left);
  const b = semverPattern.exec(right);
  for (let index = 1; index <= 3; index += 1) {
    const difference = Number(a[index]) - Number(b[index]);
    if (difference !== 0) return difference;
  }
  if (a[4] === b[4]) return 0;
  if (!a[4]) return 1;
  if (!b[4]) return -1;
  return a[4].localeCompare(b[4], "en", { numeric: true });
}

function read(path) {
  return readFileSync(resolve(repoRoot, path), "utf8");
}

function writeFiles(files) {
  for (const [path, content] of files) {
    if (read(path) !== content) writeFileSync(resolve(repoRoot, path), content);
  }
}

function fail(message) {
  throw new Error(message);
}

export { compareVersions, nativeBuildNumber, nextVersion, requireCalver };
