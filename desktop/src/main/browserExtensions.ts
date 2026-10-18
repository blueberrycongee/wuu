import { existsSync, readFileSync, statSync } from "node:fs";
import { basename, isAbsolute, join, resolve } from "node:path";
import type { BrowserExtensionRecord } from "../shared/protocol";
import { writeTextFileAtomicSync } from "./atomicFile";
import { wuuHomePath } from "./projects";

type LoadedExtension = {
  id: string;
  name: string;
  version: string;
  path: string;
};

type BrowserExtensionSession = {
  extensions: {
    loadExtension(
      path: string,
      options?: { allowFileAccess?: boolean },
    ): Promise<LoadedExtension>;
    removeExtension(extensionId: string): void;
    getAllExtensions(): LoadedExtension[];
  };
};

type StoredBrowserExtension = {
  id?: string;
  path: string;
  enabled: boolean;
};

type BrowserExtensionStore = {
  version: 1;
  extensions: StoredBrowserExtension[];
};

const STORE_VERSION = 1;
const MAX_EXTENSIONS = 100;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function pathKey(path: string): string {
  const value = resolve(path);
  return process.platform === "win32" ? value.toLowerCase() : value;
}

function readStore(path: string): StoredBrowserExtension[] {
  try {
    const parsed = JSON.parse(readFileSync(path, "utf8")) as unknown;
    if (!isRecord(parsed) || parsed.version !== STORE_VERSION) return [];
    if (!Array.isArray(parsed.extensions)) return [];
    const seen = new Set<string>();
    const extensions: StoredBrowserExtension[] = [];
    for (const item of parsed.extensions) {
      if (extensions.length >= MAX_EXTENSIONS) break;
      if (!isRecord(item) || typeof item.path !== "string") continue;
      if (!isAbsolute(item.path)) continue;
      if (typeof item.enabled !== "boolean") continue;
      const key = pathKey(item.path);
      if (seen.has(key)) continue;
      seen.add(key);
      extensions.push({
        path: resolve(item.path),
        enabled: item.enabled,
        ...(typeof item.id === "string" && item.id.trim()
          ? { id: item.id.trim() }
          : {}),
      });
    }
    return extensions;
  } catch {
    return [];
  }
}

function writeStore(path: string, extensions: StoredBrowserExtension[]): void {
  const store: BrowserExtensionStore = {
    version: STORE_VERSION,
    extensions: extensions.slice(0, MAX_EXTENSIONS).map((extension) => ({
      path: extension.path,
      enabled: extension.enabled,
      ...(extension.id ? { id: extension.id } : {}),
    })),
  };
  writeTextFileAtomicSync(path, `${JSON.stringify(store, null, 2)}\n`);
}

function validateExtensionDirectory(
  directory: string,
): { name: string; version: string } {
  const path = resolve(directory);
  if (!isAbsolute(path)) throw new Error("Extension path must be absolute.");
  if (!existsSync(path) || !statSync(path).isDirectory()) {
    throw new Error("Choose an unpacked extension directory.");
  }
  const manifestPath = join(path, "manifest.json");
  if (!existsSync(manifestPath)) {
    throw new Error("This directory has no manifest.json.");
  }
  let manifest: unknown;
  try {
    manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  } catch {
    throw new Error("The extension manifest is not valid JSON.");
  }
  if (!isRecord(manifest)) throw new Error("The extension manifest is invalid.");
  if (manifest.manifest_version !== 2 && manifest.manifest_version !== 3) {
    throw new Error("Only Manifest V2 and V3 extensions are supported.");
  }
  if (typeof manifest.name !== "string" || !manifest.name.trim()) {
    throw new Error("The extension manifest has no name.");
  }
  if (typeof manifest.version !== "string" || !manifest.version.trim()) {
    throw new Error("The extension manifest has no version.");
  }
  return {
    name: manifest.name.trim(),
    version: manifest.version.trim(),
  };
}

export class BrowserExtensionManager {
  private preferences: StoredBrowserExtension[] = [];
  private readonly errors = new Map<string, string>();
  private initialized = false;
  private mutation: Promise<unknown> = Promise.resolve();

  constructor(
    private readonly session: BrowserExtensionSession,
    private readonly storePath: string = join(
      wuuHomePath(),
      "browser-extensions.json",
    ),
  ) {}

  /**
   * Loads every previously enabled extension. A broken extension directory
   * stays in the store with an error instead of silently losing the user's
   * choice; startup itself never fails because one extension is broken.
   */
  async initialize(): Promise<void> {
    return this.serializeMutation(async () => {
      if (this.initialized) return;
      this.initialized = true;
      this.preferences = readStore(this.storePath);
      for (const preference of this.preferences) {
        if (!preference.enabled) continue;
        try {
          const loaded = await this.load(preference.path);
          preference.id = loaded.id;
          this.errors.delete(pathKey(preference.path));
        } catch (error) {
          this.errors.set(pathKey(preference.path), messageOf(error));
        }
      }
      writeStore(this.storePath, this.preferences);
    });
  }

  async list(): Promise<BrowserExtensionRecord[]> {
    const loaded = this.loadedExtensions();
    const byID = new Map(loaded.map((extension) => [extension.id, extension]));
    const byPath = new Map(
      loaded.map((extension) => [pathKey(extension.path), extension]),
    );
    const records: BrowserExtensionRecord[] = [];
    const represented = new Set<string>();

    for (const preference of this.preferences) {
      const key = pathKey(preference.path);
      const extension =
        (preference.id ? byID.get(preference.id) : undefined) ??
        byPath.get(key);
      if (extension) represented.add(extension.id);
      const manifest = readManifestSummary(preference.path);
      const id = extension?.id ?? preference.id ?? preference.path;
      records.push({
        id,
        name: extension?.name ?? manifest.name ?? basename(preference.path),
        version: extension?.version ?? manifest.version ?? "",
        path: preference.path,
        enabled: preference.enabled,
        loaded: Boolean(extension),
        ...(this.errors.get(key) ? { error: this.errors.get(key) } : {}),
      });
    }

    for (const extension of loaded) {
      if (represented.has(extension.id)) continue;
      records.push({
        id: extension.id,
        name: extension.name,
        version: extension.version,
        path: extension.path,
        enabled: true,
        loaded: true,
      });
    }

    return records.sort((a, b) => a.name.localeCompare(b.name));
  }

  async addFromDirectory(directory: string): Promise<BrowserExtensionRecord[]> {
    return this.serializeMutation(async () => {
      const summary = validateExtensionDirectory(directory);
      const path = resolve(directory);
      const key = pathKey(path);
      let extension = this.loadedExtensions().find(
        (candidate) => pathKey(candidate.path) === key,
      );
      if (!extension) {
        try {
          extension = await this.load(path);
        } catch (error) {
          this.errors.set(key, messageOf(error));
          throw new Error(`Could not load ${summary.name}: ${messageOf(error)}`);
        }
      }
      this.errors.delete(key);
      const existing = this.preferences.find(
        (preference) =>
          pathKey(preference.path) === key || preference.id === extension?.id,
      );
      if (existing) {
        existing.path = path;
        existing.id = extension.id;
        existing.enabled = true;
      } else {
        this.preferences.push({ path, id: extension.id, enabled: true });
      }
      writeStore(this.storePath, this.preferences);
      return this.list();
    });
  }

  async setEnabled(id: string, enabled: boolean): Promise<BrowserExtensionRecord[]> {
    return this.serializeMutation(async () => {
      const preference = this.findPreference(id);
      if (!preference) throw new Error("Extension is not managed by wuu.");
      if (enabled) {
        const loaded = await this.load(preference.path);
        preference.id = loaded.id;
        this.errors.delete(pathKey(preference.path));
      } else {
        const loaded = this.findLoaded(id, preference.path);
        if (loaded) this.session.extensions.removeExtension(loaded.id);
        this.errors.delete(pathKey(preference.path));
      }
      preference.enabled = enabled;
      writeStore(this.storePath, this.preferences);
      return this.list();
    });
  }

  async remove(id: string): Promise<BrowserExtensionRecord[]> {
    return this.serializeMutation(async () => {
      const preference = this.findPreference(id);
      const loaded = this.findLoaded(id, preference?.path);
      if (loaded) this.session.extensions.removeExtension(loaded.id);
      if (preference) {
        this.preferences = this.preferences.filter(
          (candidate) => candidate !== preference,
        );
        this.errors.delete(pathKey(preference.path));
        writeStore(this.storePath, this.preferences);
      } else if (loaded) {
        // A loaded extension that was not in the store can still be removed from
        // this session. Its source directory is never deleted.
        this.errors.delete(pathKey(loaded.path));
      }
      return this.list();
    });
  }

  // A renderer can remount while loadExtension is pending. Serialize in the
  // host so a later disable/remove always runs after the pending load settles.
  private serializeMutation<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutation.then(operation);
    this.mutation = result.catch(() => undefined);
    return result;
  }

  private findPreference(id: string): StoredBrowserExtension | undefined {
    return this.preferences.find(
      (preference) =>
        preference.id === id || pathKey(preference.path) === pathKey(id),
    );
  }

  private findLoaded(id: string, path?: string): LoadedExtension | undefined {
    const loaded = this.loadedExtensions();
    return loaded.find(
      (extension) =>
        extension.id === id ||
        (path !== undefined && pathKey(extension.path) === pathKey(path)),
    );
  }

  private loadedExtensions(): LoadedExtension[] {
    try {
      return this.session.extensions.getAllExtensions();
    } catch {
      return [];
    }
  }

  private async load(path: string): Promise<LoadedExtension> {
    const key = pathKey(path);
    const alreadyLoaded = this.loadedExtensions().find(
      (extension) => pathKey(extension.path) === key,
    );
    if (alreadyLoaded) return alreadyLoaded;
    return this.session.extensions.loadExtension(resolve(path), {
      allowFileAccess: false,
    });
  }
}

function readManifestSummary(
  directory: string,
): { name?: string; version?: string } {
  try {
    const manifest = JSON.parse(
      readFileSync(join(directory, "manifest.json"), "utf8"),
    ) as unknown;
    if (!isRecord(manifest)) return {};
    return {
      ...(typeof manifest.name === "string" && manifest.name.trim()
        ? { name: manifest.name.trim() }
        : {}),
      ...(typeof manifest.version === "string" && manifest.version.trim()
        ? { version: manifest.version.trim() }
        : {}),
    };
  } catch {
    return {};
  }
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
