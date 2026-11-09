import { chmod, mkdtemp, open, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import { basename, extname, join } from "node:path";
import type { ArtifactSourceLease } from "../shared/protocol";
import type { ArtifactSources } from "./artifactSources";

// External document opening is deliberately narrower than saving a delivery.
// Scripts, executables, shortcuts and unknown formats remain save/reveal only.
const EXTERNAL_DOCUMENT_EXTENSIONS = new Set([
  ".pdf", ".txt", ".md", ".markdown", ".patch", ".diff", ".csv", ".tsv", ".json", ".xml", ".yaml", ".yml", ".log",
  ".html", ".htm", ".rtf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp",
  ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".bmp", ".tif", ".tiff", ".heic", ".ico",
  ".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg", ".mp4", ".m4v", ".mov", ".webm", ".mkv",
  ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".epub",
]);

export function isExternalDocumentName(name: string): boolean {
  return EXTERNAL_DOCUMENT_EXTENSIONS.has(extname(name).toLowerCase());
}

export async function canOpenArtifactExternally(filePath: string): Promise<boolean> {
  if (!isExternalDocumentName(filePath)) return false;
  const file = await open(filePath, "r");
  try {
    const info = await file.stat();
    if (!info.isFile() || (process.platform !== "win32" && (info.mode & 0o111) !== 0)) return false;
    const header = Buffer.alloc(4);
    const { bytesRead } = await file.read(header, 0, header.length, 0);
    const magic = header.subarray(0, bytesRead).toString("hex");
    return !magic.startsWith("2321") // shebang
      && !magic.startsWith("4d5a") // DOS/Windows executable
      && !["7f454c46", "feedface", "feedfacf", "cefaedfe", "cffaedfe", "cafebabe", "bebafeca", "cafebabf", "bfbafeca"].includes(magic);
  } finally {
    await file.close();
  }
}

/** Materialize only after an explicit open/reveal action. A failed or stale
 * action removes its private copy; successful external apps own its temp lifetime. */
export async function exportArtifactCopy(source: ArtifactSourceLease, artifacts: ArtifactSources): Promise<{ path: string; dispose(): Promise<void> }> {
  const artifact = artifacts.metadata(source.leaseId);
  const directory = await mkdtemp(join(tmpdir(), "wuu-artifact-open-"));
  const name = basename(artifact.name.replace(/\\/g, "/")).replace(/[<>:"|?*\x00-\x1f]/g, "_");
  const destination = join(directory, name && !/^\.+$/.test(name) ? name : "artifact");
  const dispose = () => rm(directory, { recursive: true, force: true });
  try {
    const response = await artifacts.response(new Request(source.url));
    if (!response.ok || !response.body) throw new Error("Artifact is unavailable");
    const reader = response.body.getReader();
    const file = await open(destination, "wx", 0o600);
    const hash = createHash("sha256"); let size = 0;
    try {
      while (true) {
        const {done, value} = await reader.read(); if (done) break;
        size += value.byteLength;
        if (size > artifact.size) throw new Error("Artifact size changed");
        hash.update(value); await file.writeFile(value);
      }
      await file.sync();
    } finally { await reader.cancel(); await file.close(); }
    if (size !== artifact.size || hash.digest("hex") !== artifact.sha256) throw new Error("Artifact contents changed");
    await chmod(destination, 0o600);
    return { path: destination, dispose };
  } catch (error) { await dispose(); throw error; }
}
