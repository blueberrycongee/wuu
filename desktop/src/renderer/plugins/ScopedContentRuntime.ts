import * as React from "react";
import type { ExtensionInventoryRecord, PluginScopeInitializeParams, WuuDesktopApi } from "../../shared/protocol";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";

type Bridge = Required<Pick<WuuDesktopApi, "initializePluginScope" | "onPluginScopeInvalidated" | "loadPluginDesktopModule" | "reportPluginDesktopActivation" | "requestPluginRuntime" | "cancelPluginRuntime">>;
type ContentModule = { activateContent(api: unknown): void | Promise<void> };
type Loader = (url: string) => Promise<unknown>;
const importModule: Loader = url => import(/* @vite-ignore */ url);

/** A renderer registry for one authenticated source graph and target user scope. */
export class ScopedContentRuntime {
  readonly host: PluginHost;
  readonly controller: WorkbenchController;
  private readonly inventory = new Map<string, ExtensionInventoryRecord>();
  private readonly off: () => void;
  private epoch = 0;
  private disposed = false;
  private abort?: AbortController;
  private cleanup: Promise<void> = Promise.resolve();

  constructor(readonly source: PluginScopeInitializeParams, private readonly bridge: Bridge, private readonly load: Loader = importModule) {
    this.host = new PluginHost({ react: React, reportActivation: params => this.bridge.reportPluginDesktopActivation(params) });
    const request = async (id: string, generation: string, method: string, input: unknown) => {
      const plugin = this.inventory.get(id);
      if (!plugin || plugin.fingerprint !== generation || !plugin.graph_generation || !this.host.isGenerationActive(id, generation)) throw new Error("Scoped renderer is no longer active");
      const signal = this.abort?.signal;
      if (!signal || signal.aborted) throw new Error("Scoped renderer is no longer active");
      const identity = { id, fingerprint: generation, graph_generation: plugin.graph_generation, request_id: crypto.randomUUID() };
      const cancel = () => { void this.bridge.cancelPluginRuntime(identity).catch(error => console.error("Scoped content cancellation failed", error)); };
      signal.addEventListener("abort", cancel, { once: true });
      try {
        if (signal.aborted) { cancel(); throw new Error("Scoped renderer is no longer active"); }
        const result = await this.bridge.requestPluginRuntime({ ...identity, method, input, user_space_id: source.user_space_id, product_domain: source.product_domain, composition_profile: source.composition_profile });
        if (signal.aborted || this.inventory.get(id) !== plugin || !this.host.isGenerationActive(id, generation)) throw new Error("Scoped renderer is no longer active");
        return result.result;
      } finally { signal.removeEventListener("abort", cancel); }
    };
    this.controller = new WorkbenchController(this.host, {
      getStorage: async (id, generation, key, scope) => {
        if (scope !== "user") throw new Error("Scoped content supports only user storage");
        const result = await request(id, generation, "view-state.get", { key });
        if (!result || typeof result !== "object" || !("value" in result) || !(typeof result.value === "string" || result.value === null)) throw new Error("Invalid view state response");
        return result.value;
      },
      setStorage: async (id, generation, key, value, scope) => {
        if (scope !== "user") throw new Error("Scoped content supports only user storage");
        await request(id, generation, "view-state.set", { key, value });
      },
    }, { getItem: () => null, setItem: () => {}, removeItem: () => {} });
    const scope = JSON.stringify([source.user_space_id, source.product_domain, source.composition_profile || undefined]);
    this.off = bridge.onPluginScopeInvalidated(event => {
      if (event.id !== source.id || event.fingerprint !== source.fingerprint || event.graph_generation !== source.graph_generation || event.scope !== scope || this.disposed) return;
      this.revoke();
      void this.refresh().catch(() => {});
    });
  }

  private revoke(): void {
    this.abort?.abort();
    this.abort = undefined;
    ++this.epoch;
    const cleanups: Promise<void>[] = [];
    for (const id of this.inventory.keys()) {
      const cleanup = this.host.unloadAndWait(id);
      if (cleanup) cleanups.push(cleanup);
    }
    if (cleanups.length) this.cleanup = Promise.allSettled([this.cleanup, ...cleanups]).then(() => {});
    this.inventory.clear();
    this.host.setInventoryGraphs([]);
  }

  async refresh(): Promise<void> {
    this.revoke();
    if (this.disposed) return;
    const epoch = this.epoch, abort = new AbortController();
    this.abort = abort;
    await this.cleanup;
    if (this.disposed || epoch !== this.epoch) return;
    const records = await this.bridge.initializePluginScope(this.source);
    if (this.disposed || epoch !== this.epoch) return;
    this.host.setInventoryGraphs(records);
    for (const record of records) {
      const declarations = record.desktop?.content_renderers;
      if (record.kind !== "plugin" || record.enabled === false || !["active", "granted"].includes(record.state)
        || !["official", "granted"].includes(record.approval_state ?? "") || record.runtime_state && record.runtime_state !== "active"
        || !record.graph_generation || !record.fingerprint || !declarations?.length) continue;
      this.inventory.set(record.id, record);
      try {
        await this.host.activateGeneration({ pluginId: record.id, manifestId: record.provenance.plugin_id ?? record.id,
          generation: record.fingerprint, graphGeneration: record.graph_generation, signal: abort.signal,
          register: async api => {
            const loaded = await this.bridge.loadPluginDesktopModule({ id: record.id, fingerprint: record.fingerprint!, graph_generation: record.graph_generation });
            if (loaded.id !== record.id || loaded.fingerprint !== record.fingerprint) throw new Error("Scoped renderer module identity mismatch");
            const value = await this.load(loaded.url);
            abort.signal.throwIfAborted();
            if (!value || typeof value !== "object" || !("activateContent" in value) || typeof value.activateContent !== "function") throw new Error("Scoped renderer module does not export activateContent");
            const activateContent = value.activateContent;
            const module: ContentModule = { activateContent: input => Reflect.apply(activateContent, value, [input]) };
            await module.activateContent(Object.freeze({ react: api.react, ui: api.ui, signal: api.signal,
              registerRenderer: (definition: Parameters<typeof api.registerRenderer>[0]) => {
                if (typeof definition.match !== "string" || !declarations.some(item => item.category === definition.category && item.match === definition.match)) throw new Error("Content renderer does not match its declaration");
                return api.registerRenderer(definition);
              }, registerCSSSnippet: api.registerCSSSnippet, registerStyle: api.registerStyle, registerCleanup: api.registerCleanup }));
          } });
      } catch (error) {
        this.inventory.delete(record.id);
        if (!abort.signal.aborted) console.error("Scoped content renderer activation failed", error);
      }
      if (this.disposed || epoch !== this.epoch) return;
    }
  }

  async dispose(): Promise<void> {
    if (this.disposed) return;
    this.disposed = true;
    this.off();
    this.revoke();
    await this.cleanup;
    this.controller.dispose();
  }
}

type Entry = { runtime: ScopedContentRuntime; count: number };
const scopes = new Map<string, Entry>();

/** Share a scoped graph across all mounted messages of the same source. */
export function useScopedContentRuntime(source: { id: string; fingerprint: string; graph_generation: string } | undefined,
  scope: { userSpaceId: string; productDomain: "personal" | "coding"; compositionProfile?: string } | undefined): ScopedContentRuntime | undefined {
  const key = source && scope ? JSON.stringify([source.id, source.fingerprint, source.graph_generation, scope.userSpaceId, scope.productDomain, scope.compositionProfile || undefined]) : undefined;
  const [current, setCurrent] = React.useState<{ key: string; runtime: ScopedContentRuntime }>();
  React.useEffect(() => {
    if (!source || !scope || !key || !window.wuu?.initializePluginScope || !window.wuu.onPluginScopeInvalidated) return;
    let entry = scopes.get(key);
    if (!entry) {
      const runtime = new ScopedContentRuntime({ ...source, user_space_id: scope.userSpaceId, product_domain: scope.productDomain, composition_profile: scope.compositionProfile || undefined }, { ...window.wuu, initializePluginScope: window.wuu.initializePluginScope, onPluginScopeInvalidated: window.wuu.onPluginScopeInvalidated });
      entry = { runtime, count: 0 }; scopes.set(key, entry);
      void runtime.refresh().catch(error => console.error("Scoped content initialization failed", error));
    }
    entry.count++;
    setCurrent({ key, runtime: entry.runtime });
    return () => {
      if (!entry || --entry.count !== 0) return;
      if (scopes.get(key) === entry) scopes.delete(key);
      void entry.runtime.dispose().catch(error => console.error("Scoped content cleanup failed", error));
    };
  }, [key]);
  return current && current.key === key ? current.runtime : undefined;
}
