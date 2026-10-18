import { execFile } from "node:child_process";
import { createDecipheriv, createHash, pbkdf2Sync } from "node:crypto";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
} from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { backup, DatabaseSync } from "node:sqlite";
import { promisify } from "node:util";
import type { Session } from "electron";
import type {
  BrowserCookieImportResult,
  BrowserProfile,
} from "../shared/protocol";

const BROWSER_EPOCH_OFFSET_SECONDS = 11_644_473_600;
const BROWSER_EPOCH_MICROSECONDS = 1_000_000;
const execFileAsync = promisify(execFile);

export type BrowserSafeStorageCredential = {
  service: string;
  account: string;
};

export type BrowserProfileDescriptor = {
  id: string;
  appName: string;
  root: string;
  safeStorage?: BrowserSafeStorageCredential;
};

type BrowserCookieRow = {
  host_key: string;
  name: string;
  value: string;
  encrypted_value: Uint8Array;
  path: string;
  top_frame_site_key: string;
  expires_utc: number | bigint;
  is_secure: number | bigint;
  is_httponly: number | bigint;
  samesite: number | bigint;
};

type CookieSetDetails = Parameters<Session["cookies"]["set"]>[0];

export type BrowserCookieImportOptions = {
  profilePath: string;
  session: Pick<Session, "cookies">;
  /** Test seam; production resolves the descriptor from the OS roots. */
  descriptor?: BrowserProfileDescriptor;
  /**
   * Test seam. Production reads the macOS Keychain item created by the
   * selected source browser.
   * The value is never logged or persisted by this module.
   */
  readSafeStorageSecret?: (
    credential: BrowserSafeStorageCredential,
  ) => Promise<string> | string;
};

function filesystemError(error: unknown, path: string): Error {
  const code =
    typeof error === "object" && error !== null && "code" in error
      ? String((error as { code?: unknown }).code ?? "")
      : "";
  if (code === "EPERM" || code === "EACCES") {
    return new Error(
      `wuu cannot read ${path}. On macOS, grant wuu Full Disk Access in System Settings > Privacy & Security, then try again.`,
    );
  }
  return error instanceof Error ? error : new Error(String(error));
}

function readJSON(path: string): Record<string, unknown> | undefined {
  try {
    const parsed = JSON.parse(readFileSync(path, "utf8")) as unknown;
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)
      : undefined;
  } catch (error) {
    const code =
      typeof error === "object" && error !== null && "code" in error
        ? String((error as { code?: unknown }).code ?? "")
        : "";
    if (code === "ENOENT") return undefined;
    throw filesystemError(error, path);
  }
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim().length > 0
    ? value.trim()
    : undefined;
}

function isDirectory(path: string): boolean {
  try {
    return statSync(path).isDirectory();
  } catch (error) {
    const code =
      typeof error === "object" && error !== null && "code" in error
        ? String((error as { code?: unknown }).code ?? "")
        : "";
    if (code === "ENOENT") return false;
    throw filesystemError(error, path);
  }
}

/**
 * Local browser profiles share their metadata and cookie database layout but
 * keep their own roots and Safe Storage Keychain items. Adapters stay data-only,
 * so a new source only needs a descriptor.
 */
export function browserProfileDescriptors(
  platform: NodeJS.Platform = process.platform,
  home: string = homedir(),
): BrowserProfileDescriptor[] {
  if (platform === "darwin") {
    const support = join(home, "Library", "Application Support");
    return [
      {
        id: "chrome",
        appName: "Google Chrome",
        root: join(support, "Google", "Chrome"),
        safeStorage: { service: "Chrome Safe Storage", account: "Chrome" },
      },
      {
        id: "chromium",
        appName: "Chromium",
        root: join(support, "Chromium"),
        safeStorage: {
          service: "Chromium Safe Storage",
          account: "Chromium",
        },
      },
      {
        id: "doubao",
        appName: "豆包浏览器",
        root: join(support, "Doubao"),
        safeStorage: {
          service: "Doubao Browser Safe Storage",
          account: "Doubao Browser",
        },
      },
      {
        id: "edge",
        appName: "Microsoft Edge",
        root: join(support, "Microsoft Edge"),
        safeStorage: {
          service: "Microsoft Edge Safe Storage",
          account: "Microsoft Edge",
        },
      },
      {
        id: "brave",
        appName: "Brave",
        root: join(support, "BraveSoftware", "Brave-Browser"),
        safeStorage: { service: "Brave Safe Storage", account: "Brave" },
      },
      {
        id: "vivaldi",
        appName: "Vivaldi",
        root: join(support, "Vivaldi"),
        safeStorage: { service: "Vivaldi Safe Storage", account: "Vivaldi" },
      },
      {
        id: "opera",
        appName: "Opera",
        root: join(support, "com.operasoftware.Opera"),
        safeStorage: { service: "Opera Safe Storage", account: "Opera" },
      },
    ];
  }
  if (platform === "win32") {
    const localAppData = process.env.LOCALAPPDATA;
    return [
      {
        id: "chrome",
        appName: "Google Chrome",
        root: join(
          localAppData && localAppData.trim()
            ? localAppData
            : join(home, "AppData", "Local"),
          "Google",
          "Chrome",
          "User Data",
        ),
      },
    ];
  }
  return [
    {
      id: "chrome",
      appName: "Google Chrome",
      root: join(home, ".config", "google-chrome"),
    },
  ];
}

function profileInfoCache(root: string): Record<string, Record<string, unknown>> {
  const localState = readJSON(join(root, "Local State"));
  const infoCache = record(localState?.profile)?.info_cache;
  const info = record(infoCache);
  if (!info) return {};
  const result: Record<string, Record<string, unknown>> = {};
  for (const [directoryName, value] of Object.entries(info)) {
    const entry = record(value);
    if (entry) result[directoryName] = entry;
  }
  return result;
}

/**
 * Lists local browser profiles without touching cookies or passwords. The
 * profile
 * directory name is what the import request pins; the renderer never supplies
 * an arbitrary filesystem path.
 */
export function listBrowserProfiles(
  descriptors: BrowserProfileDescriptor[] = browserProfileDescriptors(),
): BrowserProfile[] {
  const profiles: BrowserProfile[] = [];
  let firstError: unknown;
  for (const descriptor of descriptors) {
    try {
      if (!isDirectory(descriptor.root)) continue;
      const info = profileInfoCache(descriptor.root);
      const discovered = new Set<string>(Object.keys(info));
      for (const entry of readdirSync(descriptor.root, { withFileTypes: true })) {
        if (!entry.isDirectory()) continue;
        if (entry.name === "Default" || entry.name.startsWith("Profile ")) {
          discovered.add(entry.name);
        }
      }
      for (const profileDirectoryName of discovered) {
        // Some forks keep auxiliary contexts under nested Local State keys
        // (for example an "aha/users/.../profile_explorer" entry). Only
        // direct profile directories belong in the import picker.
        if (
          profileDirectoryName.includes("/") ||
          profileDirectoryName.includes("\\")
        ) {
          continue;
        }
        const profilePath = join(descriptor.root, profileDirectoryName);
        if (!isDirectory(profilePath)) continue;
        const preferences = readJSON(join(profilePath, "Preferences"));
        const profilePreferences = record(preferences?.profile);
        const cached = info[profileDirectoryName] ?? {};
        const profileName =
          stringValue(cached.name) ??
          stringValue(profilePreferences?.name) ??
          profileDirectoryName;
        const userName =
          stringValue(cached.user_name) ??
          stringValue(cached.gaia_name) ??
          stringValue(profilePreferences?.user_name) ??
          stringValue(profilePreferences?.gaia_name);
        const hasCookies = existsSync(join(profilePath, "Cookies"));
        const hasPasswords = existsSync(join(profilePath, "Login Data"));
        if (!hasCookies && !hasPasswords) continue;
        profiles.push({
          browserId: descriptor.id,
          appName: descriptor.appName,
          profileDirectoryName,
          profileName,
          profilePath,
          hasCookies,
          hasPasswords,
          ...(userName ? { userName } : {}),
        });
      }
    } catch (error) {
      // One unreadable browser root must not hide another browser's profiles.
      firstError ??= error;
    }
  }
  if (profiles.length === 0 && firstError) {
    throw filesystemError(firstError, "the browser profile roots");
  }
  return profiles.sort((a, b) => {
    if (a.profileDirectoryName === "Default") return -1;
    if (b.profileDirectoryName === "Default") return 1;
    return `${a.appName}:${a.profileName}`.localeCompare(
      `${b.appName}:${b.profileName}`,
    );
  });
}

/**
 * Resolves a renderer-selected profile against a fresh discovery pass. This is
 * both a TOCTOU check and the boundary that prevents an arbitrary path from
 * reaching the SQLite reader.
 */
export function resolveBrowserProfile(
  profilePath: string,
  descriptors: BrowserProfileDescriptor[] = browserProfileDescriptors(),
): { profile: BrowserProfile; descriptor: BrowserProfileDescriptor } {
  const requested = resolve(profilePath);
  const profile = listBrowserProfiles(descriptors).find(
    (candidate) => resolve(candidate.profilePath) === requested,
  );
  const descriptor = profile
    ? descriptors.find((candidate) => candidate.id === profile.browserId)
    : undefined;
  if (!profile || !descriptor) {
    throw new Error(
      "This browser profile is no longer importable. Refresh the profile list.",
    );
  }
  return { profile, descriptor };
}

export function browserTimestampToUnixSeconds(value: number | bigint): number {
  if (typeof value === "bigint") {
    if (value <= 0n) return 0;
    return (
      Number(value / BigInt(BROWSER_EPOCH_MICROSECONDS)) -
      BROWSER_EPOCH_OFFSET_SECONDS
    );
  }
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.floor(
    value / BROWSER_EPOCH_MICROSECONDS - BROWSER_EPOCH_OFFSET_SECONDS,
  );
}

function numeric(value: number | bigint): number {
  return typeof value === "bigint" ? Number(value) : value;
}

/**
 * Supported source profiles on macOS store protected values as v10 +
 * AES-128-CBC. The key is derived from the browser's Safe Storage Keychain
 * item. This helper intentionally accepts only the macOS v10 form; platform-
 * specific encryption and unknown schemes fail closed instead of importing
 * garbage.
 */
export function decryptBrowserCookieValue(
  encryptedValue: Uint8Array,
  safeStorageSecret: string,
  boundHost?: string,
): string {
  const encrypted = Buffer.from(encryptedValue);
  if (encrypted.length === 0) return "";
  if (encrypted.subarray(0, 3).toString("ascii") !== "v10") {
    throw new Error("Unsupported cookie encryption format.");
  }
  const key = pbkdf2Sync(safeStorageSecret, "saltysalt", 1003, 16, "sha1");
  const iv = Buffer.alloc(16, " ");
  const decipher = createDecipheriv("aes-128-cbc", key, iv);
  const plaintext = Buffer.concat([
    decipher.update(encrypted.subarray(3)),
    decipher.final(),
  ]);
  if (boundHost !== undefined) {
    // Chromium schema 24 binds encrypted values to their host with a SHA-256
    // prefix. Validate before removing it; legacy schemas contain only values.
    const hostHash = createHash("sha256").update(boundHost).digest();
    if (!plaintext.subarray(0, hostHash.length).equals(hostHash)) {
      throw new Error("The encrypted cookie does not match its host.");
    }
    return plaintext.subarray(hostHash.length).toString("utf8");
  }
  return plaintext.toString("utf8");
}

export function browserCookieToSessionCookie(
  row: BrowserCookieRow,
  safeStorageSecret?: () => string,
  databaseVersion = 0,
): CookieSetDetails | null {
  const host = row.host_key.trim();
  const name = row.name.trim();
  const path = row.path.trim().startsWith("/") ? row.path.trim() : "/";
  if (!host || !name || /[\u0000-\u001f\u007f]/.test(name)) return null;
  // Partitioned cookies are scoped to a top-level site. The target cookie API
  // does not preserve that partition here, so importing one as a normal cookie
  // would broaden where it can be sent. Fail closed.
  if (
    typeof row.top_frame_site_key === "string" &&
    row.top_frame_site_key.trim().length > 0
  ) {
    return null;
  }

  let value = row.value;
  if (value.length === 0 && row.encrypted_value.length > 0) {
    if (!safeStorageSecret) {
      throw new Error("The browser Safe Storage key is unavailable.");
    }
    value = decryptBrowserCookieValue(
      row.encrypted_value,
      safeStorageSecret(),
      databaseVersion >= 24 ? row.host_key : undefined,
    );
  }

  const expires = browserTimestampToUnixSeconds(row.expires_utc);
  if (expires > 0 && expires <= Math.floor(Date.now() / 1000)) return null;

  const domain = host.startsWith(".") ? host : undefined;
  const urlHost = host.startsWith(".") ? host.slice(1) : host;
  if (!urlHost) return null;
  const scheme = numeric(row.is_secure) === 1 ? "https" : "http";
  let url: string;
  try {
    url = new URL(`${scheme}://${urlHost}${path}`).toString();
  } catch {
    return null;
  }

  const sameSiteValue = numeric(row.samesite);
  const sameSite =
    sameSiteValue === 0
      ? "no_restriction"
      : sameSiteValue === 1
        ? "lax"
        : sameSiteValue === 2
          ? "strict"
          : "unspecified";

  return {
    url,
    name,
    value,
    path,
    secure: numeric(row.is_secure) === 1,
    httpOnly: numeric(row.is_httponly) === 1,
    sameSite,
    ...(domain ? { domain } : {}),
    ...(expires > 0 ? { expirationDate: expires } : {}),
  };
}

async function readSafeStorageSecretFromKeychain(
  credential: BrowserSafeStorageCredential,
): Promise<string> {
  if (process.platform !== "darwin") {
    throw new Error("Browser cookie import currently supports macOS only.");
  }
  try {
    const { stdout } = await execFileAsync(
      "/usr/bin/security",
      [
        "find-generic-password",
        "-s",
        credential.service,
        "-a",
        credential.account,
        "-w",
      ],
      {
        encoding: "utf8",
        timeout: 60_000,
        maxBuffer: 64 * 1024,
        windowsHide: true,
      },
    );
    const secret = String(stdout).trim();
    if (!secret) throw new Error("empty Keychain secret");
    return secret;
  } catch {
    throw new Error(
      `The ${credential.service} key is unavailable. Unlock the login Keychain and allow wuu to access it, then retry. Close the source browser if its profile is being written.`,
    );
  }
}

async function copyDatabaseSnapshot(profilePath: string): Promise<{
  directory: string;
  database: string;
}> {
  const source = join(profilePath, "Cookies");
  if (!existsSync(source)) {
    throw new Error("This browser profile has no Cookies database.");
  }
  const directory = mkdtempSync(join(tmpdir(), "wuu-browser-cookies-"));
  const database = join(directory, "Cookies");
  let sourceDatabase: DatabaseSync | undefined;
  try {
    // SQLite coordinates the database and WAL while the browser is writing;
    // copying their files separately can combine different committed states.
    sourceDatabase = new DatabaseSync(source, { readOnly: true });
    await backup(sourceDatabase, database);
    return { directory, database };
  } catch (error) {
    rmSync(directory, { recursive: true, force: true });
    throw filesystemError(error, source);
  } finally {
    sourceDatabase?.close();
  }
}

export async function importBrowserCookies({
  profilePath,
  session,
  descriptor,
  readSafeStorageSecret: readSecret = readSafeStorageSecretFromKeychain,
}: BrowserCookieImportOptions): Promise<BrowserCookieImportResult> {
  const resolved = resolveBrowserProfile(
    profilePath,
    descriptor ? [descriptor] : undefined,
  );
  const { profile } = resolved;
  const snapshot = await copyDatabaseSnapshot(profile.profilePath);
  let db: DatabaseSync | undefined;
  let secret: string | undefined;
  let discovered = 0;
  let imported = 0;
  let skippedExpired = 0;
  let skippedInvalid = 0;
  let failed = 0;

  try {
    db = new DatabaseSync(snapshot.database, {
      readOnly: true,
      readBigInts: true,
    });
    const databaseVersion = Number(
      db.prepare("SELECT value FROM meta WHERE key = 'version'").get()?.value,
    );
    if (!Number.isInteger(databaseVersion) || databaseVersion < 0) {
      throw new Error("The browser cookie database has no valid schema version.");
    }
    const rows = db
      .prepare(
        "SELECT host_key, name, value, encrypted_value, path, top_frame_site_key, expires_utc, is_secure, is_httponly, samesite FROM cookies",
      )
      .all() as unknown as BrowserCookieRow[];
    discovered = rows.length;
    // Fail the whole import with the Keychain error when encryption is
    // actually present. Counting every row as a generic failure would hide
    // the actionable cause behind a zero-import result.
    if (
      rows.some(
        (row) =>
          row.encrypted_value instanceof Uint8Array &&
          row.encrypted_value.length > 0,
      )
    ) {
      if (!resolved.descriptor.safeStorage) {
        throw new Error("This browser has no Safe Storage adapter.");
      }
      secret = await readSecret(resolved.descriptor.safeStorage);
    }
    for (const row of rows) {
      const encryptedValue = row.encrypted_value;
      let details: CookieSetDetails | null;
      try {
        details = browserCookieToSessionCookie(
          {
            ...row,
            encrypted_value:
              encryptedValue instanceof Uint8Array
                ? encryptedValue
                : new Uint8Array(encryptedValue ?? []),
          },
          secret ? () => secret! : undefined,
          databaseVersion,
        );
      } catch {
        failed += 1;
        continue;
      }
      if (!details) {
        const expires = browserTimestampToUnixSeconds(row.expires_utc);
        if (expires > 0 && expires <= Math.floor(Date.now() / 1000)) {
          skippedExpired += 1;
        } else {
          skippedInvalid += 1;
        }
        continue;
      }
      try {
        await session.cookies.set(details);
        imported += 1;
      } catch {
        failed += 1;
      }
    }
    // A flush failure does not invalidate cookies already accepted by the
    // in-memory cookie store; report the import result instead of losing it.
    await session.cookies.flushStore().catch(() => undefined);
  } catch (error) {
    throw filesystemError(error, snapshot.database);
  } finally {
    db?.close();
    rmSync(snapshot.directory, { recursive: true, force: true });
  }

  return {
    appName: profile.appName,
    profileName: profile.profileName,
    profilePath: profile.profilePath,
    discovered,
    imported,
    skippedExpired,
    skippedInvalid,
    failed,
  };
}
