import { net, protocol, type CustomScheme } from "electron";
import { pathToFileURL } from "node:url";
import { renderableResponseHeaders, rangedFileResponse } from "./renderableFileRange";
import type { ArtifactSources } from "./artifactSources";
import { videoMimeType } from "../shared/videoMimeType";
import {
  filePathFromRenderableURL,
  isRenderableImageFile,
  isRenderablePdfFile,
  isRenderableVideoFile,
} from "./renderableFileURLs";

export const renderableFileSchemes: CustomScheme[] =
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
  }));

export function registerRenderableFileScheme(): void {
  protocol.registerSchemesAsPrivileged(renderableFileSchemes);
}

export function registerRenderableFileProtocol(artifacts: ArtifactSources): void {
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
  protocol.handle("wuu-artifact", request => artifacts.response(request));
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
