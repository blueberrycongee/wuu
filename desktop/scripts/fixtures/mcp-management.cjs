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
  const evidence = { registration: 0, exchanges: 0, authenticatedLists: 0, connectorRequests: 0, identityCalls: 0, conversationCalls: [], offeredTools: [], checks: [] };
  const peer = createServer(async (request, response) => {
    const url = new URL(request.url, endpoint);
    if (url.pathname === "/connector-mcp") evidence.connectorRequests++;
    assert.notEqual(url.pathname, "/setup-required", "A setup-only connector must never contact its endpoint");
    console.log(`MCP fixture: ${request.method} ${url.pathname}`);
    response.setHeader("Content-Type", "application/json");
    const json = value => response.end(JSON.stringify(value));
    let body = "";
    for await (const chunk of request) body += chunk;
    if (url.pathname === "/v1/chat/completions") {
      const completion = JSON.parse(body);
      const toolName = JSON.stringify(completion.messages).match(/mcp_plugin_mcp_journey_account_echo_[a-f0-9]+/)?.[0];
      const selected = completion.tools?.find(tool => tool.function?.name === toolName);
      const codeMode = completion.tools?.find(tool => tool.function?.name === "run_code");
      const task = JSON.stringify(completion.messages).includes("CONNECTOR_CONVERSATION_E2E");
      const done = completion.messages.some(message => message.role === "tool");
      if (task && completion.tools?.length) {
        evidence.offeredTools = (completion.tools ?? []).map(tool => tool.function?.name);
        console.log("Conversation tools:", JSON.stringify(evidence.offeredTools));
      }
      const message = task && toolName && (selected || codeMode) && !done
        ? { role: "assistant", content: null, tool_calls: [{ id: "fixture-call", type: "function", function: selected
          ? { name: selected.function.name, arguments: "{}" }
          : { name: "run_code", arguments: JSON.stringify({ input: `text(await tools[${JSON.stringify(toolName)}]({}));` }) } }] }
        : { role: "assistant", content: task ? "CONNECTOR_CONVERSATION_DONE" : "Fixture conversation" };
      if (completion.stream) {
        response.setHeader("Content-Type", "text/event-stream");
        const delta = { ...message, ...(message.tool_calls ? { tool_calls: message.tool_calls.map((call, index) => ({ ...call, index })) } : {}) };
        response.end(`data: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta, finish_reason: null }] })}\n\ndata: ${JSON.stringify({ id: "fixture", choices: [{ index: 0, delta: {}, finish_reason: message.tool_calls ? "tool_calls" : "stop" }] })}\n\ndata: [DONE]\n\n`);
      } else json({ id: "fixture", choices: [{ index: 0, message, finish_reason: message.tool_calls ? "tool_calls" : "stop" }] });
      return;
    }
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
    else if (rpc.method === "tools/list") {
      if (pauseToolList) await pauseToolList;
      evidence.authenticatedLists++;
      const tools = [{ name: "echo", description: "Echo fixture", inputSchema: { type: "object", properties: {} } }];
      if (url.pathname === "/connector-mcp") tools.push({ name: "account-self", description: "Read this connection's account and workspace", inputSchema: { type: "object", properties: {} } });
      reply({ tools });
    }
    else if (rpc.method === "tools/call" && rpc.params.name === "account-self") {
      assert.equal(url.pathname, "/connector-mcp");
      assert.deepEqual(rpc.params.arguments, {});
      evidence.identityCalls++;
      // The first response is deliberately incomplete: identity failure must not
      // undo successful OAuth or transport setup, and retry must not reauthorize.
      reply({ content: [{ type: "text", text: JSON.stringify({
        user: { ...(evidence.identityCalls > 1 ? { id: "fixture-user-42" } : {}), name: "Fixture researcher", type: "person", email: "fixture@example.test" },
        workspace: { id: "fixture-workspace-42", name: "Fixture research workspace" },
      }) }] });
    }
    else if (rpc.method === "tools/call") {
      evidence.conversationCalls.push({ path: url.pathname, tool: rpc.params.name });
      reply({ content: [{ type: "text", text: "fixture-ok" }] });
    }
    else if (rpc.id !== undefined) json({ jsonrpc: "2.0", id: rpc.id, error: { code: -32601, message: "unsupported" } });
    else response.writeHead(202).end();
  });
  await new Promise(resolve => peer.listen(0, "127.0.0.1", resolve));
  endpoint = `http://127.0.0.1:${peer.address().port}`;
  const configPath = path.join(fixture, "wuu-home", "config.json");
  const config = JSON.parse(fs.readFileSync(configPath, "utf8"));
  config.providers.fixture.base_url = `${endpoint}/v1`;
  fs.writeFileSync(configPath, JSON.stringify(config));
  const originalOpen = shell.openExternal;
  shell.openExternal = async url => { authorizationURL = url; };
  const pluginDir = path.join(fixture, "mcp-plugin");
  fs.mkdirSync(pluginDir);
  fs.writeFileSync(path.join(pluginDir, "plugin.json"), JSON.stringify({ schema_version: 1, id: "mcp-journey", name: "MCP Journey", mcp_servers: {
    docs: { url: `${endpoint}/mcp` },
    account: { url: `${endpoint}/connector-mcp`, enabled: false, tool_overrides: { "account-self": { read_only: true }, echo: { read_only: true } }, connector: {
      title: "Fixture workspace", description: "A user-installed connector, not a bundled product ID.", documentation_url: "https://example.test/connector",
      identity_probe: { tool: "account-self", arguments: {}, result: "single_text_json", account: { id: "/user/id", label: "/user/name", email: "/user/email", kind: "/user/type" }, workspace: { id: "/workspace/id", label: "/workspace/name" } },
    } },
    alternate: { url: `${endpoint}/connector-mcp`, enabled: false, tool_overrides: { echo: { read_only: true } }, connector: { title: "Fixture workspace", description: "A separate saved connection with the same display name." } },
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
      // Row menus are native in the packaged shell; pick the item from the menu
      // Electron is about to show instead of driving the OS menu.
      const menuSelect = async (selector, label, keyboard = false) => {
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
            await until(selector => !document.querySelector(`${selector} [aria-expanded="true"]`), selector);
          } finally { Menu.prototype.popup = originalPopup; }
      };
      const click = async ([selector, label], keyboard = false) => {
        if (selector.startsWith('[data-mcp=') && ["刷新", "移除 OAuth 登录", "编辑", "移除"].includes(label)) return menuSelect(selector, label, keyboard);
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
      await capture("connector-sign-in");
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
      assert.equal(evidence.identityCalls, 1, "Only user Connect runs the admitted account probe");
      await capture("connector-connected-compact-light");
      const details = `[data-connector-details="${connectorName}"]`;
      await menuSelect(connectorRow, "连接详情");
      await until(selector => document.querySelector(`${selector} [data-connector-detail="scopes"]`)?.textContent.includes("tools:read"), details);
      assert.equal(await evaluate(selector => document.querySelector(`${selector} [data-connector-detail="account"]`)?.getAttribute("data-verified"), details), "false", "An incomplete provider result must not fabricate identity");
      await capture("connector-identity-unavailable");
      const exchangesBeforeIdentity = evidence.exchanges;
      await clickElement('[role="dialog"] [data-connector-action="identity"]');
      await until(selector => document.querySelector(`${selector} [data-connector-detail="account"]`)?.getAttribute("data-verified") === "true", details);
      await capture("connector-details-verified");
      await clickElement('[role="dialog"] .environment-dialog-header .icon-button');
      await until(() => !document.querySelector('[role="dialog"]'));
      assert.equal(evidence.identityCalls, 2);
      assert.equal(evidence.exchanges, exchangesBeforeIdentity, "Identity retry must reuse the grant, not sign in again");
      const verified = await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName);
      assert.equal(verified.identity.account.id, "fixture-user-42");
      assert.equal(verified.identity.workspace.id, "fixture-workspace-42");
      assert.equal(verified.identity.account.email, "fixture@example.test");
      await evaluate(name => Promise.all([window.wuu.getMCPAuthStatus(name), window.wuu.listMCPServers(), window.wuu.getMCPAuthStatus(name)]), connectorName);
      assert.equal(evidence.identityCalls, 2, "Status and list reads must not run identity tools");
      await capture("connector-connected-light");
      await evaluate(() => window.wuu.setThemePreference("dark"));
      win.setSize(800, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "20px"));
      await capture("connector-connected-dark-narrow-large");
      await evaluate(() => window.wuu.setThemePreference("light"));
      win.setSize(1280, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "15px"));
      await clickElement('[data-connector="plugin.mcp-journey.alternate"] [data-connector-action="connect"]');
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      await fetch(authorizationURL);
      await until(() => !document.querySelector('[role="dialog"]') && document.querySelector('[data-connector="plugin.mcp-journey.alternate"]')?.getAttribute("data-connected") === "true");
      const retainedIdentity = await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName);
      assert.equal(retainedIdentity.identity?.account?.id, "fixture-user-42", "Enabling a sibling connection must retain verified identity across runtime snapshots");
      assert.equal(evidence.identityCalls, 2, "A runtime refresh must not re-probe accounts");
      const exchangesBeforeReconnect = evidence.exchanges;
      await clickElement('[data-ribbon-id="conversations"]');
      await until(() => document.querySelector('[data-main-conversation-composer] [data-wuu-component="composer-input"]'));
      win.focus(); win.webContents.focus();
      const editorSelector = '[data-main-conversation-composer] [data-wuu-component="composer-input"]';
      const key = async (keyCode, modifiers = []) => {
        win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
        win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
        await evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
      };
      await evaluate(selector => document.querySelector(selector).focus(), editorSelector);
      await win.webContents.insertText("CONNECTOR_CONVERSATION_E2E @");
      await until(() => document.querySelector('.composer-plus-menu [data-plugin-choice]:not(:disabled)'));
      const mentionChoices = await evaluate(() => [...document.querySelectorAll('.composer-plus-menu [data-plugin-choice]')].map(button => button.textContent));
      await capture("conversation-at-menu-light");
      await key("Escape");
      await clickElement('.composer-plus-button');
      await until(() => document.querySelector('.composer-plus-menu [data-plugin-choice]:not(:disabled)'));
      assert.deepEqual(await evaluate(() => [...document.querySelectorAll('.composer-plus-menu [data-plugin-choice]')].map(button => button.textContent)), mentionChoices, "@ and + expose the same plugin choices and states");
      await capture("conversation-plus-menu-light");
      await key("Escape");
      await evaluate(selector => { const input = document.querySelector(selector); input.focus(); input.setSelectionRange(input.value.length, input.value.length); }, editorSelector);
      await win.webContents.insertText("Fixture");
      await until(() => document.querySelector('.composer-plus-menu [data-plugin-choice]:not(:disabled)'));
      await key("Enter");
      await until(() => document.querySelector('.composer-rich-input .composer-plugin-reference'));
      const originalToken = await evaluate(() => document.querySelector('.composer-rich-input .composer-plugin-reference').dataset.pluginToken);
      await key("z", ["meta"]);
      await until(() => !document.querySelector('.composer-rich-input .composer-plugin-reference'));
      await key("z", ["meta", "shift"]);
      await until(() => document.querySelector('.composer-rich-input .composer-plugin-reference'));
      await key("Backspace");
      await key("Backspace");
      await until(() => !document.querySelector('.composer-rich-input .composer-plugin-reference'));
      await key("z", ["meta"]);
      await until(() => document.querySelector('.composer-rich-input .composer-plugin-reference'));
      assert.equal(await evaluate(() => document.querySelector('.composer-rich-input .composer-plugin-reference').dataset.pluginToken), originalToken);
      await win.webContents.insertText(" 请读取工作区 @Fixture");
      await clickElement('[data-plugin-key="mcp:plugin.mcp-journey.alternate"]:not(:disabled)');
      await until(() => document.querySelectorAll('.composer-rich-input .composer-plugin-reference').length === 2);
      const referenceTokens = await evaluate(() => [...document.querySelectorAll('.composer-rich-input .composer-plugin-reference')].map(element => element.dataset.pluginToken));
      assert.notEqual(referenceTokens[0], referenceTokens[1], "Same display names must retain distinct connection and authorization identities");
      await capture("conversation-inline-light");
      await clickElement('[data-ribbon-id="catalog"]');
      await until(() => document.querySelector('[data-testid="mcp-services"]'));
      await clickElement('[data-ribbon-id="conversations"]');
      await until(() => document.querySelectorAll('.composer-rich-input .composer-plugin-reference').length === 2);
      assert.equal(await evaluate(() => document.querySelector('.composer-rich-input .composer-plugin-reference').dataset.pluginToken), originalToken, "Draft navigation preserves opaque account and plugin identity");
      await evaluate(() => window.wuu.setThemePreference("dark"));
      win.setSize(800, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "20px"));
      await capture("conversation-inline-dark-narrow-large");
      await clickElement('.composer-plus-button');
      await until(() => document.querySelector('.composer-plus-menu [data-plugin-choice]'));
      await capture("conversation-menu-dark-narrow-large");
      await key("Escape");
      await evaluate(() => window.wuu.setThemePreference("light"));
      win.setSize(1280, 900);
      await evaluate(() => document.documentElement.style.setProperty("--conversation-message-font-size", "15px"));
      await evaluate(selector => document.querySelector(selector).focus(), editorSelector);
      await key("Enter");
      await until(() => document.querySelector('.conversation-pane')?.textContent.includes("CONNECTOR_CONVERSATION_DONE"));
      assert.ok(evidence.conversationCalls.some(call => call.path === "/connector-mcp" && call.tool === "echo"), "The real native loop must invoke the referenced connection's MCP tool");
      await capture("conversation-sent-and-tool-result");
      await evaluate(selector => document.querySelector(selector).focus(), editorSelector);
      await win.webContents.insertText("Keep this draft if the account changes @Fixture");
      await until(() => document.querySelector('.composer-plus-menu [data-plugin-choice]:not(:disabled)'));
      await key("Enter");
      await until(() => document.querySelector('.composer-rich-input .composer-plugin-reference'));
      evidence.checks.push("Same @/+ panel, searchable keyboard insertion, atomic deletion and undo/redo, same-name connections with distinct stable inline references, draft navigation, light/wide/default and dark/narrow/large font, sent references and native loop MCP execution with a deterministic model endpoint");
      await clickElement('[data-ribbon-id="catalog"]');
      await until(selector => document.querySelector(selector)?.getAttribute("data-connected") === "true", connectorRow);
      await menuSelect(connectorRow, "重新连接");
      await until(() => document.querySelector('[role="dialog"]')?.textContent.includes("请在浏览器"));
      const reconnecting = await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName);
      assert.equal(reconnecting.authenticated, true, "Reconnect keeps the accepted grant until replacement succeeds");
      assert.equal(reconnecting.identity, undefined, "Reconnect clears the prior identity, not the accepted credentials");
      await click(['[role="dialog"]', "取消登录"]);
      await until(() => !document.querySelector('[role="dialog"]'));
      assert.equal(evidence.exchanges, exchangesBeforeReconnect);
      const callsBeforeStaleReference = evidence.conversationCalls.length;
      await clickElement('[data-ribbon-id="conversations"]');
      await until(() => document.querySelector('.composer-rich-input .composer-plugin-reference'));
      await evaluate(selector => document.querySelector(selector).focus(), editorSelector);
      await key("Enter");
      await until(() => document.querySelector('[role="alert"]')?.textContent.includes("重新选择插件"));
      assert.equal(evidence.conversationCalls.length, callsBeforeStaleReference);
      assert.ok(await evaluate(() => document.querySelector('.composer-rich-input')?.textContent.includes("Keep this draft")));
      assert.ok(await evaluate(() => document.querySelector('.composer-rich-input .composer-plugin-reference')?.dataset.pluginToken.includes("fixture-user-42")), "Rejected delivery must retain the account-bound reference, not only its label");
      await capture("conversation-stale-account-draft-retained");
      // Passive, synthetic-fixture-only diagnostics for a disconnect/navigation
      // handoff. Read React's attached props; never replace or replay a handler.
      await evaluate(() => {
        const records = window.__mcpCatalogNavigation = [];
        const identities = new WeakMap(); let nextIdentity = 0;
        const scopes = new Set();
        const selector = '[data-plugin="mcp-journey"] .catalog-row-open';
        const sample = phase => {
          const control = document.querySelector(selector);
          const fiberKey = control && Object.keys(control).find(key => key.startsWith('__reactFiber$'));
          for (let fiber = fiberKey && control[fiberKey]; fiber; fiber = fiber.return) {
            const application = fiber.memoizedProps?.application;
            if (application) { scopes.add(application); break; }
          }
          const applications = [...scopes].map(application => {
            if (!identities.has(application)) identities.set(application, ++nextIdentity);
            try {
              const navigation = application.getSnapshot().navigation;
              const inventory = application.catalog.getSnapshot().inventory;
              return { scope: identities.get(application), selectedPluginId: navigation.catalogPluginId,
                section: navigation.section, catalogSection: navigation.catalogSection,
                generations: [...new Set(inventory.map(record => record.graph_generation).filter(Boolean))] };
            } catch (error) { return { scope: identities.get(application), scopeError: String(error).slice(0, 200) }; }
          });
          const propsKey = control && Object.keys(control).find(key => key.startsWith('__reactProps$'));
          const state = { controlPresent: Boolean(control), controlConnected: Boolean(control?.isConnected),
            inert: Boolean(control?.closest('[inert]')), handlerPresent: Boolean(propsKey && control[propsKey]?.onClick),
            detailPresent: Boolean(document.querySelector('[data-wuu-component="plugin-detail"]')),
            docsPresent: Boolean(document.querySelector('[data-mcp="plugin.mcp-journey.docs"]')), applications };
          const serialized = JSON.stringify(state);
          if (records.length < 80 && (phase !== 'mutation' || serialized !== records.at(-1)?.state)) records.push({ phase, at: performance.now(), state: serialized });
        };
        document.addEventListener('click', event => {
          if (!event.target.closest?.(selector)) return;
          sample('click-capture'); queueMicrotask(() => sample('click-microtask'));
          requestAnimationFrame(() => sample('click-animation-frame'));
          setTimeout(() => sample('click-after-100ms'), 100);
          setTimeout(() => sample('click-after-1000ms'), 1000);
        }, true);
        document.addEventListener('click', event => { if (event.target.closest?.(selector)) sample('click-bubble'); });
        window.addEventListener('wuu:mcp-connector-handoff', event => sample('handoff-' + event.detail?.phase));
        const observer = new MutationObserver(() => sample('mutation'));
        observer.observe(document, { childList: true, subtree: true, attributes: true });
        setTimeout(() => observer.disconnect(), 40_000);
        sample('before-disconnect');
      });
      evidence.checks.push("Changed account identity rejects the old reference before model/tool execution and preserves the draft");
      await clickElement('[data-ribbon-id="catalog"]');
      await menuSelect(connectorRow, "断开");
      await clickElement('[data-confirm-action="confirm"]');
      await until(selector => !document.querySelector('[role="dialog"]') && document.querySelector(selector)?.getAttribute("data-connected") === "false" && document.querySelector(`${selector} [data-connector-action="connect"]`), connectorRow);
      assert.equal((await evaluate(name => window.wuu.getMCPAuthStatus(name), connectorName)).authenticated, false);
      const connectorPreference = await evaluate(name => window.wuu.readMCPDefinition(name), connectorName);
      assert.deepEqual(connectorPreference.config, { enabled: false });
      evidence.checks.push("External plugin connector stays lazy, reads only its declared identity tool on Connect or explicit retry, rejects incomplete identity without losing authorization, persists verified account/workspace, performs no probe on status/list, and clears identity on cancelled reconnect while retaining accepted credentials");
      evidence.checks.push("Connector disconnect removes local authorization plus startup preference");
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
      return evaluate(() => ({ catalogNavigation: window.__mcpCatalogNavigation || [], phases: window.__mcpFixtureHandoff || [], rows: [...document.querySelectorAll('[data-connector]')].slice(0, 10).map(row => ({
        name: row.getAttribute("data-connector"), diagnostic: [...row.querySelectorAll("pre")].map(element => element.textContent.replace(/https?:\/\/\S+/g, "[URL]").slice(0, 512)),
      })) }));
    },
    close() { shell.openExternal = originalOpen; peer.closeAllConnections(); peer.close(); },
  };
};
