import { chmod, copyFile, mkdtemp, open, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, extname, join } from "node:path";
import type { ArtifactItemMenuParams } from "../shared/protocol";
import { managedArtifactFileFromURL, verifyManagedArtifactFile, type ManagedArtifactFile } from "./renderableFileURLs";

// External document opening is deliberately narrower than saving a delivery.
// Scripts, executables, shortcuts and unknown formats remain save/reveal only.
const EXTERNAL_DOCUMENT_EXTENSIONS = new Set([
  ".pdf", ".txt", ".md", ".markdown", ".patch", ".diff", ".csv", ".tsv", ".json", ".xml", ".yaml", ".yml", ".log",
  ".html", ".htm", ".rtf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp",
  ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".bmp", ".tif", ".tiff", ".heic", ".ico",
  ".mp3", ".wav", ".m4a", ".aac", ".flac", ".ogg", ".mp4", ".m4v", ".mov", ".webm", ".mkv",
  ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".epub",
]);

export async function artifactMenuFile(
  input: ArtifactItemMenuParams,
  wuuHome: string,
): Promise<ManagedArtifactFile> {
  if (!input || typeof input.uri !== "string" || typeof input.threadId !== "string" || !input.threadId) {
    throw new Error("Invalid artifact reference");
  }
  const url = new URL(input.uri);
  if (url.protocol !== "wuu-artifact:" || decodeURIComponent(url.pathname.split("/")[1] ?? "") !== input.threadId) {
    throw new Error("Artifact does not belong to the source conversation");
  }
  const artifact = managedArtifactFileFromURL(input.uri, wuuHome);
  if (!artifact || !await verifyManagedArtifactFile(artifact)) {
    throw new Error("Artifact is unavailable or has changed");
  }
  return artifact;
}

export async function canOpenArtifactExternally(filePath: string): Promise<boolean> {
  if (!EXTERNAL_DOCUMENT_EXTENSIONS.has(extname(filePath).toLowerCase())) return false;
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

/** Editors receive an independent temporary copy, never the immutable snapshot. */
export async function exportArtifactCopy(input: ArtifactItemMenuParams, wuuHome: string): Promise<string> {
  const artifact = await artifactMenuFile(input, wuuHome);
  const directory = await mkdtemp(join(tmpdir(), "wuu-artifact-open-"));
  const destination = join(directory, basename(artifact.filePath));
  try {
    await copyFile(artifact.filePath, destination);
    await chmod(destination, 0o600);
    // Check the copied bytes too, so a changed source cannot race verification.
    if (!await verifyManagedArtifactFile({ filePath: destination, sha256: artifact.sha256 })) {
      throw new Error("Artifact changed while preparing the file");
    }
    // Keep successful exports for the OS temporary-directory lifecycle: an
    // external app may still have the document open after Wuu quits.
    return destination;
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
}
