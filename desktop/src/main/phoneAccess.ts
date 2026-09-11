import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { networkInterfaces } from "node:os";
import { access } from "node:fs/promises";
import { join } from "node:path";
import { resolveWuuCommand } from "./wuuCommand";
import { RemoteHostManager } from "./remoteControl";
import { getPhoneAccessEnabled, setPhoneAccessEnabled } from "./desktopSettings";

export function phoneAddress(interfaces = networkInterfaces()): string {
  const entries = Object.entries(interfaces).sort(([a], [b]) => Number(b === "en0") - Number(a === "en0"));
  for (const [, addresses] of entries) {
    for (const item of addresses ?? []) {
      if (item.family === "IPv4" && !item.internal && /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(item.address)) return item.address;
    }
  }
  throw new Error("未找到局域网地址，请先将电脑连接到 Wi-Fi。");
}

export function phonePairLink(base: string | null, uri: string | null): string | null {
  if (!base || !uri) return null;
  const url = new URL(base);
  url.hash = new URLSearchParams({ pair: uri }).toString();
  return url.href;
}

/** Deployment belongs to the operator; the same host supports LAN, private
 * networks, a local reverse proxy, or a separately hosted blind relay. */
export function phoneAccessConfig(env = process.env): { base: string; relay: string; localRelay?: string; args: string[]; external: boolean } {
  const external = Boolean(env.WUU_WEB_RELAY_URL);
  const configured = env.WUU_WEB_URL;
  if (external && !configured) throw new Error("WUU_WEB_RELAY_URL requires WUU_WEB_URL");
  const cert = env.WUU_WEB_TLS_CERT, key = env.WUU_WEB_TLS_KEY;
  if (Boolean(cert) !== Boolean(key)) throw new Error("WUU_WEB_TLS_CERT and WUU_WEB_TLS_KEY must be supplied together");
  const address = env.WUU_WEB_LISTEN || (!configured ? `${phoneAddress()}:8787` : "127.0.0.1:8787");
  const base = new URL(configured || `${cert ? "https" : "http"}://${address}/`);
  if (!/^https?:$/.test(base.protocol) || base.username || base.password || base.pathname !== "/" || base.search || base.hash) {
    throw new Error("WUU_WEB_URL must be an http(s) origin without credentials, path, query or fragment");
  }
  if (["0.0.0.0", "[::]"].includes(base.hostname)) throw new Error("Set WUU_WEB_URL to a reachable address when listening on all interfaces");
  const relay = new URL(env.WUU_WEB_RELAY_URL || "v1/connect", base);
  if (!external) relay.protocol = base.protocol === "https:" ? "wss:" : "ws:";
  if (!/^wss?:$/.test(relay.protocol) || relay.username || relay.password || relay.hash || (base.protocol === "https:" && relay.protocol !== "wss:")) {
    throw new Error("WUU_WEB_RELAY_URL must be a ws(s) URL; HTTPS pages require wss");
  }
  // The desktop can reach its own HTTP listener without going through the
  // public reverse proxy or its DNS. TLS listeners retain the certificate's
  // advertised hostname for verification.
  let localRelay: string | undefined;
  if (!external) {
    const local = new URL(`ws://${address}/v1/connect`);
    if (local.hostname === "0.0.0.0") local.hostname = "127.0.0.1";
    if (local.hostname === "[::]") local.hostname = "[::1]";
    localRelay = cert ? relay.href : local.href;
  }
  return { base: base.href, relay: relay.href, localRelay, external, args: ["--addr", address, "--public-url", base.href, ...(cert && key ? ["--tls-cert", cert, "--tls-key", key] : [])] };
}

/** Owns the LAN listener alongside the existing encrypted remote host. */
export class PhoneAccess {
  private relay: ChildProcessWithoutNullStreams | null = null;
  private exited: Promise<void> | null = null;
  private base: string | null = null;
  private queue: Promise<unknown> = Promise.resolve();
  private restoreError: string | null = null;
  private shuttingDown = false;
  constructor(private readonly host: RemoteHostManager, private readonly webRoot: string, private readonly changed: () => void, private readonly appServer?: { start(workdir: string): Promise<unknown>; stop(): void }, private readonly settingsPath?: string) {}
  url(): string | null { return this.base; }
  error(): string | null { return this.restoreError; }
  enabled(): boolean { return getPhoneAccessEnabled(this.settingsPath); }
  run<T>(action: () => Promise<T>): Promise<T> {
    const next = this.queue.then(action);
    this.queue = next.catch(() => {});
    return next;
  }
  async restore(workdir: string): Promise<void> {
    if (this.shuttingDown || !getPhoneAccessEnabled(this.settingsPath)) return;
    try {
      // Restarting the desktop restores access, never permission to enroll a
      // new device. Existing browser credentials remain valid across restarts.
      await this.start(workdir, false);
      this.changed();
    } catch (error) {
      if (this.shuttingDown) return;
      this.restoreError = error instanceof Error ? error.message : String(error);
      this.changed();
    }
  }
  async setEnabled(workdir: string, enabled: boolean): Promise<void> {
    if (!enabled) {
      // Save explicit disablement even if process cleanup later fails.
      setPhoneAccessEnabled(false, this.settingsPath);
      await this.stop();
      return;
    }
    this.assertOpen();
    const status = await this.host.status(workdir);
    await this.start(workdir, status.devices.length === 0);
    setPhoneAccessEnabled(true, this.settingsPath);
  }
  async openPairing(workdir: string): Promise<void> {
    await this.start(workdir, true);
    setPhoneAccessEnabled(true, this.settingsPath);
  }
  private async start(workdir: string, pair = false): Promise<void> {
    this.assertOpen();
    this.restoreError = null;
    if (this.base && this.host.isRunning() && !pair) return;
    const status = await this.host.status(workdir);
    const config = status.account_server ? { base: status.account_server, relay: status.account_server.replace(/^http/, 'ws') + '/v1/connect', external: true, args: [] } : phoneAccessConfig();
    if (status.account_server) pair = false;
    if (!this.relay && !config.external) {
      await access(join(this.webRoot, "index.html"));
      this.assertOpen();
      const command = resolveWuuCommand(process.env, workdir, process.env.WUU_SOURCE_ROOT, process.resourcesPath);
      const relay = spawn(command.command, [...command.args, "relay", ...config.args, "--web-root", this.webRoot], { cwd: command.cwd, env: process.env });
      this.relay = relay;
      this.exited = new Promise(resolve => relay.once("close", () => resolve()));
      relay.once("close", () => {
        if (this.relay !== relay) return;
        this.relay = null; this.base = null;
        this.appServer?.stop();
        void this.host.stopHost().then(this.changed, this.changed);
      });
      try {
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(() => finish(new Error("手机访问服务启动超时")), 15_000);
          let output = "", errors = "";
          const finish = (error?: Error) => {
            clearTimeout(timer);
            relay.stdout.off("data", onData); relay.stderr.off("data", onErrorData);
            relay.off("error", onError); relay.off("exit", onExit);
            error ? reject(error) : resolve();
          };
          const onData = (chunk: Buffer) => { output += chunk.toString(); if (output.includes("connect url:")) finish(); };
          const onErrorData = (chunk: Buffer) => { errors = (errors + chunk.toString()).slice(-2000); };
          const onError = (error: Error) => finish(error);
          const onExit = () => finish(new Error(errors || "手机访问服务未能启动"));
          relay.stdout.on("data", onData); relay.stderr.on("data", onErrorData);
          relay.once("error", onError); relay.once("exit", onExit);
        });
        // Drain ongoing diagnostics without retaining pairing data or logs.
        relay.stdout.resume(); relay.stderr.resume();
        this.assertOpen();
        this.base = config.base;
      } catch (error) { await this.stop(); throw error; }
    }
    this.base = config.base;
    try {
      await this.host.stopHost();
      this.assertOpen();
      await this.appServer?.start(workdir);
      this.assertOpen();
      this.host.startHost(workdir, { pair, relay: config.relay, ...("localRelay" in config && config.localRelay ? { localRelay: config.localRelay } : {}) });
    } catch (error) { await this.stop(); throw error; }
    // Keep the action pending until the relay confirms enrollment readiness.
    // A timeout leaves existing phone access running and lets the user retry.
    if (pair) await this.host.waitForPairing();
  }
  shutdown(): Promise<void> {
    // Quit must invalidate queued and suspended starts before killing children.
    this.shuttingDown = true;
    return this.stop();
  }
  private assertOpen(): void {
    if (this.shuttingDown) throw new Error("Phone access is shutting down");
  }
  async stop(): Promise<void> {
    // Process shutdown is not user disablement: preserve the saved preference.
    this.restoreError = null;
    const hostStopped = this.host.stopHost();
    this.appServer?.stop();
    const relay = this.relay, exited = this.exited;
    this.relay = null; this.base = null;
    if (relay) {
      const timer = setTimeout(() => relay.kill("SIGKILL"), 3000);
      try { relay.kill("SIGTERM"); await exited; } finally { clearTimeout(timer); }
    }
    await hostStopped;
    this.changed();
  }
}
