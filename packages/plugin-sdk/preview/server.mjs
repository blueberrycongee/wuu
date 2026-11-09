import { build } from 'esbuild';
import { checkSources } from './check.mjs';
import { createServer } from 'node:http';
import { createHash } from 'node:crypto';
import { readFile, realpath } from 'node:fs/promises';
import { watch } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = fileURLToPath(new URL('../', import.meta.url));

/** Serve trusted author files on loopback. No plugin install or build step. */
export async function startPreview({ sources, port = 0, state } = {}) {
  if (!sources?.length) throw new Error('Provide at least one .tsx or .ts source file.');
  sources = await Promise.all(sources.map(async (source) => {
    const file = await realpath(resolve(source));
    if (!/\.tsx?$/.test(file)) throw new Error(`Preview source must be .ts or .tsx: ${file}`);
    return file;
  }));
  const initialState = state ? JSON.parse(await readFile(resolve(state), 'utf8')) : undefined;
  const browser = await build({
    entryPoints: [resolve(packageRoot, 'preview/client.tsx')], bundle: true, write: false,
    format: 'esm', platform: 'browser', sourcemap: 'inline', jsx: 'transform',
    define: { 'process.env.NODE_ENV': '"development"' }, logLevel: 'silent',
  });
  const clients = new Set();
  const modules = new Map();
  let status = { revision: null, diagnostics: [], sources };
  let attempted;
  let closed = false;
  let activeBuild;
  let pending = false;
  const publish = () => { for (const client of clients) client.write(`data: ${JSON.stringify(status)}\n\n`); };
  const rebuild = async () => {
    if (closed) return;
    if (activeBuild) { pending = true; return activeBuild; }
    activeBuild = (async () => {
      let contents;
      try { contents = await Promise.all(sources.map((file) => readFile(file, 'utf8'))); }
      catch (error) {
        status = { ...status, diagnostics: [{ message: error.message, file: error.path }] };
        publish();
        return;
      }
      const fingerprint = createHash('sha256').update(JSON.stringify(contents)).digest('hex');
      if (fingerprint === attempted) return;
      attempted = fingerprint;
      const diagnostics = checkSources(sources, contents);
      if (diagnostics.length) { status = { ...status, diagnostics }; publish(); return; }
      try {
        const result = await build({
          stdin: { contents: sources.map((_, i) => `import * as module${i} from 'author:${i}';`).join('\n') +
            `\nexport default [${sources.map((_, i) => `module${i}`).join(',')}];`, resolveDir: packageRoot },
          bundle: true, write: false, format: 'esm', platform: 'browser', sourcemap: 'inline',
          jsx: 'transform', logLevel: 'silent',
          plugins: [{ name: 'public-author-contract', setup(builder) {
            builder.onResolve({ filter: /^author:/ }, ({ path }) => ({ path: sources[Number(path.slice(7))], namespace: 'author' }));
            builder.onLoad({ filter: /.*/, namespace: 'author' }, ({ path }) => ({
              contents: contents[sources.indexOf(path)], loader: path.endsWith('.tsx') ? 'tsx' : 'ts', resolveDir: dirname(path),
            }));
            builder.onResolve({ filter: /^@wuu\/plugin-sdk(?:\/ui)?$/ }, ({ path }) => ({
              path: resolve(packageRoot, path.endsWith('/ui') ? 'dist/ui.js' : 'dist/index.js'),
            }));
            builder.onResolve({ filter: /.*/, namespace: 'author' }, ({ path }) => ({ errors: [{
              text: `Single-file preview imports only @wuu/plugin-sdk. Use api.react for the host React instance (received ${JSON.stringify(path)}).`,
            }] }));
          } }],
        });
        modules.set(fingerprint, result.outputFiles[0].text);
        while (modules.size > 2) modules.delete(modules.keys().next().value);
        status = { ...status, revision: fingerprint, diagnostics: [] };
      } catch (error) {
        status = { ...status, diagnostics: (error.errors ?? [{ text: error.message }]).map(({ text, location }) => ({
          message: text, file: location?.file?.replace(/^author:/, ''), line: location?.line,
          column: location ? location.column + 1 : undefined, lineText: location?.lineText,
        })) };
      }
      publish();
    })().finally(() => { activeBuild = undefined; if (pending) { pending = false; void rebuild(); } });
    return activeBuild;
  };
  await rebuild();
  let url;
  const http = createServer((request, response) => {
    if (request.headers.host !== new URL(url).host || (request.headers.origin && request.headers.origin !== url)) {
      response.writeHead(403).end('Local preview origin required'); return;
    }
    response.setHeader('Cache-Control', 'no-store');
    response.setHeader('X-Content-Type-Options', 'nosniff');
    if (request.method !== 'GET') { response.writeHead(405).end(); return; }
    const path = new URL(request.url, url).pathname;
    if (path === '/events') {
      response.writeHead(200, { 'Content-Type': 'text/event-stream' });
      response.write(`data: ${JSON.stringify(status)}\n\n`);
      clients.add(response); request.on('close', () => clients.delete(response)); return;
    }
    if (path === '/status' || path === '/state') {
      response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(path === '/status' ? status : initialState ?? null)); return;
    }
    if (path === '/client.js') {
      response.writeHead(200, { 'Content-Type': 'text/javascript' }).end(browser.outputFiles[0].text); return;
    }
    const revision = path.match(/^\/module\/([a-f0-9]+)\.mjs$/)?.[1];
    if (revision && modules.has(revision)) {
      response.writeHead(200, { 'Content-Type': 'text/javascript' }).end(modules.get(revision)); return;
    }
    if (path === '/') {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }).end(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Wuu component preview</title></head><body><div id="preview"></div><script type="module" src="/client.js"></script></body></html>`); return;
    }
    response.writeHead(404).end('Not found');
  });
  await new Promise((accept, reject) => { http.once('error', reject); http.listen(port, '127.0.0.1', accept); });
  url = `http://127.0.0.1:${http.address().port}`;
  let debounce;
  const schedule = () => { clearTimeout(debounce); debounce = setTimeout(() => void rebuild(), 60); };
  const watchers = [...new Set(sources.map(dirname))].map((directory) => {
    const watcher = watch(directory, (_event, name) => { if (!name || sources.includes(resolve(directory, String(name)))) schedule(); });
    watcher.on('error', schedule); return watcher;
  });
  // Reconcile missed filesystem notifications, including replaced directories.
  const interval = setInterval(() => void rebuild(), 1000);
  return {
    url,
    async close() {
      closed = true; clearTimeout(debounce); clearInterval(interval);
      for (const watcher of watchers) watcher.close();
      for (const client of clients) client.end();
      await activeBuild;
      await new Promise((accept) => http.close(accept));
    },
  };
}
