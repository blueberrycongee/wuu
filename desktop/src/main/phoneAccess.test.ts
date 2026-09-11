import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PhoneAccess, phoneAddress, phonePairLink, phoneAccessConfig } from "./phoneAccess";
import type { RemoteHostManager } from "./remoteControl";
import { getPhoneAccessEnabled, setPhoneAccessEnabled, getThemePreference, setThemePreference } from "./desktopSettings";
vi.mock("node:os", async importOriginal => ({ ...(await importOriginal<typeof import("node:os")>()), networkInterfaces: () => ({ en0: [{ address: "192.168.1.8", family: "IPv4", internal: false }] }) }));
const mocks = vi.hoisted(() => ({ spawn: vi.fn() }));
vi.mock("node:child_process", () => ({ spawn: mocks.spawn }));
vi.mock("./wuuCommand", () => ({ resolveWuuCommand: () => ({ command: "wuu", args: [], cwd: "/tmp" }) }));
const roots: string[] = [];
afterEach(async () => { vi.clearAllMocks(); vi.unstubAllEnvs(); for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

// The desktop dev session exports WUU_WEB_* for its own phone listener; these
// tests must not inherit the ambient deployment configuration.
beforeEach(() => {
  for (const name of ["WUU_WEB_URL", "WUU_WEB_RELAY_URL", "WUU_WEB_LISTEN", "WUU_WEB_TLS_CERT", "WUU_WEB_TLS_KEY"]) {
    delete process.env[name];
  }
});

it("uses a LAN address and keeps the pairing secret out of HTTP queries", () => {
  const entry = (address: string, internal = false) => ({ address, family: "IPv4", internal, netmask: "", cidr: null, mac: "" } as const);
  expect(phoneAddress({ lo0: [entry("127.0.0.1", true)], en0: [entry("192.168.1.8")] })).toBe("192.168.1.8");
  expect(() => phoneAddress({ lo0: [entry("127.0.0.1", true)] })).toThrow();
  const link = new URL(phonePairLink("http://192.168.1.8:8787/", "wuu://pair?secret=abc")!);
  expect(link.search).toBe("");
  expect(new URLSearchParams(link.hash.slice(1)).get("pair")).toBe("wuu://pair?secret=abc");
});

describe("phone access lifecycle", () => {
  async function fixture({ fail = false, paired = false, settingsPath: savedPath }: { fail?: boolean; paired?: boolean; settingsPath?: string } = {}) {
    const root = await mkdtemp(join(tmpdir(), "phone-web-")); roots.push(root);
    const settingsPath = savedPath ?? join(root, "desktop-settings.json");
    await writeFile(join(root, "index.html"), "Web");
    const child = Object.assign(new EventEmitter(), { stdout: new PassThrough(), stderr: new PassThrough(), kill: vi.fn(() => { queueMicrotask(() => child.emit("close", 0)); return true; }) });
    mocks.spawn.mockImplementation(() => {
      queueMicrotask(() => {
        if (fail) { child.stderr.write("address already in use"); child.emit("exit", 1); child.emit("close", 1); }
        else child.stdout.write("connect url: ws://192.168.1.8:8787/v1/connect\n");
      });
      return child;
    });
    const host = { stopHost: vi.fn(async () => {}), startHost: vi.fn(), waitForPairing: vi.fn(async () => {}), isRunning: () => true, status: vi.fn(async () => ({ devices: paired ? [{ fingerprint: "known-phone" }] : [] })) };
    const appServer = { start: vi.fn(async (_workdir: string) => {}), stop: vi.fn() };
    const changed = vi.fn();
    return { service: new PhoneAccess(host as unknown as RemoteHostManager, root, changed, appServer, settingsPath), host, child, settingsPath, appServer, changed };
  }
  it("uses an external relay without spawning a LAN server and retains desktop execution", async () => {
    vi.stubEnv("WUU_WEB_URL", "https://web.example");
    vi.stubEnv("WUU_WEB_RELAY_URL", "wss://relay.example/v1/connect");
    const { service, host, appServer } = await fixture();
    try {
      await service.setEnabled("/tmp", true);
      expect(mocks.spawn).not.toHaveBeenCalled();
      expect(appServer.start).toHaveBeenCalledWith("/tmp");
      expect(host.startHost).toHaveBeenCalledWith("/tmp", { pair: true, relay: "wss://relay.example/v1/connect" });
      expect(service.url()).toBe("https://web.example/");
    } finally { await service.stop(); }
    expect(appServer.stop).toHaveBeenCalled();
  });
  it("restores an account computer using its saved server and shared execution pool", async () => {
    const { service, host, appServer, settingsPath } = await fixture();
    host.status.mockResolvedValue({ devices: [], account_server: 'https://account.example' } as never);
    setPhoneAccessEnabled(true, settingsPath);
    try {
      await service.restore('/tmp');
      expect(mocks.spawn).not.toHaveBeenCalled();
      expect(appServer.start).toHaveBeenCalledWith('/tmp');
      expect(host.startHost).toHaveBeenCalledWith('/tmp', {pair:false,relay:'wss://account.example/v1/connect'});
    } finally { await service.stop(); }
  });
  it("starts Web before pairing and closes the listener with access", async () => {
    const { service, host, child } = await fixture();
    try {
      await service.setEnabled("/tmp", true);
      expect(host.startHost).toHaveBeenCalledWith("/tmp", { pair: true, relay: expect.stringMatching(/^ws:\/\/.*:8787\/v1\/connect$/), localRelay: "ws://192.168.1.8:8787/v1/connect" });
      expect(service.url()).toMatch(/^http:/);
      await service.stop();
      expect(child.kill).toHaveBeenCalledWith("SIGTERM");
      expect(service.url()).toBeNull();
    } finally { await service.stop(); }
  });
  it("does not open pairing when the listener fails", async () => {
    const { service, host, settingsPath } = await fixture({ fail: true });
    await expect(service.setEnabled("/tmp", true)).rejects.toThrow("address already in use");
    expect(host.startHost).not.toHaveBeenCalled();
    expect(service.url()).toBeNull();
    expect(getPhoneAccessEnabled(settingsPath)).toBe(false);
  });

  it("restores access after desktop restart without reopening enrollment", async () => {
    const first = await fixture();
    await first.service.setEnabled("/tmp", true);
    const url = first.service.url();
    await first.service.shutdown(); // Desktop quit, not the access switch.
    expect(getPhoneAccessEnabled(first.settingsPath)).toBe(true);
    setThemePreference("dark", first.settingsPath);

    const second = await fixture({ settingsPath: first.settingsPath });
    try {
      await second.service.restore("/tmp");
      expect(second.host.startHost).toHaveBeenCalledWith("/tmp", { pair: false, relay: "ws://192.168.1.8:8787/v1/connect", localRelay: "ws://192.168.1.8:8787/v1/connect" });
      expect(second.host.status).toHaveBeenCalledWith("/tmp");
      expect(second.service.url()).toBe(url);
      expect(second.appServer.start).toHaveBeenCalledWith("/tmp");
      expect(second.changed).toHaveBeenCalled();
      await second.service.setEnabled("/tmp", false);
      expect(second.child.kill).toHaveBeenCalledWith("SIGTERM");
      expect(getThemePreference(first.settingsPath)).toBe("dark");
    } finally { await second.service.stop(); }

    const third = await fixture({ settingsPath: first.settingsPath });
    const spawns = mocks.spawn.mock.calls.length;
    await third.service.restore("/tmp");
    expect(mocks.spawn).toHaveBeenCalledTimes(spawns);
    expect(third.host.startHost).not.toHaveBeenCalled();
    expect(getPhoneAccessEnabled(first.settingsPath)).toBe(false);
  });

  it("re-enables known devices without pairing and enrolls new devices only on request", async () => {
    const { service, host } = await fixture({ paired: true });
    try {
      await service.setEnabled("/tmp", true);
      expect(host.startHost).toHaveBeenLastCalledWith("/tmp", { pair: false, relay: "ws://192.168.1.8:8787/v1/connect", localRelay: "ws://192.168.1.8:8787/v1/connect" });
      await service.openPairing("/tmp");
      expect(host.startHost).toHaveBeenLastCalledWith("/tmp", { pair: true, relay: "ws://192.168.1.8:8787/v1/connect", localRelay: "ws://192.168.1.8:8787/v1/connect" });
    } finally { await service.stop(); }
  });

  it("waits for pairing readiness and reports failure without stopping existing access", async () => {
    const { service, host } = await fixture({ paired: true });
    let fail!: (error: Error) => void;
    let entered!: () => void;
    const waiting = new Promise<void>(resolve => { entered = resolve; });
    host.waitForPairing.mockImplementationOnce(() => {
      entered();
      return new Promise((_resolve, reject) => { fail = reject; });
    });
    try {
      await service.setEnabled("/tmp", true);
      const finished = vi.fn();
      const pairing = service.openPairing("/tmp");
      void pairing.then(finished, () => {});
      await waiting;
      expect(finished).not.toHaveBeenCalled();
      const stopped = host.stopHost.mock.calls.length;
      const rejected = expect(pairing).rejects.toThrow("relay unavailable");
      fail(new Error("relay unavailable"));
      await rejected;
      expect(host.stopHost).toHaveBeenCalledTimes(stopped);
      expect(service.enabled()).toBe(true);
    } finally { await service.stop(); }
  });

  it("connects locally while advertising the HTTPS reverse proxy to phones", async () => {
    vi.stubEnv("WUU_WEB_URL", "https://computer.example");
    const { service, host } = await fixture();
    try {
      await service.openPairing("/tmp");
      expect(host.startHost).toHaveBeenCalledWith("/tmp", {
        pair: true, relay: "wss://computer.example/v1/connect", localRelay: "ws://127.0.0.1:8787/v1/connect",
      });
      expect(mocks.spawn.mock.calls[0][1]).toContain("127.0.0.1:8787");
      expect(service.url()).toBe("https://computer.example/");
      expect(host.waitForPairing).toHaveBeenCalledOnce();
    } finally { await service.stop(); }
  });

  it("does not enable access from a missing or malformed preference", async () => {
    const { service, host, settingsPath } = await fixture();
    await service.restore("/tmp");
    await writeFile(settingsPath, JSON.stringify({ phone_access_enabled: "true" }));
    await service.restore("/tmp");
    expect(mocks.spawn).not.toHaveBeenCalled();
    expect(host.startHost).not.toHaveBeenCalled();
  });

  it("keeps enabled intent after restore failure and recovers without enrollment", async () => {
    const first = await fixture({ fail: true });
    setPhoneAccessEnabled(true, first.settingsPath);
    await first.service.restore("/tmp");
    expect(first.service.error()).toContain("address already in use");
    expect(first.service.url()).toBeNull();
    expect(getPhoneAccessEnabled(first.settingsPath)).toBe(true);

    const second = await fixture({ settingsPath: first.settingsPath });
    try {
      await second.service.restore("/tmp");
      expect(second.service.error()).toBeNull();
      expect(second.host.startHost).toHaveBeenCalledWith("/tmp", { pair: false, relay: "ws://192.168.1.8:8787/v1/connect", localRelay: "ws://192.168.1.8:8787/v1/connect" });
    } finally { await second.service.stop(); }
  });

  it("closes the LAN listener if the shared backend cannot start", async () => {
    const { service, host, child, appServer, settingsPath } = await fixture();
    appServer.start.mockRejectedValueOnce(new Error("backend unavailable"));
    setPhoneAccessEnabled(true, settingsPath);
    await service.restore("/tmp");
    expect(service.error()).toBe("backend unavailable");
    expect(service.url()).toBeNull();
    expect(child.kill).toHaveBeenCalledWith("SIGTERM");
    expect(host.startHost).not.toHaveBeenCalled();
    expect(getPhoneAccessEnabled(settingsPath)).toBe(true);
  });

  it("does not run a queued restore after desktop quit", async () => {
    const { service, host, settingsPath } = await fixture();
    setPhoneAccessEnabled(true, settingsPath);
    let release!: () => void;
    const gate = new Promise<void>(resolve => { release = resolve; });
    void service.run(() => gate);
    const restoring = service.run(() => service.restore("/tmp"));
    await service.shutdown();
    release();
    await restoring;
    expect(mocks.spawn).not.toHaveBeenCalled();
    expect(host.startHost).not.toHaveBeenCalled();
    expect(getPhoneAccessEnabled(settingsPath)).toBe(true);
  });

  it("cleans up a restore suspended in backend startup when the desktop quits", async () => {
    const { service, host, child, appServer, settingsPath } = await fixture();
    setPhoneAccessEnabled(true, settingsPath);
    let release!: () => void;
    let entered!: () => void;
    const starting = new Promise<void>(resolve => { entered = resolve; });
    const gate = new Promise<void>(resolve => { release = resolve; });
    appServer.start.mockImplementationOnce(() => { entered(); return gate; });
    const restoring = service.restore("/tmp");
    await starting;
    await service.shutdown();
    release();
    await restoring;
    expect(child.kill).toHaveBeenCalledWith("SIGTERM");
    expect(host.startHost).not.toHaveBeenCalled();
    expect(service.url()).toBeNull();
    expect(service.error()).toBeNull();
    expect(appServer.stop).toHaveBeenCalled();
    expect(getPhoneAccessEnabled(settingsPath)).toBe(true);
  });
});

 it("supports private network addresses and reverse proxies independently of LAN discovery", () => {
  expect(phoneAccessConfig({ WUU_WEB_LISTEN: "100.64.0.1:9000" }).base).toBe("http://100.64.0.1:9000/");
  const proxy = phoneAccessConfig({ WUU_WEB_URL: "https://wuu.example" });
  expect(proxy.args).toContain("127.0.0.1:8787");
  expect(proxy.relay).toBe("wss://wuu.example/v1/connect");
  expect(phoneAccessConfig({ WUU_WEB_URL: "https://web.example", WUU_WEB_RELAY_URL: "wss://relay.example/v1/connect" }).external).toBe(true);
  expect(() => phoneAccessConfig({ WUU_WEB_URL: "https://web.example", WUU_WEB_RELAY_URL: "ws://relay.example/v1/connect" })).toThrow();
  expect(() => phoneAccessConfig({ WUU_WEB_LISTEN: "0.0.0.0:8787" })).toThrow("WUU_WEB_URL");
  expect(() => phoneAccessConfig({ WUU_WEB_TLS_CERT: "cert.pem" })).toThrow();
 });
