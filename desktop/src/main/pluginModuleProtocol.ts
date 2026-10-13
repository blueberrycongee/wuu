import { protocol, type CustomScheme } from "electron";
import { createHash } from "node:crypto";

import type {
  PluginDesktopModuleLoadResult,
  PluginDesktopModuleReadResult,
  PluginIconLoadResult,
  PluginIconReadResult,
} from "../shared/protocol";

const PLUGIN_MODULE_SCHEME = "wuu-plugin";
const MAX_CACHED_MODULES = 32;
const modules = new Map<string, string>();
const assets = new Map<string, { data: ArrayBuffer; mediaType: string }>();

export const pluginModuleScheme: CustomScheme = {
  scheme: PLUGIN_MODULE_SCHEME,
  privileges: {
    standard: true,
    secure: true,
    supportFetchAPI: true,
    corsEnabled: true,
    codeCache: true,
  },
};

export function registerPluginModuleProtocol(): void {
  protocol.handle(PLUGIN_MODULE_SCHEME, (request) => {
	const assetDigest = digestFromPluginAssetURL(request.url);
	const asset = assetDigest ? assets.get(assetDigest) : undefined;
	if (asset) {
	  return new Response(asset.data, {
		headers: {
		  "Content-Type": asset.mediaType,
		  "Cache-Control": "no-store",
		  "Cross-Origin-Resource-Policy": "same-origin",
		  "X-Content-Type-Options": "nosniff",
		},
	  });
	}
    const digest = digestFromPluginModuleURL(request.url);
    const source = digest ? modules.get(digest) : undefined;
    if (!source) {
      return new Response("Not found", { status: 404 });
    }
    return new Response(source, {
      headers: {
        "Content-Type": "text/javascript; charset=utf-8",
        "Cache-Control": "no-store",
        "Cross-Origin-Resource-Policy": "same-origin",
      },
    });
  });
}

export function cachePluginIcon(icon: PluginIconReadResult): PluginIconLoadResult {
  const data = Buffer.from(icon.data, "base64");
  const digest = createHash("sha256").update(data).digest("hex");
  if (digest !== icon.digest || !/^[a-f0-9]{64}$/.test(digest)) {
    throw new Error("Plugin icon digest mismatch");
  }
  assets.delete(digest);
  const body = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) as ArrayBuffer;
  assets.set(digest, { data: body, mediaType: icon.media_type });
  while (assets.size > MAX_CACHED_MODULES * 4) {
    const oldest = assets.keys().next().value;
    if (typeof oldest !== "string") break;
    assets.delete(oldest);
  }
  return {
    id: icon.id,
    fingerprint: icon.fingerprint,
    path: icon.path,
    digest,
    url: `${PLUGIN_MODULE_SCHEME}://asset/${digest}`,
  };
}

export function cachePluginDesktopModule(
  module: PluginDesktopModuleReadResult,
): PluginDesktopModuleLoadResult {
  const digest = createHash("sha256").update(module.source, "utf8").digest("hex");
  if (digest !== module.digest || !/^[a-f0-9]{64}$/.test(digest)) {
    throw new Error("Desktop plugin module digest mismatch");
  }
  modules.delete(digest);
  modules.set(digest, module.source);
  while (modules.size > MAX_CACHED_MODULES) {
    const oldest = modules.keys().next().value;
    if (typeof oldest !== "string") {
      break;
    }
    modules.delete(oldest);
  }
  return {
    id: module.id,
    fingerprint: module.fingerprint,
    digest,
    url: `${PLUGIN_MODULE_SCHEME}://module/${digest}.js`,
  };
}

export function digestFromPluginModuleURL(value: string): string | undefined {
  try {
    const url = new URL(value);
    if (url.protocol !== `${PLUGIN_MODULE_SCHEME}:` || url.hostname !== "module") {
      return undefined;
    }
    const match = /^\/([a-f0-9]{64})\.js$/.exec(url.pathname);
    return match?.[1];
  } catch {
    return undefined;
  }
}

export function digestFromPluginAssetURL(value: string): string | undefined {
  try {
    const url = new URL(value);
    if (url.protocol !== `${PLUGIN_MODULE_SCHEME}:` || url.hostname !== "asset") {
      return undefined;
    }
    return /^\/([a-f0-9]{64})$/.exec(url.pathname)?.[1];
  } catch {
    return undefined;
  }
}
