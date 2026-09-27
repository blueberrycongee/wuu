const VIDEO_MIME_TYPES = new Map([
  ["mp4", "video/mp4"],
  ["m4v", "video/mp4"],
  ["webm", "video/webm"],
  ["mov", "video/quicktime"],
  ["ogv", "video/ogg"],
]);

// Container recognition does not guarantee that Chromium can decode its tracks.
export function videoMimeType(path: string): string | undefined {
  const extension = /\.([a-z0-9]+)$/i.exec(path)?.[1].toLowerCase();
  return extension ? VIDEO_MIME_TYPES.get(extension) : undefined;
}
