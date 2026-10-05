// README media, captured from the real desktop app and Go core. A scripted
// local model provider drives real tool calls against synthetic projects, so
// no account, key or personal data appears in the output.
//
// Build the app being described (usually a release tag checkout), then run
// with Node from this checkout. A display is required (xvfb-run on Linux), and
// ffmpeg encodes the GIFs:
//   (cd <app>/desktop && npm run build:core && npm run build)
//   node desktop/scripts/generate-readme-media.cjs [<app>/desktop]
// Output: landing/assets/readme/{hero-light,hero-dark,files,models,plugins}-<lang>.* and demo-<lang>.gif
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

const repository = path.resolve(__dirname, '../..');
const output = path.join(repository, 'landing/assets/readme');
const brand = path.join(repository, 'landing/assets/brand');

if (!process.versions.electron) {
  const appDesktop = path.resolve(process.argv[2] || path.join(repository, 'desktop'));
  for (const file of ['out/main/index.js', 'build/bin/wuu-core']) {
    assert.ok(fs.existsSync(path.join(appDesktop, file)), `Build ${file} in ${appDesktop} first`);
  }
  const temporaryRoot = path.join(repository, 'desktop/.tmp');
  fs.mkdirSync(temporaryRoot, { recursive: true });
  fs.mkdirSync(output, { recursive: true });
  const env = { ...process.env };
  delete env.ELECTRON_RUN_AS_NODE;
  const flags = process.platform === 'linux' ? ['--no-sandbox', '--disable-gpu'] : [];
  for (const lang of ['en', 'zh']) {
    const temporary = fs.mkdtempSync(path.join(temporaryRoot, `readme-media-${lang}-`));
    try {
      const capture = spawnSync(require(path.join(appDesktop, 'node_modules/electron')), [...flags, __filename, appDesktop, lang, temporary], {
        env, stdio: 'inherit', timeout: 900_000,
      });
      if (capture.error) throw capture.error;
      assert.equal(capture.status, 0, `Capture failed for ${lang}`);
      const encode = spawnSync('ffmpeg', [
        '-v', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', path.join(temporary, 'frames.txt'),
        '-vf', 'fps=12,scale=1600:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle',
        '-loop', '0', path.join(output, `demo-${lang}.gif`),
      ], { stdio: 'inherit' });
      if (encode.error) throw encode.error;
      assert.equal(encode.status, 0, `ffmpeg failed for ${lang}`);
    } finally {
      fs.rmSync(temporary, { recursive: true, force: true });
    }
  }
} else {
  capture(...process.argv.slice(-3));
}

function capture(desktop, lang, temporary) {
  const os = require('node:os');
  const { pathToFileURL } = require('node:url');
  const { app, BrowserWindow } = require('electron');
  const zh = lang === 'zh';
  app.commandLine.appendSwitch('force-device-scale-factor', '2');
  app.commandLine.appendSwitch('force-color-profile', 'srgb');
  const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-readme-'));
  const home = path.join(fixture, 'wuu-home');
  const userHome = path.join(fixture, 'user-home');
  const projects = path.join(fixture, 'projects');
  for (const dir of [home, userHome, projects]) fs.mkdirSync(dir);
  app.setPath('userData', path.join(fixture, 'profile'));
  process.env.WUU_HOME = home;
  process.env.HOME = userHome;
  // The demo bug only reproduces west of UTC.
  process.env.TZ = 'America/Los_Angeles';
  process.env.WUU_DESKTOP_CORE = path.join(desktop, 'build/bin/wuu-core');
  delete process.env.ELECTRON_RENDERER_URL;
  // Keeps plugin tools out of the scripted turns; the Plugins page still lists every plugin.
  process.env.WUU_SAFE_MODE = '1';
  process.env.GIT_AUTHOR_NAME = process.env.GIT_COMMITTER_NAME = 'Ada Lovelace';
  process.env.GIT_AUTHOR_EMAIL = process.env.GIT_COMMITTER_EMAIL = 'ada@example.test';

  // ---- labels ----------------------------------------------------------------
  const L = zh ? {
    openRight: '打开右侧栏', newPage: '新建页面', account: '账户菜单', settings: '设置', plugins: '插件',
    back: '返回应用', closeRight: '关闭右侧栏', newIn: name => `在 ${name} 中新建对话`,
    files: '文件', review: '审查', providers: '模型服务', addProvider: '添加服务',
  } : {
    openRight: 'Open right sidebar', newPage: 'New page', account: 'Account menu', settings: 'Settings', plugins: 'Plugins',
    back: 'Back to app', closeRight: 'Close right sidebar', newIn: name => `Start a new conversation in ${name}`,
    files: 'Files', review: 'Review', providers: 'Model providers', addProvider: 'Add a provider',
  };

  // ---- synthetic projects -----------------------------------------------------
  const write = (file, text) => {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, text);
  };
  const sh = (cwd, command) => {
    const result = spawnSync('sh', ['-c', command], { cwd, encoding: 'utf8' });
    assert.equal(result.status, 0, `${command}\n${result.stderr}`);
  };
  const ledger = path.join(projects, 'pocket-ledger');
  const site = path.join(projects, 'landing-site');
  write(path.join(ledger, 'package.json'), `${JSON.stringify({ name: 'pocket-ledger', private: true, type: 'module', scripts: { test: 'node --test' } }, null, 2)}\n`);
  write(path.join(ledger, 'README.md'), '# Pocket Ledger\n\nA small offline ledger for household spending.\n');
  write(path.join(ledger, 'src/totals.js'), `export function monthKey(isoDate) {
  const date = new Date(isoDate);
  const month = date.getMonth() + 1;
  return \`\${date.getFullYear()}-\${String(month).padStart(2, '0')}\`;
}

export function monthlyTotals(entries) {
  const totals = new Map();
  for (const entry of entries) {
    const key = monthKey(entry.date);
    totals.set(key, (totals.get(key) ?? 0) + entry.amount);
  }
  return Object.fromEntries(totals);
}
`);
  write(path.join(ledger, 'src/import.js'), "import { parse } from './csv.js';\n\nexport function importEntries(text) {\n  return parse(text).map(row => ({ date: row.date, amount: Number(row.amount), note: row.note ?? '' }));\n}\n");
  write(path.join(ledger, 'src/csv.js'), "export function parse(text) {\n  const [header, ...rows] = text.trim().split('\\n');\n  const keys = header.split(',');\n  return rows.map(line => Object.fromEntries(line.split(',').map((value, i) => [keys[i], value])));\n}\n");
  write(path.join(ledger, 'test/totals.test.js'), `import { test } from 'node:test';
import assert from 'node:assert/strict';
import { monthlyTotals } from '../src/totals.js';

test('groups entries by calendar month', () => {
  const entries = [
    { date: '2026-09-30', amount: 42 },
    { date: '2026-10-01', amount: 18 },
  ];
  assert.deepEqual(monthlyTotals(entries), { '2026-09': 42, '2026-10': 18 });
});
`);
  write(path.join(ledger, 'docs/design.md'), zh ? `# 设计说明

## 金额与日期

账目以 ISO 日期字符串保存，例如 \`2026-10-01\`。所有按月汇总都以账目记录的日历日期为准，不随查看者所在的时区变化。

金额以分为单位的整数保存，避免浮点误差。显示时再按用户的地区格式化。

## 导入

导入 CSV 时，未知的列会被忽略，缺少日期或金额的行会被跳过，并在导入结果里列出。
` : `# Design notes

## Amounts and dates

Entries store ISO date strings such as \`2026-10-01\`. Monthly totals always follow the calendar date written on the entry, never the viewer's time zone.

Amounts are stored as integer cents to avoid floating-point drift, and are formatted for the user's locale only when displayed.

## Imports

When importing a CSV, unknown columns are ignored. Rows without a date or amount are skipped and listed in the import summary.
`);
  sh(ledger, 'git init -q -b main && git add -A && git commit -q -m "feat: monthly totals"');
  write(path.join(site, 'index.html'), '<!doctype html>\n<title>Pocket Ledger</title>\n<h1>Spend calmly</h1>\n');
  sh(site, 'git init -q -b main && git add -A && git commit -q -m "chore: initial page"');

  // ---- scripted provider ------------------------------------------------------
  const fixEdits = [
    {
      path: 'src/totals.js',
      old_text: [
        '  const date = new Date(isoDate);',
        '  const month = date.getMonth() + 1;',
        "  return `${date.getFullYear()}-${String(month).padStart(2, '0')}`;",
      ].join('\n'),
      new_text: [
        '  // ISO dates parse as UTC midnight; read them in UTC.',
        '  const date = new Date(isoDate);',
        '  const month = date.getUTCMonth() + 1;',
        "  return `${date.getUTCFullYear()}-${String(month).padStart(2, '0')}`;",
      ].join('\n'),
    },
    {
      path: 'test/totals.test.js',
      old_text: '});\n',
      new_text: [
        '});',
        '',
        "test('keeps New Year entries in January', () => {",
        "  assert.deepEqual(monthlyTotals([{ date: '2027-01-01', amount: 5 }]), { '2027-01': 5 });",
        '});',
        '',
      ].join('\n'),
    },
  ];
  // Programmatic tool calling: every tool runs inside run_code.
  const program = (description, lines) => ({ name: 'run_code', args: { description, code: lines.join('\n') } });
  const show = "const text = result => result.model_text ?? result.content.map(part => part.text).join('');";
  const runTests = description => program(description, [
    show,
    "try { console.log(text(await tools.bash({ command: 'npm test' }))); } catch (error) { console.log(text(error.result)); }",
  ]);
  const fixPlan = ([read, test, edit, retest], [say1, say2, say3, done]) => [
    { text: say1, calls: [program(read, [
      show,
      "const [file, uses] = await Promise.all([tools.read_file({ path: 'src/totals.js' }), tools.grep({ pattern: 'monthKey', path: 'src' })]);",
      'console.log(text(file));',
      'console.log(text(uses));',
    ])] },
    { text: say2, calls: [runTests(test)] },
    { text: say3, calls: [program(edit, [
      show,
      ...fixEdits.map(edit => `console.log(text(await tools.edit_file(${JSON.stringify(edit)})));`),
    ])] },
    { calls: [runTests(retest)] },
    { text: done },
  ];
  const S = zh ? {
    fixPrompt: '时区在 UTC 以西的用户反馈：10 月 1 日的支出被算进了 9 月。找到原因修好它，并确认测试通过。',
    fixTitle: '修复按月汇总的时区问题',
    filesQuote: '导入 CSV 时，未知的列会被忽略，缺少日期或金额的行会被跳过，并在导入结果里列出。',
    fix: fixPlan(['读取按月分组的逻辑', '运行测试复现问题', '按 UTC 读取年月并补充测试', '再次运行测试'], [
      '先看看账目是怎样按月分组的。',
      '先跑一遍测试，确认能复现。',
      '原因找到了：`new Date(\'2026-10-01\')` 得到的是 UTC 零点，在洛杉矶仍是 9 月 30 日，而 `getMonth()` 读的是本地日期。改成按 UTC 读取年月，再补一个跨年的测试。',
      '已修复。ISO 日期按 UTC 零点解析，但 `monthKey` 用本地时间读回月份，所以 UTC 以西的用户会看到每月 1 日的账目落进上个月。\n\n- `src/totals.js` 改为按 UTC 读取年和月\n- `test/totals.test.js` 新增跨年用例\n\n`npm test` 全部通过：2 个测试，0 个失败。',
    ]),
    explainPrompt: '导入的 CSV 是怎么变成账目的？',
    explainTitle: 'CSV 导入流程',
    explain: [{ text: 'CSV 导入分两步：\n\n1. `csv.js` 把文本按行拆开，用表头作为字段名。\n2. `import.js` 把每一行转成账目，金额转成数字，备注默认为空。\n\n```js\nexport function importEntries(text) {\n  return parse(text).map(row => ({ date: row.date, amount: Number(row.amount) }));\n}\n```\n\n目前没有处理带引号的逗号，需要的话我可以补上。' }],
    thinkPrompt: '预算数据该放 SQLite 还是普通 JSON 文件？',
    thinkTitle: '预算存储用 SQLite 还是 JSON',
    think: [{ reasoning: '数据量小、单用户、需要可读可备份。SQLite 的优势在查询和并发，这里都用不上。', text: '建议先用 JSON 文件：数据量小，便于备份和人工检查。等出现跨月查询或多设备同步的需求，再迁移到 SQLite。' }],
    sitePrompt: '把首页的标题层级整理一下',
    siteTitle: '整理首页标题层级',
    site: [{ text: '首页只有一个 `h1`，层级已经正确。我把副标题改成了段落，避免和导航混在一起。' }],
  } : {
    fixPrompt: 'Users west of UTC say October 1 expenses show up in September. Find the cause, fix it, and make sure the tests pass.',
    fixTitle: 'Fix monthly totals west of UTC',
    filesQuote: 'When importing a CSV, unknown columns are ignored. Rows without a date or amount are skipped and listed in the import summary.',
    fix: fixPlan(['Read how entries are grouped by month', 'Run the tests to reproduce', 'Read the month in UTC and add a test', 'Run the tests again'], [
      'Let me look at how entries are grouped by month.',
      'I\'ll run the tests first to reproduce it.',
      'Found it: `new Date(\'2026-10-01\')` is midnight UTC, which is still September 30 in Los Angeles, and `getMonth()` reads the local date. I\'ll read the year and month in UTC and add a year-boundary test.',
      'Fixed. ISO dates are parsed as midnight UTC, but `monthKey` read the month back in local time, so anyone west of UTC saw first-of-month entries land in the previous month.\n\n- `src/totals.js` now reads the year and month in UTC.\n- `test/totals.test.js` adds a New Year case.\n\n`npm test` passes: 2 tests, 0 failures.',
    ]),
    explainPrompt: 'How does an imported CSV become ledger entries?',
    explainTitle: 'How CSV imports become entries',
    explain: [{ text: 'An import takes two steps:\n\n1. `csv.js` splits the text into rows and uses the header for field names.\n2. `import.js` turns each row into an entry, converting the amount to a number.\n\n```js\nexport function importEntries(text) {\n  return parse(text).map(row => ({ date: row.date, amount: Number(row.amount) }));\n}\n```\n\nQuoted commas are not handled yet. I can add that if you need it.' }],
    thinkPrompt: 'Should budgets live in SQLite or plain JSON files?',
    thinkTitle: 'SQLite or JSON for budgets',
    think: [{ reasoning: 'Small data, a single user, and it should stay readable and easy to back up. SQLite helps with queries and concurrency, neither of which matters yet.', text: 'Start with JSON files: the data is small and easy to back up or inspect by hand. Move to SQLite when you need cross-month queries or sync between devices.' }],
    sitePrompt: 'Tighten the heading hierarchy on the home page',
    siteTitle: 'Tighten home page headings',
    site: [{ text: 'The page already has a single `h1`. I turned the tagline into a paragraph so it no longer competes with the navigation.' }],
  };
  const scenarios = [
    { prompt: S.fixPrompt, title: S.fixTitle, plan: S.fix },
    { prompt: S.explainPrompt, title: S.explainTitle, plan: S.explain },
    { prompt: S.thinkPrompt, title: S.thinkTitle, plan: S.think },
    { prompt: S.sitePrompt, title: S.siteTitle, plan: S.site },
  ];
  // Pacing makes streaming and tool activity readable in the recording.
  let pace = false;
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const textOf = message => typeof message?.content === 'string' ? message.content : JSON.stringify(message?.content || '');
  const chunk = (res, delta, finish) => {
    res.write(`data: ${JSON.stringify({ id: 'readme', object: 'chat.completion.chunk', model: 'readme', choices: [{ index: 0, delta, finish_reason: finish || null }] })}\n\n`);
  };
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', part => { body += part; });
    req.on('end', async () => {
      const payload = body ? JSON.parse(body) : {};
      const messages = payload.messages || [];
      if (req.url.endsWith('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ data: [{ id: 'gpt-6.1-sol', object: 'model' }] }));
      }
      let lastUser = -1;
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].role === 'user' && !textOf(messages[i]).startsWith('<system-reminder>')) { lastUser = i; break; }
      }
      const userText = textOf(messages[lastUser]);
      const scenario = scenarios.find(item => userText.includes(item.prompt)) || scenarios[1];
      // Requests without the agent system prompt ask for a conversation title.
      if (!textOf(messages[0]).startsWith('You are wuu, a coding agent')) {
        if (payload.stream) {
          res.writeHead(200, { 'Content-Type': 'text/event-stream' });
          chunk(res, { role: 'assistant', content: scenario.title });
          chunk(res, {}, 'stop');
          return res.end('data: [DONE]\n\n');
        }
        res.writeHead(200, { 'Content-Type': 'application/json' });
        return res.end(JSON.stringify({ id: 'readme', object: 'chat.completion', choices: [{ index: 0, message: { role: 'assistant', content: scenario.title }, finish_reason: 'stop' }], usage: { prompt_tokens: 20, completion_tokens: 6, total_tokens: 26 } }));
      }
      const step = messages.slice(lastUser + 1).filter(message => message.role === 'assistant').length;
      const plan = scenario.plan[Math.min(step, scenario.plan.length - 1)];
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      if (pace) await delay(450);
      if (plan.reasoning) chunk(res, { role: 'assistant', reasoning_content: plan.reasoning });
      const size = pace ? (zh ? 3 : 7) : 48;
      for (const part of plan.text ? plan.text.match(new RegExp(`[\\s\\S]{1,${size}}`, 'g')) : []) {
        chunk(res, { role: 'assistant', content: part });
        if (pace) await delay(22);
      }
      if (pace && plan.calls) await delay(350);
      plan.calls?.forEach((call, index) => chunk(res, { tool_calls: [{ index, id: `call_${step}_${index}`, type: 'function', function: { name: call.name, arguments: JSON.stringify(call.args) } }] }));
      chunk(res, {}, plan.calls ? 'tool_calls' : 'stop');
      res.write(`data: ${JSON.stringify({ id: 'readme', choices: [], usage: { prompt_tokens: 4200 + step * 900, completion_tokens: 260, total_tokens: 4460 + step * 900 } })}\n\n`);
      res.end('data: [DONE]\n\n');
    });
  });

  // ---- driving the app --------------------------------------------------------
  const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
  const waitFor = async (win, fn, arg, timeout = 30000) => {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      if (await evaluate(win, fn, arg)) return;
      await delay(50);
    }
    throw new Error(`Timed out: ${fn} ${arg === undefined ? '' : JSON.stringify(arg)}`);
  };
  const settle = win => evaluate(win, async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    await Promise.all(document.getAnimations().filter(a => a.playState === 'running' && Number.isFinite(a.effect.getComputedTiming().endTime)).map(a => a.finished.catch(() => {})));
  });
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
  const submit = win => evaluate(win, () => document.querySelector('.composer textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })));
  // A turn is done once the transcript and send button stop changing.
  const waitTurn = async (win, settleMs = 2000) => {
    let quiet = 0;
    let last = '';
    for (let waited = 0; waited < 90000 && quiet < settleMs; waited += 250) {
      const state = await evaluate(win, () => `${document.querySelectorAll('.turn').length}:${document.querySelector('.composer-send-button')?.getAttribute('aria-label')}:${document.body.innerText.length}`);
      quiet = state === last ? quiet + 250 : 0;
      last = state;
      await delay(250);
    }
  };
  const send = async (win, text, settleMs) => {
    await draft(win, text);
    await delay(150);
    await submit(win);
    await waitTurn(win, settleMs);
  };
  const newThread = async (win, project) => {
    await clickLabel(win, L.newIn(project));
    await delay(500);
  };
  // Collapsed workspaces hide their conversations from the sidebar.
  const expandWorkspaces = async win => {
    await evaluate(win, () => {
      for (const row of document.querySelectorAll('button.project-row:not(.expanded)')) row.click();
    });
    await delay(400);
  };
  const openThread = async (win, title) => {
    await expandWorkspaces(win);
    await evaluate(win, needle => {
      const row = [...document.querySelectorAll('.thread-row-main')].find(item => (item.getAttribute('aria-label') || item.textContent).includes(needle));
      if (!row) throw new Error(`No thread named ${needle}`);
      row.click();
    }, title);
    await delay(1000);
  };
  // Leave settings, close the right panel and composer menus.
  const reset = async win => {
    win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
    win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
    for (let i = 0; i < 12; i++) {
      const state = await evaluate(win, ({ back, close }) => {
        const name = element => (element.getAttribute('aria-label') || element.textContent || '').trim();
        const visible = label => [...document.querySelectorAll('button')].find(element => name(element) === label && element.getBoundingClientRect().width > 0);
        const control = visible(back) || visible(close) || document.querySelector('.composer button[aria-expanded="true"]');
        control?.click();
        return !control;
      }, { back: L.back, close: L.closeRight });
      if (state) break;
      await delay(350);
    }
    await delay(250);
  };
  const openReviewFile = async (win, name) => {
    const until = Date.now() + 10000;
    while (!(await evaluate(win, name => {
      // Some builds open Review on a stacked diff, others on a file list first.
      if ([...document.querySelectorAll('.workspace-review-file-name')].some(item => item.textContent.trim() === name)) return true;
      const match = [...document.querySelectorAll('.workspace-review-row')].find(item => item.textContent.trim().startsWith(name));
      match?.click();
      return !!match;
    }, name))) {
      assert.ok(Date.now() < until, `Review lists ${name}`);
      await delay(200);
    }
    await delay(1200);
  };
  // Release builds run on macOS. Stamp the platform so the window chrome
  // reserves the traffic-light corner, and draw the lights where the app
  // positions them (x 18, y 17).
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
  const setTheme = async (win, theme) => {
    await evaluate(win, theme => document.documentElement.setAttribute('data-theme', theme), theme);
    await delay(200);
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
  // Open a file from the Files tree, which renders inside a shadow root.
  const openFile = async (win, filePath) => {
    for (const target of [filePath.split('/')[0], filePath]) {
      assert.ok(await evaluate(win, wanted => {
        const root = document.querySelector('.workspace-file-tree-frame file-tree-container')?.shadowRoot;
        const row = [...(root?.querySelectorAll('[data-item-path]') ?? [])].find(item => item.getAttribute('data-item-path').replace(/\/$/, '') === wanted);
        row?.click();
        return !!row;
      }, target), `The file tree shows ${target}`);
      await delay(700);
    }
    await waitFor(win, () => document.querySelector('.workspace-file-resource.active .file-selection-content'));
    await delay(600);
  };
  // Select a sentence in the rendered document so the selection actions appear.
  const selectQuote = async (win, quote) => {
    assert.ok(await evaluate(win, wanted => {
      const content = document.querySelector('.workspace-file-resource.active .file-selection-content');
      const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode()) && !node.nodeValue.includes(wanted));
      if (!node) return false;
      const start = node.nodeValue.indexOf(wanted);
      const range = document.createRange();
      range.setStart(node, start);
      range.setEnd(node, start + wanted.length);
      window.getSelection().removeAllRanges();
      window.getSelection().addRange(range);
      document.dispatchEvent(new Event('selectionchange'));
      return true;
    }, quote), `The document contains ${quote}`);
    await waitFor(win, () => document.querySelector('.file-selection-action-menu[role=toolbar]'));
    await delay(400);
  };
  const settingsPage = async (win, name) => {
    await clickLabel(win, L.account);
    await delay(350);
    await clickLabel(win, L.settings);
    await delay(1000);
    await clickLabel(win, name);
    await delay(900);
  };
  const shots = {};
  const shot = async (win, name) => {
    await evaluate(win, () => document.activeElement?.blur?.());
    await settle(win);
    await delay(300);
    const file = path.join(temporary, `${name}.png`);
    fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
    shots[name] = file;
  };
  // Bounds of the matched elements in window pixels, padded and kept in the
  // window. The app zooms its page, so CSS pixels are rescaled to the window.
  const bounds = (win, selector, pad) => evaluate(win, ({ selector, pad, width }) => {
    const zoom = width / innerWidth;
    const rects = [...document.querySelectorAll(selector)].map(element => element.getBoundingClientRect());
    const left = Math.max(0, Math.min(...rects.map(rect => rect.left)) - pad);
    const top = Math.max(0, Math.min(...rects.map(rect => rect.top)) - pad);
    const right = Math.min(innerWidth, Math.max(...rects.map(rect => rect.right)) + pad);
    const bottom = Math.min(innerHeight, Math.max(...rects.map(rect => rect.bottom)) + pad);
    return { x: Math.round(left * zoom), y: Math.round(top * zoom), width: Math.round((right - left) * zoom), height: Math.round((bottom - top) * zoom) };
  }, { selector, pad, width: win.getContentSize()[0] });

  // Capture frames continuously while `body` runs; ffmpeg reads the timings.
  const record = async (win, body) => {
    const dir = path.join(temporary, 'frames');
    fs.mkdirSync(dir);
    const frames = [];
    let recording = true;
    const loop = (async () => {
      while (recording) {
        const at = Date.now();
        const file = path.join(dir, `${String(frames.length).padStart(5, '0')}.png`);
        fs.writeFileSync(file, (await win.webContents.capturePage()).toPNG());
        frames.push({ file, at });
        await delay(Math.max(0, 90 - (Date.now() - at)));
      }
    })();
    await body();
    recording = false;
    await loop;
    const lines = frames.flatMap((frame, index) => {
      const next = frames[index + 1];
      return [`file '${frame.file}'`, `duration ${((next ? next.at - frame.at : 3000) / 1000).toFixed(3)}`];
    });
    // The concat demuxer ignores the final duration unless the last file repeats.
    lines.push(`file '${frames.at(-1).file}'`);
    fs.writeFileSync(path.join(temporary, 'frames.txt'), `${lines.join('\n')}\n`);
  };

  // ---- framing ----------------------------------------------------------------
  // Lay captures out on an offscreen page at the capture scale, then export.
  const compose = async (name, size, body, format) => {
    const page = path.join(temporary, `${name}.html`);
    fs.writeFileSync(page, `<!doctype html><meta charset="utf-8"><style>
      html, body { margin: 0; background: transparent; }
      .stage { position: relative; width: ${size.width}px; height: ${size.height}px; overflow: hidden; }
      .backdrop { position: absolute; inset: -12px; width: calc(100% + 24px); height: calc(100% + 24px); object-fit: cover; filter: blur(2.5px); }
      .shot { position: absolute; overflow: hidden; background-repeat: no-repeat; background-size: 1440px 900px; }
      .shot::after { content: ''; position: absolute; inset: 0; border-radius: inherit; box-shadow: inset 0 0 0 1px var(--edge); }
    </style><div class="stage">${body}</div>`);
    const win = new BrowserWindow({
      show: false, width: size.width, height: size.height, useContentSize: true, frame: false, transparent: true,
      backgroundColor: '#00000000', webPreferences: { offscreen: true },
    });
    try {
      await win.loadFile(page);
      await evaluate(win, () => Promise.all([...document.images].map(image => image.decode())));
      await delay(300);
      const image = (await win.webContents.capturePage()).resize({ width: format.width, quality: 'best' });
      fs.writeFileSync(path.join(output, `${name}-${lang}.${format.jpeg ? 'jpg' : 'png'}`), format.jpeg ? image.toJPEG(86) : image.toPNG());
    } finally {
      win.destroy();
    }
  };
  const url = file => pathToFileURL(file).href;
  const windowFrame = (file, left, top, edge, shadow) => `<div class="shot" style="left:${left}px;top:${top}px;width:1440px;height:900px;border-radius:12px;--edge:${edge};background-image:url('${url(file)}');box-shadow:${shadow}"></div>`;
  const frameAll = async crops => {
    for (const [theme, scene, edge] of [['light', 'shore', 'rgba(0,0,0,.18)'], ['dark', 'dusk', 'rgba(255,255,255,.16)']]) {
      await compose(`hero-${theme}`, { width: 1640, height: 1100 },
        `<img class="backdrop" src="${url(path.join(brand, `${scene}.webp`))}">${windowFrame(shots[`hero-${theme}`], 100, 85, edge, '0 12px 40px rgba(0,0,0,.59)')}`,
        { width: 1800, jpeg: true });
    }
    await compose('files', { width: 1500, height: 960 }, windowFrame(shots.files, 30, 20, 'rgba(0,0,0,.18)', '0 7px 26px rgba(0,0,0,.27)'), { width: 1600 });
    for (const name of ['models', 'plugins']) {
      const crop = crops[name];
      await compose(name, { width: crop.width + 40, height: crop.height + 45 },
        `<div class="shot" style="left:20px;top:15px;width:${crop.width}px;height:${crop.height}px;border-radius:10px;--edge:rgba(0,0,0,.16);background-image:url('${url(shots[name])}');background-position:-${crop.x}px -${crop.y}px;box-shadow:0 5px 20px rgba(0,0,0,.2)"></div>`,
        { width: 1400 });
    }
  };

  let main;
  app.on('browser-window-created', (_event, win) => { main ||= win; });
  const run = async () => {
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const endpoint = `http://127.0.0.1:${server.address().port}/v1`;
    // External engines stay off so the model picker shows only the scripted provider.
    const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
    write(path.join(home, 'config.json'), JSON.stringify({ default_provider: 'openai', engines, providers: { openai: { type: 'openai-compatible', base_url: endpoint, api_key: 'readme-only', model: 'gpt-6.1-sol' } } }));
    const stamp = '2026-09-20T00:00:00Z';
    write(path.join(home, 'projects.json'), JSON.stringify({
      projects: [
        { id: 'pocket-ledger', name: 'pocket-ledger', path: ledger, created_at: stamp, updated_at: stamp },
        { id: 'landing-site', name: 'landing-site', path: site, created_at: stamp, updated_at: stamp },
      ],
      active_context: { kind: 'project', project_id: 'pocket-ledger', cwd: ledger },
    }));
    write(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: zh ? 'zh-CN' : 'en-US', theme: 'light' }));
    await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
    while (!main) await delay(25);
    main.setContentSize(1440, 900);
    await waitFor(main, () => document.querySelector('.composer textarea'));
    // A wider right panel keeps diff lines from wrapping.
    await evaluate(main, () => { localStorage.setItem('wuu.desktop.workspaceRightPanelWidth', '600'); location.reload(); });
    await delay(500);
    await waitFor(main, () => document.querySelector('.composer textarea'));
    await macChrome(main);

    // Background conversations so the sidebar reads like real use.
    await send(main, S.explainPrompt);
    await newThread(main, 'pocket-ledger');
    await send(main, S.thinkPrompt);
    await newThread(main, 'landing-site');
    await send(main, S.sitePrompt);
    // The Git card opens beside non-empty conversations in a repository.
    // Closing it once keeps it closed for the session, including the recording.
    await waitFor(main, () => document.querySelector('.environment-toggle-button.active'), undefined, 15000);
    await evaluate(main, () => document.querySelector('.environment-toggle-button.active').click());
    await waitFor(main, () => !document.querySelector('.environment-toggle-button.active'));

    await expandWorkspaces(main);
    await newThread(main, 'pocket-ledger');
    await delay(800);
    pace = true;
    await record(main, async () => {
      await delay(700);
      const stride = zh ? 1 : 3;
      for (let i = stride; i < S.fixPrompt.length + stride; i += stride) {
        await draft(main, S.fixPrompt.slice(0, i));
        await delay(zh ? 45 : 30);
      }
      await delay(600);
      await submit(main);
      await waitTurn(main, 1600);
      await delay(1200);
      await openTool(main, L.review);
      await delay(1500);
      await openReviewFile(main, 'totals.js');
      await delay(3000);
    });
    pace = false;

    for (const theme of ['light', 'dark']) {
      await reset(main);
      await setTheme(main, theme);
      await openThread(main, S.fixTitle);
      await evaluate(main, () => {
        // Show the work behind the answer: reads, test runs and the patch.
        for (const toggle of document.querySelectorAll('.turn-process-toggle[aria-expanded="false"]')) toggle.click();
      });
      await openTool(main, L.review);
      await delay(1200);
      await openReviewFile(main, 'totals.js');
      await shot(main, `hero-${theme}`);
    }
    await reset(main);
    await setTheme(main, 'light');
    await openThread(main, S.fixTitle);
    await openTool(main, L.files);
    await delay(1200);
    await openFile(main, 'docs/design.md');
    await selectQuote(main, S.filesQuote);
    await shot(main, 'files');

    const crops = {};
    await reset(main);
    await settingsPage(main, L.providers);
    await evaluate(main, title => [...document.querySelectorAll('.settings-section')]
      .find(section => section.querySelector('.settings-section-title')?.textContent.trim() === title)
      .scrollIntoView({ block: 'end' }), L.addProvider);
    await delay(400);
    await evaluate(main, title => {
      for (const section of document.querySelectorAll('.settings-section')) {
        section.toggleAttribute('data-readme-crop', section.querySelector('.settings-section-title')?.textContent.trim() === title);
      }
    }, L.addProvider);
    await shot(main, 'models');
    crops.models = await bounds(main, '[data-readme-crop]', 14);
    await reset(main);
    await clickLabel(main, L.plugins);
    await delay(1400);
    await shot(main, 'plugins');
    crops.plugins = await bounds(main, '.skills-catalog > *', 14);
    await frameAll(crops);
  };
  const timeout = setTimeout(() => { console.error('Timed out'); app.exit(1); }, 840_000);
  run().then(() => {
    clearTimeout(timeout);
    server.close();
    fs.rmSync(fixture, { recursive: true, force: true });
    app.exit(0);
  }).catch(async error => {
    console.error(error);
    if (main) fs.writeFileSync(path.join(temporary, '..', `readme-media-failure-${lang}.png`), (await main.webContents.capturePage()).toPNG());
    app.exit(1);
  });
}
