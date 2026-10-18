import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { BrowserExtensionManager } from "./browserExtensions";

const roots: string[] = [];

function tempRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "wuu-browser-extension-test-"));
  roots.push(root);
  return root;
}

function createExtension(root: string, name: string, manifest?: unknown): string {
  const directory = join(root, name);
  mkdirSync(directory, { recursive: true });
  writeFileSync(
    join(directory, "manifest.json"),
    JSON.stringify(
      manifest ?? {
        manifest_version: 3,
        name,
        version: "1.2.3",
      },
      null,
      2,
    ),
  );
  return directory;
}

class FakeSession {
  readonly loaded = new Map<
    string,
    { id: string; name: string; version: string; path: string }
  >();

  readonly extensions = {
    loadExtension: async (path: string) => {
      const manifestPath = join(path, "manifest.json");
      if (!existsSync(manifestPath)) {
        throw new Error("manifest.json is missing");
      }
      const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
        name?: unknown;
        version?: unknown;
      };
      if (typeof manifest.name !== "string" || typeof manifest.version !== "string") {
        throw new Error("manifest is invalid");
      }
      const extension = {
        id: `id-${basename(path)}`,
        name: manifest.name,
        version: manifest.version,
        path,
      };
      this.loaded.set(extension.id, extension);
      return extension;
    },
    removeExtension: (id: string) => {
      this.loaded.delete(id);
    },
    getAllExtensions: () => [...this.loaded.values()],
  };
}

afterEach(() => {
  for (const root of roots.splice(0)) {
    rmSync(root, { recursive: true, force: true });
  }
});

describe("browser extension manager", () => {
  it("loads, persists, and restores an unpacked extension", async () => {
    const root = tempRoot();
    const extensionPath = createExtension(root, "reader");
    const storePath = join(root, "browser-extensions.json");
    const first = new BrowserExtensionManager(new FakeSession(), storePath);

    const added = await first.addFromDirectory(extensionPath);
    expect(added).toEqual([
      {
        id: "id-reader",
        name: "reader",
        version: "1.2.3",
        path: extensionPath,
        enabled: true,
        loaded: true,
      },
    ]);

    const secondSession = new FakeSession();
    const second = new BrowserExtensionManager(secondSession, storePath);
    await second.initialize();
    expect(secondSession.loaded.has("id-reader")).toBe(true);
    expect(await second.list()).toEqual(added);
  });

  it("disables, re-enables, and removes without deleting the source folder", async () => {
    const root = tempRoot();
    const extensionPath = createExtension(root, "reader");
    const storePath = join(root, "browser-extensions.json");
    const session = new FakeSession();
    const manager = new BrowserExtensionManager(session, storePath);
    await manager.addFromDirectory(extensionPath);

    const disabled = await manager.setEnabled("id-reader", false);
    expect(disabled[0]).toMatchObject({ enabled: false, loaded: false });
    expect(session.loaded.size).toBe(0);

    const enabled = await manager.setEnabled("id-reader", true);
    expect(enabled[0]).toMatchObject({ enabled: true, loaded: true });

    const removed = await manager.remove("id-reader");
    expect(removed).toEqual([]);
    expect(session.loaded.size).toBe(0);
    expect(existsSync(extensionPath)).toBe(true);
  });

  it("rejects an invalid manifest before touching the session", async () => {
    const root = tempRoot();
    const extensionPath = createExtension(root, "broken", {
      manifest_version: 3,
      version: "1.0.0",
    });
    const manager = new BrowserExtensionManager(
      new FakeSession(),
      join(root, "browser-extensions.json"),
    );

    await expect(manager.addFromDirectory(extensionPath)).rejects.toThrow(
      /no name/,
    );
  });

  it("keeps a broken stored extension visible with its load error", async () => {
    const root = tempRoot();
    const storePath = join(root, "browser-extensions.json");
    const missingPath = join(root, "missing");
    writeFileSync(
      storePath,
      JSON.stringify({
        version: 1,
        extensions: [{ path: missingPath, enabled: true }],
      }),
    );
    const manager = new BrowserExtensionManager(new FakeSession(), storePath);

    await manager.initialize();
    await expect(manager.list()).resolves.toEqual([
      expect.objectContaining({
        path: missingPath,
        enabled: true,
        loaded: false,
        error: expect.stringContaining("manifest.json"),
      }),
    ]);
  });
});
