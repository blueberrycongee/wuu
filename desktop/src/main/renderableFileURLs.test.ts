import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { managedArtifactFileFromURL, verifyManagedArtifactFile } from "./renderableFileURLs";

it("resolves built-in presented snapshots using the existing manifest and integrity contract", async () => {
  const home = mkdtempSync(join(tmpdir(), "wuu-present-artifact-"));
  try {
    const id = "a".repeat(32);
    const name = "图表.svg";
    const content = '<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>';
    const sha256 = createHash("sha256").update(content).digest("hex");
    const directory = join(home, "workspaces", "workspace", "sessions", "thread", "artifacts", id);
    mkdirSync(directory, { recursive: true });
    const path = join(directory, name);
    writeFileSync(path, content);
    writeFileSync(join(directory, ".artifact.json"), JSON.stringify({
      version: 1, id, thread_id: "thread", execution_id: "call", call_id: "call",
      plugin_id: "builtin.present_artifact", name, mime_type: "image/svg+xml",
      size: Buffer.byteLength(content), sha256, source_kind: "path", source_name: name,
    }));
    const uri = `wuu-artifact://workspace/thread/${id}/${encodeURIComponent(name)}?sha256=${sha256}`;
    const artifact = managedArtifactFileFromURL(uri, home);
    expect(artifact).toBeDefined();
    expect(await verifyManagedArtifactFile(artifact!)).toBe(true);
    expect(managedArtifactFileFromURL(uri.replace("/thread/", "/other/"), home)).toBeUndefined();
    expect(managedArtifactFileFromURL(uri.replace(sha256, "b".repeat(64)), home)).toBeUndefined();
    // Never silently render changed bytes under an old snapshot's identity.
    writeFileSync(path, content.replace('width="10"', 'width="20"'));
    expect(await verifyManagedArtifactFile(artifact!)).toBe(false);
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});
