import { mkdtempSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
vi.mock("electron", () => ({ app: { getPath: () => "/unused" } }));
import { resolveUserHostContext } from "./userHost";
const roots: string[] = [];
afterEach(() => { vi.unstubAllEnvs(); roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true })); });
it("resolves the same explicit user identity without registering or selecting a project", () => {
  const home = mkdtempSync(join(tmpdir(), "wuu-user-host-")); roots.push(home); vi.stubEnv("WUU_HOME", home);
  const first = resolveUserHostContext("local", "personal");
  expect(resolveUserHostContext("local", "personal")).toEqual(first);
  expect(first.cwd).toBe(join(home, "user-spaces", "local", "personal", "files"));
  expect(existsSync(first.cwd)).toBe(true);
  expect(existsSync(join(home, "projects.json"))).toBe(false);
  expect(resolveUserHostContext("local", "coding").cwd).not.toBe(first.cwd);
});
it.each(["../other", "a/b", "", ".", "..", "a\\b"])("rejects unsafe user host identity %s", value => {
  expect(() => resolveUserHostContext(value, "personal")).toThrow();
  expect(() => resolveUserHostContext("local", value)).toThrow();
});
