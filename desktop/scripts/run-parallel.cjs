#!/usr/bin/env node
// Run multiple npm scripts in parallel and exit with the first non-zero
// exit code. Cross-platform: uses npm on Unix and npm via shell on Windows.
const { spawn } = require("node:child_process");

const scripts = process.argv.slice(2);
if (scripts.length === 0) {
  console.error("usage: run-parallel.cjs <npm-script> ...");
  process.exit(2);
}

const children = scripts.map((script) =>
  spawn("npm", ["run", script], {
    stdio: "inherit",
    // Windows npm shims are .cmd files and require a shell to execute.
    shell: process.platform === "win32",
  })
);

let exitCode = 0;
let settled = 0;

children.forEach((child) => {
  child.on("error", (error) => {
    console.error(`run-parallel: ${error.message}`);
    exitCode = 1;
    settled += 1;
    if (settled === children.length) process.exit(exitCode);
  });

  child.on("exit", (code, signal) => {
    if (code !== 0) exitCode = code || 1;
    settled += 1;
    if (settled === children.length) process.exit(exitCode);
  });
});
