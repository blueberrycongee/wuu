#!/usr/bin/env node

import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const semverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;
const calverPattern = /^[1-9]\d{3}\.(?:[1-9]|1[0-2])\.[1-9]\d*(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/;
const nativeAndroid = "clients/native/android/app/build.gradle.kts";
const nativeIOS = "clients/native/ios/Wuu.xcodeproj/project.pbxproj";

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
        process.stdout.write(`${releaseNotes(requireSemver(rawVersion))}\n`);
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
    if (read(path) !== expected) {
      fail(`release version differs from VERSION=${version}: ${path}; run make version-sync`);
    }
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
  const version = requireCalver(rawNextVersion || nextVersion(current, now));
  if (compareVersions(version, current) <= 0) fail(`new version ${version} must be greater than current version ${current}`);

  const changelog = read("CHANGELOG.md");
  const unreleased = findSection(changelog, "Unreleased");
  if (findSection(changelog, version)) fail(`CHANGELOG.md already contains ${version}`);
  const candidate = current.includes("-") && version === current.split("-")[0]
    ? findSection(changelog, current)
    : null;
  const body = [candidate?.body, unreleased?.body].filter(Boolean).join("\n\n");
  if (!body) fail("CHANGELOG.md [Unreleased] is empty; document user-visible changes first");

  const released = `## [Unreleased]\n\n## [${version}] - ${now.toISOString().slice(0, 10)}\n\n${body}\n\n`;
  const files = versionFiles(version);
  files.set("VERSION", `${version}\n`);
  const unreleasedStart = unreleased?.start ?? changelog.length;
  const unreleasedEnd = unreleased?.end ?? changelog.length;
  files.set("CHANGELOG.md", changelog.slice(0, unreleasedStart) + released + changelog.slice(unreleasedEnd));
  writeFiles(files);
  console.log(`prepared release ${version}`);
}

function releaseNotes(version) {
  const changelog = read("CHANGELOG.md");
  const section = findSection(changelog, version);
  if (!section || !section.body) fail(`CHANGELOG.md section for ${version} is empty or missing`);
  return `# wuu v${version}\n\n${section.body}`;
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
  const [base, modifier] = version.split("-", 2);
  const [year, month, sequence] = base.split(".").map(Number);
  // Android limits versionCode to 2,100,000,000. Two-digit year + month + sequence
  // keeps the value monotonic for this product while leaving room for many releases.
  // Candidate builds use their numeric prerelease suffix; the final build uses 99,
  // so promoting a candidate never reuses a store build number.
  const candidateNumber = modifier?.match(/(?:^|\.)(\d+)$/)?.[1];
  const buildIteration = modifier ? Math.min(Number(candidateNumber || 1), 98) : 99;
  const value = (year % 100) * 10000000 + month * 100000 + sequence * 100 + buildIteration;
  if (!Number.isSafeInteger(value) || value < 1 || value > 2100000000) {
    fail(`version ${version} cannot be represented as an Android build number`);
  }
  return String(value);
}

function nextVersion(rawCurrent, now) {
  const current = requireCalver(rawCurrent);
  const [year, month, sequence] = current.split("-")[0].split(".").map(Number);
  const isPrerelease = current.includes("-");
  const nextYear = now.getUTCFullYear();
  const nextMonth = now.getUTCMonth() + 1;
  if (year > nextYear || (year === nextYear && month > nextMonth)) {
    fail(`current version ${current} is ahead of the UTC release month`);
  }
  if (year === nextYear && month === nextMonth) {
    return `${year}.${month}.${isPrerelease ? sequence : sequence + 1}`;
  }
  return `${nextYear}.${nextMonth}.1`;
}

function currentVersion() {
  return read("VERSION").trim();
}

function requireSemver(rawVersion) {
  const version = String(rawVersion ?? "").trim().replace(/^v/, "");
  if (!semverPattern.test(version)) fail(`invalid version: ${rawVersion ?? ""}`);
  return version;
}

function requireCalver(rawVersion) {
  const version = requireSemver(rawVersion);
  if (!calverPattern.test(version)) fail(`invalid product version: ${version}; expected YYYY.M.N`);
  return version;
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

function findSection(changelog, version) {
  const headings = [...changelog.matchAll(/^## \[([^\]]+)\][^\n]*\n/gm)];
  const index = headings.findIndex((heading) => heading[1] === version);
  if (index < 0) return null;
  const heading = headings[index];
  const end = headings[index + 1]?.index ?? changelog.length;
  return {
    start: heading.index,
    end,
    body: changelog.slice(heading.index + heading[0].length, end).trim(),
  };
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
