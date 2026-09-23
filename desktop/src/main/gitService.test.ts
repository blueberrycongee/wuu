import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RuntimeContext } from "../shared/protocol";
import { GitService, gitWorkingTreeBusy, type CommitMessageGenerator } from "./gitService";

const roots: string[] = [];

function makeRepository(): string {
  const root = mkdtempSync(join(tmpdir(), "wuu-git-service-"));
  roots.push(root);
  execFileSync("git", ["init", "-q", root]);
  execFileSync("git", ["-C", root, "config", "core.hooksPath", "/dev/null"]);
  execFileSync("git", ["-C", root, "config", "user.email", "test@example.com"]);
  execFileSync("git", ["-C", root, "config", "user.name", "Wuu Test"]);
  execFileSync("git", ["-C", root, "config", "commit.gpgsign", "false"]);
  writeFileSync(join(root, "README.md"), "workspace\n");
  execFileSync("git", ["-C", root, "add", "README.md"]);
  execFileSync("git", ["-C", root, "commit", "-qm", "initial"]);
  return root;
}

function serviceFor(
  root: string,
  runningThreadCwds: string[] = [],
  knownThreadCwds: string[] = [],
  generateCommitMessage?: CommitMessageGenerator,
): GitService {
  const context: RuntimeContext = { kind: "no_project", cwd: root };
  return new GitService(
    () => context,
    () => runningThreadCwds,
    () => knownThreadCwds,
    generateCommitMessage,
  );
}

afterEach(() => {
  vi.unstubAllEnvs();
  for (const root of roots.splice(0)) {
    rmSync(root, { recursive: true, force: true });
  }
});

describe("GitService file previews", () => {
  it.each([
    "plain.txt",
    "中文.txt",
    " leading space.txt",
    ...(process.platform === "win32" ? [] : [
      "trailing space.txt ",
      "tab\tname.txt",
      "line\nname.txt",
      'quote"name.txt',
      "back\\slash.txt",
    ]),
  ])("preserves untracked filename %j in changes, previews and totals", (path) => {
    // Force Git's default quoting regardless of the developer's configuration.
    vi.stubEnv("GIT_CONFIG_COUNT", "1");
    vi.stubEnv("GIT_CONFIG_KEY_0", "core.quotePath");
    vi.stubEnv("GIT_CONFIG_VALUE_0", "true");
    const root = makeRepository();
    const text = "第一行\n第二行\n";
    writeFileSync(join(root, path), text);
    const service = serviceFor(root);

    const changes = service.changes();
    expect.soft(changes.files).toEqual([
      { path, status: "untracked", additions: 2, deletions: 0, binary: false },
    ]);
    const result = service.fileDiff(changes.files[0].path);
    expect.soft(result).toMatchObject({
      path,
      status: "untracked",
      additions: 2,
      deletions: 0,
      binary: false,
      original_text: "",
      modified_text: text,
      truncated: false,
    });
    expect.soft(result.patch).toContain("+第一行\n+第二行");
    expect.soft(service.status().diff).toEqual({ files: 1, additions: 2, deletions: 0 });
  });

  it("returns ignored text files as complete new-file previews", () => {
    const root = makeRepository();
    writeFileSync(join(root, ".gitignore"), "/docs/plans/*\n");
    mkdirSync(join(root, "docs/plans"), { recursive: true });
    writeFileSync(join(root, "docs/plans/brief.md"), "# Brief\n\nBody\n");

    const result = serviceFor(root).fileDiff("docs/plans/brief.md");

    expect(result.status).toBe("ignored");
    expect(result.additions).toBe(3);
    expect(result.patch).toContain("new file mode 100644");
    expect(result.patch).toContain("+# Brief");
    expect(result.patch).toContain("+Body");
    expect(result.original_text).toBe("");
    expect(result.modified_text).toBe("# Brief\n\nBody\n");
  });

  it("returns both text revisions for a modified file", () => {
    const root = makeRepository();
    writeFileSync(join(root, "README.md"), "workspace improved\n");

    const result = serviceFor(root).fileDiff("README.md");

    expect(result.original_text).toBe("workspace\n");
    expect(result.modified_text).toBe("workspace improved\n");
  });

  it("keeps ignored files out of the workspace Git change list", () => {
    const root = makeRepository();
    writeFileSync(join(root, ".gitignore"), "ignored.txt\n");
    writeFileSync(join(root, "ignored.txt"), "private draft\n");

    expect(
      serviceFor(root)
        .changes()
        .files.map((file) => file.path),
    ).not.toContain("ignored.txt");
  });
});

describe("GitService commit", () => {
  function headMessage(root: string): string {
    return execFileSync("git", ["-C", root, "log", "-1", "--pretty=%s"], {
      encoding: "utf8",
    }).trim();
  }

  function headHash(root: string): string {
    return execFileSync("git", ["-C", root, "rev-parse", "HEAD"], {
      encoding: "utf8",
    }).trim();
  }

  function writeChange(root: string): void {
    writeFileSync(join(root, "feature.ts"), "export const feature = true;\n");
  }

  it("commits with the confirmed message without calling the generator", async () => {
    const root = makeRepository();
    writeChange(root);
    let called = 0;
    const generate: CommitMessageGenerator = async () => {
      called += 1;
      return "unused";
    };

    await serviceFor(root, [], [], generate).commit({ message: "manual message" });

    expect(called).toBe(0);
    expect(headMessage(root)).toBe("manual message");
  });

  it("requires a message — implicit generation was removed", async () => {
    const root = makeRepository();
    writeChange(root);
    const before = headHash(root);

    await expect(serviceFor(root).commit({ message: "  " })).rejects.toThrow(
      "commit message is required",
    );

    expect(headHash(root)).toBe(before);
    // The staged change is preserved so the user can retry with a message.
    expect(serviceFor(root).status().dirty_count).toBeGreaterThan(0);
  });

  it("still rejects when there is nothing staged", async () => {
    const root = makeRepository();

    await expect(serviceFor(root).commit({ message: "x" })).rejects.toThrow(
      "there are no staged changes to commit",
    );
  });
});

describe("GitService commit message generation", () => {
  function headHash(root: string): string {
    return execFileSync("git", ["-C", root, "rev-parse", "HEAD"], {
      encoding: "utf8",
    }).trim();
  }

  function writeChange(root: string): void {
    writeFileSync(join(root, "feature.ts"), "export const feature = true;\n");
  }

  it("returns the AI message without committing, staging unstaged files", async () => {
    const root = makeRepository();
    writeChange(root);
    const before = headHash(root);
    const calls: { diff: string; files: string[] }[] = [];
    const generate: CommitMessageGenerator = async (_context, input) => {
      calls.push(input);
      return "feat(desktop): add feature flag";
    };

    const result = await serviceFor(root, [], [], generate).commitMessage({});

    expect(result.message).toBe("feat(desktop): add feature flag");
    expect(headHash(root)).toBe(before);
    expect(calls).toHaveLength(1);
    expect(calls[0].files).toEqual(["feature.ts"]);
    expect(calls[0].diff).toContain("+export const feature = true;");
    // Generation stages the change exactly like a commit would.
    expect(serviceFor(root).status().staged_diff?.files).toBe(1);
  });

  it("leaves unstaged files alone when include_unstaged is false", async () => {
    const root = makeRepository();
    writeChange(root);
    const generate: CommitMessageGenerator = async () => "unused";

    await expect(
      serviceFor(root, [], [], generate).commitMessage({ include_unstaged: false }),
    ).rejects.toThrow("there are no staged changes to commit");
  });

  it("rejects when no generator is wired", async () => {
    const root = makeRepository();
    writeChange(root);

    await expect(serviceFor(root).commitMessage({})).rejects.toThrow(
      "AI commit message generation is not available",
    );
  });

  it("rejects when the generator fails or returns empty", async () => {
    const root = makeRepository();
    writeChange(root);
    const failing: CommitMessageGenerator = async () => {
      throw new Error("BYOK model runtime is not available");
    };

    await expect(
      serviceFor(root, [], [], failing).commitMessage({}),
    ).rejects.toThrow("AI commit message generation failed");

    const empty: CommitMessageGenerator = async () => "   ";
    await expect(
      serviceFor(root, [], [], empty).commitMessage({}),
    ).rejects.toThrow("empty message");
  });
});

describe("GitService worktree roots", () => {
  it("returns the same root for sibling directories in one working tree", () => {
    const root = makeRepository();
    const frontend = join(root, "frontend");
    const backend = join(root, "backend");
    mkdirSync(frontend);
    mkdirSync(backend);
    const service = serviceFor(root);

    const frontendRoot = service.worktreeRoot(frontend);
    expect(service.worktreeRoot(backend)).toBe(frontendRoot);
    expect(realpathSync.native(frontendRoot)).toBe(realpathSync.native(root));
  });

  it("does not let a running non-Git session block a different project", () => {
    const repository = makeRepository();
    const root = mkdtempSync(join(tmpdir(), "wuu-non-git-service-"));
    roots.push(root);

    expect(() => serviceFor(root).worktreeRoot(root)).toThrow();
    expect(gitWorkingTreeBusy(repository, [root])).toBe(false);
    execFileSync("git", ["-C", repository, "branch", "feature"]);
    const service = serviceFor(repository, [root]);
    expect(service.checkoutBranch("feature").branch).toBe("feature");
    expect(service.createCheckoutBranch("next").status.branch).toBe("next");
    expect(gitWorkingTreeBusy(repository, [root, repository])).toBe(true);
  });

  it("stays locked when a running cwd is missing rather than a confirmed non-repository", () => {
    const repository = makeRepository();
    expect(gitWorkingTreeBusy(repository, [join(repository, "missing")])).toBe(true);
  });

  it("does not let a running session in another repository block checkout", () => {
    const repository = makeRepository();
    const other = makeRepository();
    execFileSync("git", ["-C", repository, "branch", "feature"]);
    expect(serviceFor(repository, [other]).checkoutBranch("feature").branch).toBe("feature");
  });

  it("stays locked when the target root cannot be resolved", () => {
    const root = mkdtempSync(join(tmpdir(), "wuu-non-git-service-"));
    roots.push(root);

    expect(gitWorkingTreeBusy(root, [])).toBe(true);
  });

  it("matches symlinked sibling paths to the same working tree", () => {
    const root = makeRepository();
    const frontend = join(root, "frontend");
    const backend = join(root, "backend");
    const alias = mkdtempSync(join(tmpdir(), "wuu-git-alias-"));
    rmSync(alias, { recursive: true, force: true });
    roots.push(alias);
    mkdirSync(frontend);
    mkdirSync(backend);
    symlinkSync(root, alias, "dir");

    expect(gitWorkingTreeBusy(frontend, [join(alias, "backend")])).toBe(true);
  });

  it("allows a running thread in an independent linked worktree", () => {
    const root = makeRepository();
    const linked = mkdtempSync(join(tmpdir(), "wuu-linked-worktree-"));
    rmSync(linked, { recursive: true, force: true });
    roots.push(linked);
    execFileSync("git", ["-C", root, "worktree", "add", "-qb", "linked", linked]);

    expect(gitWorkingTreeBusy(root, [linked])).toBe(false);
  });

  it(
    "reads and mutates the explicitly selected linked worktree",
    () => {
      const root = makeRepository();
      const linked = mkdtempSync(join(tmpdir(), "wuu-linked-worktree-"));
      rmSync(linked, { recursive: true, force: true });
      roots.push(linked);
      execFileSync("git", ["-C", root, "worktree", "add", "-qb", "linked", linked]);
      execFileSync("git", ["-C", root, "branch", "next"]);
      const service = serviceFor(root, [], [linked]);

      expect(service.status({}, linked).branch).toBe("linked");
      expect(service.status().branch).not.toBe("linked");

      service.checkoutBranch("next", linked);

      expect(service.status({}, linked).branch).toBe("next");
      expect(service.status().branch).not.toBe("next");
    },
    15_000,
  );

  it("rejects relative Git root overrides", () => {
    const root = makeRepository();

    expect(() => serviceFor(root).status({}, "relative/worktree")).toThrow(
      "Git working directory must be absolute",
    );
  });

  it("rejects Git roots that are not bound to the current project", () => {
    const root = makeRepository();
    const unrelated = makeRepository();

    expect(() => serviceFor(root).status({}, unrelated)).toThrow(
      "Git working directory is not associated with the current project",
    );
  });

  it("rechecks the authoritative running cwd before checkout", () => {
    const root = makeRepository();
    const frontend = join(root, "frontend");
    const backend = join(root, "backend");
    mkdirSync(frontend);
    mkdirSync(backend);
    execFileSync("git", ["-C", root, "branch", "feature"]);

    expect(() => serviceFor(frontend, [backend]).checkoutBranch("feature")).toThrow(
      "cannot run Git actions while a thread is running in this working tree",
    );
    expect(() => serviceFor(frontend, [backend]).createCheckoutBranch("new")).toThrow(
      "cannot run Git actions while a thread is running in this working tree",
    );
  });
});
