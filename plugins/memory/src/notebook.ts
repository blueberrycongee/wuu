import { randomBytes } from "node:crypto";
import { mkdir, open, readFile, rename, unlink } from "node:fs/promises";
import { dirname, join } from "node:path";

export const INDEX_FILE = "MEMORY.md";
export const MAX_MEMORY_BYTES = 256 * 1024;
const MAX_INDEX_LINES = 200;
const MAX_INDEX_BYTES = 25_000;
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
  let content = raw.split("\n").map((line) => unsafeLine(line) ? "[memory line removed: security]" : line).join("\n").trim();
  if (!content) return "";
  const lines = content.split("\n");
  const truncated = lines.length > MAX_INDEX_LINES || Buffer.byteLength(content) > MAX_INDEX_BYTES;
  if (lines.length > MAX_INDEX_LINES) content = lines.slice(0, MAX_INDEX_LINES).join("\n");
  const bytes = Buffer.from(content);
  if (bytes.length > MAX_INDEX_BYTES) {
    const newline = bytes.subarray(0, MAX_INDEX_BYTES).lastIndexOf(10);
    let cut = newline > 0 ? newline : MAX_INDEX_BYTES;
    if (newline <= 0) while (cut > 0 && (bytes[cut]! & 0xc0) === 0x80) cut--;
    content = bytes.subarray(0, cut).toString();
  }
  if (truncated) content += "\n\n> WARNING: MEMORY.md was truncated before prompt injection. Keep index entries short and move detail into topic files.";
  return content;
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
