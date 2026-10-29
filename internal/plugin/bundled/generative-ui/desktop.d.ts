export function activate(api: import("../../../../packages/plugin-sdk/src").PluginGenerationApi): Promise<void>;
/** Shared data-only validation used by the tool runtime and desktop renderer. */
export function parseSpec(raw: string): {
  readonly version: 1 | 2;
  readonly title: string;
  readonly fallback: string;
  readonly [key: string]: unknown;
};
