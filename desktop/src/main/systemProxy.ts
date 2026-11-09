import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { normalizeProxyURL, type DesktopPreferences } from "../shared/protocol";

const execFileAsync = promisify(execFile);

const PROXY_KEYS = ["HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"];

// Go's HTTP transport reads proxy environment variables, not macOS settings.
// Preserve explicit launch configuration; only bridge static system settings.
export function applySystemProxy(env: NodeJS.ProcessEnv, settings: string): void {
  if (PROXY_KEYS.some((key) => env[key] !== undefined)) return;
  const value = (key: string) => settings.match(new RegExp(`^\\s*${key} : (.+)$`, "m"))?.[1]?.trim();
  // PAC routing is destination-dependent and cannot be represented by one URL.
  if (value("ProxyAutoConfigEnable") === "1" || value("ProxyAutoDiscoveryEnable") === "1") return;
  for (const protocol of ["HTTP", "HTTPS"]) {
    const host = value(`${protocol}Proxy`);
    const port = Number(value(`${protocol}Port`));
    if (value(`${protocol}Enable`) !== "1" || !host || !Number.isInteger(port) || port < 1 || port > 65535) continue;
    const authority = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
    env[`${protocol}_PROXY`] = `http://${authority}:${port}`;
  }
  if (!env.HTTP_PROXY && !env.HTTPS_PROXY) return;
  if (env.NO_PROXY !== undefined || env.no_proxy !== undefined) return;
  const exceptions = settings.match(/ExceptionsList : <array> \{([\s\S]*?)\}/)?.[1] ?? "";
  const hosts = [...exceptions.matchAll(/^\s*\d+ : (.+)$/gm)]
    .map((match) => match[1].trim())
    // Go supports CIDRs and domain suffixes, but not Apple's <local> token.
    .filter((host) => !host.startsWith("<"));
  env.NO_PROXY = [...new Set(["localhost", "127.0.0.1", "::1", ...hosts])].join(",");
}

// A choice made in Settings replaces proxies inherited from the launch
// environment; an undefined address means connect directly.
export function overrideProxy(env: NodeJS.ProcessEnv, url: string | undefined): void {
  for (const key of PROXY_KEYS) delete env[key];
  if (!url) return;
  env.HTTP_PROXY = url;
  env.HTTPS_PROXY = url;
  if (env.NO_PROXY === undefined && env.no_proxy === undefined) env.NO_PROXY = "localhost,127.0.0.1,::1";
}

// Backend children inherit process.env when they spawn, so this runs once at
// startup and a changed preference applies after a restart.
export async function configureBackendProxy(preferences: Pick<DesktopPreferences, "proxy_mode" | "proxy_url">): Promise<void> {
  const url = normalizeProxyURL(preferences.proxy_url);
  if (preferences.proxy_mode === "none") return overrideProxy(process.env, undefined);
  // Manual mode without an address keeps following the system.
  if (preferences.proxy_mode === "manual" && url) return overrideProxy(process.env, url);
  await inheritSystemProxy();
}

async function inheritSystemProxy(): Promise<void> {
  if (process.platform !== "darwin") return;
  try {
    const { stdout } = await execFileAsync("/usr/sbin/scutil", ["--proxy"], { timeout: 2000 });
    applySystemProxy(process.env, stdout);
  } catch {
    // Proxy discovery must not prevent an offline desktop from starting.
    console.warn("[desktop] could not read macOS proxy settings");
  }
}
