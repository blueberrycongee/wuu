import { createHash } from "node:crypto";
import { describe, expect, it } from "vitest";

import { cachePluginDesktopModule, digestFromPluginModuleURL } from "./pluginModuleProtocol";

describe("plugin module protocol", () => {
  it("creates a content-addressed URL after verifying source", () => {
    const source = "export const activate = () => {};";
    const digest = createHash("sha256").update(source).digest("hex");
    const result = cachePluginDesktopModule({
      id: "user:demo",
      fingerprint: "package-fingerprint",
      entry: "desktop.js",
      media_type: "text/javascript",
      digest,
      source,
    });

    expect(result.url).toBe(`wuu-plugin://module/${digest}.js`);
    expect(digestFromPluginModuleURL(result.url)).toBe(digest);
  });

  it("rejects changed source and unrelated URLs", () => {
    expect(() => cachePluginDesktopModule({
      id: "user:demo",
      fingerprint: "package-fingerprint",
      entry: "desktop.js",
      media_type: "text/javascript",
      digest: "0".repeat(64),
      source: "changed",
    })).toThrow("digest mismatch");
    expect(digestFromPluginModuleURL("https://example.test/module.js")).toBeUndefined();
    expect(digestFromPluginModuleURL("wuu-plugin://module/not-a-digest.js")).toBeUndefined();
  });
});
