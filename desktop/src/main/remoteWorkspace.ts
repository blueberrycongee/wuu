import { resolve, sep } from "node:path";
import type { AppServerResponse, GitCommitMessageResult, RuntimeContext, SkillContentParams, SkillListResult } from "../shared/protocol";
import type { AppServerClientPool } from "./appServerClients";
import { GitService } from "./gitService";
import { createNoProjectContext, ProjectManager, wuuHomePath } from "./projects";
import { requestRemoteFiles } from "./remoteFiles";
import { requestRemoteGit } from "./remoteGit";
import { requestRemoteProjects } from "./remoteProjects";
import { readCatalogSkill } from "./remoteSkills";
import { requestRemoteTerminal } from "./remoteTerminal";
import type { TerminalSessionManager } from "./terminalSessions";

/** Desktop and headless hosts share workspace ownership and OS operations. */
export async function requestRemoteWorkspace(
  projects: ProjectManager,
  pool: AppServerClientPool,
  terminals: TerminalSessionManager,
  workdir: string,
  method: string,
  params: unknown,
  reply: (response: Pick<AppServerResponse, "result" | "error">) => void,
  peerID: number,
  request = (context: RuntimeContext, method: string, params: unknown) => pool.requestInContext(context, method, params, reply),
): Promise<unknown> {
  if (method === "shutdown") throw new Error("Remote clients cannot shut down the shared execution service");
  if (method.startsWith("desktop/projects/")) return requestRemoteProjects(projects, method, params);
  const cwd = resolve(workdir);
  const state = projects.list();
  const project = state.projects.find(project => resolve(project.path) === cwd && !project.missing);
  let context: RuntimeContext;
  if (project) context = { kind: "project", project_id: project.id, cwd: project.path };
  else if (state.projects.some(project => pool.threadCwdsForWorkdir(project.path).some(path => resolve(path) === cwd))) context = { kind: "no_project", cwd };
  else if (state.active_context?.kind === "no_project" && resolve(state.active_context.cwd) === cwd) context = state.active_context;
  else if (cwd.startsWith(resolve(wuuHomePath(), "scratch") + sep)) context = { kind: "no_project", cwd };
  // Discovery must survive removing or relocating the host's startup root.
  // A neutral context leaves saved selection and scoped request checks intact.
  else if (method === "workspace/list") context = createNoProjectContext();
  else throw new Error("Unknown or unavailable remote workspace");
  if (method.startsWith("desktop/terminal/")) return requestRemoteTerminal(terminals, context, peerID, method, params);
  if (method === "desktop/skill/content") return readCatalogSkill(await pool.requestInContext<SkillListResult>(context, "skill/list"), params as SkillContentParams);
  if (method.startsWith("desktop/file/")) return requestRemoteFiles(context, method, params);
  if (method.startsWith("desktop/git/")) {
    const git = new GitService(
      () => context,
      () => pool.runningThreadCwds(),
      () => pool.threadCwdsForWorkdir(context.cwd),
      async (context, input) => (await pool.requestInContext<GitCommitMessageResult>(context, "git/commit-message", input)).message ?? "",
    );
    return requestRemoteGit(git, method, params);
  }
  return request(context, method, params);
}
