import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  realpathSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

beforeEach(() => {
  // Force Git's default quoting regardless of the developer's configuration.
  vi.stubEnv("GIT_CONFIG_COUNT", "1");
  vi.stubEnv("GIT_CONFIG_KEY_0", "core.quotePath");
  vi.stubEnv("GIT_CONFIG_VALUE_0", "true");
});

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
  ])("preserves filename %j across untracked, staged, modified and deleted changes", (path) => {
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

    execFileSync("git", ["--literal-pathspecs", "-C", root, "add", "--", path]);
    const staged = service.changes();
    expect.soft(staged.files).toEqual([
      { path, status: "added", additions: 2, deletions: 0, binary: false },
    ]);
    const stagedPreview = service.fileDiff(staged.files[0].path);
    expect.soft(stagedPreview).toMatchObject({
      path, status: "added", original_text: "", modified_text: text,
      additions: 2, deletions: 0, binary: false,
    });
    expect.soft(stagedPreview.patch).toContain("+第一行\n+第二行");
    expect.soft(service.status()).toMatchObject({
      diff: { files: 1, additions: 2, deletions: 0 },
      staged_diff: { files: 1, additions: 2, deletions: 0 },
    });

    execFileSync("git", ["-C", root, "commit", "-qm", "add file"]);
    const modifiedText = `${text}第三行\n`;
    writeFileSync(join(root, path), modifiedText);
    for (const staged of [false, true]) {
      if (staged) {
        execFileSync("git", ["--literal-pathspecs", "-C", root, "add", "--", path]);
      }
      const changes = service.changes();
      expect.soft(changes.files).toEqual([
        { path, status: "modified", additions: 1, deletions: 0, binary: false },
      ]);
      const preview = service.fileDiff(changes.files[0].path);
      expect.soft(preview).toMatchObject({
        path, status: "modified", original_text: text, modified_text: modifiedText,
        additions: 1, deletions: 0, binary: false,
      });
      expect.soft(preview.patch).toContain("+第三行");
      expect.soft(service.status()).toMatchObject({
        diff: { files: 1, additions: 1, deletions: 0 },
        staged_diff: staged
          ? { files: 1, additions: 1, deletions: 0 }
          : { files: 0, additions: 0, deletions: 0 },
      });
    }

    execFileSync("git", ["-C", root, "commit", "-qm", "modify file"]);
    rmSync(join(root, path));
    const deleted = service.changes();
    expect.soft(deleted.files).toEqual([
      { path, status: "deleted", additions: 0, deletions: 3, binary: false },
    ]);
    const deletedPreview = service.fileDiff(deleted.files[0].path);
    expect.soft(deletedPreview).toMatchObject({
      path, status: "deleted", original_text: modifiedText, modified_text: "",
      additions: 0, deletions: 3, binary: false,
    });
    expect.soft(deletedPreview.patch).toContain("-第一行\n-第二行\n-第三行");
  });

  it("keeps rename paths, text revisions and binary statistics aligned with other changes", () => {
    const root = makeRepository();
    const oldPath = process.platform === "win32" ? "旧 name.txt" : "旧\tname.txt";
    const path = process.platform === "win32" ? "新 name.txt" : "新\nname.txt";
    const originalText = "first\nsecond\nthird\nfourth\n";
    const modifiedText = "first\nsecond\nthird\nupdated\n";
    writeFileSync(join(root, oldPath), originalText);
    writeFileSync(join(root, "binary old.bin"), Buffer.from([0, 1, 2]));
    execFileSync("git", ["-C", root, "add", "-A"]);
    execFileSync("git", ["-C", root, "commit", "-qm", "add rename sources"]);
    renameSync(join(root, oldPath), join(root, path));
    writeFileSync(join(root, path), modifiedText);
    renameSync(join(root, "binary old.bin"), join(root, "binary new.bin"));
    writeFileSync(join(root, "README.md"), "updated workspace\n");
    execFileSync("git", ["-C", root, "add", "-A"]);
    const service = serviceFor(root);

    const changes = service.changes();
    expect.soft(changes.files).toHaveLength(3);
    expect.soft(changes.files).toEqual(expect.arrayContaining([
      { path, old_path: oldPath, status: "renamed", additions: 1, deletions: 1, binary: false },
      { path: "binary new.bin", old_path: "binary old.bin", status: "renamed", additions: 0, deletions: 0, binary: true },
      { path: "README.md", status: "modified", additions: 1, deletions: 1, binary: false },
    ]));
    const preview = service.fileDiff(path);
    expect.soft(preview).toMatchObject({
      path, old_path: oldPath, status: "renamed", additions: 1, deletions: 1,
      original_text: originalText, modified_text: modifiedText,
    });
    expect.soft(preview.patch).toContain("-fourth\n+updated");
    expect.soft(preview.patch).not.toContain("+first");
    expect.soft(service.fileDiff("binary new.bin")).toMatchObject({
      status: "renamed", binary: true, additions: 0, deletions: 0,
    });
    expect.soft(service.status()).toMatchObject({
      diff: { files: 3, additions: 2, deletions: 2 },
      staged_diff: { files: 3, additions: 2, deletions: 2 },
    });
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

  it("returns only the selected literal filename and both revisions for a modified file", () => {
    const root = makeRepository();
    const path = "file[1].txt";
    writeFileSync(join(root, path), "workspace\n");
    writeFileSync(join(root, "file1.txt"), "other file\n");
    execFileSync("git", ["-C", root, "add", "-A"]);
    execFileSync("git", ["-C", root, "commit", "-qm", "add literal path fixtures"]);
    writeFileSync(join(root, path), "workspace improved\n");
    writeFileSync(join(root, "file1.txt"), "unrelated change\n");

    const result = serviceFor(root).fileDiff(path);

    expect(result.original_text).toBe("workspace\n");
    expect(result.modified_text).toBe("workspace improved\n");
    expect(result.patch).toContain("+workspace improved");
    expect(result.patch).not.toContain("unrelated change");
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
    const path = process.platform === "win32" ? " 中文.txt" : " 中文\tname.txt ";
    writeFileSync(join(root, path), "new file\n");
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
    expect(calls[0].files).toEqual(expect.arrayContaining(["feature.ts", path]));
    expect(calls[0].files).toHaveLength(2);
    expect(calls[0].diff).toContain("+export const feature = true;");
    // Generation stages the change exactly like a commit would.
    expect(serviceFor(root).status().staged_diff?.files).toBe(2);
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
