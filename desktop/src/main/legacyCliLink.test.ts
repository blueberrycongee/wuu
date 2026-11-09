import { mkdir, mkdtemp, readlink, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { removeLegacyDesktopCliLink } from "./legacyCliLink";

let home: string;
let installDir: string;

beforeEach(async () => {
  home = await mkdtemp(join(tmpdir(), "wuu-cli-link-migration-"));
  installDir = join(home, ".local", "bin");
  await mkdir(installDir, { recursive: true });
});

afterEach(async () => {
  await rm(home, { recursive: true, force: true });
});

describe("removeLegacyDesktopCliLink", () => {
  it("removes a link to the core bundled by an older desktop app", async () => {
    const source = "/Applications/wuu.app/Contents/Resources/bin/wuu";
    await symlink(source, join(installDir, "wuu"));

    await expect(
      removeLegacyDesktopCliLink({ homeDir: home, platform: "darwin" }),
    ).resolves.toBe(true);
    await expect(readlink(join(installDir, "wuu"))).rejects.toThrow();
  });

  it("preserves an independently installed CLI link", async () => {
    const source = join(home, "go", "bin", "wuu");
    await mkdir(join(home, "go", "bin"), { recursive: true });
    await writeFile(source, "independent cli");
    await symlink(source, join(installDir, "wuu"));

    await expect(
      removeLegacyDesktopCliLink({ homeDir: home, platform: "darwin" }),
    ).resolves.toBe(false);
    await expect(readlink(join(installDir, "wuu"))).resolves.toBe(source);
  });

  it("preserves a real CLI binary", async () => {
    await writeFile(join(installDir, "wuu"), "independent cli");
    await expect(
      removeLegacyDesktopCliLink({ homeDir: home, platform: "darwin" }),
    ).resolves.toBe(false);
  });
});
