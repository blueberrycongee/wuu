import {
  afterEach,
  describe,
  expect,
  it,
} from "vitest";
import {
  chmodSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import type { RuntimeContext } from "../shared/protocol";
import { WorkspaceFileService } from "./workspaceFiles";

const workspaces: string[] = [];

afterEach(() => {
  for (const workspace of workspaces.splice(0)) {
    rmSync(workspace, { recursive: true, force: true });
  }
});

function createWorkspace(): string {
  const workspace = mkdtempSync(join(tmpdir(), "wuu-workspace-files-"));
  workspaces.push(workspace);
  return workspace;
}

function writeWorkspaceFile(root: string, path: string, text = "ok\n"): void {
  const absolutePath = join(root, path);
  mkdirSync(dirname(absolutePath), { recursive: true });
  writeFileSync(absolutePath, text);
}

function createService(root: string): WorkspaceFileService {
  const context: RuntimeContext = {
    kind: "no_project",
    cwd: root,
  };
  return new WorkspaceFileService(() => context);
}

describe("WorkspaceFileService file reference resolution", () => {
  it("resolves explicit workspace-relative file references", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "internal/tools/tool_discovery.go");

    expect(createService(root).resolveFileReference("internal/tools/tool_discovery.go")).toMatchObject({
      root,
      reference: "internal/tools/tool_discovery.go",
      status: "resolved",
      path: "internal/tools/tool_discovery.go",
      absolute_path: join(root, "internal/tools/tool_discovery.go"),
    });
  });

  it("does not resolve missing bare filenames", () => {
    const root = createWorkspace();

    expect(createService(root).resolveFileReference("tool_search.go")).toMatchObject({
      root,
      reference: "tool_search.go",
      status: "missing",
    });
  });

  it("resolves bare filenames only when they are unique in the workspace", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "internal/tools/tool_discovery.go");

    expect(createService(root).resolveFileReference("tool_discovery.go")).toMatchObject({
      root,
      reference: "tool_discovery.go",
      status: "resolved",
      path: "internal/tools/tool_discovery.go",
    });
  });

  it("does not guess when a bare filename has multiple matches", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "app/README.md");
    writeWorkspaceFile(root, "docs/README.md");

    expect(createService(root).resolveFileReference("README.md")).toMatchObject({
      root,
      reference: "README.md",
      status: "ambiguous",
      matches: ["app/README.md", "docs/README.md"],
    });
  });

  it("strips line suffixes before resolving the file path", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "README.md");

    expect(createService(root).resolveFileReference("README.md (line 19)")).toMatchObject({
      root,
      reference: "README.md (line 19)",
      status: "resolved",
      path: "README.md",
    });
  });

  it("keeps explicit root-relative references separate from ambiguous bare names", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "README.md");
    writeWorkspaceFile(root, "docs/README.md");
    const service = createService(root);

    expect(service.resolveFileReference("README.md")).toMatchObject({
      status: "ambiguous",
    });
    expect(service.resolveFileReference("./README.md")).toMatchObject({
      status: "resolved",
      path: "README.md",
    });
  });
});

// Covers the worktree-fork panel-root gap: a thread's own cwd (e.g. a git
// worktree) can differ from the active project's runtime context. Callers
// pass that thread cwd as an explicit `root` override on each call; these
// tests prove the override actually redirects reads/listing there (and
// keeps its own containment checks) instead of silently falling back to
// the constructed context's cwd.
describe("WorkspaceFileService root override", () => {
  it("lists a directory rooted at the override, not the constructed context", () => {
    const defaultRoot = createWorkspace();
    writeWorkspaceFile(defaultRoot, "default-only.txt");
    const overrideRoot = createWorkspace();
    writeWorkspaceFile(overrideRoot, "override-only.txt");

    const service = createService(defaultRoot);
    const result = service.directoryList(undefined, overrideRoot);

    expect(result.root).toBe(overrideRoot);
    expect(result.entries.map((entry) => entry.name)).toEqual(["override-only.txt"]);
  });

  it("reads a file relative to the override root, not the constructed context", () => {
    const defaultRoot = createWorkspace();
    const overrideRoot = createWorkspace();
    writeWorkspaceFile(overrideRoot, "notes.txt");

    const service = createService(defaultRoot);
    const result = service.readFile("notes.txt", overrideRoot);

    expect(result.root).toBe(overrideRoot);
    expect(result.absolute_path).toBe(join(overrideRoot, "notes.txt"));
    expect(result.text).toBe("ok\n");
  });

  it("rejects a qualified file reference that escapes the override root even though it exists next to the constructed context", () => {
    const defaultRoot = createWorkspace();
    writeWorkspaceFile(defaultRoot, "secret.txt");
    // Nest the override root inside the default root so "../secret.txt"
    // from the override resolves to a real, readable file one directory
    // up — proving containment is enforced against the override root
    // itself, not merely "does the resolved path exist somewhere".
    const overrideRoot = join(defaultRoot, "worktree");
    mkdirSync(overrideRoot, { recursive: true });

    const service = createService(defaultRoot);

    expect(service.resolveFileReference("../secret.txt", overrideRoot)).toMatchObject({
      root: overrideRoot,
      status: "missing",
    });
  });

  it("resolves a file reference relative to the override root, not the constructed context", () => {
    const defaultRoot = createWorkspace();
    writeWorkspaceFile(defaultRoot, "shared-name.go");
    const overrideRoot = createWorkspace();
    writeWorkspaceFile(overrideRoot, "shared-name.go");

    const service = createService(defaultRoot);
    const result = service.resolveFileReference("shared-name.go", overrideRoot);

    expect(result).toMatchObject({
      root: overrideRoot,
      status: "resolved",
      path: "shared-name.go",
      absolute_path: join(overrideRoot, "shared-name.go"),
    });
  });

  it("falls back to the constructed context when no override root is given", () => {
    const defaultRoot = createWorkspace();
    writeWorkspaceFile(defaultRoot, "default-only.txt");

    const service = createService(defaultRoot);
    const result = service.directoryList();

    expect(result.root).toBe(defaultRoot);
    expect(result.entries.map((entry) => entry.name)).toEqual(["default-only.txt"]);
  });
});

describe("WorkspaceFileService file save", () => {
  it("returns save metadata when reading a text file", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "settings.json", "{\"enabled\":true}\n");

    const result = createService(root).readFile("settings.json");

    expect(result.text).toBe("{\"enabled\":true}\n");
    expect(result.sha256).toMatch(/^[a-f0-9]{64}$/);
    expect(result.mtime_ms).toBeGreaterThan(0);
  });

  it.each([
    ["assets/mascot/wuu-mascot-concept-01.png", "image"],
    ["videos/demo.MP4", "video"],
  ])("returns a renderable URL when reading %s", (path, kind) => {
    const root = createWorkspace();
    const imagePath = join(root, path);
    mkdirSync(dirname(imagePath), { recursive: true });
    writeFileSync(imagePath, Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0x01]));

    const result = createService(root).readFile(path);

    expect(result.binary).toBe(true);
    expect(result.renderable_kind).toBe(kind);
    expect(result.text).toBeUndefined();
    expect(result.renderable_url).toBe(
      `wuu-file://local/${Buffer.from(imagePath, "utf8").toString("base64url")}`,
    );
  });

  it("writes a text file when the base metadata still matches", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "settings.json", "{\"enabled\":true}\n");
    const service = createService(root);
    const base = service.readFile("settings.json");

    const result = service.writeFile({
      path: "settings.json",
      text: "{\"enabled\":false}\n",
      base_sha256: base.sha256,
      base_mtime_ms: base.mtime_ms,
    });

    expect(result.status).toBe("saved");
    expect(result.file.text).toBe("{\"enabled\":false}\n");
    expect(readFileSync(join(root, "settings.json"), "utf8")).toBe("{\"enabled\":false}\n");
    expect(result.file.sha256).not.toBe(base.sha256);
  });

  it("preserves existing file permissions when saving", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "script.sh", "echo old\n");
    const path = join(root, "script.sh");
    chmodSync(path, 0o750);
    const service = createService(root);
    const base = service.readFile("script.sh");

    service.writeFile({
      path: "script.sh",
      text: "echo new\n",
      base_sha256: base.sha256,
      base_mtime_ms: base.mtime_ms,
    });

    if (process.platform !== "win32") {
      expect(statSync(path).mode & 0o777).toBe(0o750);
    }
  });

  it.skipIf(process.platform === "win32")(
    "saves through an in-workspace symlink without replacing the link",
    () => {
      const root = createWorkspace();
      writeWorkspaceFile(root, "target.txt", "old\n");
      symlinkSync("target.txt", join(root, "link.txt"));
      const service = createService(root);
      const base = service.readFile("link.txt");

      const result = service.writeFile({
        path: "link.txt",
        text: "new\n",
        base_sha256: base.sha256,
        base_mtime_ms: base.mtime_ms,
      });

      expect(result.status).toBe("saved");
      expect(lstatSync(join(root, "link.txt")).isSymbolicLink()).toBe(true);
      expect(readFileSync(join(root, "target.txt"), "utf8")).toBe("new\n");
    },
  );

  it("reports a conflict without overwriting when the file changed after read", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "settings.json", "{\"enabled\":true}\n");
    const service = createService(root);
    const base = service.readFile("settings.json");
    writeWorkspaceFile(root, "settings.json", "{\"external\":true}\n");

    const result = service.writeFile({
      path: "settings.json",
      text: "{\"enabled\":false}\n",
      base_sha256: base.sha256,
      base_mtime_ms: base.mtime_ms,
    });

    expect(result.status).toBe("conflict");
    expect(result.file.text).toBe("{\"external\":true}\n");
    expect(readFileSync(join(root, "settings.json"), "utf8")).toBe("{\"external\":true}\n");
  });

  it("rejects writes outside the workspace root", () => {
    const root = createWorkspace();
    const service = createService(root);

    expect(() =>
      service.writeFile({
        path: "../escape.txt",
        text: "no\n",
        base_sha256: "0".repeat(64),
        base_mtime_ms: 1,
      }),
    ).toThrow(/outside workspace/i);
  });

  it("rejects writes to binary and truncated files", () => {
    const root = createWorkspace();
    writeWorkspaceFile(root, "binary.dat", "hello\0world");
    writeWorkspaceFile(root, "large.txt", "x".repeat(520 * 1024));
    const service = createService(root);

    expect(() =>
      service.writeFile({
        path: "binary.dat",
        text: "no\n",
        base_sha256: "0".repeat(64),
        base_mtime_ms: 1,
      }),
    ).toThrow(/binary/i);

    expect(() =>
      service.writeFile({
        path: "large.txt",
        text: "no\n",
        base_sha256: "0".repeat(64),
        base_mtime_ms: 1,
      }),
    ).toThrow(/truncated/i);
  });
});
