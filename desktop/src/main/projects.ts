import { app } from "electron";
import { randomUUID } from "node:crypto";
import { mainTranslate } from "./i18n";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  statSync,
} from "node:fs";
import { homedir } from "node:os";
import { basename, join, resolve } from "node:path";
import type {
  DesktopProject,
  ProjectListResult,
  ProjectRuntimeIssue,
  RuntimeContext,
} from "../shared/protocol";
import { writeTextFileAtomicSync } from "./atomicFile";

type ProjectStore = {
  projects: DesktopProject[];
  active_context?: RuntimeContext;
};

export class ProjectManager {
  private store: ProjectStore = { projects: [] };

  load(): void {
    this.store = loadProjectStore();
  }

  list(): ProjectListResult {
    this.load();
    const context = this.reconcileRuntimeContext();
    const projects = this.store.projects.map((project) => ({
      ...project,
      missing: !isDirectory(project.path),
    }));
    const activeProject =
      context.kind === "project"
        ? projects.find((project) => project.id === context.project_id)
        : undefined;
    return {
      // Annotate (without persisting) each project with whether its directory
      // still exists, so the sidebar can grey out and lock a workspace whose
      // folder was moved away or deleted.
      projects,
      active_context: context,
      active_project_id:
        context.kind === "project" ? context.project_id : undefined,
      runtime_issue:
        activeProject?.missing === true
          ? activeProjectUnavailableIssue(activeProject)
          : undefined,
    };
  }

  activeWorkdir(): string | undefined {
    return this.store.active_context
      ? resolve(this.store.active_context.cwd)
      : undefined;
  }

  ensureRuntimeContext(): RuntimeContext {
    const context = this.reconcileRuntimeContext();
    if (context.kind === "project") {
      const project = this.store.projects.find(
        (candidate) => candidate.id === context.project_id,
      );
      if (project && !isDirectory(project.path)) {
        throw new Error(activeProjectUnavailableIssue(project).message);
      }
    }
    return context;
  }

  resolveSubmissionContext(context: RuntimeContext): RuntimeContext {
    this.load();
    if (context.kind === "project") {
      const project = this.store.projects.find((candidate) => candidate.id === context.project_id);
      if (!project || resolve(project.path) !== resolve(context.cwd)) {
        throw new Error("submission workspace is no longer registered at this path");
      }
      if (!isDirectory(project.path)) {
        throw new Error(activeProjectUnavailableIssue(project).message);
      }
      return { kind: "project", project_id: project.id, cwd: project.path };
    }
    if (context.kind !== "no_project" || !isDirectory(context.cwd)) {
      throw new Error("submission workspace is unavailable");
    }
    // Scratch conversations can retain a legacy cwd. Resolving a submission
    // must not select it globally or recreate a removed directory.
    return { kind: "no_project", cwd: resolve(context.cwd) };
  }

  private reconcileRuntimeContext(): RuntimeContext {
    const active = this.store.active_context;
    if (active?.kind === "project") {
      const project = this.store.projects.find(
        (candidate) => candidate.id === active.project_id,
      );
      if (project) {
        const context: RuntimeContext = {
          kind: "project",
          project_id: project.id,
          cwd: project.path,
        };
        if (active.cwd !== project.path) {
          this.store.active_context = context;
          this.save();
        }
        return context;
      }
    }
    if (active?.kind === "no_project") {
      return active;
    }
    const fallback = createNoProjectContext();
    this.store.active_context = fallback;
    this.save();
    return fallback;
  }

  add(projectPath: string): ProjectListResult {
    this.load();
    const resolvedPath = resolve(projectPath);
    if (!isDirectory(resolvedPath)) {
      throw new Error("selected project is not a directory");
    }
    const now = new Date().toISOString();
    // Dedup by path — a project is one folder — but the id is a stable, opaque
    // UUID minted once and never derived from the path, so relocating the
    // folder (via relocate, not re-add) keeps the same identity and its
    // workspace state + conversation history stay connected.
    const existingIndex = this.store.projects.findIndex(
      (project) => resolve(project.path) === resolvedPath,
    );
    const existing =
      existingIndex >= 0 ? this.store.projects[existingIndex] : undefined;
    const project: DesktopProject = {
      id: existing ? existing.id : newProjectID(),
      name: projectName(resolvedPath),
      path: resolvedPath,
      created_at: existing ? existing.created_at : now,
      updated_at: now,
    };
    if (existingIndex >= 0) {
      this.store.projects[existingIndex] = project;
    } else {
      this.store.projects = [project, ...this.store.projects];
    }
    this.save();
    return this.list();
  }

  select(projectIDToSelect: string): ProjectListResult {
    this.load();
    const project = this.store.projects.find(
      (candidate) => candidate.id === projectIDToSelect,
    );
    if (!project) {
      throw new Error("project not found");
    }
    this.store.active_context = {
      kind: "project",
      project_id: project.id,
      cwd: project.path,
    };
    this.save();
    return this.list();
  }

  remove(projectIDToRemove: string): ProjectListResult {
    this.load();
    this.store.projects = this.store.projects.filter(
      (project) => project.id !== projectIDToRemove,
    );
    // If the removed project was the active context, ensureRuntimeContext
    // (called by list()) reconciles the now-dangling project_id down to the
    // shared 对话 workspace. This is an explicit user removal, not a silent
    // fallback masking a missing directory. The project's threads stay in the
    // sessions DB (they simply stop matching any registered project), so
    // re-adding the folder restores them.
    this.save();
    return this.list();
  }

  relocate(projectIDToRelocate: string, newPath: string): ProjectListResult {
    this.load();
    const resolvedPath = resolve(newPath);
    if (!isDirectory(resolvedPath)) {
      throw new Error("selected relocation target is not a directory");
    }
    const index = this.store.projects.findIndex(
      (project) => project.id === projectIDToRelocate,
    );
    if (index < 0) {
      throw new Error("project not found");
    }
    // Keep the stable id; only the path (and derived name) move. Because the
    // workspace state dir and its sessions are keyed by the id, everything
    // reconnects at the new location — this is the remedy for a moved folder.
    this.store.projects[index] = {
      ...this.store.projects[index],
      name: projectName(resolvedPath),
      path: resolvedPath,
      updated_at: new Date().toISOString(),
    };
    const active = this.store.active_context;
    if (
      active?.kind === "project" &&
      active.project_id === projectIDToRelocate
    ) {
      this.store.active_context = {
        kind: "project",
        project_id: projectIDToRelocate,
        cwd: resolvedPath,
      };
    }
    this.save();
    return this.list();
  }

  selectNoProject(fresh: boolean, cwd?: string): ProjectListResult {
    this.load();
    if (!fresh && cwd) {
      const resolvedCwd = resolve(cwd);
      mkdirSync(resolvedCwd, { recursive: true });
      this.store.active_context = { kind: "no_project", cwd: resolvedCwd };
    } else if (fresh || this.store.active_context?.kind !== "no_project") {
      this.store.active_context = createNoProjectContext();
    }
    this.save();
    return this.list();
  }

  private save(): void {
    writeProjectStoreFile(projectStorePath(), this.store);
  }
}

function projectStorePath(): string {
  return join(wuuHomePath(), "projects.json");
}

function legacyProjectStorePath(): string {
  return join(app.getPath("userData"), "projects.json");
}

export function wuuHomePath(): string {
  const override = process.env.WUU_HOME?.trim();
  if (override) {
    return resolve(override);
  }
  return join(homedir(), ".wuu");
}

function loadProjectStore(): ProjectStore {
  const path = projectStorePath();
  const loaded = readProjectStoreFile(path);
  if (loaded) {
    return loaded;
  }

  // Legacy app-data projects are a one-time import source. Once the
  // canonical ~/.wuu/projects.json exists, it must be the only source of
  // truth so explicitly removed workspaces cannot be resurrected by an old
  // Electron store file.
  const legacyPath = legacyProjectStorePath();
  const store = readProjectStoreFile(legacyPath) ?? {
    projects: [],
  };
  writeProjectStoreFile(path, store);
  archiveLegacyProjectStoreFile(legacyPath);
  return store;
}

function archiveLegacyProjectStoreFile(path: string): void {
  if (!existsSync(path)) {
    return;
  }
  try {
    renameSync(path, legacyProjectStoreArchivePath(path));
  } catch {
    // Best effort: migration must not block startup if the legacy file is busy.
  }
}

function legacyProjectStoreArchivePath(path: string): string {
  const base = `${path}.migrated`;
  if (!existsSync(base)) {
    return base;
  }
  for (let index = 1; index < 1000; index += 1) {
    const candidate = `${base}.${index}`;
    if (!existsSync(candidate)) {
      return candidate;
    }
  }
  return `${base}.${Date.now()}`;
}

function readProjectStoreFile(path: string): ProjectStore | undefined {
  let contents: string;
  try {
    contents = readFileSync(path, "utf8");
  } catch (error) {
    if (isMissingFileError(error)) {
      return undefined;
    }
    throw projectStoreError(path, "read", error);
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(contents);
  } catch (error) {
    throw projectStoreError(path, "parse", error);
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw invalidProjectStoreError(path, "expected an object");
  }

  const record = parsed as Record<string, unknown>;
  if (!Array.isArray(record.projects)) {
    throw invalidProjectStoreError(path, "projects must be an array");
  }
  const invalidProjectIndex = record.projects.findIndex(
    (project) => !isDesktopProject(project),
  );
  if (invalidProjectIndex >= 0) {
    throw invalidProjectStoreError(
      path,
      `projects[${invalidProjectIndex}] is invalid`,
    );
  }
  if (
    record.active_context !== undefined &&
    !isRuntimeContext(record.active_context)
  ) {
    throw invalidProjectStoreError(path, "active_context is invalid");
  }
  if (
    record.active_project_id !== undefined &&
    typeof record.active_project_id !== "string"
  ) {
    throw invalidProjectStoreError(path, "active_project_id is invalid");
  }

  const projects = record.projects as DesktopProject[];
  let activeContext: RuntimeContext | undefined;
  try {
    activeContext =
      normalizeRuntimeContext(record.active_context, projects) ??
      legacyProjectContext(record.active_project_id, projects);
  } catch (error) {
    throw projectStoreError(path, "load", error);
  }
  return {
    projects,
    active_context: activeContext,
  };
}

function isMissingFileError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "ENOENT"
  );
}

function projectStoreError(
  path: string,
  operation: "read" | "parse" | "load",
  cause: unknown,
): Error {
  const detail = cause instanceof Error ? cause.message : String(cause);
  return new Error(
    `failed to ${operation} project store at ${path}: ${detail}`,
    { cause },
  );
}

function invalidProjectStoreError(path: string, detail: string): Error {
  return new Error(`invalid project store at ${path}: ${detail}`);
}

function isRuntimeContext(value: unknown): value is RuntimeContext {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const context = value as Record<string, unknown>;
  if (context.kind === "project") {
    return (
      typeof context.project_id === "string" &&
      typeof context.cwd === "string"
    );
  }
  return context.kind === "no_project" && typeof context.cwd === "string";
}

function isDesktopProject(value: unknown): value is DesktopProject {
  if (!value || typeof value !== "object") {
    return false;
  }
  const project = value as Partial<DesktopProject>;
  return (
    typeof project.id === "string" &&
    typeof project.name === "string" &&
    typeof project.path === "string" &&
    typeof project.created_at === "string" &&
    typeof project.updated_at === "string"
  );
}

function normalizeRuntimeContext(
  value: unknown,
  projects: DesktopProject[],
): RuntimeContext | undefined {
  if (!value || typeof value !== "object") {
    return undefined;
  }
  const context = value as Partial<RuntimeContext>;
  if (context.kind === "project" && typeof context.project_id === "string") {
    const project = projects.find(
      (candidate) => candidate.id === context.project_id,
    );
    return project
      ? { kind: "project", project_id: project.id, cwd: project.path }
      : undefined;
  }
  if (context.kind === "no_project" && typeof context.cwd === "string") {
    const cwd = resolve(context.cwd);
    mkdirSync(cwd, { recursive: true });
    return { kind: "no_project", cwd };
  }
  return undefined;
}

function legacyProjectContext(
  value: unknown,
  projects: DesktopProject[],
): RuntimeContext | undefined {
  if (typeof value !== "string") {
    return undefined;
  }
  const project = projects.find((candidate) => candidate.id === value);
  return project
    ? { kind: "project", project_id: project.id, cwd: project.path }
    : undefined;
}

function writeProjectStoreFile(path: string, store: ProjectStore): void {
  writeTextFileAtomicSync(path, `${JSON.stringify(store, null, 2)}\n`);
}

function createNoProjectContext(): RuntimeContext {
  return { kind: "no_project", cwd: allocateNoProjectCwd() };
}

// SCRATCH_WORKSPACE_DIRNAME is the single, stable directory backing the "对话"
// workspace. Every no-project conversation shares this one cwd — one workspace,
// many sessions — rather than getting its own dated directory. This keeps the
// "对话" workspace always present (it can never be moved/deleted, so it is never
// "unavailable"), and lets the draft-tab-per-context dedup collapse repeated
// "新建对话" clicks onto a single shared draft. Legacy per-date scratch threads
// keep their own recorded cwd and still list under 对话 via the isScratchThread
// cwd check.
const SCRATCH_WORKSPACE_DIRNAME = "default";

function allocateNoProjectCwd(): string {
  const dir = join(wuuHomePath(), "scratch", SCRATCH_WORKSPACE_DIRNAME);
  mkdirSync(dir, { recursive: true });
  return dir;
}

function newProjectID(): string {
  return randomUUID();
}

function projectName(projectPath: string): string {
  return basename(projectPath) || projectPath;
}

function activeProjectUnavailableIssue(
  project: Pick<DesktopProject, "id" | "path">,
): ProjectRuntimeIssue {
  return {
    code: "active_project_unavailable",
    message: mainTranslate("projectUnavailable", { path: project.path }),
    project_id: project.id,
    cwd: project.path,
  };
}

function isDirectory(projectPath: string): boolean {
  try {
    return statSync(projectPath).isDirectory();
  } catch {
    return false;
  }
}
