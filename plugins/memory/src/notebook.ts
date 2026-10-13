import { constants } from "node:fs";
import { createHash, randomBytes } from "node:crypto";
import { lstat, mkdir, open, readFile, rename, unlink } from "node:fs/promises";
import { dirname, join } from "node:path";

export const INDEX_FILE = "MEMORY.md";
export const MAX_MEMORY_BYTES = 256 * 1024;
const MAX_INDEX_LINES = 200;
export const MAX_INDEX_BYTES = 25_000;
const threatPatterns = [
  /ignore\s+(previous|all|above|prior)\s+instructions/i,
  /you\s+are\s+now\s+/i,
  /do\s+not\s+tell\s+the\s+user/i,
  /system\s+prompt\s+override/i,
  /disregard\s+(your|all|any)\s+(instructions|rules|guidelines)/i,
  /act\s+as\s+(if|though)\s+you\s+(have\s+no|don'?t\s+have)\s+(restrictions|limits|rules)/i,
  /(curl|wget)\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)/i,
  /cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)/i,
  /authorized_keys|\$HOME\/\.ssh|~\/\.ssh|\$HOME\/\.wuu\/\.env|~\/\.wuu\/\.env/i,
];

export function missing(error: unknown): boolean {
  return (error as NodeJS.ErrnoException)?.code === "ENOENT";
}

export function unsafeLine(line: string): boolean {
  return /[\u200b\u200c\u200d\u2060\ufeff\u202a-\u202e]/u.test(line) || threatPatterns.some((pattern) => pattern.test(line));
}

export async function readSafeIndex(notebook: string): Promise<string> {
  let raw: string;
  try { raw = await readFile(join(notebook, INDEX_FILE), "utf8"); }
  catch (error) {
    if (missing(error)) return "";
    throw new Error(`memory: read index: ${error instanceof Error ? error.message : String(error)}`);
  }
  const result = safeIndex(raw, MAX_INDEX_BYTES);
  return result.content + (result.truncated ? "\n\n> WARNING: MEMORY.md was truncated before prompt injection. Keep index entries short and move detail into topic files." : "");
}

function boundedText(content: string, maxBytes: number): { content: string; truncated: boolean } {
  const bytes = Buffer.from(content);
  if (bytes.length <= maxBytes) return { content, truncated: false };
  let cut = maxBytes;
  while (cut > 0 && (bytes[cut]! & 0xc0) === 0x80) cut--;
  return { content: bytes.subarray(0, cut).toString(), truncated: true };
}

function safeIndex(raw: string, maxBytes: number): { content: string; truncated: boolean } {
  const lines = raw.split("\n").map((line) => unsafeLine(line) ? "[memory line removed: security]" : line).join("\n").trim().split("\n");
  const content = lines.slice(0, MAX_INDEX_LINES).join("\n");
  const bytes = Buffer.from(content);
  if (bytes.length > maxBytes) {
    const newline = bytes.subarray(0, maxBytes).lastIndexOf(10);
    if (newline > 0) return { content: bytes.subarray(0, newline).toString(), truncated: true };
  }
  const bounded = boundedText(content, maxBytes);
  return { content: bounded.content, truncated: lines.length > MAX_INDEX_LINES || bounded.truncated };
}

/** Bound reads from regular notebook files and reject symlink replacements. */
export async function readNotebookFile(notebook: string, name: string): Promise<Buffer> {
  const path = join(notebook, name);
  const before = await lstat(path);
  if (!before.isFile()) throw new Error("memory file must be a regular file");
  const file = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
  try {
    const info = await file.stat();
    if (!info.isFile() || before.dev !== info.dev || before.ino !== info.ino) throw new Error("memory file changed while opening");
    const buffer = Buffer.alloc(MAX_MEMORY_BYTES + 1);
    let length = 0;
    while (length < buffer.length) {
      const { bytesRead } = await file.read(buffer, length, buffer.length - length, null);
      if (!bytesRead) break;
      length += bytesRead;
    }
    if (length > MAX_MEMORY_BYTES) throw new Error(`memory file exceeds ${MAX_MEMORY_BYTES} bytes`);
    const raw = buffer.subarray(0, length);
    try { new TextDecoder("utf-8", { fatal: true }).decode(raw); }
    catch { throw new Error("memory file must contain UTF-8 text"); }
    return raw;
  } finally {
    await file.close();
  }
}

export async function readNotebookSnapshot(notebook: string, method: string, params: Record<string, unknown>): Promise<unknown> {
  if (params.file != null && typeof params.file !== "string") throw new Error("memory.notebook file must be a string");
  let name = (params.file as string | undefined)?.trim() ?? "";
  let limit = 16 * 1024;
  let maximum = 64 * 1024;
  switch (method) {
    case "index":
      if (name) throw new Error("memory.notebook index does not accept a file");
      name = INDEX_FILE;
      limit = maximum = MAX_INDEX_BYTES;
      break;
    case "read":
      if (!name || name.startsWith(".") || /[/\\]/.test(name) || !name.endsWith(".md") || name.toLowerCase() === INDEX_FILE.toLowerCase()) {
        throw new Error("memory.notebook read requires a plain, non-hidden topic .md filename; use index for MEMORY.md");
      }
      break;
    default: throw new Error(`unknown memory.notebook method ${JSON.stringify(method)}`);
  }
  const maxBytes = params.max_bytes ?? 0;
  if (typeof maxBytes !== "number" || !Number.isInteger(maxBytes) || maxBytes < 0 || maxBytes > maximum) {
    throw new Error(`memory.notebook max_bytes must be between 1 and ${maximum}, or 0 for the default`);
  }
  if (maxBytes) limit = maxBytes;
  const source = { scope: "user_notebook", file: name };
  let raw: Buffer;
  try { raw = await readNotebookFile(notebook, name); }
  catch (error) {
    if (missing(error)) return { version: 1, source, found: false, content: "", truncated: false };
    throw error;
  }
  const bounded = method === "index" ? safeIndex(raw.toString(), limit) : boundedText(raw.toString(), limit);
  return { version: 1, source, found: true, revision: `sha256:${createHash("sha256").update(raw).digest("hex")}`, ...bounded };
}

/** A same-directory rename publishes only complete, flushed files. */
export async function writeAtomicFile(path: string, content: string, mode: number, createDirectory: boolean): Promise<void> {
  if (createDirectory) await mkdir(dirname(path), { recursive: true, mode: 0o755 });
  const temporary = join(dirname(path), `.memory-${randomBytes(10).toString("hex")}.tmp`);
  const file = await open(temporary, "wx", mode);
  try {
    await file.chmod(mode);
    await file.writeFile(content);
    await file.sync();
    await file.close();
    await rename(temporary, path);
  } finally {
    await file.close();
    await unlink(temporary).catch((error: unknown) => { if (!missing(error)) throw error; });
  }
}

export function frontmatter(content: string): { type?: string; description?: string } {
  const lines = content.split("\n");
  if (lines[0]?.trim() !== "---") return {};
  let type = "";
  let description = "";
  for (const line of lines.slice(1)) {
    if (line.trim() === "---") break;
    const colon = line.indexOf(":");
    if (colon < 0) continue;
    switch (line.slice(0, colon).trim()) {
      case "type": type = line.slice(colon + 1).trim(); break;
      case "description": description = line.slice(colon + 1).trim(); break;
    }
  }
  return { ...(type ? { type } : {}), ...(description ? { description } : {}) };
}

export function promptSection(notebook: string, index: string): string {
  return [
    "# Durable user memory",
    "The memory plugin owns a persistent notebook at `" + notebook + "`. Use memory_list and memory_read to inspect it, and memory_write/memory_delete to maintain it.",
    "The plugin also provides session_memory for workspace project_memory and current-session summary, checkpoint, and notes files. Read project_memory on demand; do not copy it into every prompt.",
    "Save only durable cross-project user preferences, explicit feedback, stable references, and reusable lessons. Never save secrets, raw transcripts, task progress, commit ids, or facts derivable from the current repository.",
    "Each memory belongs in a topic .md file with frontmatter fields name, description, and type (user | feedback | reference | lesson). MEMORY.md is only a one-line-per-topic index. A save or forget operation must update both the topic file and MEMORY.md.",
    "If remembered information conflicts with current evidence, trust current evidence.",
    "\n## MEMORY.md index\n\n" + (index.trim() ? index : "The MEMORY.md index is currently empty."),
  ].join("\n\n");
}
