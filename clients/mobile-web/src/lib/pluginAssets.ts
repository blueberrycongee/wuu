import type {
  PluginDesktopModuleReadResult,
  PluginDesktopModuleLoadResult,
  PluginIconReadResult,
  PluginIconLoadResult,
} from "@wuu/protocol";

/** Assets originate from extensions already installed on the selected computer. */
export class PluginAssets {
  private generation = 0;
  private urls = new Map<string, string>();
  private async url(
    data: Uint8Array,
    digest: string,
    mediaType: string,
  ): Promise<string> {
    const generation = this.generation;
    const actual = Array.from(
      new Uint8Array(
        await crypto.subtle.digest("SHA-256", new Uint8Array(data).buffer),
      ),
      (byte) => byte.toString(16).padStart(2, "0"),
    ).join("");
    if (generation !== this.generation)
      throw new Error("Plugin connection was closed");
    if (actual !== digest) throw new Error("Plugin asset digest mismatch");
    const key = mediaType + ":" + digest;
    let url = this.urls.get(key);
    if (!url) {
      url = URL.createObjectURL(
        new Blob([new Uint8Array(data).buffer], { type: mediaType }),
      );
      this.urls.set(key, url);
    }
    return url;
  }
  async module(
    module: PluginDesktopModuleReadResult,
  ): Promise<PluginDesktopModuleLoadResult> {
    const url = await this.url(
      new TextEncoder().encode(module.source),
      module.digest,
      "text/javascript",
    );
    return {
      id: module.id,
      fingerprint: module.fingerprint,
      digest: module.digest,
      url,
    };
  }
  async icon(icon: PluginIconReadResult): Promise<PluginIconLoadResult> {
    if (!["image/svg+xml", "image/png", "image/webp"].includes(icon.media_type))
      throw new Error("Unsupported plugin icon");
    const data = Uint8Array.from(atob(icon.data), (c) => c.charCodeAt(0));
    const url = await this.url(data, icon.digest, icon.media_type);
    return {
      id: icon.id,
      fingerprint: icon.fingerprint,
      path: icon.path,
      digest: icon.digest,
      url,
    };
  }
  clear(): void {
    this.generation++;
    for (const url of this.urls.values()) URL.revokeObjectURL(url);
    this.urls.clear();
  }
}
