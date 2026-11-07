// Build first: npm --prefix desktop run build:core && npm --prefix desktop run build.
// Run: electron desktop/scripts/subagent-settings-e2e.cjs
// Opens Settings → Subagent in the real App with an isolated WUU_HOME, two
// configured providers and seeded model aliases, and captures the page.
// WUU_SUBAGENT_SETTINGS_PHASE=before only captures; the default also drives
// add / pick model / rename / remove and checks what reaches config.json.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');

const desktop = path.resolve(__dirname, '..');
const repository = path.dirname(desktop);
const output = path.resolve(process.env.WUU_SUBAGENT_SETTINGS_OUTPUT || path.join(repository, 'artifacts/subagent-settings'));
const phaseName = process.env.WUU_SUBAGENT_SETTINGS_PHASE || 'after';
const run = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-subagent-settings-'));
const home = path.join(run, 'home');
const workspace = path.join(run, 'workspace');
const core = path.resolve(process.env.WUU_SUBAGENT_SETTINGS_CORE || path.join(desktop, 'build/bin/wuu-core'));
for (const dir of [output, home, workspace]) fs.mkdirSync(dir, { recursive: true });
app.setPath('userData', path.join(run, 'profile'));
Object.assign(process.env, { WUU_HOME: home, WUU_DESKTOP_CORE: core,
  WUU_SAFE_MODE: '0', WUU_ENABLE_BROWSER: '0', WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
  WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP: '1' });
delete process.env.ELECTRON_RENDERER_URL;

const report = { status: 'running', phase: phaseName, captures: [], checks: [], errors: [] };
let win;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await check()) return; await sleep(50); }
  throw new Error(`Timed out: ${label}`);
}
const evaluate = (fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
const save = () => fs.writeFileSync(path.join(output, `report-${phaseName}.json`), JSON.stringify(report, null, 2));
function check(name, condition, detail) {
  report.checks.push({ name, passed: Boolean(condition), detail });
  assert.ok(condition, `${name}: ${detail ?? ''}`);
}
const aliasesOnDisk = () => JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')).agent?.model_aliases ?? {};

app.on('browser-window-created', (_event, created) => {
  if (win) return;
  win = created; created.hide();
  created.webContents.setBackgroundThrottling(false);
  created.webContents.on('console-message', event => { if (event.level === 'error') report.errors.push(event.message); });
});

async function capture(name) {
  await evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  await sleep(250);
  fs.writeFileSync(path.join(output, `${phaseName}-${name}.png`), (await win.webContents.capturePage()).toPNG());
  report.captures.push(`${phaseName}-${name}.png`);
}
async function setLook({ theme = 'light', font = 15, width = 1440, height = 960 } = {}) {
  win.setContentSize(width, height);
  await evaluate(({ theme, font }) => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.setProperty('--font-ui', `${font}px`);
  }, { theme, font });
}
// React listens to native input/change events; set values through the setter.
const setField = (selector, value, event = 'input') => evaluate(({ selector, value, event }) => {
  const el = document.querySelector(selector);
  el.focus();
  const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, value);
  el.dispatchEvent(new Event(event, { bubbles: true }));
  return true;
}, { selector, value, event });

async function main() {
  const models = {
    'claude-sonnet-4-5': { name: 'Claude Sonnet 4.5', tool_call: true, supported_efforts: ['low', 'medium', 'high'] },
    'claude-haiku-4-5': { name: 'Claude Haiku 4.5', tool_call: true },
  };
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'anthropic-proxy', agent: { permission_mode: 'unconfined', model_aliases: {
      fast: { provider: 'deepseek', model: 'deepseek-chat' },
      deep: { provider: 'anthropic-proxy', model: 'claude-sonnet-4-5', effort: 'high' },
    } },
    providers: {
      'anthropic-proxy': { type: 'openai-compatible', base_url: 'http://127.0.0.1:9/v1', api_key: 'fixture-only', model: 'claude-sonnet-4-5', models },
      deepseek: { type: 'openai-compatible', base_url: 'http://127.0.0.1:9/v1', api_key: 'fixture-only', model: 'deepseek-chat', models: { 'deepseek-chat': { name: 'DeepSeek V3', tool_call: true } } },
    },
    engines: { default_engine: 'wuu' }, skills: { enabled: false }, ptc: { enabled: false } }));
  const stamp = new Date().toISOString();
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({ projects: [{ id: 'fixture', name: 'wuu', path: workspace, created_at: stamp, updated_at: stamp }], active_context: { kind: 'project', project_id: 'fixture', cwd: workspace } }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: process.env.WUU_SUBAGENT_SETTINGS_LANGUAGE || 'zh-CN', theme: 'light' }));
  const enabled = spawnSync(core, ['plugin', 'enable', 'subagent'], { env: process.env, encoding: 'utf8', timeout: 30000 });
  assert.equal(enabled.status, 0, `enable subagent: ${enabled.stdout}${enabled.stderr}`);

  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  await until(() => win && !win.isDestroyed(), 'Wuu window');
  await setLook();
  win.webContents.debugger.attach('1.3');
  await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] });
  await until(() => evaluate(() => Boolean(document.querySelector('[data-settings-entry="settings"]'))), 'settings entry', 60000);
  await evaluate(() => document.querySelector('[data-settings-entry="settings"]').click());
  const clickSubagentNav = () => {
    const item = [...document.querySelectorAll('.settings-nav [data-wuu-page^="plugin-view:"]')].find(n => n.textContent.trim() === 'Subagent');
    item?.click();
    return Boolean(item);
  };
  await until(() => evaluate(clickSubagentNav), 'Subagent nav item');
  await until(() => evaluate(() => Boolean(document.querySelector('.plugin-subagent-settings'))), 'Subagent page');
  await capture('light-wide');
  await setLook({ theme: 'dark' }); await capture('dark-wide');
  await setLook({ font: 20, width: 900, height: 900 }); await capture('light-large-narrow');
  await setLook();

  if (phaseName !== 'before') {
    // Opens the host menu, captures it, and picks the option with this value.
    const pick = async (menu, value, shot) => {
      await evaluate(m => document.querySelector(`${m} .select-menu-trigger`).click(), menu);
      await until(() => evaluate(v => Boolean(document.querySelector(`.select-menu-item[data-value='${v}']`)), value), `${shot} option`);
      await capture(shot);
      const picked = await evaluate(v => {
        const option = document.querySelector(`.select-menu-item[data-value='${v}']`);
        option?.click();
        return option ? 'clicked' : [...document.querySelectorAll('.select-menu-item')].map(n => n.dataset.value).join('|');
      }, value);
      if (picked !== 'clicked') throw new Error(`${shot}: option ${value} missing; menu has ${picked}`);
    };
    const rows = () => evaluate(() => document.querySelectorAll('.plugin-subagent-alias').length);
    check('seeded aliases render as rows', await rows() === 2, String(await rows()));
    // A new alias is saved once it has a valid name and a model.
    await evaluate(() => document.querySelector('.plugin-subagent-add').click());
    await until(async () => await rows() === 3, 'new row');
    await setField('.plugin-subagent-alias:last-child .plugin-subagent-name', 'Bad Name');
    await evaluate(() => document.querySelector('.plugin-subagent-alias:last-child .plugin-subagent-name').blur());
    await until(() => evaluate(() => Boolean(document.querySelector('.plugin-subagent-alias:last-child [role="alert"]'))), 'name hint');
    await capture('invalid-name');
    await setField('.plugin-subagent-alias:last-child .plugin-subagent-name', 'review');
    await evaluate(() => document.querySelector('.plugin-subagent-alias:last-child .plugin-subagent-name').blur());
    await pick('.plugin-subagent-alias:last-child .plugin-ui-model-picker-model', JSON.stringify(['anthropic-proxy', 'claude-haiku-4-5']), 'model-menu');
    await until(() => Boolean(aliasesOnDisk().review), 'review alias saved');
    check('new alias saved with picked model', aliasesOnDisk().review.model === 'claude-haiku-4-5', JSON.stringify(aliasesOnDisk()));
    check('other aliases kept', aliasesOnDisk().fast?.model === 'deepseek-chat' && aliasesOnDisk().deep?.effort === 'high', JSON.stringify(aliasesOnDisk()));
    await capture('added');
    // Changing the level saves it as the alias variant.
    await pick('.plugin-subagent-alias:first-child .plugin-ui-model-picker-level', 'medium', 'level-menu');
    await until(() => aliasesOnDisk().deep?.variant === 'medium', 'deep level saved');
    check('level replaces legacy effort', !aliasesOnDisk().deep.effort, JSON.stringify(aliasesOnDisk().deep));
    // Removing a row saves immediately.
    await until(() => evaluate(() => {
      const button = document.querySelector('.plugin-subagent-alias:first-child .plugin-subagent-remove:not(:disabled)');
      button?.click();
      return Boolean(button);
    }), 'first remove button');
    await until(() => Object.keys(aliasesOnDisk()).length === 2, 'alias removed');
    check('removed alias leaves config', !aliasesOnDisk().deep && aliasesOnDisk().fast && aliasesOnDisk().review, JSON.stringify(aliasesOnDisk()));
    // Empty state.
    for (let left = Object.keys(aliasesOnDisk()).length; left > 0; left--) {
      await until(() => evaluate(() => {
        const button = document.querySelector('.plugin-subagent-remove:not(:disabled)');
        button?.click();
        return Boolean(button);
      }), 'remove button');
      await until(() => Object.keys(aliasesOnDisk()).length === left - 1, `alias count ${left - 1}`);
    }
    await until(() => Object.keys(aliasesOnDisk()).length === 0, 'all removed');
    await capture('empty');
    await setLook({ theme: 'dark' }); await capture('empty-dark'); await setLook();
  }
  report.status = 'passed'; save();
  console.log(`PASS subagent settings ${phaseName}; evidence ${output}`);
  app.quit();
}

const watchdog = setTimeout(() => { report.status = 'failed'; report.errors.push('watchdog timeout'); save(); app.exit(1); }, 180000);
main().catch(async error => {
  report.status = 'failed'; report.errors.push(error.stack || String(error));
  if (win && !win.isDestroyed()) { try { fs.writeFileSync(path.join(output, `${phaseName}-failure.png`), (await win.webContents.capturePage()).toPNG()); } catch {} }
  save(); console.error(error); clearTimeout(watchdog); app.exit(1);
});
app.on('before-quit', () => clearTimeout(watchdog));
