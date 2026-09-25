import { test } from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { publishCandidate } from "./publish.mjs";
const candidate = { base_repo: "/project", base_revision: "a".repeat(40), revision: "b".repeat(40), report: { result: "Search uses pagination", evidence_refs: ["unit tests passed"], unresolved_items: [] } };
test("publishes only candidate patch in an isolated checkout and cleans up", async () => {
 const calls=[];
 const run=async (command,args,cwd)=>{
  calls.push({command,args,cwd});
  if(args[0]==="pr"&&args[1]==="list")return "[]";
  if(args[0]==="repo")return JSON.stringify({defaultBranchRef:{name:"main"}});
  if(args[0]==="diff")return "diff --git a/file b/file\n";
  if(args[0]==="pr"&&args[1]==="create") { assert.match(await readFile(args.at(-1),"utf8"),/unit tests passed/);return "https://github.com/example/project/pull/1"; }
  return "";
 };
 const result=await publishCandidate({candidate,title:"Paginate search"},run);
 assert.match(result.url,/pull\/1$/);
 const apply=calls.find(c=>c.args[0]==="apply"&&!c.args.includes("--check"));
 assert.notEqual(apply.cwd,candidate.base_repo);
 assert.ok(calls.find(c=>c.args[0]==="push"&&!c.args.includes("--force")));
 assert.ok(calls.find(c=>c.args[0]==="worktree"&&c.args[1]==="remove"));
});
test("retry returns existing PR without modifying Git",async()=>{
 let count=0;const result=await publishCandidate({candidate},async(command,args)=>{count++;assert.equal(command,"gh");assert.equal(args[1],"list");return '[{"url":"https://github.com/example/project/pull/1"}]'});
 assert.equal(count,1);assert.match(result.url,/pull\/1$/);
});

test("real Git publication excludes pre-existing local changes", async () => {
 const { execFile } = await import("node:child_process");
 const { promisify } = await import("node:util");
 const { mkdtemp, writeFile, rm } = await import("node:fs/promises");
 const { tmpdir } = await import("node:os");
 const { join } = await import("node:path");
 const exec=promisify(execFile),directory=await mkdtemp(join(tmpdir(),"wuu-git-test-")),root=join(directory,"repo"),remote=join(directory,"origin.git");
 const env={...process.env,GIT_AUTHOR_NAME:"Test",GIT_AUTHOR_EMAIL:"test@example.invalid",GIT_COMMITTER_NAME:"Test",GIT_COMMITTER_EMAIL:"test@example.invalid"};
 const git=async(args,cwd=root)=>(await exec("git",args,{cwd,env})).stdout;
 try {
  await exec("git",["init","--bare",remote],{env});await exec("git",["init","-b","main",root],{env});
  await writeFile(join(root,"baseline.txt"),"public\n");await git(["add","."]);await git(["commit","-m","base"]);await git(["remote","add","origin",remote]);await git(["push","-u","origin","main"]);
  await writeFile(join(root,"private-local.txt"),"unrelated local work\n");await git(["add","."]);await git(["commit","-m","local baseline"]);const base_revision=(await git(["rev-parse","HEAD"])).trim();
  await writeFile(join(root,"feature.txt"),"candidate\n");await git(["add","."]);await git(["commit","-m","candidate"]);const revision=(await git(["rev-parse","HEAD"])).trim();
  const run=async(program,args,cwd)=>{
   if(program==="git")return git(args,cwd);
   if(args[0]==="repo")return '{"defaultBranchRef":{"name":"main"}}';
   if(args[1]==="list")return "[]";
   if(args[1]==="create")return "https://github.com/example/project/pull/2\n";
   throw new Error("Unexpected command");
  };
  let firstAttempt = true;
  const failCreateOnce = async (program,args,cwd) => {
    if (program === "gh" && args[1] === "create" && firstAttempt) { firstAttempt=false; throw new Error("GitHub unavailable"); }
    return run(program,args,cwd);
  };
  await assert.rejects(publishCandidate({candidate:{...candidate,base_repo:root,base_revision,revision}},failCreateOnce), /GitHub unavailable/);
  await publishCandidate({candidate:{...candidate,base_repo:root,base_revision,revision}},failCreateOnce);
  const published=`refs/remotes/origin/wuu/candidate-${revision.slice(0,16)}`;
  const files=await git(["ls-tree","--name-only",published]);
  assert.match(files,/feature.txt/);assert.doesNotMatch(files,/private-local.txt/);
  assert.equal((await git(["rev-parse","HEAD"])).trim(),revision);
  assert.equal((await git(["status","--porcelain"])).trim(),"");
 }finally{await rm(directory,{recursive:true,force:true})}
});
