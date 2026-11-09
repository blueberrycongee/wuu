import { mkdir, mkdtemp, readFile, rm, symlink, utimes, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { afterEach, beforeEach, expect, it } from "vitest";
import { discoverRecentWorkspaces } from "./recentWorkspaces";

let home: string;
beforeEach(async () => { home = await mkdtemp(join(tmpdir(), "recent-workspaces-")); });
afterEach(async () => { await rm(home, { recursive: true, force: true }); });

async function session(source: "codex" | "claude", id: string, cwd: string, date: string) {
  const file = source === "codex"
    ? join(home, ".codex", "sessions", "2026", "10", "05", `${id}.jsonl`)
    : join(home, ".claude", "projects", "project", `${id}.jsonl`);
  await mkdir(dirname(file), { recursive: true });
  await writeFile(file, `broken record\n${JSON.stringify(source === "codex"
    ? { type: "session_meta", payload: { cwd } }
    : { type: "user", cwd })}\n`);
  await utimes(file, new Date(date), new Date(date));
  return file;
}

it("merges source histories by real directory, orders recent activity and never changes histories", async () => {
  const first = join(home, "work", "first");
  const second = join(home, "work", "second");
  const alias = join(home, "alias");
  await mkdir(join(first, ".git"), { recursive: true });
  await mkdir(second, { recursive: true });
  await symlink(first, alias);
  const file = await session("codex", "a", first, "2026-10-01T00:00:00Z");
  const original = await readFile(file, "utf8");
  await session("claude", "b", alias, "2026-10-05T00:00:00Z");
  await session("codex", "c", second, "2026-10-03T00:00:00Z");
  const result = await discoverRecentWorkspaces({ homeDir: home });
  expect(result.candidates.map((item) => item.name)).toEqual(["first", "second"]);
  expect(result.candidates[0]).toMatchObject({ sources: ["codex", "claude"], sessionCount: 2, isRepository: true });
  expect(result.candidates[1].isRepository).toBe(false);
  expect(await readFile(file, "utf8")).toBe(original);
});

it("ignores missing folders, scratch directories, linked worktrees and already registered workspaces", async () => {
  const imported = join(home, "work", "imported");
  const linked = join(home, "work", "linked");
  const downloaded = join(home, "Downloads", "example");
  for (const path of [imported, linked, downloaded]) await mkdir(path, { recursive: true });
  await writeFile(join(linked, ".git"), "gitdir: /repo/.git/worktrees/linked\n");
  for (const [i, cwd] of [home, imported, linked, downloaded, join(home, "missing")].entries()) {
    await session("codex", String(i), cwd, "2026-10-05T00:00:00Z");
  }
  const result = await discoverRecentWorkspaces({ homeDir: home, existingPaths: [imported] });
  expect(result.candidates).toEqual([]);
});

it("uses configured history roots and reports bounded or unreadable history without losing valid candidates", async () => {
  const custom = join(home, "custom-codex");
  const project = join(home, "work");
  await mkdir(project, { recursive: true });
  await mkdir(join(custom, "sessions"), { recursive: true });
  await writeFile(join(custom, "sessions", "valid.jsonl"), JSON.stringify({ type: "session_meta", payload: { cwd: project } }) + "\n");
  await writeFile(join(custom, "sessions", "oversized.jsonl"), "x".repeat(300_000));
  const result = await discoverRecentWorkspaces({ homeDir: home, codexHome: custom });
  expect(result.candidates).toHaveLength(1);
  expect(result.incomplete).toBe(true);
  expect(await discoverRecentWorkspaces({ homeDir: join(home, "empty") })).toEqual({ candidates: [], incomplete: false });
});
