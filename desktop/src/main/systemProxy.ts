import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

// Go's HTTP transport reads proxy environment variables, not macOS settings.
// Preserve explicit launch configuration; only bridge static system settings.
export function applySystemProxy(env: NodeJS.ProcessEnv, settings: string): void {
  if (["HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"]
    .some((key) => env[key] !== undefined)) return;
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

export async function inheritSystemProxy(): Promise<void> {
  if (process.platform !== "darwin") return;
  try {
    const { stdout } = await execFileAsync("/usr/sbin/scutil", ["--proxy"], { timeout: 2000 });
    applySystemProxy(process.env, stdout);
  } catch {
    // Proxy discovery must not prevent an offline desktop from starting.
    console.warn("[desktop] could not read macOS proxy settings");
  }
}
