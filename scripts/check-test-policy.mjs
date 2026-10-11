#!/usr/bin/env node
/**
 * Merge-gate test policy: product tests must not read stylesheet source
 * to pin declarations. Fixture names, tool-call paths, and CSS module
 * mocks are not this check.
 */

import { readdirSync, readFileSync, statSync } from "node:fs";
import { relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(fileURLToPath(new URL("..", import.meta.url)));
const scanRoots = [
  "cmd",
  "internal",
  "plugins",
  "prompts",
  "packages",
  "clients",
  "desktop/src",
  "scripts",
];
const cssSourceRead =
  /\b(?:readFileSync|readFile)\s*\(\s*(?:[^)]|\([^)]*\))*?\.css|\bread\s*\(\s*['"][^'"]+\.css['"]/;
const skipDirs = new Set(["node_modules", "vendor", "dist", "out"]);

if (process.argv.includes("--self-test")) {
  selfTest();
} else {
  const violations = [];
  for (const root of scanRoots) {
    const abs = resolve(repoRoot, root);
    try {
      statSync(abs);
    } catch {
      continue;
    }
    walk(abs, violations);
  }
  if (violations.length > 0) {
    console.error("CSS-source tests are not allowed in the merge gate:");
    for (const path of violations) console.error(`  ${path}`);
    console.error(
      "Assert observable behavior instead.",
    );
    process.exit(1);
  }
  console.log("test policy: no stylesheet-source tests");
}

function walk(dir, violations) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (skipDirs.has(entry.name)) continue;
      walk(resolve(dir, entry.name), violations);
      continue;
    }
    if (!isTestFile(entry.name)) continue;
    const path = resolve(dir, entry.name);
    const rel = relative(repoRoot, path).split("\\").join("/");
    const source = readFileSync(path, "utf8");
    if (cssSourceRead.test(source)) violations.push(rel);
  }
}

function isTestFile(name) {
  return (
    /\.(test|spec)\.(ts|tsx|js|mjs|cjs)$/.test(name) || /_test\.go$/.test(name)
  );
}

function selfTest() {
  if (
    !cssSourceRead.test(
      'readFileSync(resolve(__dirname, "styles/sidebar.css"), "utf-8")',
    )
  ) {
    fail("should flag a CSS path read");
  }
  if (!cssSourceRead.test('read("src/renderer/styles/sidebar.css")')) {
    fail("should flag a CSS helper read");
  }
  if (cssSourceRead.test('name: "fixture.css"')) {
    fail("should not flag CSS fixture names");
  }
  if (cssSourceRead.test('makeReadFileTool("src/turns.css")')) {
    fail("should not flag tool-call path fixtures");
  }
  if (cssSourceRead.test('vi.mock("./styles/workspace-pdf-preview.css?inline"')) {
    fail("should not flag CSS module mocks");
  }
  console.log("test policy self-test ok");
}

function fail(message) {
  console.error(message);
  process.exit(1);
}
