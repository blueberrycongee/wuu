import { statSync } from "node:fs";
import { extname } from "node:path";
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
