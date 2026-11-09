import { execFileSync } from "node:child_process";
import { describe, expect, it } from "vitest";
import { normalizeProxyURL } from "../shared/protocol";
import { applySystemProxy, overrideProxy } from "./systemProxy";

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

describe("explicit proxy preference", () => {
  const inherited = { HTTP_PROXY: "http://launch.example:1", https_proxy: "http://launch.example:2", ALL_PROXY: "socks5://launch.example:3" };

  it("replaces inherited proxies with the manual address and keeps local traffic direct", () => {
    const env: NodeJS.ProcessEnv = { ...inherited };
    overrideProxy(env, "http://127.0.0.1:7890");
    const output = execFileSync(process.execPath, ["-e", "console.log(JSON.stringify(process.env))"], { env, encoding: "utf8" });
    const child = JSON.parse(output);
    expect(child).toMatchObject({ HTTP_PROXY: "http://127.0.0.1:7890", HTTPS_PROXY: "http://127.0.0.1:7890", NO_PROXY: "localhost,127.0.0.1,::1" });
    expect(child).not.toHaveProperty("https_proxy");
    expect(child).not.toHaveProperty("ALL_PROXY");
  });

  it("removes every inherited proxy when proxies are turned off", () => {
    const env: NodeJS.ProcessEnv = { ...inherited, no_proxy: "internal.example" };
    overrideProxy(env, undefined);
    expect(env).toEqual({ no_proxy: "internal.example" });
  });

  it("accepts only addresses Go can use as a proxy", () => {
    expect(normalizeProxyURL(" 127.0.0.1:7890 ")).toBe("http://127.0.0.1:7890");
    expect(normalizeProxyURL("socks5://127.0.0.1:1080")).toBe("socks5://127.0.0.1:1080");
    for (const input of ["", "ftp://127.0.0.1:21", "http://", "not a url"]) {
      expect(normalizeProxyURL(input)).toBeUndefined();
    }
  });
});
