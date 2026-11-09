const IMAGE_FILE_PATTERN = /\.(apng|avif|gif|jpe?g|png|svg|webp)(?:[?#].*)?$/i;

export function imageTarget(rawSource: string): string {
  let source = rawSource.trim();
  for (const [open, close] of [["`", "`"], ['"', '"'], ["'", "'"], ["<", ">"]]) {
    if (source.startsWith(open) && source.endsWith(close)) { source = source.slice(open.length, -close.length).trim(); break; }
  }
  if (source.startsWith("<") && source.endsWith(">")) return source.slice(1, -1).trim();
  return source.match(/^(.*?)(?:\s+["'][^"']*["'])$/)?.[1].trim() ?? source;
}

/** Local resources require a host URL capability; web images need no host transport. */
export function resolvePreviewResourceURL(rawSource: string, cwd: string | undefined,
  encodedURL?: (encodedPath: string) => string): string | undefined {
  const source = imageTarget(rawSource);
  if (/^wuu-file:/i.test(source)) {
    try {
      const url = new URL(source);
      const encoded = url.pathname.replace(/^\/+/, "");
      return url.hostname === "local" && encoded ? encodedURL?.(encoded) : undefined;
    } catch { return undefined; }
  }
  if (/^(https?:|data:image\/|blob:)/i.test(source)) return source;
  let path: string | undefined;
  if (source.startsWith("file://")) {
    try { path = decodeURIComponent(new URL(source).pathname); }
    catch { path = source.replace(/^file:\/\//, ""); }
  } else if (source.startsWith("~/")) {
    path = `${cwd?.match(/^\/Users\/[^/]+/)?.[0] ?? ""}/${source.slice(2)}`;
  } else if (source.startsWith("/")) {
    path = source;
  } else if (source.startsWith("./") || source.startsWith("../") || cwd && IMAGE_FILE_PATTERN.test(source)) {
    const stack: string[] = [];
    for (const part of `${cwd ?? "/"}/${source}`.split("/")) {
      if (!part || part === ".") continue;
      if (part === "..") stack.pop(); else stack.push(part);
    }
    path = `/${stack.join("/")}`;
  }
  if (path === undefined) return source;
  if (!encodedURL) return undefined;
  let binary = "";
  for (const byte of new TextEncoder().encode(path)) binary += String.fromCharCode(byte);
  return encodedURL(btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, ""));
}
