#!/usr/bin/env node
// Cross-shell replacement for the POSIX `KEY=value command` prefix, which
// cmd.exe and PowerShell do not understand. Usage:
//   node scripts/env-run.cjs KEY=value [KEY2=value2 ...] command [args...]
// Leading KEY=value tokens land in the child's environment; the rest is the
// command line. npm has already prepended node_modules/.bin to PATH, so bare
// tool names (vitest, electron-vite) resolve on every platform.
const { spawn } = require("node:child_process");
const path = require("node:path");

const args = process.argv.slice(2);
const env = { ...process.env };
let index = 0;
while (index < args.length && /^[A-Za-z_][A-Za-z0-9_]*=/.test(args[index])) {
  const separator = args[index].indexOf("=");
  env[args[index].slice(0, separator)] = args[index].slice(separator + 1);
  index += 1;
}

const command = args[index];
if (!command) {
  console.error("usage: env-run.cjs KEY=value ... command [args...]");
  process.exit(2);
}

// cmd.exe rewrites the command line it is handed (it strips and re-pairs
// quotes), so arguments containing quotes or shell metacharacters must never
// pass through a shell. Only .cmd/.bat shims require cmd.exe; absolute or
// relative executable paths are spawned directly so their arguments arrive
// verbatim.
function requiresCmdShell(command) {
  if (process.platform !== "win32") {
    return false;
  }
  if (
    path.isAbsolute(command) ||
    command.includes("/") ||
    command.includes("\\")
  ) {
    return /\.(cmd|bat)$/i.test(command);
  }
  // Bare tool names on Windows resolve to npm's node_modules/.bin shims
  // (.cmd), which only a shell can start.
  return true;
}

const child = spawn(command, args.slice(index + 1), {
  stdio: "inherit",
  env,
  shell: requiresCmdShell(command),
});
child.on("error", (error) => {
  console.error(`env-run: ${error.message}`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  process.exit(code ?? (signal ? 1 : 0));
});
