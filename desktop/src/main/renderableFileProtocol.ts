import { net, protocol } from "electron";
import { pathToFileURL } from "node:url";
import { renderableResponseHeaders, rangedFileResponse } from "./renderableFileRange";
import { videoMimeType } from "../shared/videoMimeType";
import {
  managedArtifactFileFromURL,
  filePathFromRenderableURL,
  isRenderableHtmlFile,
  isRenderableImageFile,
  isRenderablePdfFile,
  isRenderableVideoFile,
  verifyManagedArtifactFile,
} from "./renderableFileURLs";

export function registerRenderableFileScheme(): void {
  protocol.registerSchemesAsPrivileged(
    ["wuu-file", "wuu-artifact"].map((scheme) => ({
      scheme,
      privileges: {
        standard: true,
        secure: true,
        // PDF.js runs in the renderer and loads large documents incrementally
        // through fetch + byte-range requests.
        supportFetchAPI: true,
        corsEnabled: true,
        stream: true,
      },
    })),
  );
}

export function registerRenderableFileProtocol(wuuHome: string): void {
  protocol.handle("wuu-file", async (request) => {
    const filePath = filePathFromRenderableURL(request.url);
    if (!filePath) {
      return new Response("Not found", { status: 404 });
    }
    if (isRenderablePdfFile(filePath) || isRenderableVideoFile(filePath)) {
      return rangedRenderableResponse(request, filePath, videoMimeType(filePath) ?? "application/pdf");
    }
    if (!isRenderableImageFile(filePath)) {
      return new Response("Not found", { status: 404 });
    }
    return net.fetch(pathToFileURL(filePath).toString());
  });
  protocol.handle("wuu-artifact", async (request) => {
    const artifact = managedArtifactFileFromURL(request.url, wuuHome);
    if (!artifact || !await verifyManagedArtifactFile(artifact)) {
      return new Response("Not found", { status: 404 });
    }
    const filePath = artifact.filePath;
    if (isRenderablePdfFile(filePath) || isRenderableVideoFile(filePath)) {
      return rangedRenderableResponse(request, filePath, videoMimeType(filePath) ?? "application/pdf");
    }
    const response = await net.fetch(pathToFileURL(filePath).toString());
    if (!isRenderableHtmlFile(filePath)) return response;
    const headers = new Headers(response.headers);
    headers.set("Content-Type", "text/html; charset=utf-8");
    headers.set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data:; media-src data: blob:");
    headers.set("X-Content-Type-Options", "nosniff");
    return new Response(response.body, {
      status: response.status,
      headers,
    });
  });
}

async function rangedRenderableResponse(request: Request, filePath: string, mimeType: string): Promise<Response> {
  const ranged = rangedFileResponse(request, filePath, mimeType);
  if (ranged) return ranged;
  const response = await net.fetch(pathToFileURL(filePath).toString());
  return new Response(response.body, {
    status: response.status,
    headers: renderableResponseHeaders(mimeType, response.headers),
  });
}
