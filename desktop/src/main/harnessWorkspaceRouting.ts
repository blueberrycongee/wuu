import { resolve } from "node:path";
import type { RuntimeContext, ServerEvent } from "../shared/protocol";
import type { AppServerClientPool } from "./appServerClients";

export const WORKSPACE_HARNESS_DISPATCH = "workspace/harness/dispatch";

// The desktop owns the existing per-project process pool. Core supplies a
// durable operation reference, never an arbitrary command or inferred CWD.
export async function routeHarnessWorkspaceRequest(
  event: Extract<ServerEvent, { kind: "server-request" }>,
  pool: AppServerClientPool,
  contextForWorkspace: (id: string) => RuntimeContext,
  initializeParams: unknown,
): Promise<void> {
  try {
    const params = event.message.params as Record<string, unknown> | undefined;
    if (!params || typeof params.workspace_id !== "string" || !params.workspace_id.trim()
      || typeof params.workspace_root !== "string" || !params.workspace_root.trim()) {
      throw new Error("workspace dispatch requires a registered project ID and root");
    }
    const context = contextForWorkspace(params.workspace_id);
    if (context.kind !== "project" || context.project_id !== params.workspace_id
      || resolve(context.cwd) !== resolve(params.workspace_root)) {
      throw new Error("workspace ID and root disagree; session was not dispatched");
    }
    await pool.requestInContext(context, "initialize", initializeParams);
    const result = await pool.requestInContext(context, "session/harness/dispatch", {
      workspace_id: context.project_id,
      workspace_root: context.cwd,
      operation_id: params.operation_id,
    });
    pool.respondToServerRequest(event.message.id, result);
  } catch (error) {
    // An exiting source can remove its reply route while the target is starting.
    // Its durable operation remains recoverable without respawning the source.
    try {
      pool.rejectServerRequest(event.message.id, error instanceof Error ? error.message : String(error));
    } catch {
      // The source no longer owns an active request.
    }
  }
}
