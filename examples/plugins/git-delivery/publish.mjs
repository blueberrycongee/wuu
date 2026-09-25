import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
const execute = promisify(execFile);

export async function publishCandidate(input, run = async (program, args, cwd) => {
  const { stdout } = await execute(program, args, { cwd, maxBuffer: 32 * 1024 * 1024, env: { ...process.env, GIT_TERMINAL_PROMPT: "0" } });
  return stdout;
}) {
  const candidate = input?.candidate;
  if (!candidate?.base_repo || !/^[a-f0-9]{40,64}$/.test(candidate.revision) || !/^[a-f0-9]{40,64}$/.test(candidate.base_revision)) throw new Error("A frozen Git candidate is required");
  const root = candidate.base_repo;
  const branch = `wuu/candidate-${candidate.revision.slice(0, 16)}`;
  const existing = JSON.parse(await run("gh", ["pr", "list", "--state", "all", "--head", branch, "--json", "url"], root));
  if (existing.length) return { url: existing[0].url };
  const repo = JSON.parse(await run("gh", ["repo", "view", "--json", "defaultBranchRef"], root));
  const base = repo.defaultBranchRef.name;
  await run("git", ["fetch", "origin", base], root);
  const patch = await run("git", ["diff", "--no-ext-diff", "--no-textconv", "--binary", candidate.base_revision, candidate.revision, "--"], root);
  if (!patch) throw new Error("The candidate has no Git changes");
  const directory = await mkdtemp(join(tmpdir(), "wuu-git-delivery-"));
  const checkout = join(directory, "checkout");
  let added = false;
  try {
    // Replay only the reviewed diff on the public base, excluding pre-existing
    // local changes from a shared execution workspace.
    await run("git", ["worktree", "add", "--detach", checkout, `refs/remotes/origin/${base}`], root);
    added = true;
    const patchPath = join(directory, "candidate.patch");
    await writeFile(patchPath, patch.endsWith("\n") ? patch : `${patch}\n`, { mode: 0o600 });
    await run("git", ["apply", "--check", patchPath], checkout);
    await run("git", ["apply", patchPath], checkout);
    await run("git", ["add", "--all", "--", "."], checkout);
    const title = String(input.title || "Apply reviewed Work candidate").replace(/[\r\n]/g, " ");
    await run("git", ["-c", "core.hooksPath=/dev/null", "commit", "-m", title], checkout);
    const remote = (await run("git", ["ls-remote", "--heads", "origin", branch], root)).trim();
    if (remote) {
      // A previous attempt may have pushed successfully before PR creation
      // failed. Reuse that exact tree; never rewrite the published branch.
      await run("git", ["fetch", "origin", `refs/heads/${branch}`], root);
      const remoteTree = (await run("git", ["rev-parse", "FETCH_HEAD^{tree}"], root)).trim();
      const candidateTree = (await run("git", ["rev-parse", "HEAD^{tree}"], checkout)).trim();
      if (remoteTree !== candidateTree) throw new Error("The published candidate branch changed; inspect it before retrying");
    } else {
      await run("git", ["push", "origin", `HEAD:refs/heads/${branch}`], checkout);
    }
    const body = join(directory, "body.md");
    await writeFile(body, `${candidate.report.result}\n\nValidation and remaining questions:\n${candidate.report.evidence_refs.map(value => `- ${value}`).join("\n")}\n${candidate.report.unresolved_items.map(value => `- ${value}`).join("\n")}\n`, { mode: 0o600 });
    const url = await run("gh", ["pr", "create", "--draft", "--base", base, "--head", branch, "--title", title, "--body-file", body], root);
    return { url: url.trim() };
  } finally {
    if (added) await run("git", ["worktree", "remove", "--force", checkout], root);
    await rm(directory, { recursive: true, force: true });
  }
}
