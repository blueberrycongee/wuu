import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createDesignServer } from "../src/server.mjs";

const BRIDGE = "/* bridge */ window.__test = true;\n";

describe("design server", () => {
  let dir, server, port, base, prefix;

  before(async () => {
    dir = await mkdtemp(join(tmpdir(), "wuu-design-"));
    await writeFile(join(dir, "index.html"), "<!DOCTYPE html><html><body><h1>Hello</h1></body></html>");
    await mkdir(join(dir, "nested"));
    await writeFile(join(dir, "nested", "page.html"), "<html><body><p>Nested</p></body></html>");
    await mkdir(join(dir, "node_modules", "pkg"), { recursive: true });
    await writeFile(join(dir, "node_modules", "pkg", "skip.html"), "<html></html>");
    await writeFile(join(dir, "style.css"), "body{margin:0}");
    server = createDesignServer({ bridgeSource: BRIDGE });
    port = await server.listen(0);
    const session = await server.open(dir);
    prefix = session.base;
    base = `http://127.0.0.1:${port}${session.base}`;
  });

  after(async () => {
    await server.close();
    await rm(dir, { recursive: true, force: true });
  });

  const get = async (path, init) => fetch(`${base}${path}`, init);

  it("rejects requests without a session token", async () => {
    const root = `http://127.0.0.1:${port}`;
    assert.equal((await fetch(`${root}/index.html`)).status, 404);
    assert.equal((await fetch(`${root}/t/unknown-token/index.html`)).status, 404);
  });

  it("serves pages with the bridge injected once", async () => {
    const res = await get("/index.html");
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type"), /text\/html/);
    const html = await res.text();
    assert.equal(html.split("bridge.js").length - 1, 1);
    assert.ok(html.includes(`src="${prefix}/__design__/bridge.js"`));
    assert.ok(html.indexOf("bridge.js") < html.indexOf("</body>"));
  });

  it("serves the bridge source", async () => {
    const res = await get("/__design__/bridge.js");
    assert.equal(res.status, 200);
    assert.match(await res.text(), /__test = true/);
  });

  it("serves static assets untouched", async () => {
    const res = await get("/style.css");
    assert.equal(res.status, 200);
    assert.equal(await res.text(), "body{margin:0}");
  });

  it("rejects traversal outside the design root", async () => {
    assert.notEqual((await get("/../package.json")).status, 200);
    assert.notEqual((await get("/%2e%2e/%2e%2e/etc/passwd")).status, 200);
  });

  it("lists only real pages, index first", async () => {
    const res = await get("/__design__/pages");
    const { pages } = await res.json();
    assert.deepEqual(pages.map((p) => p.path), ["index.html", "nested/page.html"]);
    assert.ok(pages[0].sha256);
  });

  it("saves and reports conflicts", async () => {
    const { pages } = await (await get("/__design__/pages")).json();
    const index = pages.find((p) => p.path === "index.html");
    const stale = await save("index.html", "<html><body>stale</body></html>", "deadbeef");
    assert.equal(stale.status, "conflict");
    const fresh = await save("index.html", "<!DOCTYPE html><html><body>v2</body></html>", index.sha256);
    assert.equal(fresh.status, "saved");
    assert.equal((await readFile(join(dir, "index.html"), "utf8")).includes("v2"), true);
    const again = await save("index.html", "<html><body>v3</body></html>", index.sha256);
    assert.equal(again.status, "conflict");
  });

  it("rejects saves outside the root and non-html paths", async () => {
    const a = await save("../escape.html", "<html></html>", "");
    assert.notEqual(a.status, "saved");
    const b = await save("style.css", "x", "");
    assert.notEqual(b.status, "saved");
  });

  async function save(path, html, base_sha256) {
    const res = await get("/__design__/save", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ path, html, base_sha256 }),
    });
    return res.json();
  }
});
