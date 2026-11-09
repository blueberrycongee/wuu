import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { GitService } from "./gitService";
import { requestRemoteGit } from "./remoteGit";

it("executes remote Git in the selected workspace and preserves the running-task guard", async () => {
  const root = mkdtempSync(join(tmpdir(), "wuu-remote-git-"));
  const git = (...args: string[]) => execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();
  let running: string[] = [];
  const service = new GitService(() => ({kind:"no_project",cwd:root}), () => running);
  try {
    git("init", "-q"); git("config","user.email","test@example.com"); git("config","user.name","Wuu Test"); git("config","commit.gpgsign","false"); git("config","core.hooksPath","/dev/null");
    writeFileSync(join(root,"README.md"),"original\n"); git("add","."); git("commit","-qm","Initial");
    await requestRemoteGit(service,"desktop/git/create-branch",{branch:"remote-work"});
    expect(git("branch","--show-current")).toBe("remote-work");
    writeFileSync(join(root,"README.md"),"remote change\n");
    running=[root];
    await expect(requestRemoteGit(service,"desktop/git/commit",{params:{message:"Remote change"}})).rejects.toThrow("thread is running");
    expect(git("log","-1","--format=%s")).toBe("Initial");
    running=[];
    await requestRemoteGit(service,"desktop/git/commit",{params:{message:"Remote change"}});
    expect(git("log","-1","--format=%s")).toBe("Remote change");
    await expect(requestRemoteGit(service,"desktop/git/checkout",{branch:"remote-work",root:tmpdir()})).rejects.toThrow("not associated");
  } finally { rmSync(root,{recursive:true,force:true}); }
});
