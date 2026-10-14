import { createReadStream, readFileSync, realpathSync, statSync } from "node:fs";
import { createHash } from "node:crypto";
import { basename, extname, join, relative, resolve } from "node:path";
import { videoMimeType } from "../shared/videoMimeType";

const RENDERABLE_IMAGE_EXTENSIONS = new Set([
  ".apng",
  ".avif",
  ".gif",
  ".jpeg",
  ".jpg",
  ".png",
  ".svg",
  ".webp",
]);

const RENDERABLE_PDF_EXTENSIONS = new Set([".pdf"]);
const RENDERABLE_HTML_EXTENSIONS = new Set([".htm", ".html"]);

export function renderableFileURL(filePath: string): string {
  return `wuu-file://local/${Buffer.from(filePath, "utf8").toString("base64url")}`;
}

export function filePathFromRenderableURL(rawURL: string): string | undefined {
  try {
    const url = new URL(rawURL);
    if (url.hostname !== "local") {
      return undefined;
    }
    const encodedPath = url.pathname.replace(/^\/+/, "");
    if (!encodedPath) {
      return undefined;
    }
    return Buffer.from(encodedPath, "base64url").toString("utf8");
  } catch {
    return undefined;
  }
}

export interface ManagedArtifactFile {
  filePath: string;
  sha256: string;
  sizeBytes: number;
  provenance: {
    artifact_id: string;
    name: string;
    plugin_id?: string;
    execution_id?: string;
    tool_call_id?: string;
    mime_type?: string;
  };
}

const verifiedArtifactFiles = new Map<string, string>();

export function managedArtifactFileFromURL(rawURL: string, wuuHome: string): ManagedArtifactFile | undefined {
  try {
    const url = new URL(rawURL);
    if (
      url.protocol !== "wuu-artifact:"
      || !safeArtifactSegment(url.hostname)
      || url.username !== ""
      || url.password !== ""
      || url.port !== ""
      || url.hash !== ""
    ) {
      return undefined;
    }
    const segments = url.pathname.split("/").filter(Boolean).map(decodeURIComponent);
    if (segments.length !== 3 || !segments.every(safeArtifactSegment)) {
      return undefined;
    }
    const [threadID, artifactID, name] = segments;
    if (basename(name) !== name || !/^[a-f0-9]{32}$/.test(artifactID)) return undefined;
    const digests = url.searchParams.getAll("sha256");
    if (
      Array.from(url.searchParams.keys()).some((key) => key !== "sha256")
      || digests.length !== 1
      || !/^[a-f0-9]{64}$/.test(digests[0])
    ) {
      return undefined;
    }
    const artifactRoot = resolve(
      join(wuuHome, "workspaces", url.hostname, "sessions", threadID, "artifacts", artifactID),
    );
    const filePath = resolve(join(artifactRoot, name));
    const rel = relative(artifactRoot, filePath);
    if (!rel || rel === ".." || rel.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`)) {
      return undefined;
    }
    if (!statSync(filePath).isFile()) return undefined;
    const realArtifactRoot = realpathSync(artifactRoot);
    const expectedRealArtifactRoot = resolve(
      join(realpathSync(wuuHome), "workspaces", url.hostname, "sessions", threadID, "artifacts", artifactID),
    );
    if (realArtifactRoot !== expectedRealArtifactRoot) return undefined;
    const realFilePath = realpathSync(filePath);
    const realRel = relative(realArtifactRoot, realFilePath);
    if (!realRel || realRel === ".." || realRel.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`)) {
      return undefined;
    }
    const manifest = JSON.parse(
      readFileSync(join(realArtifactRoot, ".artifact.json"), "utf8"),
    ) as Record<string, unknown>;
    const fileInfo = statSync(realFilePath);
    if (
      manifest.version !== 1
      || manifest.id !== artifactID
      || manifest.thread_id !== threadID
      || typeof manifest.plugin_id !== "string"
      || manifest.plugin_id.trim() === ""
      || manifest.name !== name
      || manifest.sha256 !== digests[0]
      || manifest.size !== fileInfo.size
    ) {
      return undefined;
    }
    return {
      filePath: realFilePath, sha256: digests[0], sizeBytes: fileInfo.size,
      // Only portable host identity is exportable. Never expose source paths,
      // arbitrary plugin metadata, conversation contents or configuration.
      provenance: {
        artifact_id: artifactID, name,
        plugin_id: portableIdentity(manifest.plugin_id),
        execution_id: portableIdentity(manifest.execution_id),
        tool_call_id: portableIdentity(manifest.call_id),
        mime_type: typeof manifest.mime_type === "string" && /^[\w.+-]+\/[\w.+-]+$/.test(manifest.mime_type.split(";", 1)[0])
          ? manifest.mime_type.split(";", 1)[0] : undefined,
      },
    };
  } catch {
    return undefined;
  }
}

export async function verifyManagedArtifactFile(artifact: Pick<ManagedArtifactFile, "filePath" | "sha256">): Promise<boolean> {
  try {
    const info = statSync(artifact.filePath);
    const cacheKey = `${artifact.filePath}\0${info.size}\0${info.mtimeMs}\0${info.ctimeMs}`;
    if (verifiedArtifactFiles.get(cacheKey) === artifact.sha256) return true;
    const hash = createHash("sha256");
    await new Promise<void>((resolvePromise, rejectPromise) => {
      const stream = createReadStream(artifact.filePath);
      stream.on("data", (chunk) => hash.update(chunk));
      stream.on("error", rejectPromise);
      stream.on("end", resolvePromise);
    });
    const actual = hash.digest("hex");
    if (actual !== artifact.sha256) return false;
    verifiedArtifactFiles.set(cacheKey, actual);
    return true;
  } catch {
    return false;
  }
}

function portableIdentity(value: unknown): string | undefined {
  return typeof value === "string" && /^[a-zA-Z0-9_.:@+-]{1,256}$/.test(value) ? value : undefined;
}

function safeArtifactSegment(value: string): boolean {
  return value.length > 0
    && value !== "."
    && value !== ".."
    && !value.includes("/")
    && !value.includes("\\")
    && !value.includes("\0");
}

export function isRenderableImageFile(filePath: string): boolean {
  return isRenderableFileWithExtensions(filePath, RENDERABLE_IMAGE_EXTENSIONS);
}

export function isRenderablePdfFile(filePath: string): boolean {
  return isRenderableFileWithExtensions(filePath, RENDERABLE_PDF_EXTENSIONS);
}

export function isRenderableVideoFile(filePath: string): boolean {
  try {
    return Boolean(videoMimeType(filePath)) && statSync(filePath).isFile();
  } catch {
    return false;
  }
}

export function isRenderableHtmlFile(filePath: string): boolean {
  return isRenderableFileWithExtensions(filePath, RENDERABLE_HTML_EXTENSIONS);
}

function isRenderableFileWithExtensions(
  filePath: string,
  extensions: Set<string>,
): boolean {
  try {
    return (
      statSync(filePath).isFile() &&
      extensions.has(extname(filePath).toLowerCase())
    );
  } catch {
    return false;
  }
}
