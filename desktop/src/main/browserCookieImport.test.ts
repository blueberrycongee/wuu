import { createCipheriv, createHash, pbkdf2Sync } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import type { Session } from "electron";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  browserProfileDescriptors,
  browserCookieToSessionCookie,
  browserTimestampToUnixSeconds,
  decryptBrowserCookieValue,
  importBrowserCookies,
  listBrowserProfiles,
  type BrowserProfileDescriptor,
} from "./browserCookieImport";

const roots: string[] = [];
const nowSeconds = Math.floor(Date.now() / 1000);
const browserEpochOffset = 11_644_473_600;

function tempRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "wuu-browser-import-test-"));
  roots.push(root);
  return root;
}

function browserMicros(unixSeconds: number): bigint {
  return BigInt(unixSeconds + browserEpochOffset) * 1_000_000n;
}

function encryptCookie(value: string, secret: string, host?: string): Buffer {
  const key = pbkdf2Sync(secret, "saltysalt", 1003, 16, "sha1");
  const cipher = createCipheriv("aes-128-cbc", key, Buffer.alloc(16, " "));
  return Buffer.concat([
    Buffer.from("v10", "ascii"),
    cipher.update(host === undefined ? Buffer.from(value) : Buffer.concat([
      createHash("sha256").update(host).digest(),
      Buffer.from(value),
    ])),
    cipher.final(),
  ]);
}

function createCookieDatabase(path: string): void {
  const db = new DatabaseSync(path);
  db.exec(`
    CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
    INSERT INTO meta VALUES ('version', '24');
    CREATE TABLE cookies (
      host_key TEXT NOT NULL,
      name TEXT NOT NULL,
      value TEXT NOT NULL,
      encrypted_value BLOB NOT NULL,
      path TEXT NOT NULL,
      top_frame_site_key TEXT NOT NULL,
      expires_utc INTEGER NOT NULL,
      is_secure INTEGER NOT NULL,
      is_httponly INTEGER NOT NULL,
      samesite INTEGER NOT NULL
    )
  `);
  const insert = db.prepare(
    "INSERT INTO cookies VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
  );
  insert.run(
    ".example.com",
    "session",
    "",
    encryptCookie("secret-value", "test-secret", ".example.com"),
    "/",
    "",
    browserMicros(nowSeconds + 3600),
    1,
    1,
    1,
  );
  insert.run(
    "expired.example.com",
    "old",
    "",
    encryptCookie("old-value", "test-secret", "expired.example.com"),
    "/",
    "",
    browserMicros(nowSeconds - 3600),
    0,
    0,
    -1,
  );
  insert.run(
    "plain.example.com",
    "plain",
    "plain-value",
    new Uint8Array(),
    "/",
    "",
    0n,
    0,
    0,
    -1,
  );
  db.close();
}

function descriptorFor(root: string): BrowserProfileDescriptor {
  return {
    id: "chrome",
    appName: "Google Chrome",
    root,
    safeStorage: { service: "Chrome Safe Storage", account: "Chrome" },
  };
}

function createProfile(root: string): string {
  const profilePath = join(root, "Default");
  mkdirSync(profilePath, { recursive: true });
  writeFileSync(
    join(root, "Local State"),
    JSON.stringify({
      profile: {
        info_cache: {
          Default: { name: "Work", user_name: "person@example.com" },
        },
      },
    }),
  );
  writeFileSync(
    join(profilePath, "Preferences"),
    JSON.stringify({ profile: { name: "Work" } }),
  );
  createCookieDatabase(join(profilePath, "Cookies"));
  return profilePath;
}

afterEach(() => {
  for (const root of roots.splice(0)) {
    rmSync(root, { recursive: true, force: true });
  }
});

describe("browser cookie import", () => {
  it("ignores nested auxiliary profile contexts", () => {
    const root = tempRoot();
    createProfile(root);
    const nested = join(root, "aha", "users", "account", "profile_explorer");
    mkdirSync(nested, { recursive: true });
    writeFileSync(join(nested, "Cookies"), "");
    writeFileSync(
      join(root, "Local State"),
      JSON.stringify({
        profile: {
          info_cache: {
            Default: { name: "默认" },
            "aha/users/account/profile_explorer": { name: "辅助上下文" },
          },
        },
      }),
    );

    expect(listBrowserProfiles([descriptorFor(root)])).toHaveLength(1);
  });

  it("maps the selected source to its own profile root and Safe Storage item", () => {
    expect(browserProfileDescriptors("darwin", "/Users/test")).toContainEqual({
      id: "doubao",
      appName: "豆包浏览器",
      root: "/Users/test/Library/Application Support/Doubao",
      safeStorage: {
        service: "Doubao Browser Safe Storage",
        account: "Doubao Browser",
      },
    });
  });

  it("discovers local browser profiles without reading credentials", () => {
    const root = tempRoot();
    const profilePath = createProfile(root);

    expect(listBrowserProfiles([descriptorFor(root)])).toEqual([
      {
        browserId: "chrome",
        appName: "Google Chrome",
        profileDirectoryName: "Default",
        profileName: "Work",
        profilePath,
        hasCookies: true,
        hasPasswords: false,
        userName: "person@example.com",
      },
    ]);
  });

  it("decrypts macOS v10 values and rejects unsupported formats", () => {
    const encrypted = encryptCookie("hello", "test-secret");
    expect(decryptBrowserCookieValue(encrypted, "test-secret")).toBe("hello");
    expect(() =>
      decryptBrowserCookieValue(Buffer.from("v20abc"), "test-secret"),
    ).toThrow(/Unsupported/);
  });

  it("converts browser timestamps and preserves cookie attributes", () => {
    const expires = nowSeconds + 60;
    const cookie = browserCookieToSessionCookie({
      host_key: ".example.com",
      name: "session",
      value: "value",
      encrypted_value: new Uint8Array(),
      path: "/app",
      top_frame_site_key: "",
      expires_utc: browserMicros(expires),
      is_secure: 1,
      is_httponly: 1,
      samesite: 1,
    });

    expect(browserTimestampToUnixSeconds(browserMicros(expires))).toBe(expires);
    expect(cookie).toMatchObject({
      url: "https://example.com/app",
      domain: ".example.com",
      name: "session",
      value: "value",
      secure: true,
      httpOnly: true,
      sameSite: "lax",
      expirationDate: expires,
    });
  });

  it("refuses to broaden a partitioned cookie", () => {
    expect(
      browserCookieToSessionCookie({
        host_key: "example.com",
        name: "partitioned",
        value: "value",
        encrypted_value: new Uint8Array(),
        path: "/",
        top_frame_site_key: "https://top.example",
        expires_utc: 0n,
        is_secure: 1,
        is_httponly: 0,
        samesite: -1,
      }),
    ).toBeNull();
  });

  it("imports live cookies, skips expired rows, and never logs values", async () => {
    const root = tempRoot();
    const profilePath = createProfile(root);
    const set = vi.fn(async (_cookie: { name: string; value: string }) => undefined);
    const flushStore = vi.fn(async () => undefined);
    const session = {
      cookies: { set, flushStore },
    } as unknown as Pick<Session, "cookies">;

    const result = await importBrowserCookies({
      profilePath,
      descriptor: descriptorFor(root),
      session,
      readSafeStorageSecret: async () => "test-secret",
    });

    expect(result).toMatchObject({
      profileName: "Work",
      discovered: 3,
      imported: 2,
      skippedExpired: 1,
      skippedInvalid: 0,
      failed: 0,
    });
    expect(set).toHaveBeenCalledTimes(2);
    expect(set.mock.calls.map(([cookie]) => cookie.name)).toEqual([
      "session",
      "plain",
    ]);
    expect(set.mock.calls[0][0]).toMatchObject({ value: "secret-value" });
    expect(flushStore).toHaveBeenCalledOnce();
  });

  it("fails loudly when the source Keychain secret is unavailable", async () => {
    const root = tempRoot();
    const profilePath = createProfile(root);
    const session = {
      cookies: {
        set: vi.fn(async () => undefined),
        flushStore: vi.fn(async () => undefined),
      },
    } as unknown as Pick<Session, "cookies">;

    await expect(
      importBrowserCookies({
        profilePath,
        descriptor: descriptorFor(root),
        session,
        readSafeStorageSecret: async () => {
          throw new Error("Keychain access denied");
        },
      }),
    ).rejects.toThrow(/Keychain access denied/);
  });

  it("rejects a path that is not a currently discovered profile", async () => {
    const root = tempRoot();
    createProfile(root);
    const session = {
      cookies: {
        set: vi.fn(async () => undefined),
        flushStore: vi.fn(async () => undefined),
      },
    } as unknown as Pick<Session, "cookies">;

    await expect(
      importBrowserCookies({
        profilePath: join(root, "..", "SomewhereElse"),
        descriptor: descriptorFor(root),
        session,
        readSafeStorageSecret: async () => "test-secret",
      }),
    ).rejects.toThrow(/no longer importable/);
  });
});
