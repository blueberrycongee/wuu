import { createServer } from "node:http";
import { createHash, randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import { extname, join, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

const MIME_TYPES = {
  ".html": "text/html; charset=utf-8",
  ".htm": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".map": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".avif": "image/avif",
  ".ico": "image/x-icon",
  ".woff": "font/woff",
  ".woff2": "font/woff2",
  ".ttf": "font/ttf",
  ".otf": "font/otf",
  ".mp4": "video/mp4",
  ".webm": "video/webm",
  ".mp3": "audio/mpeg",
  ".wav": "audio/wav",
  ".pdf": "application/pdf",
  ".txt": "text/plain; charset=utf-8",
  ".md": "text/markdown; charset=utf-8",
  ".xml": "application/xml; charset=utf-8",
  ".webpmanifest": "application/manifest+json",
};

const MAX_FILE_BYTES = 32 * 1024 * 1024;
const MAX_SAVE_BYTES = 8 * 1024 * 1024;
const MAX_PAGES = 200;
const MAX_DEPTH = 5;
const SKIP_DIRS = new Set(["node_modules", "dist", "build", "out", "coverage", "__pycache__"]);

function sha256(text) {
  return createHash("sha256").update(text).digest("hex");
}

function send(res, status, body, headers = {}) {
  const data = typeof body === "string" || Buffer.isBuffer(body) ? body : JSON.stringify(body);
  res.writeHead(status, {
    "Content-Type": typeof body === "object" && !Buffer.isBuffer(body) ? "application/json; charset=utf-8" : headers["Content-Type"] ?? "text/plain; charset=utf-8",
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
    ...headers,
  });
  res.end(data);
}

async function readBody(req, limit) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > limit) throw new Error("request body too large");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

/** Lists HTML pages under root; skips hidden and dependency directories. */
async function listPages(root) {
  const pages = [];
  async function walk(dir, depth) {
    if (pages.length >= MAX_PAGES || depth > MAX_DEPTH) return;
    let entries;
    try {
      entries = await fs.readdir(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (pages.length >= MAX_PAGES) return;
      if (entry.name.startsWith(".") || SKIP_DIRS.has(entry.name)) continue;
      const full = join(dir, entry.name);
      if (entry.isDirectory()) {
        await walk(full, depth + 1);
      } else if (entry.isFile() && /\.html?$/i.test(entry.name)) {
        try {
          const stat = await fs.stat(full);
          if (stat.size > MAX_FILE_BYTES) continue;
          const text = await fs.readFile(full, "utf8");
          pages.push({
            path: full.slice(root.length + 1).split(sep).join("/"),
            bytes: stat.size,
            mtime_ms: Math.round(stat.mtimeMs),
            sha256: sha256(text),
          });
        } catch { /* unreadable files are skipped, not fatal */ }
      }
    }
  }
  await walk(root, 0);
  pages.sort((a, b) => (a.path === "index.html" ? -1 : b.path === "index.html" ? 1 : a.path.localeCompare(b.path)));
  return pages;
}

async function resolveInside(realRoot, requestPath) {
  const decoded = decodeURIComponent(requestPath);
  const target = resolve(realRoot, "." + sep + decoded.split("/").join(sep));
  const real = await fs.realpath(target);
  if (real !== realRoot && !real.startsWith(realRoot + sep)) throw new Error("path escapes the design root");
  return real;
}

/**
 * Loopback file server for design pages. Every rooted session gets a random
 * token that prefixes all URLs so relative navigations stay authorized and
 * unrelated local pages cannot reach the files.
 */
export function createDesignServer({ bridgeSource, tokenBytes = 16 } = {}) {
  const roots = new Map(); // token -> { root, realRoot }
  const server = createServer(async (req, res) => {
    try {
      const url = new URL(req.url ?? "/", "http://127.0.0.1");
      const parts = url.pathname.split("/").filter(Boolean);
      if (parts[0] !== "t" || parts.length < 2) return send(res, 404, { error: "unknown route" });
      const session = roots.get(parts[1]);
      if (!session) return send(res, 404, { error: "unknown design session" });
      const rest = parts.slice(2).join("/");
      const prefixed = (p) => `/t/${parts[1]}/${p}`;

      if (rest === "__design__/bridge.js") {
        return send(res, 200, bridgeSource ?? "", { "Content-Type": "text/javascript; charset=utf-8" });
      }
      if (rest.startsWith("__design__/")) {
        // The random token in the path authorizes these calls; cross-origin
        // reads stay open on the API namespace so the desktop view and the
        // standalone component preview can both reach it.
        res.setHeader("Access-Control-Allow-Origin", "*");
        res.setHeader("Access-Control-Allow-Headers", "content-type");
        if (req.method === "OPTIONS") return send(res, 204, "");
        if (rest === "__design__/pages" && req.method === "GET") {
          return send(res, 200, { pages: await listPages(session.realRoot) });
        }
        if (rest === "__design__/save") {
          if (req.method !== "POST") return send(res, 405, { error: "method not allowed" });
          const input = JSON.parse(await readBody(req, MAX_SAVE_BYTES + 4096));
          const result = await savePage(session.realRoot, input);
          return send(res, 200, result);
        }
        return send(res, 404, { error: "unknown route" });
      }
      if (rest.startsWith("__design__")) return send(res, 404, { error: "unknown route" });
      if (req.method !== "GET" && req.method !== "HEAD") return send(res, 405, { error: "method not allowed" });

      let requestPath = rest || "index.html";
      let real = await resolveInside(session.realRoot, requestPath);
      const stat = await fs.stat(real).catch(() => undefined);
      if (!stat) return send(res, 404, { error: "not found" });
      if (stat.isDirectory()) {
        const index = join(real, "index.html");
        try {
          await fs.access(index);
          return send(res, 302, "", { Location: prefixed(`${rest === "" ? "" : rest + "/"}index.html`) });
        } catch {
          return send(res, 404, { error: "directory listings are unavailable" });
        }
      }
      if (stat.size > MAX_FILE_BYTES) return send(res, 413, { error: "file too large" });
      const type = MIME_TYPES[extname(real).toLowerCase()] ?? "application/octet-stream";
      if (type.startsWith("text/html")) {
        let html = await fs.readFile(real, "utf8");
        const tag = `<script src="${prefixed("__design__/bridge.js")}" data-wuu-design-bridge=""></script>`;
        const bodyClose = html.lastIndexOf("</body>");
        html = bodyClose === -1 ? html + tag : html.slice(0, bodyClose) + tag + html.slice(bodyClose);
        return send(res, 200, html, { "Content-Type": type });
      }
      const data = await fs.readFile(real);
      return send(res, 200, data, { "Content-Type": type });
    } catch (error) {
      return send(res, error?.code === "ENOENT" ? 404 : 400, { error: String(error?.message ?? error) });
    }
  });

  async function savePage(realRoot, input) {
    const rel = typeof input?.path === "string" ? input.path : "";
    const html = typeof input?.html === "string" ? input.html : "";
    if (!rel || !html) throw new Error("design.save requires path and html");
    if (!/\.html?$/i.test(rel)) throw new Error("only HTML pages can be saved");
    if (Buffer.byteLength(html, "utf8") > MAX_SAVE_BYTES) throw new Error("page exceeds the save size limit");
    const real = await resolveInside(realRoot, rel);
    const current = await fs.readFile(real, "utf8").catch(() => undefined);
    if (current === undefined) return { status: "missing", path: rel };
    const currentSha = sha256(current);
    if (typeof input.base_sha256 === "string" && input.base_sha256 && input.base_sha256 !== currentSha) {
      const stat = await fs.stat(real);
      return { status: "conflict", path: rel, sha256: currentSha, mtime_ms: Math.round(stat.mtimeMs) };
    }
    const temp = `${real}.${process.pid}.${randomBytes(4).toString("hex")}.tmp`;
    await fs.writeFile(temp, html, "utf8");
    await fs.rename(temp, real);
    const stat = await fs.stat(real);
    return { status: "saved", path: rel, sha256: sha256(html), bytes: stat.size, mtime_ms: Math.round(stat.mtimeMs) };
  }

  let listening;
  return {
    async listen(port = 0) {
      listening ??= new Promise((resolveListen, rejectListen) => {
        server.once("error", rejectListen);
        server.listen(port, "127.0.0.1", () => resolveListen(server.address().port));
      });
      return listening;
    },
    /** Registers a design directory and returns its authorized URL prefix. */
    async open(root) {
      if (typeof root !== "string" || !root.trim()) throw new Error("design.open requires a root path");
      const realRoot = await fs.realpath(root);
      const stat = await fs.stat(realRoot);
      if (!stat.isDirectory()) throw new Error("design root is not a directory");
      for (const [token, existing] of roots) {
        if (existing.realRoot === realRoot) return { token, base: `/t/${token}` };
      }
      const token = randomBytes(tokenBytes).toString("hex");
      roots.set(token, { root, realRoot });
      return { token, base: `/t/${token}` };
    },
    listPages,
    save: savePage,
    async close() {
      server.closeAllConnections?.();
      await new Promise((resolveClose) => server.close(resolveClose));
      roots.clear();
    },
  };
}

// Bundling inlines this module into runtime.mjs; only run the CLI when the
// invoked file is literally server.mjs.
if (process.argv[1]?.endsWith("server.mjs") && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const args = process.argv.slice(2);
  let root = "", port = 0, bridge = "";
  for (let i = 0; i < args.length; i++) {
    if (args[i] === "--root") root = args[++i];
    else if (args[i] === "--port") port = Number(args[++i]);
    else if (args[i] === "--bridge") bridge = args[++i];
  }
  if (!root) {
    console.error("usage: server.mjs --root <dir> [--port 0] [--bridge bridge.js]");
    process.exit(1);
  }
  const bridgeSource = bridge ? await fs.readFile(bridge, "utf8") : "";
  const design = createDesignServer({ bridgeSource });
  const actualPort = await design.listen(port);
  const { base } = await design.open(resolve(root));
  console.log(`http://127.0.0.1:${actualPort}${base}/`);
}
