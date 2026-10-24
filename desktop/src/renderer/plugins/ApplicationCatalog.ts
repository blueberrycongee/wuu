import type { ApplicationCatalogV1, ApplicationCatalogSnapshotV1, CatalogCallOptionsV1, CatalogPackageUpdateV1, CatalogPackageResultV1, CatalogSkillV1, CatalogExtensionV1, CatalogThreadSkillsInputV1, CatalogThreadSkillsV1 } from "../../../../packages/plugin-sdk/src/ui-catalog";
import type { ExtensionInventoryRecord } from "../../shared/protocol";
import { hostSupports } from "../HostCapabilities";

function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => {
    if (!item || typeof item !== "object" || Object.isFrozen(item)) return;
    Object.values(item).forEach(freeze); Object.freeze(item);
  };
  freeze(copy); return copy;
}

/** Host transport and source fences; the captured application owns admission. */
export class ApplicationCatalog implements ApplicationCatalogV1 {
  private snapshot?: ApplicationCatalogSnapshotV1;
  private snapshotKey = "";
  constructor(private readonly host: {
    getSource: () => {contextKey: string; inventory: readonly ExtensionInventoryRecord[]};
    assertActive: () => void;
  }) {}

  getSnapshot(): ApplicationCatalogSnapshotV1 {
    this.host.assertActive();
    const source = this.host.getSource();
    const inventory: CatalogExtensionV1[] = source.inventory.map(record => ({
      id:record.id, name:record.name, description:record.description, long_description:record.long_description,
      developer:record.developer, icon:record.icon, kind:record.kind, provenance:record.provenance, state:record.state,
      fingerprint:record.fingerprint, graph_generation:record.graph_generation, package_source:record.package_source,
      requested_permissions:record.requested_permissions, approval_state:record.approval_state,
      runtime_state:record.runtime_state, last_error:record.last_error, activation_issues:record.activation_issues,
      enabled:record.enabled, enabled_override:record.enabled_override, pending_update:record.pending_update,
      contributions:record.contributions,
    }));
    const capabilities = {install:hostSupports("installPluginPackage"),update:hostSupports("updateExtensionPackage"),remove:hostSupports("removePluginPackage")};
    const key = JSON.stringify([source.contextKey, inventory, capabilities]);
    if (!this.snapshot || key !== this.snapshotKey) {
      this.snapshot = immutable({contextKey:source.contextKey,inventory,capabilities}); this.snapshotKey=key;
    }
    return this.snapshot;
  }
  private begin(options?: CatalogCallOptionsV1): string {
    this.host.assertActive(); options?.signal?.throwIfAborted(); return this.host.getSource().contextKey;
  }
  private finish(source: string, options?: CatalogCallOptionsV1, discardChanged=false): boolean {
    this.host.assertActive(); options?.signal?.throwIfAborted();
    if (source === this.host.getSource().contextKey) return true;
    if (discardChanged) return false;
    throw new Error("Catalog workspace changed during the operation");
  }
  async listSkills(options?: CatalogCallOptionsV1): Promise<readonly CatalogSkillV1[]> {
    const source=this.begin(options); const result=await window.wuu.listSkills(); this.finish(source,options); return immutable(result.skills);
  }
  async listSkillsForThread(input: CatalogThreadSkillsInputV1, options?: CatalogCallOptionsV1): Promise<CatalogThreadSkillsV1> {
    const source = this.begin(options);
    if (input.contextKey !== undefined && input.contextKey !== source) throw new Error("Catalog workspace changed before the operation");
    const threadId = input.threadId || undefined;
    const result = await window.wuu.listSkills(threadId ? { thread_id: threadId } : undefined);
    this.finish(source, options);
    return immutable({ contextKey: source, threadId, skills: result.skills });
  }
  async readSkill(input: {name:string;source:string}, options?: CatalogCallOptionsV1): Promise<{content:string}> {
    const source=this.begin(options); const result=await window.wuu.readSkillContent(input); this.finish(source,options); return {content:result.content};
  }
  async refresh(options?: CatalogCallOptionsV1): Promise<readonly CatalogSkillV1[] | undefined> {
    const source=this.begin(options); const result=await window.wuu.refreshExtensionCatalog();
    return this.finish(source,options,true) ? immutable(result.skills) : undefined;
  }
  async update(input: CatalogPackageUpdateV1, options?: CatalogCallOptionsV1): Promise<void> {
    const source=this.begin(options);
    await window.wuu.updateExtensionPackage(input.updates ? { updates: input.updates.map(update => ({ ...update })) } : { ...input });
    this.finish(source,options);
  }
  async install(options?: CatalogCallOptionsV1): Promise<CatalogPackageResultV1 | undefined> {
    const source=this.begin(options); const result=await window.wuu.installPluginPackage();
    return this.finish(source,options,true) && result ? immutable({skills:result.skills,extension_inventory:result.extension_inventory,package:{id:result.package.id}}) : undefined;
  }
  async remove(id: string, options?: CatalogCallOptionsV1): Promise<CatalogPackageResultV1 | undefined> {
    const source=this.begin(options); const result=await window.wuu.removePluginPackage(id);
    return this.finish(source,options,true) ? immutable({skills:result.skills,extension_inventory:result.extension_inventory}) : undefined;
  }
  async loadIcon(id:string,fingerprint:string,path:string, options?: CatalogCallOptionsV1): Promise<string> {
    const source=this.begin(options); const result=await window.wuu.loadPluginIcon({id,fingerprint,path}); this.finish(source,options);
    if (result.id !== id || result.fingerprint !== fingerprint || result.path !== path) throw new Error("Plugin icon identity mismatch");
    return result.url;
  }
}
