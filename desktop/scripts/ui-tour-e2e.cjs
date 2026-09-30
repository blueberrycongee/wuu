// Real Electron/main/preload/Go tour of the product's main surfaces, for visual
// review and for cross-surface continuity. Build the core and desktop first;
// WUU_TOUR_CORE overrides the core binary. Keys, config and projects live in a
// disposable WUU_HOME and HOME, and a scripted local provider answers every turn
// (plain text, tool calls, reasoning, errors), so no account or inference is used.
//
//   npm --prefix desktop run test:e2e:ui-tour
//
// It seeds conversations through the real composer, then walks the surfaces in
// each appearance of the matrix (theme x UI size x window width), capturing a
// screenshot and a geometry audit per stop, and checks the journeys a person takes
// between surfaces: a draft survives Settings and thread switches, the selected
// row follows navigation, closing a layer returns focus, a long conversation keeps
// its reading position. Screenshots, audit.json and report.json go to
// artifacts/ui-tour-e2e/ (WUU_TOUR_OUTPUT, wiped at the start of a run). Screenshots are evidence for review, not
// a visual gate; the audit records measurements, and only overflow, renderer
// errors and failed journeys fail the run.
//
// WUU_E2E_HIDDEN=true renders without showing, focusing or accepting input on any
// window, so a run on a developer's own desktop never steals focus.
// WUU_TOUR_ONLY=home,settings selects stops by substring; WUU_TOUR_MATRIX=light
// or light:14.5:1280,dark:20:720 overrides the appearances.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app, BrowserWindow } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = path.resolve(process.env.WUU_TOUR_OUTPUT || path.join(desktop, '../artifacts/ui-tour-e2e'));
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-ui-tour-'));
const home = path.join(fixture, 'wuu-home');
const userHome = path.join(fixture, 'user-home');
const projects = path.join(fixture, 'projects');
for (const dir of [home, userHome, projects]) fs.mkdirSync(dir);
fs.rmSync(output, { recursive: true, force: true });
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
// Subscription logins are discovered from HOME; keep the developer's out of view.
process.env.HOME = userHome;
process.env.WUU_DESKTOP_CORE = process.env.WUU_TOUR_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';
process.env.GIT_AUTHOR_NAME = process.env.GIT_COMMITTER_NAME = 'Ada Lovelace';
process.env.GIT_AUTHOR_EMAIL = process.env.GIT_COMMITTER_EMAIL = 'ada@example.test';

if (process.env.WUU_E2E_HIDDEN === 'true') {
  app.dock?.hide();
  const quiet = function () { this.setOpacity(0); this.setIgnoreMouseEvents(true); this.showInactive(); };
  for (const method of ['show', 'focus', 'moveTop']) BrowserWindow.prototype[method] = quiet;
  app.focus = () => {};
}

const only = process.env.WUU_TOUR_ONLY ? process.env.WUU_TOUR_ONLY.split(',') : null;
const matrix = (process.env.WUU_TOUR_MATRIX
  || 'light:14.5:1280,dark:14.5:1280,light:20:1280,light:14.5:720')
  .split(',').map(item => {
    const [theme, size = '14.5', width = '1280'] = item.split(':');
    return { theme, size: Number(size), width: Number(width) };
  });
const height = 860;

// ---- synthetic projects ------------------------------------------------------
function write(file, text) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, text);
}
function sh(cwd, command) {
  const result = spawnSync('sh', ['-c', command], { cwd, encoding: 'utf8' });
  assert.equal(result.status, 0, `${command}\n${result.stderr}`);
}
const repo = path.join(projects, 'wuu-desktop');
const site = path.join(projects, 'landing-site');
write(path.join(repo, 'README.md'), '# Wuu Desktop\n\nA calm desktop client for coding agents.\n');
write(path.join(repo, 'package.json'), `${JSON.stringify({ name: 'wuu-desktop', scripts: { test: 'vitest run' } }, null, 2)}\n`);
write(path.join(repo, 'src/server.ts'), 'export interface ServerOptions {\n  port: number;\n}\n\nexport function createServer(options: ServerOptions) {\n  const startedAt = Date.now();\n  return { port: options.port, startupMs: Date.now() - startedAt };\n}\n');
write(path.join(repo, 'src/utils/format.ts'), 'export function formatDuration(ms: number): string {\n  if (ms < 1000) return `${ms}ms`;\n  return `${(ms / 1000).toFixed(1)}s`;\n}\n');
write(path.join(repo, 'src/components/Sidebar.tsx'), 'export function Sidebar({ items }: { items: string[] }) {\n  return <nav>{items.map(item => <a key={item}>{item}</a>)}</nav>;\n}\n');
write(path.join(repo, 'docs/architecture.md'), '# Architecture\n\nThe renderer talks to the core over a JSON-RPC bridge.\n');
sh(repo, 'git init -q -b main && git add -A && git commit -q -m "chore: initial import" && git checkout -q -b feature/settings-refresh');
// Uncommitted work gives Review, the environment card and the branch picker real content.
write(path.join(repo, 'src/utils/format.ts'), 'export function formatDuration(ms: number): string {\n  if (ms < 1000) return `${ms}ms`;\n  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;\n  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;\n}\n');
write(path.join(repo, 'src/components/Sidebar.tsx'), 'export function Sidebar({ items, active }: { items: string[]; active?: string }) {\n  return (\n    <nav aria-label="Main">\n      {items.map(item => <a key={item} aria-current={item === active ? "page" : undefined}>{item}</a>)}\n    </nav>\n  );\n}\n');
write(path.join(repo, 'notes/todo.md'), '- polish settings\n- review sidebar\n');
write(path.join(site, 'index.html'), '<!doctype html>\n<title>Landing</title>\n<h1>Hello</h1>\n');

// ---- scripted provider -------------------------------------------------------
const longText = Array.from({ length: 14 }, (_, i) => `### 第 ${i + 1} 部分\n\n阅读体验来自稳定的节奏。文字大小可以按个人习惯调整，而段落、标题和列表之间的关系应该始终清楚。第 ${i + 1} 段还包含 \`inline-code\` 和一个 [链接](https://example.com)。\n\n- 要点一：保持对齐\n- 要点二：保持留白\n`).join('\n');
const answer = `已经把设置页的间距整理了一遍，下面是这次改动的要点。

## 改动概览

- **统一节奏**：标题、分组和操作之间使用同一组间距。
- **层级更清楚**：正文保持常规字重，只有分组标题使用半粗体。
- **状态可读**：错误、警告和已完成使用不同的符号，而不是只靠颜色。

| 区域 | 之前 | 现在 |
| --- | --- | --- |
| 分组标题 | 与内容贴得很近 | 与内容保持 16px |
| 说明文字 | 每行都有 | 只保留必要的约束 |

> 说明文字只保留那些标题没有说清楚的限制或后果。

\`\`\`ts
export function formatDuration(ms: number): string {
  return ms < 1000 ? \`\${ms}ms\` : \`\${(ms / 1000).toFixed(1)}s\`;
}
\`\`\`

如果需要，我可以继续检查窄窗口下的表现。`;
const scenarios = {
  md: [{ text: answer }],
  tools: [
    { text: '我先看一下相关文件，再决定怎么改。', calls: [{ name: 'read_file', args: { path: 'src/utils/format.ts' } }, { name: 'list_files', args: { path: 'src' } }, { name: 'grep', args: { pattern: 'formatDuration', path: 'src' } }] },
    { calls: [{ name: 'bash', args: { command: 'git status --short' } }] },
    { calls: [{ name: 'edit_file', args: { path: 'src/server.ts', old_text: 'export interface ServerOptions {\n  port: number;\n}', new_text: 'export interface ServerOptions {\n  port: number;\n  host?: string;\n}' } }, { name: 'write_file', args: { path: 'notes/summary.md', content: '# Summary\n\n- server accepts a host option\n' } }] },
    { text: '已经改好：`createServer` 现在接受可选的 `host`，默认仍然只监听本机。\n\n- 修改了 `src/server.ts`\n- 新增了 `notes/summary.md`' },
  ],
  thinking: [{ reasoning: '先列出两种方案各自的代价，再比较：方案 B 需要迁移调用点，但之后更容易维护。', text: '我建议采用第二种方案：先把共享逻辑抽到一处，再让两个调用点使用它。' }],
  long: [{ text: longText }],
  unauthorized: [{ status: 401 }],
  short: [{ text: '好的，已经处理完成。' }],
};
const holds = new Set();
const textOf = message => typeof message?.content === 'string' ? message.content : JSON.stringify(message?.content || '');
function chunk(res, delta, finish) {
  res.write(`data: ${JSON.stringify({ id: 'tour', object: 'chat.completion.chunk', model: 'tour', choices: [{ index: 0, delta, finish_reason: finish || null }] })}\n\n`);
}
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', part => { body += part; });
  req.on('end', async () => {
    const payload = body ? JSON.parse(body) : {};
    const messages = payload.messages || [];
    if (req.url.endsWith('/models')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ data: [{ id: 'tour', object: 'model' }] }));
    }
    let lastUser = -1;
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === 'user' && !textOf(messages[i]).startsWith('<system-reminder>')) { lastUser = i; break; }
    }
    const userText = textOf(messages[lastUser]);
    if (!textOf(messages[0]).startsWith('You are wuu, a coding agent')) {
      // Auxiliary requests (conversation titles) get a short plain title.
      const title = userText.replace(/[\s\S]*?\[[a-z]+\]\s*/, '').replace(/["\n].*$/s, '').slice(0, 22) || '新对话';
      if (payload.stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        chunk(res, { role: 'assistant', content: title });
        chunk(res, {}, 'stop');
        return res.end('data: [DONE]\n\n');
      }
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ id: 'tour', object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: title }, finish_reason: 'stop' }], usage: { prompt_tokens: 20, completion_tokens: 6, total_tokens: 26 } }));
    }
    const name = Object.keys(scenarios).find(key => userText.includes(`[${key}]`)) || 'md';
    const step = messages.slice(lastUser + 1).filter(message => message.role === 'assistant').length;
    const plan = scenarios[name][Math.min(step, scenarios[name].length - 1)];
    if (plan.status) {
      res.writeHead(plan.status, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ error: { message: 'Incorrect API key provided.', type: 'invalid_request_error' } }));
    }
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
    if (plan.reasoning) chunk(res, { role: 'assistant', reasoning_content: plan.reasoning });
    for (const part of plan.text ? plan.text.match(/[\s\S]{1,48}/g) : []) chunk(res, { role: 'assistant', content: part });
    plan.calls?.forEach((call, index) => chunk(res, { tool_calls: [{ index, id: `call_${name}_${step}_${index}`, type: 'function', function: { name: call.name, arguments: JSON.stringify(call.args) } }] }));
    chunk(res, {}, plan.calls ? 'tool_calls' : 'stop');
    res.write(`data: ${JSON.stringify({ id: 'tour', choices: [], usage: { prompt_tokens: 1800 + step * 400, completion_tokens: 220, total_tokens: 2020 + step * 400 } })}\n\n`);
    res.end('data: [DONE]\n\n');
  });
});

// ---- driving the app ---------------------------------------------------------
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await evaluate(win, fn, arg)) return;
    await delay(50);
  }
  throw new Error(`Timed out: ${fn} ${arg === undefined ? '' : JSON.stringify(arg)}`);
}
const settle = win => evaluate(win, async () => {
  await document.fonts.ready;
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  await Promise.all(document.getAnimations().filter(a => a.playState === 'running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
});
// Click the visible control whose accessible name equals (or, with `loose`, contains) `label`.
const clickLabel = (win, label, loose = false) => evaluate(win, ({ label, loose }) => {
  const nameOf = element => (element.getAttribute('aria-label') || element.title || element.textContent || '').trim();
  const control = [...document.querySelectorAll('button, [role="button"], [role="menuitem"], [role="tab"], a, summary')]
    .find(element => element.getBoundingClientRect().width > 0 && !element.classList.contains('linux-window-control') && (loose ? nameOf(element).includes(label) : nameOf(element) === label));
  if (!control) throw new Error(`No control named ${label}`);
  control.click();
}, { label, loose });
const key = async (win, keyCode, modifiers = []) => {
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode, modifiers });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode, modifiers });
  await delay(150);
};
const draft = (win, text) => evaluate(win, value => {
  const textarea = document.querySelector('.composer textarea');
  textarea.focus();
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, value);
  textarea.dispatchEvent(new Event('input', { bubbles: true }));
}, text);
async function send(win, text, settleMs = 1800) {
  await draft(win, text);
  await delay(150);
  await evaluate(win, () => document.querySelector('.composer textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })));
  // A turn is over when the send button stops being a stop button and the stream is quiet.
  let quiet = 0;
  let last = '';
  for (let waited = 0; waited < 60000 && quiet < settleMs; waited += 250) {
    const state = await evaluate(win, () => `${document.querySelectorAll('.turn').length}:${document.querySelector('.composer-send-button')?.getAttribute('aria-label')}:${document.body.innerText.length}`);
    quiet = state === last ? quiet + 250 : 0;
    last = state;
    await delay(250);
  }
}
async function newThread(win, project) {
  await clickLabel(win, `在 ${project} 中新建对话`);
  await delay(500);
}
async function openThread(win, title) {
  const has = () => evaluate(win, needle => [...document.querySelectorAll('.thread-row-main')].some(row => (row.getAttribute('aria-label') || row.textContent).includes(needle)), title);
  if (!(await has())) {
    await evaluate(win, () => [...document.querySelectorAll('.project-row')].filter(row => (row.getAttribute('aria-label') || '').startsWith('展开')).forEach(row => row.click()));
    await delay(400);
  }
  if (!(await has())) {
    await evaluate(win, () => document.querySelector('.thread-list-more')?.click());
    await delay(400);
  }
  await evaluate(win, needle => {
    const row = [...document.querySelectorAll('.thread-row-main')].find(item => (item.getAttribute('aria-label') || item.textContent).includes(needle));
    if (!row) throw new Error(`No thread named ${needle}`);
    row.click();
  }, title);
  await delay(1000);
}
// Leave any settings page, panel, menu or dialog the previous stop opened.
async function reset(win) {
  await key(win, 'Escape');
  for (let i = 0; i < 12; i++) {
    const state = await evaluate(win, () => {
      const name = element => (element.getAttribute('aria-label') || element.textContent || '').trim();
      const visible = label => [...document.querySelectorAll('button')].find(element => name(element) === label && element.getBoundingClientRect().width > 0);
      const back = visible('返回应用');
      if (back) { back.click(); return 'settings'; }
      const close = visible('关闭右侧栏');
      if (close) { close.click(); return 'panel'; }
      return 'clean';
    });
    if (state === 'clean') break;
    await delay(350);
  }
  await delay(250);
}
async function setAppearance(win, { theme, size, width }) {
  win.setContentSize(width, height);
  await evaluate(win, ({ theme, size }) => {
    document.documentElement.setAttribute('data-theme', theme);
    document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
    window.dispatchEvent(new Event('wuu-content-size-change'));
  }, { theme, size });
  await delay(250);
}

// ---- geometry audit ----------------------------------------------------------
// Measurements of the rendered stop. Only `overflowX` gates the run; the rest is
// evidence a reviewer can compare between revisions.
const audit = win => evaluate(win, () => {
  // Computed colours are rgb()/rgba(), or color(srgb …) for color-mix() results
  // such as --ink-tertiary; ignoring the latter would hide the quietest text.
  const parse = value => {
    const mixed = value.match(/color\(srgb ([^)]+)\)/);
    if (mixed) { const p = mixed[1].split(/[ ,/]+/).filter(Boolean).map(Number); return { r: p[0] * 255, g: p[1] * 255, b: p[2] * 255, a: p.length > 3 ? p[3] : 1 }; }
    const match = value.match(/rgba?\(([^)]+)\)/);
    if (!match) return null;
    const p = match[1].split(/[ ,/]+/).filter(Boolean).map(Number);
    return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
  };
  const over = (top, bottom) => ({ r: top.r * top.a + bottom.r * (1 - top.a), g: top.g * top.a + bottom.g * (1 - top.a), b: top.b * top.a + bottom.b * (1 - top.a), a: 1 });
  const luminance = c => { const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }; return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b); };
  const contrast = (a, b) => { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const canvas = { r: 251, g: 251, b: 252, a: 1 };
  const dark = document.documentElement.dataset.theme === 'dark';
  const base = dark ? { r: 29, g: 32, b: 36, a: 1 } : canvas;
  const backgroundOf = element => {
    const layers = [];
    for (let node = element; node; node = node.parentElement) {
      const color = parse(getComputedStyle(node).backgroundColor);
      if (color && color.a > 0) { layers.push(color); if (color.a > 0.99) break; }
    }
    return layers.reduceRight((below, layer) => over(layer, below), base);
  };
  const visible = element => { const r = element.getBoundingClientRect(); const s = getComputedStyle(element); return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none' && parseFloat(s.opacity) > 0.05; };
  const radii = new Set(); const lowContrast = []; const smallTargets = []; const clipped = [];
  for (const element of document.querySelectorAll('body *')) {
    if (!visible(element)) continue;
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    const painted = ['Top', 'Right', 'Bottom', 'Left'].some(side => parseFloat(style[`border${side}Width`]) > 0) || (parse(style.backgroundColor)?.a ?? 0) > 0.02;
    if (painted && rect.width > 6 && style.borderRadius !== '0px') radii.add(style.borderRadius.split(' ')[0]);
    const hasText = [...element.childNodes].some(node => node.nodeType === 3 && node.textContent.trim());
    if (hasText) {
      const color = parse(style.color);
      const background = backgroundOf(element);
      if (color && parseFloat(style.fontSize) < 24) {
        const ratio = contrast(over(color, background), background);
        if (ratio < 4.5) lowContrast.push({ text: element.textContent.trim().slice(0, 28), ratio: Number(ratio.toFixed(2)), size: style.fontSize });
      }
      if (element.scrollWidth > element.clientWidth + 1 && style.overflow !== 'visible' && style.textOverflow !== 'ellipsis' && !element.closest('pre, code, textarea, [class*="scroll"], .monaco-editor')) {
        clipped.push(element.textContent.trim().slice(0, 28));
      }
    }
    if (element.matches('button, a[href], [role="button"], [role="menuitem"], [role="tab"], input, select, textarea') && (rect.width < 24 || rect.height < 24) && rect.width > 0) {
      smallTargets.push(`${(element.getAttribute('aria-label') || element.textContent || element.tagName).trim().slice(0, 24)} ${Math.round(rect.width)}x${Math.round(rect.height)}`);
    }
  }
  return {
    overflowX: document.documentElement.scrollWidth - window.innerWidth,
    radii: [...radii].sort((a, b) => parseFloat(a) - parseFloat(b)),
    lowContrast: lowContrast.slice(0, 8),
    lowContrastCount: lowContrast.length,
    smallTargets: [...new Set(smallTargets)].slice(0, 8),
    clipped: [...new Set(clipped)].slice(0, 8),
  };
});

const shots = [];
const audits = {};
let main;
const errors = [];
app.on('browser-window-created', (_event, win) => {
  main ||= win;
  win.webContents.on('console-message', event => {
    if (event.level === 'error' && !/Autofill|dbus|ssl_client/.test(event.message)) errors.push(event.message.slice(0, 200));
  });
});
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 900000);

// Open the right panel on one tool. A panel that reopens on its last tool shows
// no picker, so ask for it with the add button first.
async function openTool(win, label) {
  await openThread(win, '修复 formatDuration');
  await clickLabel(win, '打开右侧栏');
  await delay(700);
  const pick = () => evaluate(win, wanted => {
    const item = [...document.querySelectorAll('.workspace-tool-menu-item')].find(entry => entry.textContent.trim() === wanted);
    if (item) item.click();
    return !!item;
  }, label);
  if (!(await pick())) {
    await clickLabel(win, '选择工具');
    await delay(500);
    assert.ok(await pick(), `The tool picker offers ${label}`);
  }
}

// ---- stops -------------------------------------------------------------------
const settingsPage = name => async win => {
  await clickLabel(win, '账户菜单');
  await delay(350);
  await clickLabel(win, '设置');
  await delay(1000);
  await clickLabel(win, name);
  await delay(900);
};
const stops = [
  { id: 'home', run: async win => { await clickLabel(win, '新对话'); await delay(900); } },
  { id: 'conversation', run: win => openThread(win, '请把设置页') },
  { id: 'conversation-tools', run: async win => {
    await openThread(win, '修复 formatDuration');
    await evaluate(win, () => document.querySelector('.turn-process-toggle')?.click());
    await delay(600);
    await evaluate(win, () => document.querySelector('.process-surface-row')?.click());
    await delay(600);
  } },
  { id: 'conversation-reasoning', run: async win => {
    await openThread(win, '这两种方案我该怎么选');
    await evaluate(win, () => document.querySelector('.turn-process-toggle')?.click());
    await delay(600);
  } },
  { id: 'conversation-error', run: async win => {
    await openThread(win, '[unauthorized]');
    await evaluate(win, () => document.querySelector('.process-surface-row, .turn-notice')?.click());
    await delay(600);
  } },
  { id: 'menu-thread', narrow: false, run: async win => {
    await openThread(win, '这两种方案我该怎么选');
    await evaluate(win, () => {
      const row = [...document.querySelectorAll('.thread-row-main')].find(item => item.textContent.includes('这两种方案我该怎么选'));
      const rect = row.getBoundingClientRect();
      row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: rect.x + rect.width / 2, clientY: rect.y + rect.height / 2, button: 2 }));
    });
    await delay(450);
  } },
  { id: 'menu-plus', narrow: false, run: async win => { await openThread(win, '请把设置页'); await clickLabel(win, '更多操作'); await delay(450); } },
  { id: 'menu-permission', narrow: false, run: async win => { await openThread(win, '请把设置页'); await clickLabel(win, '权限模式', true); await delay(450); } },
  { id: 'menu-runtime', narrow: false, run: async win => { await openThread(win, '请把设置页'); await clickLabel(win, 'tour', true); await delay(550); } },
  { id: 'menu-account', narrow: false, run: async win => { await clickLabel(win, '账户菜单'); await delay(450); } },
  { id: 'search', narrow: false, run: async win => { await clickLabel(win, '搜索对话'); await delay(650); } },
  { id: 'panel-tools', run: async win => {
    await openThread(win, '修复 formatDuration');
    await clickLabel(win, '打开右侧栏');
    await delay(750);
    if (!(await evaluate(win, () => !!document.querySelector('.workspace-tool-menu-item')))) {
      await clickLabel(win, '选择工具');
      await delay(450);
    }
  } },
  { id: 'panel-files', run: async win => { await openTool(win, '文件'); await delay(1400); } },
  { id: 'panel-review', run: async win => { await openTool(win, '审查'); await delay(2000); } },
  { id: 'plugins', run: async win => { await clickLabel(win, '插件'); await delay(1400); } },
  ...[['settings-models', '模型服务'], ['settings-agents', 'Agent'], ['settings-runtime', '运行'], ['settings-general', '常规'], ['settings-appearance', '外观'], ['settings-mcp', 'MCP 服务器'], ['settings-usage', '用量'], ['settings-archive', '归档']]
    .map(([id, label]) => ({ id, run: settingsPage(label) })),
];

// ---- journeys ----------------------------------------------------------------
const journeys = [];
function journey(name, ok, detail) {
  journeys.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  (${detail})` : ''}`);
}
const focusDescription = win => evaluate(win, () => { const a = document.activeElement; return a ? `${a.tagName.toLowerCase()}.${String(a.className).split(' ')[0]}` : 'none'; });
const composerValue = win => evaluate(win, () => document.querySelector('.composer textarea')?.value ?? null);
async function runJourneys(win) {
  await reset(win);
  await setAppearance(win, { theme: 'light', size: 14.5, width: 1280 });

  await openThread(win, '这两种方案我该怎么选');
  await draft(win, '草稿：先别发送');
  await clickLabel(win, '账户菜单');
  await delay(300);
  await clickLabel(win, '设置');
  await delay(900);
  await clickLabel(win, '返回应用');
  await delay(900);
  journey('A draft survives opening Settings and returning', (await composerValue(win)) === '草稿：先别发送');
  await openThread(win, '修复 formatDuration');
  journey('Another conversation keeps its own, empty, draft', (await composerValue(win)) === '');
  await openThread(win, '这两种方案我该怎么选');
  journey('The draft returns with its conversation', (await composerValue(win)) === '草稿：先别发送');
  await draft(win, '');

  await openThread(win, '修复 formatDuration');
  await clickLabel(win, '插件');
  await delay(900);
  await openThread(win, '修复 formatDuration');
  const selected = await evaluate(win, () => [...document.querySelectorAll('.thread-row.active .thread-row-main')].map(row => row.textContent).join('|'));
  journey('The selected row is restored after visiting Plugins', selected.includes('修复 formatDuration'), selected);

  await evaluate(win, () => document.querySelector('.composer textarea').focus());
  await clickLabel(win, '更多操作');
  await delay(350);
  await key(win, 'Escape');
  await delay(300);
  journey('Escape closes the plus menu and focus returns to the composer', /textarea|composer-tool-button/.test(await focusDescription(win)), await focusDescription(win));

  await evaluate(win, () => document.querySelector('.composer textarea').focus());
  await key(win, 'P', ['control']);
  await delay(700);
  journey('Ctrl+P opens search with its input focused', await evaluate(win, () => !!document.querySelector('.conversation-search-dialog') && document.activeElement?.tagName === 'INPUT'));
  await key(win, 'Escape');
  await delay(500);
  journey('Escape closes search and focus returns to the composer', !(await evaluate(win, () => !!document.querySelector('.conversation-search-dialog'))) && /textarea/.test(await focusDescription(win)), await focusDescription(win));

  // A long conversation keeps its reading position across a thread switch.
  if (!(await evaluate(win, () => [...document.querySelectorAll('.thread-row-main')].some(row => row.textContent.includes('长文'))))) {
    await clickLabel(win, '新对话');
    await delay(600);
    await send(win, '[long] 给我一篇长文');
  }
  await openThread(win, '长文');
  const findScroller = 'const s = [...document.querySelectorAll("div")].find(e => e.scrollHeight > e.clientHeight + 400 && getComputedStyle(e).overflowY !== "visible" && e.getBoundingClientRect().width > 400);';
  const position = await evaluate(win, `() => { ${findScroller} if (s) s.scrollTop = Math.floor((s.scrollHeight - s.clientHeight) / 2); return s ? Math.round(s.scrollTop) : -1; }`);
  await delay(400);
  await openThread(win, '修复 formatDuration');
  await openThread(win, '长文');
  await delay(600);
  const restored = await evaluate(win, `() => { ${findScroller} return s ? Math.round(s.scrollTop) : -1; }`);
  journey('A long conversation keeps its reading position across a thread switch', position > 0 && Math.abs(restored - position) < 60, `${position} -> ${restored}`);
  await reset(win);
}

// ---- run ---------------------------------------------------------------------
async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  write(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'tour', engines, providers: { tour: { type: 'openai-compatible', base_url: endpoint, api_key: 'tour-only', model: 'tour' } } }));
  const stamp = '2026-09-20T00:00:00Z';
  write(path.join(home, 'projects.json'), JSON.stringify({
    projects: [
      { id: 'wuu-desktop', name: 'wuu-desktop', path: repo, created_at: stamp, updated_at: stamp },
      { id: 'landing-site', name: 'landing-site', path: site, created_at: stamp, updated_at: stamp },
    ],
    active_context: { kind: 'project', project_id: 'wuu-desktop', cwd: repo },
  }));
  write(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'zh-CN', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  main.setContentSize(1280, height);
  await waitFor(main, () => document.querySelector('.composer textarea'));

  // Conversations with every kind of turn, made through the real composer.
  await send(main, '[md] 请把设置页的间距整理一下，并说明改了什么');
  for (const [project, text] of [
    ['wuu-desktop', '[tools] 修复 formatDuration 不显示分钟的问题'],
    ['wuu-desktop', '[thinking] 这两种方案我该怎么选？'],
    ['wuu-desktop', '[unauthorized] 检查一下发布流程'],
    ['landing-site', '[short] 检查一下首页的标题层级'],
  ]) {
    await newThread(main, project);
    await send(main, text, /tools/.test(text) ? 2500 : 1800);
  }
  await evaluate(main, () => document.querySelectorAll('.project-row').forEach(row => { if ((row.getAttribute('aria-label') || '').startsWith('展开')) row.click(); }));

  for (const appearance of matrix) {
    for (const stop of stops) {
      if (only && !only.some(fragment => stop.id.includes(fragment))) continue;
      // Overlays close when the window resizes, so they are captured wide only.
      if (appearance.width < 1000 && stop.narrow === false) continue;
      // Navigation needs the docked sidebar; narrow captures resize afterwards.
      await setAppearance(main, { ...appearance, width: 1280 });
      await reset(main);
      await stop.run(main);
      if (appearance.width !== 1280) {
        main.setContentSize(appearance.width, height);
        await delay(500);
      }
      await settle(main);
      const name = `${stop.id}.${appearance.theme}.${appearance.size}.${appearance.width}.png`;
      fs.writeFileSync(path.join(output, name), (await main.webContents.capturePage()).toPNG());
      shots.push(name);
      const result = await audit(main);
      audits[name] = result;
      assert.ok(result.overflowX <= 1, `${name} overflows horizontally by ${result.overflowX}px`);
    }
  }
  await setAppearance(main, { theme: 'light', size: 14.5, width: 1280 });
  if (!only) await runJourneys(main);
  assert.deepEqual(journeys.filter(item => !item.ok).map(item => item.name), [], 'Every journey passes.');
  assert.deepEqual(errors, [], 'The renderer logged no errors.');

  fs.writeFileSync(path.join(output, 'audit.json'), `${JSON.stringify(audits, null, 2)}\n`);
  fs.writeFileSync(path.join(output, 'report.json'), `${JSON.stringify({
    electron: process.versions.electron,
    chromium: process.versions.chrome,
    platform: process.platform,
    matrix,
    stops: stops.map(stop => stop.id),
    shots,
    journeys,
  }, null, 2)}\n`);
  console.log(`ui tour passed; ${shots.length} captures and ${journeys.length} journeys in ${output}`);
}

run().then(() => {
  clearTimeout(timeout);
  server.close();
  app.quit();
}).catch(async error => {
  console.error(error, 'FIXTURE', fixture);
  if (main) fs.writeFileSync(path.join(output, 'failure.png'), (await main.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(output, 'audit.json'), `${JSON.stringify(audits, null, 2)}\n`);
  fs.writeFileSync(path.join(output, 'report.json'), `${JSON.stringify({ failed: String(error.message || error), matrix, shots, journeys }, null, 2)}\n`);
  server.close();
  app.exit(1);
});
