import { mkdir, readdir, realpath } from "node:fs/promises";
import { dirname, join, isAbsolute } from "node:path";
import { homedir } from "node:os";
import type { ProjectManager } from "./projects";

/** Account controllers act as the signed-in desktop user, including choosing
 * a new workspace. Directory results contain folder names, never file content. */
export async function requestRemoteProjects(
  projects: ProjectManager,
  method: string,
  input: unknown,
): Promise<unknown> {
  const p = (input ?? {}) as {
    path?: string;
    name?: string;
    id?: string;
    fresh?: boolean;
    cwd?: string;
    includeArchives?: boolean;
  };
  if (
    p.path !== undefined &&
    (typeof p.path !== "string" || !isAbsolute(p.path))
  )
    throw new Error("An absolute computer folder is required");
  switch (method) {
    case "desktop/projects/folders": {
      const path = await realpath(p.path || homedir());
      const entries = await readdir(path, { withFileTypes: true });
      const folders = entries
        .filter((e) => e.isDirectory())
        .map((e) => ({ name: e.name, path: join(path, e.name) }))
        .sort((a, b) => a.name.localeCompare(b.name));
      const archives = p.includeArchives === true ? entries
        .filter(e => e.isFile() && e.name.toLowerCase().endsWith('.zip'))
        .map(e => ({name:e.name,path:join(path,e.name)}))
        .sort((a,b) => a.name.localeCompare(b.name)) : [];
      return {
        path,
        parent: dirname(path),
        folders: folders.slice(0, 1000),
        archives: archives.slice(0, 1000),
        truncated: folders.length > 1000 || archives.length > 1000,
      };
    }
    case "desktop/projects/mkdir":
      if (
        !p.path ||
        typeof p.name !== "string" ||
        !p.name.trim() ||
        p.name === "." ||
        p.name === ".." ||
        /[\\/\0]/.test(p.name)
      )
        throw new Error("Invalid folder name");
      await mkdir(join(p.path, p.name));
      return { path: join(p.path, p.name) };
    case "desktop/projects/add":
      if (!p.path) throw new Error("Folder is required");
      return projects.add(p.path);
    case "desktop/projects/remove":
      if (typeof p.id !== "string") throw new Error("Project id is required");
      return projects.remove(p.id);
    case "desktop/projects/relocate":
      if (!p.path || typeof p.id !== "string")
        throw new Error("Project and folder are required");
      return projects.relocate(p.id, p.path);
    case "desktop/projects/no-project":
      return projects.selectNoProject(Boolean(p.fresh), p.cwd);
    default:
      throw new Error("Unknown project operation");
  }
}
