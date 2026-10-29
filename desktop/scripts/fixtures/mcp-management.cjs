// Real Electron/preload/app-server MCP journey with a loopback OAuth provider.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createServer } = require("node:http");
const { createHash } = require("node:crypto");
const { shell, dialog, Menu } = require("electron");
const clickReady = require("./mcp-click.cjs");

module.exports = async function prepareMCPFixture({ fixture, output }) {
  let endpoint, authorizationURL;
  let failedInitializations = 0;
  let pauseToolList = null;
  const manualRedirect = "https://callback.example.test/mcp";
  const codes = new Map();
  const evidence = { registration: 0, exchanges: 0, authenticatedLists: 0, connectorRequests: 0, checks: [] };
  const peer = createServer(async (request, response) => {
    const url = new URL(request.url, endpoint);
    if (url.pathname === "/connector-mcp") evidence.connectorRequests++;
    assert.notEqual(url.pathname, "/setup-required", "A setup-only connector must never contact its endpoint");
    console.log(`MCP fixture: ${request.method} ${url.pathname}`);
    response.setHeader("Content-Type", "application/json");
    const json = value => response.end(JSON.stringify(value));
    let body = "";
    for await (const chunk of request) body += chunk;
    if (url.pathname.startsWith("/.well-known/oauth-protected-resource")) {
      json({ resource: `${endpoint}/${url.pathname.includes("connector") ? "connector-mcp" : "mcp"}`, authorization_servers: [endpoint], scopes_supported: ["tools:read"] }); return;
    }
    if (url.pathname === "/.well-known/oauth-authorization-server") {
      json({ issuer: endpoint, authorization_endpoint: `${endpoint}/authorize`, token_endpoint: `${endpoint}/token`, registration_endpoint: `${endpoint}/register`, code_challenge_methods_supported: ["S256"] }); return;
    }
    if (url.pathname === "/register") {
      const client = JSON.parse(body);
      assert.ok(client.redirect_uris[0].startsWith("http://127.0.0.1:") || client.redirect_uris[0] === manualRedirect);
      evidence.registration++;
      json({ client_id: "fixture-client", token_endpoint_auth_method: "none" }); return;
    }
    if (url.pathname === "/authorize") {
      assert.equal(url.searchParams.get("code_challenge_method"), "S256");
      assert.ok([`${endpoint}/mcp`, `${endpoint}/connector-mcp`].includes(url.searchParams.get("resource")));
      const code = `code-${codes.size}`;
      codes.set(code, url.searchParams);
      const callback = new URL(url.searchParams.get("redirect_uri"));
      callback.searchParams.set("state", url.searchParams.get("state"));
      callback.searchParams.set("code", code);
      response.writeHead(302, { Location: callback.href }).end(); return;
    }
    if (url.pathname === "/token") {
      const form = new URLSearchParams(body);
      const auth = codes.get(form.get("code"));
      assert.ok(auth, "Only issued authorization codes can be exchanged");
      assert.equal(createHash("sha256").update(form.get("code_verifier")).digest("base64url"), auth.get("code_challenge"));
      assert.equal(form.get("redirect_uri"), auth.get("redirect_uri"));
      assert.equal(form.get("resource"), auth.get("resource"));
      evidence.exchanges++;
      json({ access_token: "synthetic-mcp-token", token_type: "Bearer", expires_in: 3600, scope: "tools:read" }); return;
    }
    if (request.headers.authorization !== "Bearer synthetic-mcp-token") {
      response.writeHead(401, { "WWW-Authenticate": `Bearer resource_metadata="${endpoint}/.well-known/oauth-protected-resource${url.pathname === "/connector-mcp" ? "/connector" : ""}"` }).end(); return;
    }
    if (request.method !== "POST") { response.writeHead(request.method === "DELETE" ? 204 : 405).end(); return; }
    const rpc = JSON.parse(body);
    if (rpc.method === "initialize" && failedInitializations > 0) {
      failedInitializations--;
      response.writeHead(503).end(); return;
    }
    const reply = result => json({ jsonrpc: "2.0", id: rpc.id, result });
    if (rpc.method === "initialize") { response.setHeader("Mcp-Session-Id", "fixture-session"); reply({ protocolVersion: "2025-03-26", capabilities: { tools: {} }, serverInfo: { name: "fixture", version: "1" } }); }
    else if (rpc.method === "tools/list") { if (pauseToolList) await pauseToolList; evidence.authenticatedLists++; reply({ tools: [{ name: "echo", description: "Echo fixture", inputSchema: { type: "object", properties: {} } }] }); }
    else if (rpc.method === "tools/call") reply({ content: [{ type: "text", text: "fixture-ok" }] });
    else if (rpc.id !== undefined) json({ jsonrpc: "2.0", id: rpc.id, error: { code: -32601, message: "unsupported" } });
    else response.writeHead(202).end();
  });
  await new Promise(resolve => peer.listen(0, "127.0.0.1", resolve));
  endpoint = `http://127.0.0.1:${peer.address().port}`;
  const originalOpen = shell.openExternal;
  shell.openExternal = async url => { authorizationURL = url; };
  const pluginDir = path.join(fixture, "mcp-plugin");
  fs.mkdirSync(pluginDir);
  fs.writeFileSync(path.join(pluginDir, "plugin.json"), JSON.stringify({ schema_version: 1, id: "mcp-journey", name: "MCP Journey", mcp_servers: {
    docs: { url: `${endpoint}/mcp` },
    account: { url: `${endpoint}/connector-mcp`, enabled: false, connector: { title: "Fixture workspace", description: "A user-installed connector, not a bundled product ID.", documentation_url: "https://example.test/connector" } },
    gated: { url: `${endpoint}/setup-required`, enabled: false, connector: { title: "Registration required", setup_required: "Register your client before this service can be connected.", documentation_url: "https://example.test/setup" } },
  } }));

  return {
    async run({ evaluate, until, snapshot, capture, win }) {
      await evaluate(() => {
        window.__mcpFixtureHandoff = [];
        window.addEventListener("wuu:mcp-connector-handoff", event => {
          if (window.__mcpFixtureHandoff.length < 64) window.__mcpFixtureHandoff.push(event.detail);
        });
      });
      const clickElement = selector => until(clickReady, { selector });
      const click = async ([selector, label], keyboard = false) => {
        const menuAction = selector.startsWith('[data-mcp=') && ["刷新", "移除 OAuth 登录", "编辑", "移除"].includes(label);
        if (menuAction) {
          await until(selector => {
            const button = document.querySelector(`${selector} [aria-haspopup="menu"]`);
            return button && !button.disabled;
          }, selector);
          const originalPopup = Menu.prototype.popup;
          let complete;
          const selected = new Promise(resolve => { complete = resolve; });
          try {
            Menu.prototype.popup = function(options) {
              const item = this.items.find(candidate => candidate.label === label);
              assert.ok(item && item.enabled, `Missing menu item ${label}`);
              item.click();
              options.callback();
              complete();
            };
            if (keyboard) {
              win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Space" });
              win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Space" });
            } else {
              await clickElement(`${selector} [aria-haspopup="menu"]`);
            }
            await selected;
            await until(() => !document.querySelector('[data-mcp] [aria-expanded="true"]'));
          } finally { Menu.prototype.popup = originalPopup; }
          return;
        }
        console.log(`MCP journey: ${label}`);
        await until(clickReady, { selector: `${selector} button`, label });
      };
      const input = async (selector, value) => evaluate(([selector, value]) => {
        const element = document.querySelector(selector);
        const prototype = element.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
        Object.getOwnPropertyDescriptor(prototype, "value").set.call(element, value);
        element.dispatchEvent(new Event("input", { bubbles: true }));
      }, [selector, value]);
      const row = '[data-mcp="journey-docs"]';
      await clickElement('[data-ribbon-id="catalog"]');
      await clickElement('[data-tab="plugins"]');
      await until(() => document.querySelector('[data-testid="mcp-services"]'));
      await until(() => document.querySelector('[data-connector="plugin.account-connectors.notion"] [data-connector-action="connect"]'));
      assert.equal((await evaluate(() => window.wuu.getMCPAuthStatus("plugin.account-connectors.notion"))).authenticated, false, "The explicitly selected bundled connector stays lazy and locally unauthenticated");
      await capture("mcp-empty-light");
      await click(['[data-testid="mcp-services"]', "添加 MCP"]);
      await until(() => document.querySelector(".mcp-editor"));
      await input('.mcp-editor input:not([type])', "journey-docs");
      await input('.mcp-editor input[type="url"]', `${endpoint}/mcp`);
      await capture("mcp-add-url");
      await click([".mcp-editor", "保存"]);
      const authRow = await snapshot(() => {
        const row = document.querySelector('[data-mcp="journey-docs"]');
        return !document.querySelector(".mcp-editor") && row?.textContent.includes("需要认证") ? { text: row.textContent } : false;
      });
      assert.equal(authRow.text.includes("HTTP 401"), false, "Authentication diagnostics stay out of the collapsed service row");
      await capture("mcp-auth-required");
      evidence.checks.push("URL definition added and activated through the catalog");

      await click([row, "OAuth 登录"]);
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      assert.ok(authorizationURL);
      const callback = new URL(new URL(authorizationURL).searchParams.get("redirect_uri"));
      callback.searchParams.set("state", "wrong-state"); callback.searchParams.set("code", "wrong-code");
      assert.equal((await fetch(callback)).status, 400);
      assert.equal(evidence.exchanges, 0);
      await fetch(authorizationURL);
      await until(() => !document.querySelector('[role="dialog"]') && document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("1 个工具"));
      assert.equal(evidence.exchanges, 1);
      assert.ok(evidence.authenticatedLists > 0);
      await capture("mcp-connected-light");
      win.focus();
      win.webContents.focus();
      await evaluate(selector => document.querySelector(`${selector} .mcp-service-actions > button:not([aria-haspopup])`).focus(), row);
      win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
      win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
      await until(() => document.activeElement?.getAttribute("aria-haspopup") === "menu");
      await capture("mcp-keyboard-focus");
      let releaseToolList;
      pauseToolList = new Promise(resolve => { releaseToolList = resolve; });
      const listsBeforeRefresh = evidence.authenticatedLists;
      await click([row, "刷新"], true);
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.getAttribute("aria-busy") === "true");
      assert.equal(await evaluate(selector => [...document.querySelectorAll(`${selector} .mcp-service-actions button`)].every(button => button.disabled), row), true);
      await capture("mcp-refresh-busy");
      releaseToolList();
      pauseToolList = null;
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.getAttribute("aria-busy") === "false");
      assert.ok(evidence.authenticatedLists > listsBeforeRefresh, "Refresh must rediscover synthetic tools");
      await click([row, "断开"]);
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("已断开"));
      await capture("mcp-disconnected");
      failedInitializations = 1;
      await click([row, "连接"]);
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("连接失败"));
      await capture("mcp-connection-error");
      await click([row, "连接"]);
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("1 个工具"));
      evidence.checks.push("Keyboard opens management menu; refresh busy state, disconnect, failed connection and retry use the synthetic service");
      evidence.checks.push("DCR, PKCE, wrong-state rejection, callback exchange and automatic tool discovery");

      await click([row, "移除 OAuth 登录"]);
      await until(() => document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("OAuth 登录"));
      await click([row, "OAuth 登录"]);
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      const cancelledURL = authorizationURL;
      await click(['[role="dialog"]', "取消登录"]);
      await until(() => !document.querySelector('[role="dialog"]'));
      await assert.rejects(fetch(cancelledURL));
      assert.equal((await evaluate(() => window.wuu.getMCPAuthStatus("journey-docs"))).authenticated, false);
      evidence.checks.push("Cancellation closes the listener and leaves credentials unauthenticated");

      await click([row, "编辑"]);
      await until(() => document.querySelector(".mcp-editor"));
      await evaluate(() => document.querySelector('.mcp-editor details').open = true);
      await input('.mcp-editor textarea', JSON.stringify({ headers: { "X-Fixture": "preserved" }, oauth: { scopes: ["tools"], redirect_uri: manualRedirect }, transport: "http", tool_timeout_ms: 45000 }));
      await click([".mcp-editor", "保存"]);
      await until(() => !document.querySelector(".mcp-editor"));
      const definition = await evaluate(() => window.wuu.readMCPDefinition("journey-docs"));
      assert.equal(definition.config.headers["X-Fixture"], "preserved");
      assert.deepEqual(definition.config.oauth.scopes, ["tools"]);
      await evaluate(def => window.wuu.configureMCPServers({ servers: { "journey-docs": def.config }, revision: def.revision }), definition);
      // Use a genuinely stale revision from before a content change.
      const stale = await evaluate(() => window.wuu.readMCPDefinition("journey-docs"));
      await evaluate(def => window.wuu.configureMCPServers({ servers: { "journey-docs": { ...def.config, tool_timeout_ms: 46000 } }, revision: def.revision }), stale);
      assert.equal(await evaluate(async def => { try { await window.wuu.configureMCPServers({ servers: { "journey-docs": null }, revision: def.revision }); return false; } catch { return true; } }, stale), true);
      evidence.checks.push("Advanced settings persist and stale edits cannot remove a changed definition");

      await click([row, "OAuth 登录"]);
      await until(() => document.querySelector('[role="dialog"] input'));
      const authorization = await fetch(authorizationURL, { redirect: "manual" });
      assert.equal(authorization.status, 302);
      await input('[role="dialog"] input', authorization.headers.get("location"));
      failedInitializations = 1;
      await click(['[role="dialog"]', "完成 OAuth 登录"]);
      await until(() => document.querySelector('[role="dialog"] [role="alert"]'));
      assert.equal(evidence.exchanges, 2);
      assert.equal(await evaluate(() => document.querySelector('[role="dialog"] [role="alert"]')?.textContent.includes("HTTP 503")), false, "Login recovery presents a concise failure instead of raw transport text");
      await clickElement('[role="dialog"] button[aria-expanded="false"]');
      assert.equal(await evaluate(() => document.querySelector('[role="dialog"] pre')?.textContent.includes("503")), true, "The original connection failure remains available on request");
      assert.equal(await evaluate(() => document.querySelector('[data-mcp="journey-docs"] pre') === null), true, "The active login dialog owns the diagnostic without a duplicate in its service row");
      await capture("mcp-manual-reconnect");
      await click(['[role="dialog"]', "连接"]);
      await until(() => !document.querySelector('[role="dialog"]') && document.querySelector('[data-mcp="journey-docs"]')?.textContent.includes("1 个工具"));
      assert.equal(evidence.exchanges, 2, "Retrying connection must not exchange a consumed authorization code");
      await click([row, "移除 OAuth 登录"]);
      evidence.checks.push("Configured external callbacks use manual authorization; connection retries retain the completed login");

      await click(['[data-testid="mcp-services"]', "添加 MCP"]);
      await until(() => document.querySelector(".mcp-editor"));
      await clickElement('.mcp-editor [aria-label="连接方式"]');
      await until(clickReady, { selector: '[role="menuitemradio"]', label: "导入 JSON", contains: true });
      await input('.mcp-editor textarea', JSON.stringify({ mcpServers: { "journey-local": { type: "stdio", command: process.execPath, args: ["--version"], enabled: false, env: { FIXTURE: "one" } } } }));
      await click([".mcp-editor", "导入"]);
      await until(() => !document.querySelector(".mcp-editor") && document.querySelector('[data-mcp="journey-local"]'));
      const local = await evaluate(() => window.wuu.readMCPDefinition("journey-local"));
      assert.equal(local.config.env.FIXTURE, "one");
      assert.equal(local.config.enabled, false);
      assert.deepEqual(local.config.args, ["--version"]);
      evidence.checks.push("Common mcpServers JSON imports local commands and preserves environment and startup state");

      await evaluate(() => window.wuu.setThemePreference("dark"));
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "20px"));
      win.setSize(800, 900);
      await capture("mcp-dark-narrow-large");
      await click([row, "编辑"]);
      await until(() => document.querySelector(".mcp-editor"));
      await capture("mcp-edit-dark-narrow-large");
      await click([".mcp-editor", "取消"]);
      await evaluate(() => window.wuu.setThemePreference("light"));
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "15px"));
      win.setSize(1280, 900);

      const originalPicker = dialog.showOpenDialog;
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [pluginDir] });
      let installed;
      try { installed = await evaluate(() => window.wuu.installPluginPackage()); }
      finally { dialog.showOpenDialog = originalPicker; }
      // Installation grants trust, not membership in this fixture's explicit
      // composition. Select the installed package through disposable native
      // configuration, then use the public catalog refresh to apply it.
      const configPath = path.join(fixture, "wuu-home", "config.json");
      const selectedConfig = JSON.parse(fs.readFileSync(configPath, "utf8"));
      selectedConfig.composition.patches.push({ modules: { "mcp-journey": true } });
      fs.writeFileSync(configPath, JSON.stringify(selectedConfig));
      installed = await evaluate(() => window.wuu.refreshExtensionCatalog());
      const record = installed.extension_inventory.find(item => item.provenance.plugin_id === "mcp-journey" && item.kind === "plugin");
      assert.ok(record);
      const connectorName = "plugin.mcp-journey.account";
      const connectorRow = '[data-connector="plugin.mcp-journey.account"]';
      const gatedRow = '[data-connector="plugin.mcp-journey.gated"]';
      await until(selector => document.querySelector(`${selector} [data-connector-action="connect"]`), connectorRow);
      assert.equal(evidence.connectorRequests, 0, "A declared lazy connector must not contact the provider before Connect");
      assert.equal(await evaluate(selector => document.querySelector(`${selector} [data-connector-action="connect"]`) === null, gatedRow), true);
      await capture("connector-picker-light");
      const connectorAction = async action => {
        const selector = `${connectorRow} [data-connector-action="${action}"]`;
        // A graph publication can replace this row between separate IPC calls.
        // Check and dispatch once in the same renderer turn, without a sleep.
        await clickElement(selector);
      };
      const registrationsBefore = evidence.registration;
      authorizationURL = undefined;
      await connectorAction("connect");
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      assert.ok(authorizationURL);
      assert.equal(evidence.registration, registrationsBefore + 1);
      const cancelledConnectorURL = authorizationURL;
      await click(['[role="dialog"]', "取消登录"]);
      await until(() => !document.querySelector('[role="dialog"]'));
      await assert.rejects(fetch(cancelledConnectorURL));
      assert.equal((await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName)).authenticated, false);
      await connectorAction("connect");
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      await fetch(authorizationURL);
      await until(selector => !document.querySelector('[role="dialog"]') && document.querySelector(selector)?.getAttribute("data-connected") === "true", connectorRow);
      assert.equal((await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName)).authenticated, true);
      await capture("connector-connected-compact-light");
      await clickElement(`${connectorRow} [data-connector-details-toggle]`);
      await until(selector => document.querySelector(`${selector} [data-connector-detail="scopes"]`)?.textContent.includes("tools:read"), connectorRow);
      assert.equal(await evaluate(selector => document.querySelector(`${selector} [data-connector-detail="account"]`)?.getAttribute("data-verified"), connectorRow), "false", "Endpoint authorization must not fabricate a verified account identity");
      await capture("connector-connected-light");
      await evaluate(() => window.wuu.setThemePreference("dark"));
      win.setSize(800, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "20px"));
      await capture("connector-connected-dark-narrow-large");
      await evaluate(() => window.wuu.setThemePreference("light"));
      win.setSize(1280, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "15px"));
      await connectorAction("disconnect");
      await clickElement('[data-confirm-action="confirm"]');
      await until(selector => !document.querySelector('[role="dialog"]') && document.querySelector(selector)?.getAttribute("data-connected") === "false" && document.querySelector(`${selector} [data-connector-action="connect"]`), connectorRow);
      assert.equal((await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName)).authenticated, false);
      const connectorPreference = await evaluate(name => window.wuu.readMCPDefinition(name), connectorName);
      assert.deepEqual(connectorPreference.config, { enabled: false });
      evidence.checks.push("External plugin connector stays lazy, cancels OAuth safely, verifies tools/scopes without invented identity, and disconnect removes local authorization plus startup preference");
      await clickElement('[data-plugin="mcp-journey"] .catalog-row-open');
      await until(() => document.querySelector('[data-mcp="plugin.mcp-journey.docs"]'));
      await capture("mcp-plugin-owned");
      assert.equal(await evaluate(() => [...document.querySelectorAll('[data-mcp="plugin.mcp-journey.docs"] button')].some(button => button.textContent === "编辑")), false);
      // An explicitly selected module is required by composition. Remove this
      // fixture's selection before disabling its package; never weaken that rule.
      const disableConfig = JSON.parse(fs.readFileSync(configPath, "utf8"));
      disableConfig.composition.patches.push({ modules: { "mcp-journey": false } });
      fs.writeFileSync(configPath, JSON.stringify(disableConfig));
      await evaluate(() => window.wuu.refreshExtensionCatalog());
      await evaluate(record => window.wuu.updateExtensionPackage({ id: record.id, action: "disable" }), record);
      await until(() => document.querySelector('[data-mcp="plugin.mcp-journey.docs"]') && document.querySelector('[data-mcp="plugin.mcp-journey.docs"] button') === null);
      assert.equal(await evaluate(selector => document.querySelector(`${selector} [data-connector-action="connect"]`) === null, connectorRow), true, "Disabling a plugin disables its connector contribution");
      await capture("mcp-plugin-disabled");
      evidence.checks.push("Plugin MCP definitions appear on their owner page and disable with the parent");
      await clickElement('[data-testid="plugin-page-back"]');
      await until(() => document.querySelector('[data-mcp="journey-docs"]'));
      await click([row, "移除"]);
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("移除“journey-docs”"));
      await click(['[role="dialog"]', "移除"]);
      await until(() => !document.querySelector('[data-mcp="journey-docs"]') && !document.querySelector('[role="dialog"]'));
      const remaining = await evaluate(() => window.wuu.listMCPServers());
      assert.ok(remaining.servers.some(server => server.name === "journey-local"));
      evidence.checks.push("Deleting a definition preserves unrelated MCP services");
      const handoff = await evaluate(() => window.__mcpFixtureHandoff || []);
      fs.writeFileSync(path.join(output, "mcp-result.json"), JSON.stringify({ passed: true, ...evidence, handoff, browser: "Authorization provider and browser redirect simulated over real HTTP; production main/preload/core used with the selected file credential provider in disposable state." }, null, 2));
    },
    async diagnostics({ evaluate }) {
      await evaluate(() => {
        for (const button of document.querySelectorAll('[data-connector] .settings-mcp-details-toggle[aria-expanded="false"]')) button.click();
      });
      return evaluate(() => ({ phases: window.__mcpFixtureHandoff || [], rows: [...document.querySelectorAll('[data-connector]')].slice(0, 10).map(row => ({
        name: row.getAttribute("data-connector"), diagnostic: [...row.querySelectorAll("pre")].map(element => element.textContent.replace(/https?:\/\/\S+/g, "[URL]").slice(0, 512)),
      })) }));
    },
    close() { shell.openExternal = originalOpen; peer.closeAllConnections(); peer.close(); },
  };
};
