import { createHash } from "node:crypto";
import { createWriteStream } from "node:fs";
import { chmod, mkdir, mkdtemp, open, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { Transform } from "node:stream";
import { pipeline } from "node:stream/promises";
import { create as createArchive } from "tar";
import type { ArtifactItemMenuParams, Thread, ToolResultContentPart, TurnStatus } from "../shared/protocol";
import { artifactMenuFile } from "./artifactExternalFile";

const MAX_EVIDENCE_FILES = 128;
const MAX_EVIDENCE_BYTES = 512 * 1024 * 1024;
const MAX_ARTIFACT_BYTES = 256 * 1024 * 1024;

export type ArtifactEvidenceGroup = {
  threadID: string;
  turnID: string;
  turnStatus: TurnStatus;
  artifacts: ArtifactItemMenuParams[];
};

function artifactURI(part: ToolResultContentPart): string | undefined {
  if (part.type === "text" || part.remote_ref) return undefined;
  const resource = part.resource !== null && typeof part.resource === "object" && !Array.isArray(part.resource)
    ? part.resource as Record<string, unknown> : undefined;
  const uri = typeof part.uri === "string" && part.uri.trim() ? part.uri : resource?.uri;
  return typeof uri === "string" ? uri.trim() : undefined;
}

/** Membership comes from persisted tool results, never a renderer-supplied list. */
export function artifactEvidenceGroup(thread: Thread, input: ArtifactItemMenuParams): ArtifactEvidenceGroup {
  const turns = thread.id === input.threadId ? thread.turns.filter(turn => turn.items.some(item =>
    item.type === "tool_call" && (input.itemId === undefined || item.id === input.itemId)
      && item.result_detail?.content?.some(part => artifactURI(part) === input.uri),
  )) : [];
  if (turns.length !== 1) throw new Error("Artifact source turn is unavailable or ambiguous");
  const turn = turns[0];
  const uris = new Set<string>();
  for (const item of turn.items) {
    if (item.type !== "tool_call") continue;
    for (const part of item.result_detail?.content ?? []) {
      const uri = artifactURI(part);
      if (uri && /^wuu-artifact:/i.test(uri)) uris.add(uri);
    }
  }
  return { threadID: thread.id, turnID: turn.id, turnStatus: turn.status, artifacts: [...uris].map(uri => ({ uri, threadId: thread.id })) };
}

/** A portable archive of immutable files, not a claim that a test or review passed. */
export async function exportArtifactEvidence(
  group: ArtifactEvidenceGroup,
  wuuHome: string,
  destination: string,
  isCurrentSource: () => boolean,
  onProgress: (fraction: number) => void,
): Promise<boolean> {
  if (!group.artifacts.length || group.artifacts.length > MAX_EVIDENCE_FILES) {
    throw new Error(`Evidence export requires 1–${MAX_EVIDENCE_FILES} artifacts`);
  }
  // Validate every member before making output. Missing files are never silently
  // dropped from a bundle that would otherwise look like complete evidence.
  const sources = [];
  let total = 0;
  for (const input of group.artifacts) {
    if (!isCurrentSource()) return false;
    const artifact = await artifactMenuFile(input, wuuHome, Math.min(MAX_ARTIFACT_BYTES, MAX_EVIDENCE_BYTES - total));
    total += artifact.sizeBytes;
    sources.push(artifact);
  }
  if (!isCurrentSource()) return false;
  // Stage beside the selected destination so rename publishes the complete
  // archive atomically, including when replacing a user-selected existing file.
  const staging = await mkdtemp(join(dirname(destination), ".wuu-evidence-"));
  let copied = 0;
  try {
    const artifacts = [];
    for (const [index, artifact] of sources.entries()) {
      if (!isCurrentSource()) return false;
      const { provenance } = artifact;
      // Original display names stay in the manifest. Archive names must also
      // extract on Windows and cannot become links, traversal or device paths.
      let name = provenance.name.replace(/[<>:"/\\|?*\x00-\x1f\x7f]/g, "_").replace(/[. ]+$/, "");
      if (!name || /^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(name)) name = `artifact-${name || "file"}`;
      const relativePath = `artifacts/${String(index + 1).padStart(3, "0")}/${name}`;
      const target = join(staging, relativePath);
      await mkdir(dirname(target), { recursive: true, mode: 0o700 });
      const source = await open(artifact.filePath, "r");
      try {
        const info = await source.stat();
        if (!info.isFile() || info.size !== artifact.sizeBytes) throw new Error("Artifact changed while preparing evidence");
        const hash = createHash("sha256");
        let size = 0;
        await pipeline(source.createReadStream(), new Transform({
          transform(chunk: Buffer, _encoding, callback) {
            size += chunk.length;
            if (!isCurrentSource()) { callback(new Error("Artifact window is no longer available")); return; }
            if (size > artifact.sizeBytes) { callback(new Error("Artifact changed while preparing evidence")); return; }
            hash.update(chunk);
            copied += chunk.length;
            onProgress(total ? copied / total * 0.8 : 0.8);
            callback(null, chunk);
          },
        }), createWriteStream(target, { flags: "wx", mode: 0o600 }));
        if (size !== artifact.sizeBytes || hash.digest("hex") !== artifact.sha256) {
          throw new Error("Artifact changed while preparing evidence");
        }
      } finally {
        await source.close();
      }
      artifacts.push({ path: relativePath, ...provenance, size_bytes: artifact.sizeBytes, sha256: artifact.sha256 });
    }
    await writeFile(join(staging, "evidence.json"), JSON.stringify({
      format: "wuu-artifact-evidence", version: 1, exported_at: new Date().toISOString(),
      thread_id: group.threadID, turn_id: group.turnID, turn_status: group.turnStatus, artifacts,
    }, null, 2) + "\n", { flag: "wx", mode: 0o600 });
    onProgress(0.8);
    const archive = join(staging, "bundle.tar.gz");
    await createArchive({ file: archive, cwd: staging, gzip: { level: 1 }, portable: true, noMtime: true, strict: true }, ["evidence.json", "artifacts"]);
    await chmod(archive, 0o600);
    const file = await open(archive, "r");
    try { await file.sync(); } finally { await file.close(); }
    if (!isCurrentSource()) return false;
    await rename(archive, destination);
    onProgress(1);
    return true;
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}
