import { afterEach, describe, expect, it } from "vitest";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { discoverPreviewEngines } from "./discoverEngines";

const directories: string[] = [];
afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

function temporaryDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), "wuu-cli-discovery-"));
  directories.push(directory);
  return directory;
}

function binary(directory: string, name: string): string {
  const path = join(directory, name + (process.platform === "win32" ? ".CMD" : ""));
  writeFileSync(path, "", { mode: 0o755 });
  return path;
}

describe("standalone onboarding CLI discovery", () => {
  it("detects installed CLIs in PATH without executing them", () => {
    const directory = temporaryDirectory();
    binary(directory, "codex");
    binary(directory, "claude");
    expect(discoverPreviewEngines({ PATH: directory }).engines.every((engine) => engine.binary_ok)).toBe(true);
  });

  it("honors explicit paths and does not fall back from a broken override", () => {
    const directory = temporaryDirectory();
    binary(directory, "codex");
    const claude = binary(temporaryDirectory(), "custom-claude");
    const { engines } = discoverPreviewEngines({
      PATH: directory,
      WUU_CODEX_BINARY: join(directory, "missing-codex"),
      WUU_CLAUDE_BINARY: claude,
    });
    expect(engines.find((engine) => engine.id === "codex")?.binary_ok).toBe(false);
    expect(engines.find((engine) => engine.id === "claude")?.binary_ok).toBe(true);
  });

  it.skipIf(process.platform === "win32")("rejects directories and files without execute permission", () => {
    const directory = temporaryDirectory();
    mkdirSync(join(directory, "codex"));
    chmodSync(binary(directory, "claude"), 0o644);
    expect(discoverPreviewEngines({
      PATH: directory,
      WUU_CODEX_BINARY: join(directory, "codex"),
      WUU_CLAUDE_BINARY: join(directory, "claude"),
    }).engines.slice(1).every((engine) => !engine.binary_ok)).toBe(true);
  });
});
