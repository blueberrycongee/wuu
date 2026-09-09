import { readFile, stat } from "node:fs/promises";
import type {
  SkillContentParams,
  SkillContentResult,
  SkillListResult,
} from "../shared/protocol";

/** Resolve content through the active catalog, never a caller-supplied path. */
export async function readCatalogSkill(
  catalog: SkillListResult,
  params: SkillContentParams,
): Promise<SkillContentResult> {
  const { name, source } = params ?? {};
  if (
    typeof name !== "string" ||
    !name ||
    typeof source !== "string" ||
    !source
  )
    throw new Error("Invalid skill content request");
  const skill = catalog.skills.find(
    (s) => s.name === name && s.source === source,
  );
  if (!skill?.path) throw new Error("Skill content unavailable");
  const info = await stat(skill.path);
  if (!info.isFile() || info.size > 512 * 1024)
    throw new Error("Skill content unavailable");
  return { content: await readFile(skill.path, "utf8") };
}
