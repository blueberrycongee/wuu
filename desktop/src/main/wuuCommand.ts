import { existsSync } from "node:fs";
import { join } from "node:path";

export type WuuCommand = {
  command: string;
  args: string[];
  cwd: string;
};

/**
 * Decide how the desktop launches its private Wuu core.
 *
 * This deliberately NEVER inspects the active workspace for a `bin/wuu` or
 * `wuu` file. A project's local build artifact — which may be stale, built for
 * another OS, or simply not executable — must not be run as the desktop's core
 * (see issue #8: selecting the wuu repo, whose `make build` writes an ignored
 * `bin/wuu`, failed with `spawn ENOEXEC` on macOS because the artifact was a
 * Linux ELF). The active workspace is for user operations; the core binary is
 * desktop-owned or explicitly configured. It never falls back to a `wuu`
 * command on PATH, because that is an independent CLI installation whose
 * version may intentionally differ from the desktop app.
 *
 * Priority:
 *  1. `WUU_DESKTOP_CORE` — an explicit development/test override.
 *  2. `go run ./cmd/wuu` — only when `WUU_DESKTOP_USE_GO_RUN=1` and a source
 *     root was found (local development against the repo).
 *  3. packaged `Resources/bin/wuu-core` — the app-owned core for releases.
 */
export function resolveWuuCommand(
  env: NodeJS.ProcessEnv,
  workdir: string,
  sourceRoot: string | undefined,
  resourcesPath?: string,
): WuuCommand {
  if (env.WUU_DESKTOP_CORE) {
    return { command: env.WUU_DESKTOP_CORE, args: [], cwd: workdir };
  }
  if (sourceRoot && env.WUU_DESKTOP_USE_GO_RUN === "1") {
    return { command: "go", args: ["run", "./cmd/wuu"], cwd: sourceRoot };
  }
  const packaged = resolvePackagedWuuCore(resourcesPath);
  if (packaged) {
    return { command: packaged, args: [], cwd: workdir };
  }
  throw new Error("wuu desktop core is missing; reinstall the desktop app");
}

function resolvePackagedWuuCore(resourcesPath: string | undefined): string | undefined {
  if (!resourcesPath) {
    return undefined;
  }
  for (const candidate of [
    join(resourcesPath, "bin", "wuu-core"),
    join(resourcesPath, "bin", "wuu-core.exe"),
  ]) {
    if (existsSync(candidate)) {
      return candidate;
    }
  }
  return undefined;
}
