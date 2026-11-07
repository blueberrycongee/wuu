import type { CatalogExtensionV1 } from "../../../packages/plugin-sdk/src/ui-catalog";

type FamilyMember = { id: string; name: string; family?: CatalogExtensionV1["family"] };

export type PluginFamilyGroup<T extends FamilyMember> = {
  id: string;
  name: string;
  description?: string;
  family: boolean;
  members: T[];
};

/** Presentation groups only; package identity, trust and lifecycle stay independent. */
export function groupPluginFamilies<T extends FamilyMember>(records: readonly T[], locale: string): PluginFamilyGroup<T>[] {
  const groups = new Map<string, PluginFamilyGroup<T>>();
  // Stable member ordering also makes inconsistent family labels deterministic.
  for (const record of [...records].sort((a, b) => a.id.localeCompare(b.id))) {
    const family = record.family;
    const id = family ? `plugin-family:${encodeURIComponent(family.id)}` : record.id;
    const group = groups.get(id);
    if (group) group.members.push(record);
    else groups.set(id, { id, name: family?.name ?? record.name, description: family?.description, family: Boolean(family), members: [record] });
  }
  for (const group of groups.values()) group.members.sort((a, b) => a.name.localeCompare(b.name, locale));
  return [...groups.values()].sort((a, b) => a.name.localeCompare(b.name, locale));
}
