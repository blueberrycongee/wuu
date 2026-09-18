import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { applySystemProxy } from "./systemProxy";

const settings = `<dictionary> {
  ExceptionsList : <array> {
    0 : 192.168.0.0/16
    1 : *.local
    2 : <local>
  }
  HTTPEnable : 1
  HTTPProxy : 127.0.0.1
  HTTPPort : 7897
  HTTPSEnable : 1
  HTTPSProxy : 127.0.0.1
  HTTPSPort : 7897
}`;

describe("macOS backend proxy inheritance", () => {
  it("passes enabled system proxies and local bypasses to backend children", () => {
    const env: NodeJS.ProcessEnv = {};
    applySystemProxy(env, settings);
    const output = execFileSync(process.execPath, ["-e", "console.log(JSON.stringify(process.env))"], { env, encoding: "utf8" });
    const child = JSON.parse(output);
    expect(child.HTTP_PROXY).toBe("http://127.0.0.1:7897");
    expect(child.HTTPS_PROXY).toBe("http://127.0.0.1:7897");
    expect(child.NO_PROXY.split(",")).toEqual(["localhost", "127.0.0.1", "::1", "192.168.0.0/16", "*.local"]);
  });

  it.each(["HTTP_PROXY", "https_proxy", "ALL_PROXY"])("preserves explicit %s including an empty override", (key) => {
    for (const value of ["http://custom.example:8080", ""]) {
      const env = { [key]: value };
      applySystemProxy(env, settings);
      expect(env).toEqual({ [key]: value });
    }
  });

  it("preserves explicit bypasses while inheriting the proxy", () => {
    const env = { no_proxy: "internal.example" };
    applySystemProxy(env, settings);
    expect(env).toMatchObject({ no_proxy: "internal.example", HTTPS_PROXY: "http://127.0.0.1:7897" });
    expect(env).not.toHaveProperty("NO_PROXY");
  });

  it("does not turn disabled or PAC settings into a global proxy", () => {
    for (const input of ["", settings.replaceAll("Enable : 1", "Enable : 0"), settings + "\nProxyAutoConfigEnable : 1", settings + "\nProxyAutoDiscoveryEnable : 1"]) {
      const env = {};
      applySystemProxy(env, input);
      expect(env).toEqual({});
    }
  });
});
