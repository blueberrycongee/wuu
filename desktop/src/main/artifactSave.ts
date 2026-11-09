import { dialog, type BrowserWindow, type Session } from "electron";
import { open, rename, rm } from "node:fs/promises";
import { createHash, randomUUID } from "node:crypto";
import { basename, dirname, extname, join } from "node:path";

/** Save original bytes only after the user chooses a destination. */
export async function saveArtifactFile(
  parent: BrowserWindow,
  session: Session,
  name: string,
  source: string,
): Promise<void> {
  const url = new URL(source);
  if (!["data:", "https:", "http:", "wuu-file:", "wuu-artifact:"].includes(url.protocol)) {
    throw new Error("Unsupported artifact source");
  }
  // Chromium net.fetch does not support data URLs. Custom file protocols must
  // use the sender's session so their existing path/integrity checks still run.
  const response = await (url.protocol === "data:" ? fetch(source) : session.fetch(source));
  if (!response.ok) throw new Error(`Could not read artifact (${response.status})`);

  const mime = response.headers.get("content-type")?.split(";")[0].toLowerCase();
  const extension = ({
    "image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
    "image/webp": ".webp", "image/svg+xml": ".svg", "image/avif": ".avif",
    "image/bmp": ".bmp", "image/tiff": ".tiff", "image/x-icon": ".ico",
  } as Record<string, string>)[mime ?? ""];
  let suggested = name;
  if (/^(https?|wuu-file|wuu-artifact):/i.test(suggested)) {
    suggested = decodeURIComponent(new URL(suggested).pathname);
  }
  suggested = basename(suggested.replace(/\\/g, "/")).replace(/[<>:"|?*\x00-\x1f]/g, "_");
  if (!suggested || /^\.+$/.test(suggested)) suggested = "image";
  if (extension && !extname(suggested)) suggested += extension;
  const result = await dialog.showSaveDialog(parent, { defaultPath: suggested });
  if (result.canceled || !result.filePath) { await response.body?.cancel(); return; }
  // Download directly into a private sibling; never buffer a cloud artifact or
  // replace the user's existing file before the complete digest is verified.
  const temporary = join(dirname(result.filePath), `.${basename(result.filePath)}.${randomUUID()}.tmp`);
  const reader = response.body?.getReader();
  let file: Awaited<ReturnType<typeof open>> | undefined;
  try {
    file = await open(temporary, "wx", 0o600);
    const hash = createHash("sha256");
    if (reader) while (true) {
      const {done, value} = await reader.read(); if (done) break;
      hash.update(value); await file.writeFile(value);
    }
    const digest = response.headers.get("x-artifact-sha256");
    if (url.protocol === "wuu-artifact:" && (!digest || hash.digest("hex") !== digest)) throw new Error("Artifact contents changed");
    await file.sync(); await file.close(); file = undefined;
    await rename(temporary, result.filePath);
  } finally {
    await reader?.cancel(); await file?.close();
    await rm(temporary, {force:true});
  }
}
