// README media for Wuu's macOS Computer Use. The capture drives the real
// desktop app and Go core with a scripted local provider, then lets the agent
// operate a disposable native AppKit invoice app. The target app is built from
// desktop/scripts/fixtures/cua-invoice.swift, so no personal application or
// document is opened.
//
// Build the desktop app being described, then run from this checkout on macOS:
//   (cd <app>/desktop && npm run build:core && npm run build)
//   node desktop/scripts/generate-cua-readme-media.cjs [<app>/desktop]
// It needs a display, Swift, ffmpeg, img2webp and webpmux, plus Screen Recording
// and Accessibility permission for the capture process.
// Output: landing/assets/readme/cua-<lang>.webp
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');

const repository = path.resolve(__dirname, '../..');
const output = path.join(repository, 'landing/assets/readme');
const brand = path.join(repository, 'landing/assets/brand');
const fixtureSource = path.join(__dirname, 'fixtures/cua-invoice.swift');
const captureScale = 2;
const windowStage = { width: 1640, height: 1100, left: 100, top: 85 };
const captureWindow = { width: 1440, height: 900 };
const wideShot = { x: windowStage.width / 2, y: windowStage.height / 2, scale: 1 };
const cameraMoveSeconds = 0.8;
const frameRate = 24;
const renderRate = 96;
const shutterFrames = 2;
const recordingSeconds = 28;
const noteFile = 'invoices/october-delivery.txt';

const messages = {
  en: {
    project: 'Northwind',
    prompt: 'In Northwind Invoice, change the delivery note to “Deliver to the loading dock before noon.” and save it.',
    oldNote: 'Deliver to the studio entrance.',
    newNote: 'Deliver to the loading dock before noon.',
    warmup: 'Review the October invoices and tell me what needs attention.',
    warmupReply: 'October has one open invoice: NW-2041, due on the 30th. The delivery note still needs a destination before it goes out.',
    warmupTitle: 'Review October invoices',
    title: 'Update the invoice delivery note',
    first: 'I’ll inspect the invoice in Northwind Invoice.',
    second: 'The delivery note is ready to edit. I’ll replace it, save, and check the file.',
    done: 'Updated the delivery note and saved the invoice.\n\n- Changed **Deliver to the studio entrance.** to **Deliver to the loading dock before noon.**\n- Confirmed the saved value in the native app and in `invoices/october-delivery.txt`.',
    openRight: 'Open right sidebar',
    newPage: 'New page',
    review: 'Review',
    newIn: 'Start a new conversation in Northwind',
    saveButton: 'Save',
  },
  zh: {
    project: 'Northwind',
    prompt: '在 Northwind Invoice 中，把送货备注改成“送至装卸区，中午前。”，然后保存。',
    oldNote: '送至工作室入口。',
    newNote: '送至装卸区，中午前。',
    warmup: '看一下十月的发票，有什么需要处理的？',
    warmupReply: '十月还有一张未结发票：NW-2041，30 日到期。发货前还需要补上送货备注。',
    warmupTitle: '查看十月发票',
    title: '更新发票的送货备注',
    first: '我先查看 Northwind Invoice 里的发票。',
    second: '送货备注已经可以修改了。我会替换内容、保存，并确认文件。',
    done: '送货备注已更新，发票也已经保存。\n\n- 已把 **送至工作室入口。** 改为 **送至装卸区，中午前。**\n- 原生应用和 `invoices/october-delivery.txt` 中都确认了保存后的内容。',
    openRight: '打开右侧栏',
    newPage: '新建页面',
    review: '审查',
    newIn: '在 Northwind 中新建对话',
    saveButton: '保存',
  },
};

if (!process.versions.electron) {
  const appDesktop = path.resolve(process.argv[2] || path.join(repository, 'desktop'));
  for (const file of ['out/main/index.js', 'build/bin/wuu-core', 'build/bin/wuu-cua-mac', 'build/bin/wuu-cua-mac-pip']) {
    assert.ok(fs.existsSync(path.join(appDesktop, file)), `Build ${file} in ${appDesktop} first`);
  }
  assert.equal(process.platform, 'darwin', 'The Computer Use demo can only be captured on macOS');
  for (const command of ['swiftc', 'ffmpeg', 'img2webp', 'webpmux']) {
    assert.equal(spawnSync('sh', ['-c', `command -v ${command}`]).status, 0, `${command} is required`);
  }
  const fixtureRoot = path.join(repository, 'desktop/.tmp/cua-media-fixture');
  fs.rmSync(fixtureRoot, { recursive: true, force: true });
  const fixtureApp = path.join(fixtureRoot, 'Northwind Invoice.app');
  const fixtureExecutable = path.join(fixtureApp, 'Contents/MacOS/northwind-invoice');
  fs.mkdirSync(path.dirname(fixtureExecutable), { recursive: true });
  const compile = spawnSync('swiftc', [fixtureSource, '-o', fixtureExecutable, '-framework', 'AppKit'], { stdio: 'inherit' });
  assert.equal(compile.status, 0, 'Build the synthetic Northwind Invoice app');
  fs.writeFileSync(path.join(fixtureApp, 'Contents/Info.plist'), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.wuu.example.northwind-invoice</string>
<key>CFBundleName</key><string>Northwind Invoice</string>
<key>CFBundleDisplayName</key><string>Northwind Invoice</string>
<key>CFBundleExecutable</key><string>northwind-invoice</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
`);
  fs.mkdirSync(output, { recursive: true });

  const temporaryRoot = path.join(repository, 'desktop/.tmp');
  fs.mkdirSync(temporaryRoot, { recursive: true });
  const env = { ...process.env };
  delete env.ELECTRON_RUN_AS_NODE;
  const flags = ['--no-sandbox'];
  for (const lang of ['en', 'zh']) {
    const temporary = fs.mkdtempSync(path.join(temporaryRoot, `cua-media-${lang}-`));
    try {
      const capture = spawnSync(require(path.join(appDesktop, 'node_modules/electron')), [...flags, __filename, appDesktop, lang, temporary, fixtureExecutable], {
        env, stdio: 'inherit', timeout: 900_000,
      });
      if (capture.error) throw capture.error;
      assert.equal(capture.status, 0, `Capture failed for ${lang}`);
    } finally {
      fs.rmSync(temporary, { recursive: true, force: true });
    }
  }
  fs.rmSync(fixtureRoot, { recursive: true, force: true });
  process.exit(0);
}

capture(...process.argv.slice(-4));

function capture(desktop, lang, temporary, fixtureExecutable) {
  const { app, BrowserWindow, screen } = require('electron');
  const { pathToFileURL } = require('node:url');
  const L = messages[lang];
  assert.ok(L, `Unknown language ${lang}`);
  const zh = lang === 'zh';
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  app.commandLine.appendSwitch('force-device-scale-factor', String(captureScale));
  app.commandLine.appendSwitch('force-color-profile', 'srgb');
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-cua-readme-'));
  const home = path.join(fixture, 'wuu-home');
  const userHome = path.join(fixture, 'user-home');
  const project = path.join(fixture, 'northwind');
  for (const dir of [home, userHome, project]) fs.mkdirSync(dir);
  app.setPath('userData', path.join(fixture, 'profile'));
  process.env.WUU_HOME = home;
  process.env.HOME = userHome;
  process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
  process.env.WUU_ENABLE_CUA_MAC = '1';
  process.env.WUU_CUA_MAC_HELPER = path.join(desktop, 'build/bin/wuu-cua-mac');
  process.env.WUU_CUA_MAC_PIP_HELPER = path.join(desktop, 'build/bin/wuu-cua-mac-pip');
  delete process.env.ELECTRON_RENDERER_URL;
  process.env.GIT_AUTHOR_NAME = process.env.GIT_COMMITTER_NAME = 'Ada Lovelace';
  process.env.GIT_AUTHOR_EMAIL = process.env.GIT_COMMITTER_EMAIL = 'ada@example.test';

  const write = (file, text) => {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, text);
  };
  const sh = (cwd, command) => {
    const result = spawnSync('sh', ['-c', command], { cwd, encoding: 'utf8' });
    assert.equal(result.status, 0, `${command}\n${result.stderr}`);
  };
  write(path.join(project, 'README.md'), '# Northwind\n\nOctober invoices and delivery notes.\n');
  write(path.join(project, noteFile), `${L.oldNote}\n`);
  write(path.join(project, 'invoices/october-summary.txt'), 'NW-2041  open    due 2026-10-30\nNW-2038  paid    due 2026-10-16\n');
  sh(project, 'git init -q -b main && git add -A && git commit -q -m "chore: October invoices"');
  const invoice = path.join(project, noteFile);

  const target = spawn(fixtureExecutable, [lang, invoice], { stdio: 'ignore' });
  const stopTarget = () => {
    if (target.exitCode === null && !target.killed) target.kill('SIGTERM');
  };
  process.on('exit', stopTarget);

  let pace = false;
  let marks = {};
  let captureStartedAt = 0;
  const mark = name => {
    if (!captureStartedAt) return;
    marks[name] = Number(((Date.now() - captureStartedAt) / 1000).toFixed(3));
    process.stdout.write(`[cua-media] ${name}=${marks[name]}s\n`);
  };
  const textOf = message => typeof message?.content === 'string' ? message.content : JSON.stringify(message?.content || '');
  const chunk = (res, delta, finish) => {
    res.write(`data: ${JSON.stringify({ id: 'cua-demo', object: 'chat.completion.chunk', model: 'gpt-6.1-sol', choices: [{ index: 0, delta, finish_reason: finish || null }] })}\n\n`);
  };
  let providerFailure = '';
  const cuaCode = (step, catalog) => {
    const binding = catalog.match(/mcp_plugin_cua_mac_computer_computer_[0-9a-f]+/)?.[0];
    if (!binding) throw new Error('The Computer Use binding is missing from the model tool catalog');
    const cua = binding;
    if (step === 0) return [
      `const seen = await tools.${cua}({ action: 'observe', app: 'com.wuu.example.northwind-invoice', mode: 'ax' });`,
      `const state = seen.structured_content;`,
      `const area = state.ax_nodes.find(node => node.role === 'AXTextArea');`,
      `console.log(JSON.stringify({ app: state.display_name, note: area && area.value }));`,
    ].join('\n');
    if (step === 1) return [
      `const seen = await tools.${cua}({ action: 'observe', app: 'com.wuu.example.northwind-invoice', mode: 'ax' });`,
      `const state = seen.structured_content;`,
      `const area = state.ax_nodes.find(node => node.role === 'AXTextArea');`,
      `const selected = await tools.${cua}({ action: 'select_text', app: 'com.wuu.example.northwind-invoice', snapshot_id: state.snapshot_id, element_id: area.element_id, text: ${JSON.stringify(L.oldNote)}, selection_type: 'text' });`,
      `const typed = await tools.${cua}({ action: 'type_text', app: 'com.wuu.example.northwind-invoice', text: ${JSON.stringify(L.newNote)} });`,
      `await tools.${cua}({ action: 'observe', app: 'com.wuu.example.northwind-invoice', mode: 'ax', scope: 'app', limit: 300 });`,
      `const saved = await tools.${cua}({ action: 'activate_control', app: 'com.wuu.example.northwind-invoice', title: ${JSON.stringify(L.saveButton)} });`,
      `const confirmed = await tools.${cua}({ action: 'observe', app: 'com.wuu.example.northwind-invoice', mode: 'ax' });`,
      `const file = await tools.bash({ command: 'cat ' + ${JSON.stringify(JSON.stringify(invoice))} });`,
      `const fileText = typeof file.model_text === 'string' ? file.model_text : Array.isArray(file.content) ? file.content.map(part => part.text || '').join('') : JSON.stringify(file);`,
      `const status = confirmed.structured_content.ax_nodes.find(node => node.identifier === 'northwind-status');`,
      `console.log(JSON.stringify({ selected: selected.structured_content?.delivery, typed: typed.structured_content?.delivery, saved: saved.structured_content?.delivery, status: status && status.value, file_updated: fileText.includes(${JSON.stringify(L.newNote)}) }));`,
    ].join('\n');
    return '';
  };
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', part => { body += part; });
    req.on('end', async () => {
      const payload = body ? JSON.parse(body) : {};
      const requestMessages = payload.messages || [];
      if (req.url.endsWith('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ data: [{ id: 'gpt-6.1-sol', object: 'model' }] }));
      }
      const contentText = message => Array.isArray(message?.content)
        ? message.content.map(part => typeof part?.text === 'string' ? part.text : '').join('\n')
        : textOf(message);
      let lastUser = -1;
      for (let i = requestMessages.length - 1; i >= 0; i--) {
        if (requestMessages[i].role === 'user' && !contentText(requestMessages[i]).startsWith('<system-reminder>')) {
          lastUser = i;
          break;
        }
      }
      const titleRequest = !textOf(requestMessages[0]).startsWith('You are wuu, a coding agent');
      const userText = contentText(requestMessages[lastUser]);
      if (titleRequest) {
        const title = userText.includes('Northwind Invoice') || userText.includes('Northwind Invoice 中') ? L.title : L.warmupTitle;
        if (payload.stream) {
          res.writeHead(200, { 'Content-Type': 'text/event-stream' });
          chunk(res, { role: 'assistant', content: title });
          chunk(res, {}, 'stop');
          return res.end('data: [DONE]\n\n');
        }
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ id: 'cua-demo', object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: title }, finish_reason: 'stop' }], usage: { prompt_tokens: 20, completion_tokens: 6, total_tokens: 26 } }));
      }
      let promptIndex = -1;
      for (let i = lastUser; i >= 0; i--) {
        const candidate = contentText(requestMessages[i]);
        if (requestMessages[i].role === 'user' && (candidate === L.prompt || candidate === L.warmup)) {
          promptIndex = i;
          break;
        }
      }
      assert.ok(promptIndex >= 0, 'The captured provider could not find the user prompt');
      const promptText = contentText(requestMessages[promptIndex]);
      const warmup = promptText === L.warmup;
      const step = requestMessages.slice(promptIndex + 1).filter(message => message.role === 'assistant').length;
      fs.appendFileSync(path.join(temporary, 'provider.log'), `${JSON.stringify({ kind: 'request', warmup, step, promptIndex, promptText: promptText.slice(0, 120), tail: requestMessages.slice(promptIndex + 1).map(message => ({ role: message.role, name: message.name, content: contentText(message).slice(0, 600), tool_calls: message.tool_calls })) }, null, 2)}\n`);
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      if (warmup) {
        const parts = L.warmupReply.match(/[\s\S]{1,7}/g) || [];
        for (const part of parts) {
          chunk(res, { role: 'assistant', content: part });
          if (pace) await delay(22);
        }
        chunk(res, {}, 'stop');
        return res.end('data: [DONE]\n\n');
      }
      if (pace) await delay(350);
      let action;
      try {
        if (step === 0) action = { text: L.first, code: cuaCode(0, JSON.stringify(payload.tools || [])) };
        else if (step === 1) action = { text: L.second, code: cuaCode(1, JSON.stringify(payload.tools || [])) };
        else action = { text: L.done };
      } catch (error) {
        providerFailure = error.message;
        action = { text: zh ? '暂时没能启动 Computer Use。' : 'I could not start Computer Use.' };
      }
      const size = pace ? (zh ? 2 : 5) : 80;
      for (const part of action.text.match(new RegExp(`[\\s\\S]{1,${size}}`, 'g')) || []) {
        chunk(res, { role: 'assistant', content: part });
        if (pace) await delay(zh ? 28 : 20);
      }
      if (action.code) {
        if (pace) await delay(350);
        chunk(res, { tool_calls: [{ index: 0, id: `call_${step}`, type: 'function', function: { name: 'run_code', arguments: JSON.stringify({ description: step ? 'Edit and save the delivery note' : 'Inspect the invoice in Northwind Invoice', code: action.code }) } }] });
        chunk(res, {}, 'tool_calls');
      } else {
        chunk(res, {}, 'stop');
      }
      res.end('data: [DONE]\n\n');
    });
  });

  const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
  const waitFor = async (win, fn, arg, timeout = 30000) => {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      if (await evaluate(win, fn, arg)) return;
      await delay(50);
    }
    throw new Error(`Timed out: ${fn} ${arg === undefined ? '' : JSON.stringify(arg)}`);
  };
  const clickLabel = (win, label) => evaluate(win, label => {
    const nameOf = element => (element.getAttribute('aria-label') || element.title || element.textContent || '').trim();
    const control = [...document.querySelectorAll('button, [role="button"], [role="menuitem"], [role="tab"], a, summary')]
      .find(element => element.getBoundingClientRect().width > 0 && nameOf(element) === label);
    if (!control) throw new Error(`No control named ${label}`);
    control.click();
  }, label);
  const draft = (win, text) => evaluate(win, value => {
    const textarea = document.querySelector('.composer textarea');
    textarea.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, value);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  }, text);
  const waitTurn = async win => {
    await waitFor(win, () => document.querySelector('.composer-stop-button'), undefined, 4000).catch(() => {});
    let quiet = 0;
    let last = '';
    for (let waited = 0; waited < 120000 && quiet < 1800; waited += 200) {
      const state = await evaluate(win, () => `${document.querySelectorAll('.turn').length}:${document.body.innerText.length}:${!!document.querySelector('.composer-stop-button')}:${document.querySelector('.composer textarea')?.value}`);
      quiet = state === last ? quiet + 200 : 0;
      last = state;
      if (await evaluate(win, () => !!document.querySelector('.composer-stop-button'))) quiet = 0;
      await delay(200);
    }
    assert.equal(await evaluate(win, () => !document.querySelector('.composer-stop-button')), true, 'The turn did not finish');
  };
  const send = async (win, text, paceText = false) => {
    await draft(win, '');
    pace = paceText;
    mark('type');
    const stride = zh ? 2 : 4;
    for (let length = stride; length <= text.length + stride; length += stride) {
      await draft(win, text.slice(0, length));
      await delay(paceText ? (zh ? 45 : 32) : 6);
    }
    await draft(win, text);
    await waitFor(win, () => !document.querySelector('.composer-send-button')?.disabled, undefined, 10000);
    const before = await evaluate(win, () => document.querySelectorAll('.turn').length);
    await evaluate(win, () => document.querySelector('.composer-send-button').click());
    await waitFor(win, turns => document.querySelectorAll('.turn').length > turns || !!document.querySelector('.composer-stop-button'), before, 8000)
      .catch(error => { throw new Error(`Message did not start: ${error.message}`); });
    mark('submit');
  };
  const expandWorkspaces = async win => {
    await evaluate(win, () => {
      for (const row of document.querySelectorAll('button.project-row:not(.expanded)')) row.click();
    });
    await delay(400);
  };
  const newThread = async win => {
    await expandWorkspaces(win);
    await clickLabel(win, L.newIn);
    await delay(900);
  };
  const openTool = async (win, label) => {
    await clickLabel(win, L.openRight);
    await delay(700);
    const pick = () => evaluate(win, wanted => {
      const item = [...document.querySelectorAll('.workspace-tool-menu-item')].find(entry => entry.textContent.trim() === wanted);
      item?.click();
      return !!item;
    }, label);
    if (!(await pick())) {
      await clickLabel(win, L.newPage);
      await delay(500);
      assert.ok(await pick(), `The tool picker offers ${label}`);
    }
  };
  const openReviewFile = async (win, name) => {
    const until = Date.now() + 12000;
    while (!(await evaluate(win, name => {
      if ([...document.querySelectorAll('.workspace-review-file-name')].some(item => item.textContent.trim() === name)) return true;
      const match = [...document.querySelectorAll('.workspace-review-row')].find(item => item.textContent.trim().startsWith(name));
      match?.click();
      return !!match;
    }, name))) {
      assert.ok(Date.now() < until, `Review lists ${name}`);
      await delay(200);
    }
    await delay(1000);
  };
  const bounds = (win, selector, pad) => evaluate(win, ({ selector, pad, width }) => {
    const zoom = width / innerWidth;
    const rects = [...document.querySelectorAll(selector)].map(element => element.getBoundingClientRect()).filter(rect => rect.width > 0 && rect.height > 0);
    if (!rects.length) return null;
    const left = Math.max(0, Math.min(...rects.map(rect => rect.left)) - pad);
    const top = Math.max(0, Math.min(...rects.map(rect => rect.top)) - pad);
    const right = Math.min(innerWidth, Math.max(...rects.map(rect => rect.right)) + pad);
    const bottom = Math.min(innerHeight, Math.max(...rects.map(rect => rect.bottom)) + pad);
    return { x: Math.round(left * zoom), y: Math.round(top * zoom), width: Math.round((right - left) * zoom), height: Math.round((bottom - top) * zoom) };
  }, { selector, pad, width: win.getContentSize()[0] });
  const framing = (rect, pad = 48, maxZoom = 1.8) => {
    const { width, height, left, top } = windowStage;
    const scale = Math.min(1, Math.max(1 / maxZoom, (rect.width + 2 * pad) / width, (rect.height + 2 * pad) / height));
    const fit = (center, size) => Math.min(Math.max(center, size * scale / 2), size * (1 - scale / 2));
    const round = value => Number(value.toFixed(3));
    return { x: round(fit(left + rect.x + rect.width / 2, width)), y: round(fit(top + rect.y + rect.height / 2, height)), scale: round(scale) };
  };
  // The capture uses the real window, so AppKit has its own traffic lights.
  // Hide those native buttons and draw one product-styled set in the page;
  // drawing over the native set produces two overlapping controls.
  const macChrome = win => evaluate(win, () => {
    document.documentElement.dataset.platform = 'darwin';
    const lights = document.createElement('div');
    lights.style.cssText = 'position:fixed;left:18px;top:17px;z-index:2147483647;display:flex;gap:8px;pointer-events:none';
    for (const [fill, edge] of [['#ff5f57', '#e0443e'], ['#febc2e', '#dea123'], ['#28c840', '#1aab29']]) {
      const light = document.createElement('span');
      light.style.cssText = `width:12px;height:12px;border-radius:50%;background:${fill};box-shadow:inset 0 0 0 0.5px ${edge}`;
      lights.append(light);
    }
    document.body.append(lights);
  });

  let main;
  app.on('browser-window-created', (_event, win) => { main ||= win; });
  const run = async () => {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
    const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
    write(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'openai', engines, providers: { openai: { type: 'openai-compatible', base_url: endpoint, api_key: 'test', model: 'gpt-6.1-sol' } } }));
    const stamp = '2026-10-17T00:00:00Z';
    write(path.join(home, 'projects.json'), JSON.stringify({
      projects: [{ id: 'northwind', name: L.project, path: project, created_at: stamp, updated_at: stamp }],
      active_context: { kind: 'project', project_id: 'northwind', cwd: project },
    }));
    write(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: zh ? 'zh-CN' : 'en-US', theme: 'light' }));
    await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
    while (!main) await delay(25);
    main.setContentSize(captureWindow.width, captureWindow.height);
    await waitFor(main, () => document.querySelector('.composer textarea'));
    const workArea = screen.getPrimaryDisplay().workArea;
    main.setBounds({
      x: workArea.x + Math.max(24, Math.round((workArea.width - captureWindow.width) / 2)),
      y: workArea.y + Math.max(24, Math.round((workArea.height - captureWindow.height) / 2)),
      width: captureWindow.width,
      height: captureWindow.height,
    });
    main.show();
    main.setWindowButtonVisibility(false);
    main.focus();
    await evaluate(main, () => { localStorage.setItem('wuu.desktop.workspaceRightPanelWidth', '600'); location.reload(); });
    await delay(500);
    await waitFor(main, () => document.querySelector('.composer textarea'));
    await macChrome(main);
    await delay(1000);
    pace = true;
    await send(main, L.warmup);
    await waitTurn(main);
    pace = false;
    await newThread(main);
    await waitFor(main, () => !document.querySelector('.composer textarea')?.value);
    await delay(2500);
    const composerBounds = await bounds(main, '.composer', 0);
    assert.ok(composerBounds, 'Composer bounds');

    main.show();
    main.focus();
    spawnSync('swift', ['-e', 'import CoreGraphics; CGWarpMouseCursorPosition(CGPoint(x: 2, y: 2))']);
    await delay(250);
    const source = main.getMediaSourceId();
    const windowID = Number(source.split(':')[1]);
    assert.ok(windowID > 0, 'Native window id');
    const recorderPath = path.join(temporary, 'capture.mov');
    captureStartedAt = Date.now();
    const recorder = spawn('screencapture', ['-x', '-v', `-V${recordingSeconds}`, `-l${windowID}`, recorderPath], { stdio: 'ignore' });
    const recorded = new Promise((resolve, reject) => {
      recorder.on('exit', code => code === 0 ? resolve() : reject(new Error(`screencapture exited ${code}`)));
    });
    mark('record');

    const pipWatch = (async () => {
      for (let attempt = 0; attempt < 120; attempt++) {
        const pip = BrowserWindow.getAllWindows().find(win => win.getTitle() === 'Wuu Live Activity');
        if (pip) {
          await delay(650);
          const mainBounds = main.getBounds();
          const placed = pip.getBounds();
          const pipRect = { x: placed.x - mainBounds.x, y: placed.y - mainBounds.y, width: placed.width, height: placed.height };
          mark('pip');
          return pipRect;
        }
        await delay(100);
      }
      throw new Error('Computer Use preview did not appear');
    })();

    await delay(850);
    await send(main, L.prompt, true);
    const pipRect = await pipWatch;
    await waitTurn(main);
    mark('done');
    assert.ok(marks.done - marks.submit > 0.8, 'Computer Use activity is long enough to see');
    assert.equal(providerFailure, '', providerFailure);
    assert.ok(fs.readFileSync(invoice, 'utf8').includes(L.newNote), 'CUA saved the delivery note');
    await delay(750);
    const pipWindow = BrowserWindow.getAllWindows().find(win => win.getTitle() === 'Wuu Live Activity');
    if (pipWindow) pipWindow.close();
    await delay(650);
    await openTool(main, L.review);
    await delay(900);
    mark('review');
    await openReviewFile(main, 'october-delivery.txt');
    mark('diff');
    const diffBounds = await bounds(main, '.workspace-diff-detail-header, .workspace-review-diff-panel .view-line', 0);
    assert.ok(diffBounds, 'Review diff bounds');
    await delay(2600);
    mark('outro');
    await recorded;
    const elapsed = (Date.now() - captureStartedAt) / 1000;
    assert.ok(elapsed >= marks.outro + 0.8, 'Recording covers the final hold');
    server.close();
    await createMedia({ temporary, lang, marks, pipRect, composerBounds, diffBounds, recorderPath });
    stopTarget();
    const cleanup = spawn('sh', ['-c', `sleep 1; rm -rf ${JSON.stringify(fixture)}`], { detached: true, stdio: 'ignore' });
    cleanup.unref();
    app.exit(0);
  };
  const timeout = setTimeout(() => {
    console.error('Timed out');
    app.exit(1);
  }, 840_000);
  run().then(() => clearTimeout(timeout)).catch(async error => {
    clearTimeout(timeout);
    console.error(error);
    if (main && !main.isDestroyed()) {
      try {
        fs.writeFileSync(path.join(temporary, `cua-media-failure-${lang}.png`), (await main.webContents.capturePage()).toPNG());
      } catch {}
    }
    stopTarget();
    app.exit(1);
  });
}

async function createMedia({ temporary, lang, marks, pipRect, composerBounds, diffBounds, recorderPath }) {
  const { pathToFileURL } = require('node:url');
  const { app, BrowserWindow } = require('electron');
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
  const url = file => pathToFileURL(file).href;
  const framing = (rect, pad = 48, maxZoom = 1.8) => {
    const { width, height, left, top } = windowStage;
    const scale = Math.min(1, Math.max(1 / maxZoom, (rect.width + 2 * pad) / width, (rect.height + 2 * pad) / height));
    const fit = (center, size) => Math.min(Math.max(center, size * scale / 2), size * (1 - scale / 2));
    const round = value => Number(value.toFixed(3));
    return { x: round(fit(left + rect.x + rect.width / 2, width)), y: round(fit(top + rect.y + rect.height / 2, height)), scale: round(scale) };
  };
  const render = async (size, body) => {
    const page = path.join(temporary, 'frame.html');
    fs.writeFileSync(page, `<!doctype html><meta charset="utf-8"><style>
      html, body { margin: 0; background: transparent; }
      .stage { position: relative; width: ${size.width}px; height: ${size.height}px; overflow: hidden; }
      .layer { position: absolute; inset: 0; }
      .backdrop { position: absolute; inset: -12px; width: calc(100% + 24px); height: calc(100% + 24px); object-fit: cover; filter: blur(2.5px); }
      .shot { position: absolute; overflow: hidden; background-repeat: no-repeat; }
      * { box-sizing: border-box; }
    </style><div class="stage">${body}</div>`);
    const win = new BrowserWindow({
      show: false, width: size.width, height: size.height, useContentSize: true, frame: false, transparent: true,
      backgroundColor: '#00000000', webPreferences: { offscreen: true },
    });
    try {
      await win.loadFile(page);
      await evaluate(win, () => Promise.all([...document.images].map(image => image.decode())));
      await delay(200);
      return await win.webContents.capturePage();
    } finally {
      win.destroy();
    }
  };
  const backdrop = scene => `<img class="backdrop" src="${url(path.join(brand, `${scene}.webp`))}">`;
  const { width, height, left, top } = windowStage;
  const right = left + captureWindow.width;
  const bottom = top + captureWindow.height;
  const radius = 12;
  const outside = `M0 0H${width}V${height}H0Z M${left + radius} ${top}H${right - radius}A${radius} ${radius} 0 0 1 ${right} ${top + radius}V${bottom - radius}A${radius} ${radius} 0 0 1 ${right - radius} ${bottom}H${left + radius}A${radius} ${radius} 0 0 1 ${left} ${bottom - radius}V${top + radius}A${radius} ${radius} 0 0 1 ${left + radius} ${top}Z`;
  const maskURL = encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><path fill-rule="evenodd" d="${outside}"/></svg>`);
  const stageImage = await render(windowStage,
    `<div class="layer" style="-webkit-mask:url('data:image/svg+xml,${maskURL}')">${backdrop('meadow')}<div class="shot" style="left:${left}px;top:${top}px;width:${captureWindow.width}px;height:${captureWindow.height}px;border-radius:${radius}px;box-shadow:0 12px 40px rgba(0,0,0,.59)"></div></div>`);
  const stagePath = path.join(temporary, 'stage.png');
  fs.writeFileSync(stagePath, stageImage.toPNG());
  const videoMask = await render({ width: captureWindow.width, height: captureWindow.height },
    `<div style="width:${captureWindow.width}px;height:${captureWindow.height}px;background:#000"><div style="position:absolute;inset:0;border-radius:${radius}px;background:#fff"></div></div>`);
  const maskPath = path.join(temporary, 'window-mask.png');
  fs.writeFileSync(maskPath, videoMask.toPNG());

  const probe = spawnSync('ffprobe', ['-v', 'error', '-select_streams', 'v:0', '-show_entries', 'stream=width,height', '-of', 'json', recorderPath], { encoding: 'utf8' });
  assert.equal(probe.status, 0, probe.stderr);
  const stream = JSON.parse(probe.stdout).streams[0];
  const contentWidth = captureWindow.width * captureScale;
  const contentHeight = captureWindow.height * captureScale;
  assert.ok(stream.width >= contentWidth && stream.height >= contentHeight, 'Capture is large enough');
  // Window shadows are not symmetric: the top shadow is often much shorter
  // than the bottom one. Find the actual content origin before cropping, or
  // the titlebar and traffic lights are cut off and shadow leaks in below.
  const sample = spawnSync('ffmpeg', [
    '-v', 'error', '-ss', '0.8', '-i', recorderPath, '-frames:v', '1',
    '-f', 'rawvideo', '-pix_fmt', 'rgb24', '-',
  ], { maxBuffer: stream.width * stream.height * 3 + 1_000_000 });
  assert.equal(sample.status, 0, 'Read a capture frame');
  assert.equal(sample.stdout.length, stream.width * stream.height * 3, 'Capture frame is complete');
  const bytes = sample.stdout;
  const luminance = (x, y) => {
    const offset = (y * stream.width + x) * 3;
    return (bytes[offset] + bytes[offset + 1] + bytes[offset + 2]) / 3;
  };
  const centerX = Math.round(stream.width / 2);
  const centerY = Math.round(stream.height / 2);
  let offsetY = -1;
  for (let y = 0; y + 6 < stream.height; y++) {
    if (luminance(centerX, y) > 100 && luminance(centerX, y + 3) > 100 && luminance(centerX, y + 6) > 100) {
      offsetY = y;
      break;
    }
  }
  let offsetX = -1;
  for (let x = 0; x + 6 < stream.width; x++) {
    if (luminance(x, centerY) > 100 && luminance(x + 3, centerY) > 100 && luminance(x + 6, centerY) > 100) {
      offsetX = x;
      break;
    }
  }
  assert.ok(offsetX >= 0 && offsetY >= 0, 'Window content origin');
  assert.ok(offsetX + contentWidth <= stream.width && offsetY + contentHeight <= stream.height, 'Window content rectangle fits');
  process.stdout.write(`[cua-media] crop=${contentWidth}x${contentHeight} @ ${offsetX},${offsetY}\n`);

  const clipStart = Math.max(0, marks.type - 0.9);
  const clipEnd = Math.min(recordingSeconds - 0.1, marks.outro + 1.1);
  const clipDuration = clipEnd - clipStart;
  const relative = name => Math.max(0, marks[name] - clipStart);
  const move = (name, to, offset = -0.2) => ({ at: Math.max(0.12, relative(name) + offset), to });
  const moves = [
    move('type', framing(composerBounds), -0.28),
    { at: relative('submit') - 0.05, to: wideShot },
    move('pip', framing(pipRect, 20, 2.5), 0.55),
    { at: relative('done') - 0.05, to: wideShot },
    move('diff', framing(diffBounds, 34), -0.1),
    { at: relative('outro') - cameraMoveSeconds - 0.05, to: wideShot },
  ].map(item => ({ at: Math.max(0, Number(item.at.toFixed(3))), to: item.to }));
  for (let index = 1; index < moves.length; index++) {
    if (moves[index].at < moves[index - 1].at + cameraMoveSeconds) {
      moves[index].at = Number((moves[index - 1].at + cameraMoveSeconds).toFixed(3));
    }
  }
  assert.ok(moves.at(-1).at + cameraMoveSeconds <= clipDuration, 'Camera moves fit the clip');
  const track = key => moves.reduceRight((rest, item, index) => {
    const from = (index ? moves[index - 1].to : wideShot)[key];
    const progress = `((it-${item.at})/${cameraMoveSeconds})`;
    const eased = `${progress}*${progress}*(3-2*${progress})`;
    return `if(lt(it,${item.at}),${from},if(lt(it,${item.at + cameraMoveSeconds}),${from}+(${item.to[key] - from})*${eased},${rest}))`;
  }, String(moves.at(-1).to[key]));
  const moving = moves.map(item => `between(t,${item.at},${item.at + cameraMoveSeconds})`).join('+');
  const camera = [
    `z='1/(${track('scale')})'`,
    `x='(${track('x')})*${captureScale}-iw/zoom/2'`,
    `y='(${track('y')})*${captureScale}-ih/zoom/2'`,
    `d=1:s=1600x${Math.round(1600 * windowStage.height / windowStage.width)}:fps=${renderRate}`,
  ].join(':');
  const rendered = path.join(temporary, 'rendered');
  fs.rmSync(rendered, { recursive: true, force: true });
  fs.mkdirSync(rendered);
  const ffmpeg = spawnSync('ffmpeg', [
    '-v', 'error', '-y', '-ss', String(clipStart), '-i', recorderPath,
    '-loop', '1', '-i', stagePath, '-loop', '1', '-i', maskPath,
    '-filter_complex', [
      `[0:v]crop=${contentWidth}:${contentHeight}:${offsetX}:${offsetY},format=rgba[window]`,
      `[2:v]format=gray[mask]`,
      `[window][mask]alphamerge[rounded]`,
      `[1:v][rounded]overlay=${windowStage.left * captureScale}:${windowStage.top * captureScale}:format=rgb,fps=${renderRate},zoompan=${camera},split[sharp][moving]`,
      `[moving]tmix=frames=${shutterFrames}[blurred]`,
      `[sharp][blurred]overlay=format=rgb:enable='${moving}',fps=${frameRate}`,
    ].join(';'),
    '-t', String(clipDuration), path.join(rendered, '%05d.png'),
  ], { stdio: 'inherit' });
  assert.equal(ffmpeg.status, 0, 'ffmpeg camera render');

  const run = (command, args) => {
    const result = spawnSync(command, args, { encoding: 'utf8' });
    if (result.error) throw result.error;
    assert.equal(result.status, 0, `${command} failed for ${lang}: ${result.stderr}`);
    return result.stdout;
  };
  const files = fs.readdirSync(rendered).sort();
  const duration = index => Math.round((index + 1) * 1000 / frameRate) - Math.round(index * 1000 / frameRate);
  const cuts = [0, ...moves.map(item => Math.floor((item.at + cameraMoveSeconds) * frameRate) + 1), files.length];
  const joined = [];
  for (let index = 0; index + 1 < cuts.length; index++) {
    const indices = files.map((_, frame) => frame).slice(cuts[index], cuts[index + 1]);
    const stretch = path.join(temporary, `stretch-${index}.webp`);
    run('img2webp', ['-loop', '0', '-mixed', '-q', '85', ...indices.flatMap(frame => ['-d', String(duration(frame)), path.join(rendered, files[frame])]), '-o', stretch]);
    const rows = [...run('webpmux', ['-info', stretch]).matchAll(/^\s*(\d+):\s+\d+\s+\d+\s+(?:yes|no)\s+(\d+)\s+(\d+)\s+(\d+)\s+(none|background)\s+(yes|no)/gm)];
    if (!rows.length) joined.push('-frame', stretch, `+${indices.reduce((sum, frame) => sum + duration(frame), 0)}+0+0+0-b`);
    for (const [, number, x, y, ms, dispose, blend] of rows) {
      const frame = path.join(temporary, `stretch-${index}-${number}.webp`);
      run('webpmux', ['-get', 'frame', number, stretch, '-o', frame]);
      joined.push('-frame', frame, `+${ms}+${x}+${y}+${dispose === 'background' ? 1 : 0}${blend === 'yes' ? '+b' : '-b'}`);
    }
  }
  const target = path.join(output, `cua-${lang}.webp`);
  run('webpmux', [...joined, '-loop', '0', '-o', target]);
  process.stdout.write(`[cua-media] wrote ${target}\n`);
}
