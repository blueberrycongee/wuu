import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { expect, it } from "vitest";
import { managedArtifactFileFromURL, verifyManagedArtifactFile } from "./renderableFileURLs";
import { artifactMenuFile, exportArtifactCopy, canOpenArtifactExternally } from "./artifactExternalFile";

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
    const request = { uri, threadId: "thread" };
    expect(await artifactMenuFile(request, home)).toEqual(artifact);
    await expect(artifactMenuFile({ ...request, threadId: "other" }, home)).rejects.toThrow();
    await expect(artifactMenuFile({ ...request, uri: `file://${path}` }, home)).rejects.toThrow();
    expect(await canOpenArtifactExternally(path)).toBe(true);
    const copy = await exportArtifactCopy(request, home);
    try {
      expect(copy).not.toBe(path);
      expect(readFileSync(copy, "utf8")).toBe(content);
      writeFileSync(copy, "edited externally");
      expect(readFileSync(path, "utf8")).toBe(content);
      expect(await verifyManagedArtifactFile(artifact!)).toBe(true);
    } finally { rmSync(dirname(copy), { recursive: true, force: true }); }
    for (const name of ["changes.patch", "changes.diff"]) {
      const document = join(home, name);
      writeFileSync(document, "--- before\n+++ after\n");
      expect(await canOpenArtifactExternally(document)).toBe(true);
    }
    for (const name of ["run.command", "run.exe", "run.bat", "run.desktop", "run.lnk", "run.url", "run.scpt", "run.py"]) {
      const executable = join(home, name);
      writeFileSync(executable, "script");
      expect(await canOpenArtifactExternally(executable)).toBe(false);
    }
    for (const bytes of [Buffer.from("#! /bin/sh\n"), Buffer.from("MZ"), Buffer.from([0x7f, 0x45, 0x4c, 0x46]), Buffer.from([0xcf, 0xfa, 0xed, 0xfe])]) {
      const disguised = join(home, "disguised.pdf");
      writeFileSync(disguised, bytes);
      expect(await canOpenArtifactExternally(disguised)).toBe(false);
    }
    expect(managedArtifactFileFromURL(uri.replace("/thread/", "/other/"), home)).toBeUndefined();
    expect(managedArtifactFileFromURL(uri.replace(sha256, "b".repeat(64)), home)).toBeUndefined();
    // Never silently render changed bytes under an old snapshot's identity.
    writeFileSync(path, content.replace('width="10"', 'width="20"'));
    expect(await verifyManagedArtifactFile(artifact!)).toBe(false);
    await expect(exportArtifactCopy(request, home)).rejects.toThrow();
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});
