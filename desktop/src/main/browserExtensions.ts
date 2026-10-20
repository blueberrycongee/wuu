import { createHash, randomUUID } from "node:crypto";
import { existsSync, lstatSync, mkdirSync, readdirSync, readFileSync, realpathSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { cp } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import type { BrowserExtensionManifest, BrowserExtensionRecord } from "../shared/protocol";
import { writeTextFileAtomicSync } from "./atomicFile";
import { wuuHomePath } from "./projects";

type LoadedExtension = { id: string; name: string; version: string; path: string };
type BrowserExtensionSession = {
  extensions: {
    loadExtension(path: string, options?: { allowFileAccess?: boolean }): Promise<LoadedExtension>;
    removeExtension(extensionId: string): void;
    getAllExtensions(): LoadedExtension[];
  };
};
type StoredBrowserExtension = { id?: string; path: string; enabled: boolean; approved?: BrowserExtensionManifest; manifestHash?: string; identityKey?: string; snapshotRevision?: string };
export type BrowserExtensionConsent = (manifest: BrowserExtensionManifest, source: string, previous?: BrowserExtensionManifest) => Promise<boolean>;

const MAX_EXTENSIONS = 100;
const SNAPSHOT_REVISION_FILE = ".wuu-installed-snapshot";
// This is a compatibility disclosure, not an API implementation or a permission grant.
const DOCUMENTED_PERMISSIONS = new Set(["storage", "scripting", "tabs", "webRequest", "webRequestBlocking", "activeTab"]);
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function pathKey(path: string): string {
  const value = resolve(path);
  return process.platform === "win32" ? value.toLowerCase() : value;
}
function strings(value: unknown, field: string): string[] {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.some(item => typeof item !== "string")) throw new Error(`Invalid ${field} in the extension manifest.`);
  return [...new Set(value as string[])].sort();
}
function isSite(value: string): boolean { return value === "<all_urls>" || value.includes("://"); }
function readManifest(directory: string): BrowserExtensionManifest {
  if (!isAbsolute(directory) || !existsSync(directory) || !statSync(directory).isDirectory()) throw new Error("Choose an unpacked extension directory containing manifest.json.");
  let manifest: unknown;
  try {
    const path = join(directory, "manifest.json");
    const stat = lstatSync(path);
    if (!stat.isFile() || stat.size > 256 * 1024) throw new Error("Manifest must be a regular file under 256 KB");
    manifest = JSON.parse(readFileSync(path, "utf8"));
  }
  catch { throw new Error("The extension manifest.json is missing or is not valid JSON."); }
  if (!isRecord(manifest)) throw new Error("The extension manifest is invalid.");
  if (manifest.manifest_version !== 2 && manifest.manifest_version !== 3) throw new Error("Only Manifest V2 and V3 can be attempted in Electron.");
  if (typeof manifest.name !== "string" || !manifest.name.trim()) throw new Error("The extension manifest has no name.");
  if (typeof manifest.version !== "string" || !manifest.version.trim()) throw new Error("The extension manifest has no version.");
  const required = strings(manifest.permissions, "permissions");
  const optional = strings(manifest.optional_permissions, "optional_permissions");
  const scripts = manifest.content_scripts;
  if (scripts !== undefined && (!Array.isArray(scripts) || scripts.some(item => !isRecord(item)))) throw new Error("Invalid content_scripts in the extension manifest.");
  const scriptRecords = (scripts as Record<string, unknown>[] | undefined) ?? [];
  const scriptSites = scriptRecords.flatMap(item => strings(item.matches, "content_scripts.matches"));
  const resources: unknown[] = scriptRecords.flatMap(item => [...strings(item.js, "content_scripts.js"), ...strings(item.css, "content_scripts.css")]);
  if (isRecord(manifest.background)) resources.push(...strings(manifest.background.scripts, "background.scripts"), manifest.background.service_worker, manifest.background.page);
  resources.push(manifest.devtools_page, manifest.options_page);
  if (isRecord(manifest.options_ui)) resources.push(manifest.options_ui.page);
  for (const action of [manifest.action, manifest.browser_action, manifest.page_action]) {
    if (isRecord(action)) resources.push(action.default_popup);
  }
  for (const resource of resources) {
    if (resource === undefined || resource === "") continue;
    if (typeof resource !== "string" || resource.includes("\\") || resource.includes(":") || resource.startsWith("/")
      || resource.split("/").includes("..")) throw new Error("Extension resources must stay inside the selected folder.");
  }
  const permissions = required.filter(item => !isSite(item));
  const optionalPermissions = optional.filter(item => !isSite(item));
  const compatibility: BrowserExtensionManifest["compatibility"] = [];
  if (isRecord(manifest.background) && manifest.background.service_worker) compatibility.push("serviceWorker");
  if (manifest.action || manifest.browser_action || manifest.page_action) compatibility.push("action");
  if (manifest.options_page || manifest.options_ui) compatibility.push("options");
  if (manifest.devtools_page) compatibility.push("devtools");
  if (manifest.chrome_url_overrides || manifest.omnibox || manifest.side_panel) compatibility.push("browserUI");
  return {
    name: manifest.name.trim(), version: manifest.version.trim(), manifestVersion: manifest.manifest_version,
    ...(typeof manifest.description === "string" ? { description: manifest.description } : {}),
    permissions,
    sites: [...new Set([...required.filter(isSite), ...strings(manifest.host_permissions, "host_permissions"), ...scriptSites])].sort(),
    optionalPermissions,
    optionalSites: [...new Set([...optional.filter(isSite), ...strings(manifest.optional_host_permissions, "optional_host_permissions")])].sort(),
    unsupportedPermissions: [...new Set([...permissions, ...optionalPermissions].filter(item => !DOCUMENTED_PERMISSIONS.has(item)))],
    compatibility,
  };
}
function grants(manifest: BrowserExtensionManifest): string[] {
  return [
    ...manifest.permissions.map(item => `permission:${item}`), ...manifest.optionalPermissions.map(item => `optional-permission:${item}`),
    ...manifest.sites.map(item => `site:${item}`), ...manifest.optionalSites.map(item => `optional-site:${item}`),
  ];
}
function needsConsent(next: BrowserExtensionManifest, previous?: BrowserExtensionManifest): boolean {
  if (!previous) return true;
  const granted = new Set(grants(previous));
  return grants(next).some(item => !granted.has(item)) || next.compatibility.some(item => !previous.compatibility.includes(item));
}
function readStore(path: string): StoredBrowserExtension[] {
  if (!existsSync(path)) return [];
  const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (!isRecord(parsed) || (parsed.version !== 1 && parsed.version !== 2) || !Array.isArray(parsed.extensions)) throw new Error("The browser extension settings are invalid.");
  const seen = new Set<string>();
  return parsed.extensions.flatMap((item): StoredBrowserExtension[] => {
    if (!isRecord(item) || typeof item.path !== "string" || !isAbsolute(item.path) || typeof item.enabled !== "boolean") return [];
    const key = pathKey(item.path);
    if (seen.has(key) || seen.size >= MAX_EXTENSIONS) return [];
    seen.add(key);
    // Version 1 loaded mutable source directories without recording permissions.
    // Keep those entries visible, but require a review before their next load.
    const value = item.approved;
    const approved = parsed.version === 2 && isRecord(value)
      && typeof value.name === "string" && typeof value.version === "string"
      && (value.manifestVersion === 2 || value.manifestVersion === 3)
      && ["permissions", "sites", "optionalPermissions", "optionalSites", "unsupportedPermissions", "compatibility"]
        .every(key => Array.isArray(value[key]) && value[key].every((entry: unknown) => typeof entry === "string"))
      ? value as BrowserExtensionManifest : undefined;
    return [{ path: resolve(item.path), enabled: item.enabled, ...(typeof item.id === "string" ? { id: item.id } : {}), ...(approved ? { approved, manifestHash: typeof item.manifestHash === "string" ? item.manifestHash : undefined, identityKey: typeof item.identityKey === "string" ? item.identityKey : undefined, snapshotRevision: typeof item.snapshotRevision === "string" ? item.snapshotRevision : undefined } : {}) }];
  });
}
function messageOf(error: unknown): string { return error instanceof Error ? error.message : String(error); }

export class BrowserExtensionManager {
  private preferences: StoredBrowserExtension[] = [];
  private readonly errors = new Map<string, string>();
  private initialized = false;
  private mutation: Promise<unknown> = Promise.resolve();
  private readonly snapshots: string;
  constructor(private readonly session: BrowserExtensionSession, private readonly storePath = join(wuuHomePath(), "browser-extensions.json")) {
    this.snapshots = join(dirname(storePath), "browser-extension-snapshots");
  }

  async initialize(): Promise<void> {
    if (this.initialized) return;
    return this.serializeMutation(async () => {
      if (this.initialized) return;
      this.preferences = readStore(this.storePath);
      this.initialized = true;
      for (const preference of this.preferences) {
        if (!preference.enabled) continue;
        try {
          if (!preference.approved) throw new Error("Review this extension's permissions with Reload before loading it.");
          const active = this.snapshotPath(preference.path, "active");
          const backup = this.snapshotPath(preference.path, "previous");
          // Recover an interrupted update before loading any extension code.
          if (existsSync(backup)) {
            if (this.isCommittedSnapshot(active, preference)) {
              rmSync(backup, { recursive: true, force: true });
            } else {
              rmSync(active, { recursive: true, force: true });
              renameSync(backup, active);
            }
          }
          const manifest = readManifest(active);
          if (this.manifestHash(active) !== preference.manifestHash) throw new Error("The installed extension manifest changed. Reload its source folder before loading it.");
          if (needsConsent(manifest, preference.approved)) throw new Error("Extension permissions changed. Review them with Reload before loading it.");
          preference.id = (await this.load(active)).id;
        } catch (error) { this.errors.set(pathKey(preference.path), messageOf(error)); }
      }
      this.save();
    });
  }

  async list(): Promise<BrowserExtensionRecord[]> {
    if (!this.initialized) await this.initialize();
    const loaded = this.session.extensions.getAllExtensions();
    return this.preferences.map(preference => {
      const extension = loaded.find(item => pathKey(item.path) === pathKey(this.snapshotPath(preference.path, "active")));
      const manifest = preference.approved;
      return {
        id: extension?.id ?? preference.id ?? preference.path,
        name: extension?.name ?? manifest?.name ?? basename(preference.path),
        version: extension?.version ?? manifest?.version ?? "", path: preference.path,
        enabled: preference.enabled, loaded: Boolean(extension),
        hasOptions: Boolean(manifest?.compatibility.includes("options")),
        ...(manifest ? { manifest } : { needsReview: true }),
        ...(this.errors.get(pathKey(preference.path)) ? { error: this.errors.get(pathKey(preference.path)) } : {}),
      };
    }).sort((a, b) => a.name.localeCompare(b.name));
  }

  async addFromDirectory(directory: string, consent: BrowserExtensionConsent): Promise<BrowserExtensionRecord[]> {
    await this.initialize();
    return this.serializeMutation(async () => {
      readManifest(directory);
      const path = realpathSync(directory);
      const existing = this.preferences.find(item => pathKey(item.path) === pathKey(path));
      if (existing) return this.list();
      if (this.preferences.length >= MAX_EXTENSIONS) throw new Error("Remove an extension before adding another.");
      const preference: StoredBrowserExtension = { path, enabled: true };
      await this.installSnapshot(preference, consent);
      return this.list();
    });
  }

  async optionsURL(id: string): Promise<string> {
    await this.initialize();
    return this.serializeMutation(async () => {
      const preference = this.requirePreference(id);
      const active = this.snapshotPath(preference.path, "active");
      const loaded = this.session.extensions.getAllExtensions().find(item => pathKey(item.path) === pathKey(active));
      if (!preference.enabled || !loaded) throw new Error("The extension must be enabled and loaded before opening its settings.");
      if (this.manifestHash(active) !== preference.manifestHash) throw new Error("The installed extension manifest changed. Reload its source folder first.");
      readManifest(active);
      const manifest = JSON.parse(readFileSync(join(active, "manifest.json"), "utf8")) as Record<string, unknown>;
      const resource = isRecord(manifest.options_ui) ? manifest.options_ui.page : manifest.options_page;
      if (typeof resource !== "string" || !resource) throw new Error("This extension does not provide a settings page.");
      const url = new URL(resource, `chrome-extension://${loaded.id}/`);
      if (url.protocol !== "chrome-extension:" || url.hostname !== loaded.id) throw new Error("Extension settings must stay inside their extension.");
      return url.href;
    });
  }

  async reload(id: string, consent: BrowserExtensionConsent): Promise<BrowserExtensionRecord[]> {
    await this.initialize();
    return this.serializeMutation(async () => {
      const preference = this.requirePreference(id);
      if (!preference.enabled) throw new Error("Enable the extension before reloading its source folder.");
      await this.installSnapshot(preference, consent);
      return this.list();
    });
  }

  async setEnabled(id: string, enabled: boolean, consent?: BrowserExtensionConsent): Promise<BrowserExtensionRecord[]> {
    await this.initialize();
    return this.serializeMutation(async () => {
      const preference = this.requirePreference(id);
      if (enabled) {
        if (!preference.approved) {
          if (!consent) throw new Error("Review extension permissions before enabling it.");
          const wasEnabled = preference.enabled;
          preference.enabled = true;
          try { if (!await this.installSnapshot(preference, consent)) preference.enabled = wasEnabled; }
          catch (error) { preference.enabled = wasEnabled; throw error; }
        } else {
          const active = this.snapshotPath(preference.path, "active");
          if (this.manifestHash(active) !== preference.manifestHash) throw new Error("The installed extension manifest changed. Remove this entry and load its source folder again; its browser data will be kept.");
          if (needsConsent(readManifest(active), preference.approved)) throw new Error("Review changed permissions with Reload before enabling this extension.");
          preference.id = (await this.load(active)).id;
          preference.enabled = true;
        }
      } else {
        this.unload(preference);
        preference.enabled = false;
      }
      this.errors.delete(pathKey(preference.path));
      this.save();
      return this.list();
    });
  }

  async remove(id: string): Promise<BrowserExtensionRecord[]> {
    await this.initialize();
    return this.serializeMutation(async () => {
      const preference = this.requirePreference(id);
      this.unload(preference);
      const next = this.preferences.filter(item => item !== preference);
      writeTextFileAtomicSync(this.storePath, `${JSON.stringify({ version: 2, extensions: next }, null, 2)}\n`);
      this.preferences = next;
      this.errors.delete(pathKey(preference.path));
      // Delete only Wuu's installed copy. Never delete the user's source folder.
      rmSync(dirname(this.snapshotPath(preference.path, "active")), { recursive: true, force: true });
      return this.list();
    });
  }

  private async installSnapshot(preference: StoredBrowserExtension, consent: BrowserExtensionConsent): Promise<boolean> {
    const active = this.snapshotPath(preference.path, "active");
    const staged = this.snapshotPath(preference.path, "pending");
    const backup = this.snapshotPath(preference.path, "previous");
    const withinSource = relative(preference.path, this.snapshots);
    if (withinSource === "" || (!withinSource.startsWith(`..${sep}`) && withinSource !== ".." && !isAbsolute(withinSource))) throw new Error("Choose the extension folder, not a parent of Wuu's extension storage.");
    // A committed backup may remain after a cleanup failure. Do not confuse
    // it with this update's rollback copy or overwrite the active version.
    if (existsSync(backup)) {
      if (!this.isCommittedSnapshot(active, preference)) throw new Error("Restart Wuu to recover the interrupted extension update before reloading.");
      rmSync(backup, { recursive: true, force: true });
    }
    rmSync(staged, { recursive: true, force: true });
    mkdirSync(dirname(staged), { recursive: true, mode: 0o700 });
    let switched = false;
    const wasLoaded = this.session.extensions.getAllExtensions().some(item => pathKey(item.path) === pathKey(active));
    try {
      // A private copy binds the reviewed manifest to the bytes that will load.
      // Reject links rather than reading or executing files outside the selection.
      let bytes = 0;
      let files = 0;
      await cp(preference.path, staged, { recursive: true, filter: source => {
        const stat = lstatSync(source);
        if (stat.isSymbolicLink() || (!stat.isDirectory() && !stat.isFile())) throw new Error("Choose a self-contained extension folder without symbolic links or special files.");
        bytes += stat.isFile() ? stat.size : 0;
        if (++files > 20_000 || bytes > 256 * 1024 * 1024) throw new Error("This extension exceeds the local install limit (20,000 files or 256 MB).");
        return true;
      } });
      for (const entry of readdirSync(staged, { recursive: true, withFileTypes: true })) {
        if (entry.isSymbolicLink() || (!entry.isDirectory() && !entry.isFile())) throw new Error("The extension folder changed while it was being copied. Try again with a self-contained folder.");
      }
      const manifest = readManifest(staged);
      const key = (JSON.parse(readFileSync(join(staged, "manifest.json"), "utf8")) as Record<string, unknown>).key;
      const identityKey = typeof key === "string"
        ? createHash("sha256").update(Buffer.from(key, "base64")).digest("hex") : "";
      // Chromium derives keyed IDs from the first 16 SHA-256 bytes using a–p
      // for hex digits (components/crx_file/id_util.cc). Check before loading:
      // Electron may otherwise replace an existing extension with that ID.
      const keyedID = identityKey.slice(0, 32).replace(/[0-9a-f]/g, digit => String.fromCharCode(97 + parseInt(digit, 16)));
      if (keyedID && (this.preferences.some(item => item !== preference && (item.identityKey === identityKey || item.id === keyedID))
        || this.session.extensions.getAllExtensions().some(item => item.id === keyedID && pathKey(item.path) !== pathKey(active)))) {
        throw new Error("This extension identity is already managed. Review or remove its existing entry before adding another source.");
      }
      if (preference.approved && identityKey !== preference.identityKey) throw new Error("The extension identity key changed. Keep the existing version and add the replacement from a different folder.");
      if (needsConsent(manifest, preference.approved) && !await consent(manifest, preference.path, preference.approved)) return false;
      // Manifest bytes alone cannot identify a JS-only update. This marker
      // moves with the snapshot and is committed together with its settings.
      const snapshotRevision = randomUUID();
      writeFileSync(join(staged, SNAPSHOT_REVISION_FILE), snapshotRevision, { mode: 0o600 });
      this.unload(preference);
      if (existsSync(active)) renameSync(active, backup);
      renameSync(staged, active);
      switched = true;
      const loaded = preference.enabled ? await this.load(active) : undefined;
      if (loaded && this.preferences.some(item => item !== preference && item.id === loaded.id)) {
        throw new Error("This extension identity is already managed. Review or remove its existing entry before adding another source.");
      }
      const previous = { ...preference };
      preference.approved = manifest;
      preference.manifestHash = this.manifestHash(active);
      preference.identityKey = identityKey;
      preference.snapshotRevision = snapshotRevision;
      if (loaded) preference.id = loaded.id;
      const adding = !this.preferences.includes(preference);
      if (adding) this.preferences.push(preference);
      try { this.save(); } catch (error) {
        if (adding) this.preferences = this.preferences.filter(item => item !== preference);
        preference.approved = previous.approved;
        preference.manifestHash = previous.manifestHash;
        preference.identityKey = previous.identityKey;
        preference.snapshotRevision = previous.snapshotRevision;
        preference.id = previous.id;
        throw error;
      }
      // The new snapshot and settings are committed. A leftover backup is
      // reconciled against the committed revision on startup, never rolled back
      // merely because cleanup failed.
      try { rmSync(backup, { recursive: true, force: true }); }
      catch (error) { console.warn("Could not clean up the previous browser extension snapshot", error); }
      this.errors.delete(pathKey(preference.path));
      return true;
    } catch (error) {
      let recovery = "";
      if (switched || existsSync(backup)) {
        this.unload(preference);
        rmSync(active, { recursive: true, force: true });
        if (existsSync(backup)) renameSync(backup, active);
        if (wasLoaded) {
          try { await this.load(active); }
          catch (restoreError) { recovery = ` Previous version could not restart: ${messageOf(restoreError)}`; }
        }
      }
      const message = `${messageOf(error)}${recovery}`;
      this.errors.set(pathKey(preference.path), message);
      throw new Error(message);
    } finally { rmSync(staged, { recursive: true, force: true }); }
  }

  private isCommittedSnapshot(directory: string, preference: StoredBrowserExtension): boolean {
    const marker = join(directory, SNAPSHOT_REVISION_FILE);
    return Boolean(preference.snapshotRevision && existsSync(marker)
      && readFileSync(marker, "utf8") === preference.snapshotRevision);
  }
  private manifestHash(directory: string): string {
    return createHash("sha256").update(readFileSync(join(directory, "manifest.json"))).digest("hex");
  }
  private save(): void { writeTextFileAtomicSync(this.storePath, `${JSON.stringify({ version: 2, extensions: this.preferences }, null, 2)}\n`); }
  private snapshotPath(source: string, name: string): string {
    return join(this.snapshots, createHash("sha256").update(pathKey(source)).digest("hex"), name);
  }
  private requirePreference(id: string): StoredBrowserExtension {
    const preference = this.preferences.find(item => item.id === id || item.path === id);
    if (!preference) throw new Error("Extension is not managed by Wuu.");
    return preference;
  }
  private unload(preference: StoredBrowserExtension): void {
    const active = this.snapshotPath(preference.path, "active");
    const loaded = this.session.extensions.getAllExtensions().find(item => pathKey(item.path) === pathKey(active));
    if (loaded) this.session.extensions.removeExtension(loaded.id);
  }
  private load(path: string): Promise<LoadedExtension> {
    const loaded = this.session.extensions.getAllExtensions().find(item => pathKey(item.path) === pathKey(path));
    return loaded ? Promise.resolve(loaded) : this.session.extensions.loadExtension(path, { allowFileAccess: false });
  }
  private serializeMutation<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.mutation.then(operation);
    this.mutation = result.catch(() => undefined);
    return result;
  }
}
