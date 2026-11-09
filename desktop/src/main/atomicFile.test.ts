import fs from "node:fs";
import { chmod, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { writeBufferFileAtomicSync, writeTextFileAtomicSync } from "./atomicFile";

let directory: string;

beforeEach(async () => {
  directory = await mkdtemp(join(tmpdir(), "wuu-atomic-file-"));
});

afterEach(async () => {
  vi.restoreAllMocks();
  await rm(directory, { recursive: true, force: true });
});

describe("writeTextFileAtomicSync", () => {
  it("replaces the file and defaults new files to owner-only permissions", async () => {
    const path = join(directory, "nested", "settings.json");

    writeTextFileAtomicSync(path, "new\n");

    expect(await readFile(path, "utf8")).toBe("new\n");
    if (process.platform !== "win32") {
      expect((await stat(path)).mode & 0o777).toBe(0o600);
    }
  });

  it("preserves the permissions of an existing file", async () => {
    const path = join(directory, "script.sh");
    await writeFile(path, "old\n", { mode: 0o750 });
    if (process.platform !== "win32") {
      await chmod(path, 0o750);
    }

    writeTextFileAtomicSync(path, "new\n");

    expect(await readFile(path, "utf8")).toBe("new\n");
    if (process.platform !== "win32") {
      expect((await stat(path)).mode & 0o777).toBe(0o750);
    }
  });

  it("leaves the original file intact and removes the temporary file when replacement fails", async () => {
    const path = join(directory, "settings.json");
    await writeFile(path, "old\n");
    vi.spyOn(fs, "renameSync").mockImplementationOnce(() => {
      throw new Error("replacement failed");
    });

    expect(() => writeTextFileAtomicSync(path, "new\n")).toThrow(
      "replacement failed",
    );

    expect(await readFile(path, "utf8")).toBe("old\n");
    expect(await readdir(directory)).toEqual(["settings.json"]);
  });
});

describe("writeBufferFileAtomicSync", () => {
  it("writes binary payloads byte-for-byte and leaves no temp file", async () => {
    const path = join(directory, "shots", "preview.png");
    const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0xff, 0x10]);

    writeBufferFileAtomicSync(path, png);

    expect(await readFile(path)).toEqual(png);
    expect(await readdir(join(directory, "shots"))).toEqual(["preview.png"]);
  });

  it("removes the temporary file when the rename fails", async () => {
    const path = join(directory, "preview.png");
    vi.spyOn(fs, "renameSync").mockImplementationOnce(() => {
      throw new Error("rename failed");
    });

    expect(() => writeBufferFileAtomicSync(path, Buffer.from("x"))).toThrow("rename failed");
    expect(await readdir(directory)).toEqual([]);
  });
});
