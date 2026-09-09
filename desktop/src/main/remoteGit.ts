import type { GitService } from "./gitService";
import type { GitCommitParams, GitPullRequestParams } from "../shared/protocol";

/** Remote Git uses the same workspace checks, busy guard and implementation
 * as native windows. This is a fixed host surface, never arbitrary IPC. */
export function requestRemoteGit(service: GitService, method: string, params: unknown): unknown {
  const input = (params ?? {}) as { branch?: string; root?: string; params?: GitCommitParams & GitPullRequestParams };
  if (input.root !== undefined && typeof input.root !== "string") throw new Error("Invalid Git root");
  switch (method) {
    case "desktop/git/busy": return service.actionBusy(input.root);
    case "desktop/git/checkout":
    case "desktop/git/create-branch":
      if (typeof input.branch !== "string" || !input.branch) throw new Error("Git branch is required");
      return method.endsWith("checkout") ? service.checkoutBranch(input.branch, input.root) : service.createCheckoutBranch(input.branch, input.root);
    case "desktop/git/commit": return service.commit(input.params ?? {}, input.root);
    case "desktop/git/commit-message": return service.commitMessage(input.params ?? {}, input.root);
    case "desktop/git/create-pr": return service.createPullRequest(input.params ?? {}, input.root);
    default: throw new Error("Unknown remote Git operation");
  }
}
