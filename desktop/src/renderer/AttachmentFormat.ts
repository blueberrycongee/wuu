/** Display helpers shared by composer attachment cards and sent-message file cards. */

export function fileNameParts(filename: string): { stem: string; extension: string } {
  const dot = filename.lastIndexOf(".");
  if (dot <= 0 || dot === filename.length - 1) {
    return { stem: filename, extension: "" };
  }
  return { stem: filename.slice(0, dot), extension: filename.slice(dot) };
}

function base64ByteLength(data: string): number {
  const payload = data.includes(",") ? data.slice(data.indexOf(",") + 1) : data;
  const padding = payload.endsWith("==") ? 2 : payload.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((payload.length * 3) / 4) - padding);
}

export function formatFileSize(data: string): string {
  const bytes = base64ByteLength(data);
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const kilobytes = bytes / 1024;
  if (kilobytes < 1024) {
    return `${kilobytes < 10 ? kilobytes.toFixed(1) : Math.round(kilobytes)} KB`;
  }
  const megabytes = kilobytes / 1024;
  return `${megabytes < 10 ? megabytes.toFixed(1) : Math.round(megabytes)} MB`;
}
