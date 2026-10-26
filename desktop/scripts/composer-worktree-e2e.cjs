// Real Electron/main/preload/Go coverage for starting a conversation in its own
// Git worktree through the public desktop API, with composer branch controls.
// Build core and desktop first;
// WUU_WORKTREE_CORE and WUU_WORKTREE_DESKTOP can select frozen build inputs.
// A local HTTP provider answers the turn, so no account or paid inference is used.
// Screenshots and the evidence
// record are written to artifacts/composer-worktree/ for visual review.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');

const desktop = path.resolve(process.env.WUU_WORKTREE_DESKTOP || path.join(__dirname, '..'));
const output = process.env.WUU_WORKTREE_OUTPUT || path.resolve(desktop, '../artifacts/composer-worktree');
const narrowWidth = Number(process.env.WUU_WORKTREE_NARROW_WIDTH || 760);
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-composer-worktree-'));
const home = path.join(fixture, 'home');
const userHome = path.join(fixture, 'user-home');
const project = path.join(fixture, 'project');
const plain = path.join(fixture, 'plain');
const longProjectName = 'an-unusually-long-project-name-for-the-narrow-composer';
const longProject = path.join(fixture, longProjectName);
for (const dir of [home, userHome, project, plain]) fs.mkdirSync(dir);
fs.rmSync(output, { recursive: true, force: true });
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
Object.assign(process.env, {
  WUU_HOME: home, HOME: userHome, XDG_CACHE_HOME: path.join(userHome, '.cache'),
  CODEX_HOME: path.join(userHome, '.codex'), GROK_HOME: path.join(userHome, '.grok'),
  WUU_ENABLE_CUA_MAC: '0', WUU_ENABLE_CUA_LINUX: '0',
});
process.env.WUU_DESKTOP_CORE = process.env.WUU_WORKTREE_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '0';
process.env.WUU_DESKTOP_DISABLE_DEV_CACHE_CLEANUP = '1';

const git = (cwd, ...args) => {
  const result = spawnSync('git', args, { cwd, encoding: 'utf8' });
  assert.equal(result.status, 0, `git ${args.join(' ')}: ${result.stderr}`);
  return result.stdout.trim();
};
git(project, 'init', '-q', '-b', 'main');
git(project, 'config', 'user.email', 'e2e@example.com');
git(project, 'config', 'user.name', 'E2E');
fs.writeFileSync(path.join(project, 'README.md'), 'committed\n');
const skillPath = path.join('.agents', 'skills', 'compact', 'SKILL.md');
fs.mkdirSync(path.dirname(path.join(project, skillPath)), { recursive: true });
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\ndisable-model-invocation: true\n---\nWORKSPACE_SKILL_MARKER ${ARGUMENTS}\nResource base: ${CLAUDE_SKILL_DIR}\n');
fs.writeFileSync(path.join(project, path.dirname(skillPath), 'resource.txt'), 'WORKSPACE_RESOURCE_MARKER');
git(project, 'add', '.');
git(project, 'commit', '-q', '-m', 'init');
git(project, 'switch', '-q', '-c', 'feature');
fs.writeFileSync(path.join(project, 'feature.txt'), 'feature\n');
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\ndisable-model-invocation: true\n---\nSELECTED_SKILL_MARKER ${ARGUMENTS}\nResource base: ${CLAUDE_SKILL_DIR}\n');
fs.writeFileSync(path.join(project, path.dirname(skillPath), 'resource.txt'), 'SELECTED_RESOURCE_MARKER');
git(project, 'add', '.');
git(project, 'commit', '-q', '-m', 'feature');
const featureHead = git(project, 'rev-parse', 'HEAD');
const longBranch = 'long-branch/an-unusually-long-branch-name-for-the-narrow-composer';
git(project, 'branch', longBranch);
git(project, 'switch', '-q', 'main');
git(fixture, 'clone', '-q', project, longProject);
git(longProject, 'branch', longBranch, `origin/${longBranch}`);
// Uncommitted work in the shared checkout stays out of a new worktree.
fs.writeFileSync(path.join(project, 'README.md'), 'uncommitted edit\n');
fs.writeFileSync(path.join(plain, 'notes.txt'), 'not a repository\n');

const requests = [];
let rebindStage = 0;
let skillAdmissionResult;
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    requests.push({ path: req.url, body: JSON.parse(body) });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = delta => `data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`;
    const messages = requests.at(-1).body.messages || [];
    const latestUser = messages.findLast(message => message.role === 'user' && !String(message.content).startsWith('<system-reminder>'));
    if (latestUser?.content === 'Check manual skill admission fixture') {
      if (messages.at(-1)?.role === 'user') {
        const ptc = requests.at(-1).body.tools?.some(tool => tool.function?.name === 'run_code');
        res.write(chunk({ role: 'assistant', tool_calls: [{ index: 0, id: 'skill-admission-fixture', type: 'function', function: {
          name: ptc ? 'run_code' : 'load_skill',
          arguments: JSON.stringify(ptc ? { code: 'text(await tools.load_skill({ name: "compact" }));' } : { name: 'compact' }),
        } }] }));
        res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }] })}\n\ndata: [DONE]\n\n`);
        return;
      }
      skillAdmissionResult = messages.findLast(message => message.role === 'tool')?.content;
      res.write(chunk({ role: 'assistant', content: 'Manual-only skill admission verified.' }));
      res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 4, total_tokens: 14 } })}\n\ndata: [DONE]\n\n`);
      return;
    }
    if (typeof latestUser?.content === 'string' && latestUser.content.includes('Rebind workspace fixture') && rebindStage < 2) {
      const discovering = rebindStage++ === 0;
      // Workspace rebinding is deferred: load its public schema before calling it.
      if (!discovering) assert(requests.at(-1).body.tools?.some(tool => tool.function?.name === 'set_session_workspace'),
        'Tool discovery must expose the workspace operation before invocation.');
      res.write(chunk({ role: 'assistant', content: discovering ? 'Finding the workspace operation.' : 'Moving this session back to the project.' }));
      res.write(chunk({ tool_calls: [{ index: 0, id: `workspace-fixture-${rebindStage}`, type: 'function', function: {
        name: discovering ? 'tool_search' : 'set_session_workspace',
        arguments: JSON.stringify(discovering ? { query: 'select:set_session_workspace' } : { root: fs.realpathSync(project) }),
      } }] }));
      res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }] })}\n\ndata: [DONE]\n\n`);
      return;
    }
    res.write(chunk({ role: 'assistant', content: 'Worktree fixture answer.' }));
    res.end(`data: ${JSON.stringify({ id: 'fixture', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 4, total_tokens: 14 } })}\n\ndata: [DONE]\n\n`);
  });
});

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (win, fn, arg) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(arg)})`);
async function waitFor(win, fn, arg, timeout = 30000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    const value = await evaluate(win, fn, arg);
    if (value) return value;
    await delay(50);
  }
  throw new Error(`Timed out: ${fn}`);
}
const settle = win => evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
async function capture(win, name) {
  await settle(win);
  await evaluate(win, () => Promise.all([...document.querySelectorAll('.collapsible-details, .environment-panel, .fork-worktree-chevron')]
    .flatMap(node => node.getAnimations())
    .filter(animation => animation.playState === 'running' && Number.isFinite(animation.effect?.getComputedTiming().endTime))
    .map(animation => animation.finished.catch(() => {}))));
  await settle(win);
  fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG());
}
async function setAppearance(win, { theme, fontSize, width }) {
  win.setContentSize(width, 820);
  await evaluate(win, ({ theme, fontSize }) => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.setProperty('--conversation-message-font-size', `${fontSize}px`);
  }, { theme, fontSize });
  await settle(win);
  await waitFor(win, expectedWidth => Math.abs(innerWidth - expectedWidth) <= 2
    && !document.documentElement.classList.contains('window-resizing')
    && !document.documentElement.classList.contains('layout-motion-active'), width / win.webContents.getZoomFactor());
  await settle(win);
}
async function captureCrop(win, rect) {
  // DOM rectangles are CSS pixels; Electron capture rectangles are DIP.
  const zoom = win.webContents.getZoomFactor();
  const crop = Object.fromEntries(Object.entries(rect).map(([key, value]) => [key, Math.round(value * zoom)]));
  return (await win.webContents.capturePage(crop)).toPNG();
}
const nativeClicks = [];
async function click(win, selector) {
  win.focus(); win.webContents.focus();
  const focusDeadline = Date.now() + 30000;
  while (!(win.isFocused() && win.webContents.isFocused()) && Date.now() < focusDeadline) await delay(50);
  assert(win.isFocused() && win.webContents.isFocused(), 'Native window focus is required for the click.');
  await evaluate(win, () => document.fonts.ready.then(() => undefined));
  const point = await waitFor(win, async selector => {
    let previous, target;
    for (let frame = 0; frame < 4; frame++) {
      await new Promise(requestAnimationFrame);
      const element = [...document.querySelectorAll(selector)].find(node => {
        const rect = node.getBoundingClientRect();
        return rect.width > 0 && rect.height > 0 && !node.disabled
          && !node.closest('[inert], [aria-hidden="true"]');
      });
      if (!element) return;
      const rect = element.getBoundingClientRect();
      const current = { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      const bounds = [rect.x, rect.y, rect.width, rect.height].join();
      if (rect.left < 0 || rect.top < 0 || rect.right > innerWidth || rect.bottom > innerHeight
        || !element.contains(document.elementFromPoint(current.x, current.y))
        || (previous && (bounds !== previous || target !== element))) return;
      previous = bounds; target = element;
      if (frame === 3) return current;
    }
  }, selector);
  nativeClicks.push({ selector, ...point });
  const zoom = win.webContents.getZoomFactor();
  const dip = { x: Math.round(point.x * zoom), y: Math.round(point.y * zoom) };
  for (const type of ['mouseMove', 'mouseDown', 'mouseUp']) {
    win.webContents.sendInputEvent({ type, button: 'left', clickCount: 1, ...dip });
  }
}
const branchControl = '.composer-branch-control button[aria-haspopup="menu"]';
const projectControl = '.composer-project-control button[aria-haspopup="menu"]';
async function waitForBranch(win, branch) {
  await waitFor(win, ({ selector, label }) => {
    const control = document.querySelector(selector);
    return control && !control.disabled && control.getAttribute('aria-label') === label;
  }, { selector: branchControl, label: `Switch branch: ${branch}` });
}
async function sendDraft(win) {
  // The controlled draft can lag behind the input event; clicking its disabled
  // send button is a no-op. Wait for rendering and confirm submission consumed it.
  await settle(win);
  await waitFor(win, () => document.querySelector('.composer textarea')?.value.trim()
    && document.querySelector('.composer-send-button[data-wuu-state="send"]:not(:disabled)'));
  await click(win, '.composer-send-button');
  await waitFor(win, () => document.querySelector('.composer textarea')?.value === '');
}
async function waitForSkillRow(win) {
  await waitFor(win, () => {
    const row = document.querySelector('.slash-command-item[id$="-skill:compact"]');
    if (!row || row.disabled) return false;
    const rect = row.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.left < 0 || rect.top < 0 || rect.right > innerWidth || rect.bottom > innerHeight) return false;
    for (let element = row; element; element = element.parentElement) {
      const style = getComputedStyle(element);
      if (style.display === 'none' || style.visibility !== 'visible' || Number(style.opacity) < 0.99) return false;
    }
    return row.contains(document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2));
  });
}
const workspaceRow = win => evaluate(win, () => {
  const barNode = document.querySelector('.composer-workspace-bar');
  const bar = barNode.getBoundingClientRect();
  const groups = [...document.querySelectorAll('.composer-workspace-group')].map(group => group.getBoundingClientRect());
  const measure = document.createElement('canvas').getContext('2d');
  const text = [...document.querySelectorAll('.composer-workspace-bar .hero-project-pill-text')]
    .map(node => {
      const style = getComputedStyle(node);
      measure.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
      return { text: node.textContent, font: measure.font, width: node.getBoundingClientRect().width,
        clientWidth: node.clientWidth, scrollWidth: node.scrollWidth,
        prefixWidth: measure.measureText(`${node.textContent.slice(0, 2)}…`).width,
        clipped: node.scrollWidth > node.clientWidth + 1 };
    });
  const buttons = [...barNode.querySelectorAll('button')].map(button => {
    const rect = button.getBoundingClientRect();
    return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, height: rect.height,
      hittable: rect.width > 0 && button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)) };
  });
  return { viewport: { width: innerWidth, height: innerHeight }, barLeft: bar.left, barRight: bar.right,
    groups: groups.map(rect => ({ left: rect.left, right: rect.right, height: rect.height })), text, buttons };
});

let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 180000);

async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'dsh', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
  }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
    projects: [
      { id: 'repo', name: 'worktree-fixture', path: project, created_at: stamp, updated_at: stamp },
      { id: 'long-repo', name: longProjectName, path: longProject, created_at: stamp, updated_at: stamp },
      { id: 'plain', name: 'plain-folder', path: plain, created_at: stamp, updated_at: stamp },
    ],
    active_context: { kind: 'project', project_id: 'repo', cwd: project },
  }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  main.webContents.setBackgroundThrottling(false);
  main.setSize(1280, 820);
  await waitFor(main, () => document.querySelector('.composer textarea:not(:disabled)'));
  await waitForBranch(main, 'main');

  // Branch choice keeps its checkout meaning; isolation is requested through the API.
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'main');
  await capture(main, '01-draft-light-default.png');

  // Select before the new checkout exists; browsing must not create a worktree.
  const worktreeCount = () => git(project, 'worktree', 'list', '--porcelain').split('\n').filter(line => line.startsWith('worktree ')).length;
  assert.equal(worktreeCount(), 1);
  main.focus();
  await evaluate(main, () => {
    const textarea = document.querySelector('.composer textarea');
    textarea.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, '/compact Summarize this repository');
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await waitForSkillRow(main);
  await capture(main, '03a-initial-skill-menu.png');
  await evaluate(main, () => document.querySelector('.slash-command-item[id$="-skill:compact"]').click());
  await waitFor(main, () => document.querySelector('.composer textarea').value.startsWith('/skill '));
  const initialSkillDraft = await evaluate(main, () => document.querySelector('.composer textarea').value);
  const projectIdentity = { root: fs.realpathSync(project), path: skillPath.split(path.sep).join('/') };
  assert.deepEqual(JSON.parse(initialSkillDraft.split('\n')[0].slice('/skill '.length)), { name: 'compact', source: 'project', project: projectIdentity });
  assert.equal(worktreeCount(), 1, 'Choosing a skill must not materialize the selected checkout.');
  await capture(main, '03b-initial-skill-draft.png');
  await evaluate(main, async ({ draft, cwd }) => {
    const { thread } = await window.wuu.startThread({ cwd, workspace_id: 'repo', workspace: 'worktree', base_revision: 'feature' });
    await window.wuu.renameThread(thread.id, 'Worktree API fixture');
    await window.wuu.startTurn(thread.id, draft);
    window.dispatchEvent(new Event('focus'));
  }, { draft: initialSkillDraft, cwd: project });
  await waitFor(main, () => [...document.querySelectorAll('.thread-row-main')].some(button => button.getAttribute('aria-label')?.includes('Worktree API fixture')));
  await evaluate(main, () => [...document.querySelectorAll('.thread-row-main')].find(button => button.getAttribute('aria-label')?.includes('Worktree API fixture')).click());
  await waitFor(main, () => [...document.querySelectorAll('[data-thread-id] .session-flow')]
    .some(flow => flow.textContent.includes('Worktree fixture answer.')), undefined, 60000);
  const thread = await evaluate(main, async () => {
    const { threads } = await window.wuu.listThreads();
    return threads.find(candidate => candidate.worktree);
  });
  assert.ok(thread?.worktree?.path, 'The new conversation is bound to a worktree.');
  assert.equal(thread.cwd, thread.worktree.path);
  assert.equal(thread.workspace_id, 'repo', 'The worktree conversation stays in its project.');
  assert.equal(thread.worktree.base_head, featureHead);
  assert.equal(git(thread.worktree.path, 'rev-parse', 'HEAD'), featureHead);
  assert.equal(fs.readFileSync(path.join(thread.worktree.path, 'README.md'), 'utf8'), 'committed\n', 'Uncommitted changes are not carried into the worktree.');
  assert.ok(fs.existsSync(path.join(thread.worktree.path, 'feature.txt')));
  assert.equal(fs.readFileSync(path.join(thread.cwd, path.dirname(skillPath), 'resource.txt'), 'utf8'), 'SELECTED_RESOURCE_MARKER');
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'main');
  assert.equal(fs.readFileSync(path.join(project, 'README.md'), 'utf8'), 'uncommitted edit\n');
  assert.ok(requests.some(request => (request.body.messages || []).some(message => message.role === 'user'
    && typeof message.content === 'string' && message.content.includes('SELECTED_SKILL_MARKER Summarize this repository')
    && !message.content.includes('WORKSPACE_SKILL_MARKER') && message.content.includes(path.join(thread.cwd, path.dirname(skillPath))))),
    'The first turn must load the selected branch skill and its resource base.');
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('.session-flow .fork-worktree-notice'))), false,
    'Current workspace metadata must not be inserted ahead of historical messages.');
  await capture(main, '04-conversation-without-worktree-banner.png');
  await waitFor(main, () => document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  await evaluate(main, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Check manual skill admission fixture');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await sendDraft(main);
  await waitFor(main, () => document.querySelector('.cached-conversation-pane[data-active="true"]')?.textContent.includes('Manual-only skill admission verified.'));
  assert.equal(typeof skillAdmissionResult, 'string', 'The synthetic-provider tool round trip must return a tool result.');
  assert.match(skillAdmissionResult, /skill.*compact.*not found/i, 'Model invocation must reject the manual-only skill.');
  assert(!skillAdmissionResult.includes('SELECTED_SKILL_MARKER'), 'Rejected model invocation must not load the skill body.');
  assert(requests.every(request => (request.body.messages || []).filter(message => message.role === 'system')
    .every(message => !String(message.content).includes('<name>compact</name>'))), 'The manual-only skill stays out of the model catalog.');
  await capture(main, '04a-skill-model-admission.png');
  // Explicit selection must beat the built-in control command and use this checkout.
  await waitFor(main, () => document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  main.focus();
  await evaluate(main, () => {
    const input = document.querySelector('.composer textarea');
    input.focus();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, '/compact');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await waitForSkillRow(main);
  await capture(main, '07-explicit-skill-menu.png');
  await evaluate(main, () => document.querySelector('.slash-command-item[id$="-skill:compact"]').click());
  await waitFor(main, () => document.querySelector('.composer textarea').value.startsWith('/skill '));
  const selectedDraft = await evaluate(main, () => document.querySelector('.composer textarea').value);
  assert.deepEqual(JSON.parse(selectedDraft.split('\n')[0].slice('/skill '.length)), {
    name: 'compact', source: 'project', project: projectIdentity,
  });
  await capture(main, '08-explicit-skill-draft-light.png');
  main.setSize(narrowWidth, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'dark';
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  await settle(main);
  assert(await evaluate(main, () => {
    const input = document.querySelector('.composer textarea');
    return input.scrollWidth <= input.clientWidth + 1 && input.getBoundingClientRect().right <= innerWidth;
  }), 'The explicit identity draft must wrap inside the narrow composer.');
  await capture(main, '09-explicit-skill-draft-dark-narrow.png');
  await evaluate(main, text => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, text + 'verify selected workflow');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }, selectedDraft);
  await sendDraft(main);
  const receivedSelectedSkill = () => requests.some(request => (request.body.messages || []).some(message =>
    message.role === 'user' && typeof message.content === 'string'
    && message.content.includes('SELECTED_SKILL_MARKER verify selected workflow')
    && !message.content.includes('WORKSPACE_SKILL_MARKER')
    && !message.content.startsWith('/skill ')));
  const skillDeadline = Date.now() + 30000;
  while (!receivedSelectedSkill() && Date.now() < skillDeadline) await delay(50);
  assert(receivedSelectedSkill(), 'The provider must receive the selected checkout instructions, not the dispatch envelope.');
  await waitFor(main, () => document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });

  // Dismissing the fork destination chooser is a non-mutating escape hatch.
  // Exercise native pointer/keyboard input and keep cropped, synthetic UI evidence.
  const threadIDsBeforeDismissal = await evaluate(main, async () => (await window.wuu.listThreads()).threads.map(item => item.id).sort());
  const worktreesBeforeDismissal = worktreeCount();
  const forkDismissals = [];
  for (const theme of ['light', 'dark']) for (const fontSize of [14, 20]) for (const width of [1280, narrowWidth]) {
    await setAppearance(main, { theme, fontSize, width });
    for (const dismissal of ['backdrop', 'Escape']) {
      await evaluate(main, () => {
        const opener = [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-message-actions button:has(svg[data-icon="split"])')].at(-1);
        if (!opener) throw new Error('missing fork opener');
        opener.scrollIntoView({ block: 'center' });
        opener.focus();
        opener.click();
      });
      await waitFor(main, () => document.querySelector('.fork-dialog'));
      await evaluate(main, () => Promise.all(document.querySelector('.fork-dialog').getAnimations({ subtree: true })
        .map(animation => animation.finished.catch(() => {}))));
      await settle(main);
      const state = await evaluate(main, () => {
        const dialog = document.querySelector('.fork-dialog');
        const rect = dialog.getBoundingClientRect();
        const options = [...dialog.querySelectorAll('.fork-dialog-option')];
        return {
          buttonCount: dialog.querySelectorAll('button').length,
          optionCount: options.length,
          focusedFirst: document.activeElement === options[0],
          fits: rect.left >= 0 && rect.right <= innerWidth && rect.top >= 0 && rect.bottom <= innerHeight
            && [...dialog.querySelectorAll('.fork-dialog-option-title, .fork-dialog-option-description')]
              .every(label => label.scrollWidth <= label.clientWidth + 1),
          crop: { x: Math.floor(rect.x), y: Math.floor(rect.y), width: Math.ceil(rect.width), height: Math.ceil(rect.height) },
          backdrop: { x: Math.max(1, Math.floor(rect.left / 2)), y: Math.round(rect.top + rect.height / 2) },
        };
      });
      assert.equal(state.buttonCount, 2, 'The only fork actions are its two destinations.');
      assert.equal(state.optionCount, 2);
      assert(state.focusedFirst && state.fits, 'Fork options must be focused, readable, and inside the window.');
      if (dismissal === 'backdrop') {
        fs.writeFileSync(path.join(output, `fork-dialog-${theme}-${fontSize}-${width}.png`), await captureCrop(main, state.crop));
        const zoom = main.webContents.getZoomFactor();
        const point = { x: Math.round(state.backdrop.x * zoom), y: Math.round(state.backdrop.y * zoom) };
        main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point });
        main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point });
      } else {
        main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
        main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
      }
      await waitFor(main, () => !document.querySelector('.fork-dialog'));
      assert(await evaluate(main, () => document.activeElement?.matches('.agent-message-actions button:has(svg[data-icon="split"])')),
        'Dismissing the chooser must return focus to its message action.');
      forkDismissals.push({ theme, fontSize, width, dismissal, ...state });
    }
  }
  assert.deepEqual(await evaluate(main, async () => (await window.wuu.listThreads()).threads.map(item => item.id).sort()), threadIDsBeforeDismissal,
    'Dismissal must never fork a conversation.');
  assert.equal(worktreeCount(), worktreesBeforeDismissal, 'Dismissal must never create a worktree.');
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
  });

  // A mid-conversation rebind stays at its actual tool position, even after
  // returning to the project (when the thread no longer has worktree metadata).
  await evaluate(main, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Rebind workspace fixture');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await waitFor(main, () => document.querySelector('.composer textarea')?.value === 'Rebind workspace fixture'
    && document.querySelector('.composer-send-button[data-wuu-state="send"]:not(:disabled)'));
  await sendDraft(main);
  await waitFor(main, () => [...document.querySelectorAll('.user-message-motion')].some(node => node.textContent.includes('Rebind workspace fixture'))
    && document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  await waitFor(main, async id => {
    const thread = (await window.wuu.listThreads()).threads.find(candidate => candidate.id === id);
    return thread && !thread.worktree && thread.status !== 'in_progress';
  }, thread.id);
  const rebindTurnId = await evaluate(main, () => [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .user-message-motion')]
    .find(node => node.textContent.includes('Rebind workspace fixture')).closest('.turn').id);
  // Wait for this turn's completion handoff, not an older collapsed turn.
  // Details remain mounted briefly during collapse and are not an open-state signal.
  await waitFor(main, id => {
    const turn = document.getElementById(id);
    return turn?.dataset.turnStatus === 'completed' && turn.querySelector('.turn-process-toggle[aria-expanded="false"]');
  }, rebindTurnId);
  await evaluate(main, id => document.getElementById(id).querySelector('.turn-process-toggle').click(), rebindTurnId);
  await waitFor(main, id => document.getElementById(id).querySelector('.turn-process-toggle[aria-expanded="true"]')
    && document.getElementById(id).querySelector('.process-surface'), rebindTurnId);
  await evaluate(main, id => document.getElementById(id).querySelectorAll('.process-surface-fold.has-details:not([open]) > summary').forEach(node => node.click()), rebindTurnId);
  await waitFor(main, id => document.getElementById(id).querySelector('.process-surface-fold[open] .workspace-tool-details'), rebindTurnId);
  const rebound = await evaluate(main, async id => (await window.wuu.listThreads()).threads.find(candidate => candidate.id === id), thread.id);
  assert.equal(fs.realpathSync(rebound.cwd), fs.realpathSync(project));
  assert.ok(!rebound.worktree);
  assert(await evaluate(main, () => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    const record = pane.querySelector('.workspace-tool-details');
    const message = [...pane.querySelectorAll('.user-message-motion')].find(node => node.textContent.includes('Rebind workspace fixture'));
    return message && Boolean(message.compareDocumentPosition(record) & Node.DOCUMENT_POSITION_FOLLOWING)
      && !pane.querySelector('.fork-worktree-notice');
  }), 'The workspace operation must follow its initiating message.');
  assert(await evaluate(main, root => document.querySelector('.workspace-tool-details')?.textContent.includes(root), fs.realpathSync(project)));
  await capture(main, '10-workspace-tool-light.png');
  main.setSize(narrowWidth, 820);
  await evaluate(main, id => {
    document.documentElement.dataset.theme = 'dark';
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
    document.getElementById(id).querySelector('.workspace-tool-details').closest('.process-surface-fold').querySelector('summary').focus();
  }, rebindTurnId);
  await capture(main, '11-workspace-tool-dark-large-narrow.png');
  assert(await evaluate(main, id => Boolean(document.getElementById(id).querySelector('.turn-process-toggle[aria-expanded="true"]')
    && document.getElementById(id).querySelector('.workspace-tool-details')), rebindTurnId),
  'The selected workspace operation stays expanded after appearance settles.');
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });

  // The next draft starts shared again; branch choice checks out the project.
  await click(main, '.session-tab-new');
  // Git status refreshes for the draft's project after leaving the worktree.
  await waitForBranch(main, 'main');
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'main');
  await click(main, branchControl);
  await waitFor(main, () => document.querySelector('.composer-branch-menu [role="menuitemradio"][title="feature"]:not(:disabled)'));
  await click(main, '.composer-branch-menu [role="menuitemradio"][title="feature"]');
  await waitForBranch(main, 'feature');
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'feature', 'Choosing a branch checks it out.');

  // Dark, large text, narrow window with a long branch name.
  main.setSize(narrowWidth, 820);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'dark');
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  await click(main, branchControl);
  await waitFor(main, branch => document.querySelector(`.composer-branch-menu [role="menuitemradio"][title="${branch}"]`), longBranch);
  await click(main, `.composer-branch-menu [role="menuitemradio"][title="${longBranch}"]`);
  await waitFor(main, () => !document.querySelector('.composer-branch-menu'));
  await waitForBranch(main, longBranch);
  await setAppearance(main, { theme: 'dark', fontSize: 20, width: narrowWidth });
  // The expanded rail uses icons: their accessible names retain the full identity.
  // Exercise that native entry point before checking the text-label fallback.
  await click(main, branchControl);
  await waitFor(main, branch => document.querySelector(`.composer-branch-menu [title="${branch}"][aria-checked="true"]`), longBranch);
  const railSelection = await evaluate(main, ({ branchSelector, projectSelector }) => ({
    branch: document.querySelector(branchSelector).getAttribute('aria-label'),
    project: document.querySelector(projectSelector).getAttribute('aria-label'),
    menuSelectedBranch: document.querySelector('.composer-branch-menu [aria-checked="true"]').title,
  }), { branchSelector: branchControl, projectSelector: projectControl });
  assert.equal(railSelection.menuSelectedBranch, longBranch);
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), longBranch);
  await capture(main, '05c-tool-rail-long-branch-dark-large-narrow.png');
  for (const type of ['keyDown', 'keyUp']) main.webContents.sendInputEvent({ type, keyCode: 'Escape' });
  await waitFor(main, () => !document.querySelector('.composer-branch-menu'));
  assert(await evaluate(main, selector => document.activeElement === document.querySelector(selector), branchControl),
    'Dismissing the branch menu returns focus to its trigger.');
  if (await evaluate(main, () => Boolean(document.querySelector('.composer-workspace-rail')))) {
    await click(main, '[data-wuu-component="workspace-tool-rail-toggle"]');
  }
  await waitFor(main, () => document.querySelector('.composer-workspace-bar'));
  await setAppearance(main, { theme: 'dark', fontSize: 20, width: narrowWidth });
  await capture(main, '05-draft-dark-large-narrow.png');
  const narrow = await workspaceRow(main);
  assert.equal(narrow.groups.length, 2);
  assert.ok(narrow.groups.every(rect => rect.right <= narrow.barRight + 0.5), 'Both groups stay inside the bar.');
  assert.ok(Math.abs(narrow.groups[0].height - narrow.groups[1].height) < 0.5, 'Both groups share one height.');
  assert(narrow.text.slice(0, 2).every(item => item.width >= item.prefixWidth),
    `Both names retain space for an identifiable prefix and ellipsis. Layout: ${JSON.stringify(narrow)}`);
  assert(narrow.text[1].clipped,
    `The long branch truncates within its available width. Layout: ${JSON.stringify(narrow)}`);

  await evaluate(main, () => document.documentElement.style.setProperty('--appearance-scale', String(15 / 14)));
  await setAppearance(main, { theme: 'dark', fontSize: 15, width: narrowWidth });
  await capture(main, '05b-draft-dark-default-narrow.png');
  const defaultNarrow = await workspaceRow(main);
  assert.deepEqual(defaultNarrow.text.filter(item => item.clipped).map(item => item.text), [longBranch],
    `At the default text size, the project label stays whole. Layout: ${JSON.stringify(defaultNarrow)}`);
  assert(defaultNarrow.text[1].width >= defaultNarrow.text[1].prefixWidth,
    `The default-size branch retains its readable prefix. Layout: ${JSON.stringify(defaultNarrow)}`);
  await evaluate(main, () => document.documentElement.style.setProperty('--appearance-scale', String(20 / 14)));
  await setAppearance(main, { theme: 'dark', fontSize: 20, width: narrowWidth });

  // A long project name must still leave the branch identifiable.
  await click(main, projectControl);
  await waitFor(main, name => document.querySelector(`.composer-project-menu button[title="${name}"]`), longProjectName);
  await click(main, `.composer-project-menu button[title="${longProjectName}"]`);
  await waitFor(main, ({ selector, name }) => document.querySelector(selector)?.getAttribute('aria-label') === `Switch workspace: ${name}`,
    { selector: projectControl, name: longProjectName });
  await waitForBranch(main, 'main');
  await click(main, branchControl);
  await waitFor(main, branch => document.querySelector(`.composer-branch-menu [role="menuitemradio"][title="${branch}"]`), longBranch);
  await click(main, `.composer-branch-menu [role="menuitemradio"][title="${longBranch}"]`);
  await waitFor(main, () => !document.querySelector('.composer-branch-menu'));
  await waitForBranch(main, longBranch);
  await setAppearance(main, { theme: 'dark', fontSize: 20, width: narrowWidth });
  await capture(main, '05a-draft-long-project-dark-large-narrow.png');
  const longProjectNarrow = await workspaceRow(main);
  assert.ok(longProjectNarrow.groups.every(rect => rect.right <= longProjectNarrow.barRight + 0.5),
    `Long project controls stay inside the bar. Layout: ${JSON.stringify(longProjectNarrow)}`);
  assert.deepEqual(longProjectNarrow.text.filter(item => item.clipped).map(item => item.text), [longProjectName, longBranch],
    `Long names truncate within the workspace row. Layout: ${JSON.stringify(longProjectNarrow)}`);
  assert(longProjectNarrow.text.slice(0, 2).every(item => item.width >= item.prefixWidth),
    `Both long names retain readable prefixes. Layout: ${JSON.stringify(longProjectNarrow)}`);
  for (const row of [narrow, defaultNarrow, longProjectNarrow]) {
    assert.equal(row.buttons.length, 2);
    assert(row.buttons.every((button, index) => button.left >= row.barLeft - 0.5
      && button.right <= row.barRight + 0.5 && button.hittable
      && row.buttons.slice(0, index).every(previous => button.left >= previous.right
        || button.top >= previous.bottom || button.bottom <= previous.top)),
    `Workspace controls stay in bounds, separate and clickable. Layout: ${JSON.stringify(row)}`);
  }

  // A folder outside Git keeps only the project group.
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'light');
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });
  await click(main, projectControl);
  await waitFor(main, () => document.querySelector('.composer-project-menu button[title="plain-folder"]'));
  await click(main, '.composer-project-menu button[title="plain-folder"]');
  await waitFor(main, selector => document.querySelector(selector)?.getAttribute('aria-label') === 'Switch workspace: plain-folder'
    && !document.querySelector('.composer-branch-control'), projectControl);
  await capture(main, '06-draft-non-git.png');

  const crossProject = await evaluate(main, async draft => {
    const { thread } = await window.wuu.startThread();
    try {
      await window.wuu.startTurn(thread.id, draft);
      return { rejected: false };
    } catch (error) {
      return { rejected: true, error: String(error) };
    }
  }, initialSkillDraft);
  assert(crossProject.rejected && crossProject.error.includes('invalid project skill selection'), 'Pasting the bound draft into another project must fail before invocation.');

  fs.writeFileSync(path.join(output, 'evidence.json'), `${JSON.stringify({
    fixture,
    featureHead,
    thread: { id: thread.id, cwd: thread.cwd, worktree: thread.worktree },
    railSelection,
    nativeClicks,
    narrow,
    defaultNarrow,
    longProjectNarrow,
    forkDismissals,
    providerRequests: requests.length,
    workspaceRebind: { cwd: rebound.cwd, chronologyVerified: true },
    crossProject,
    selectedSkill: { initialDraft: initialSkillDraft, draft: selectedDraft, initialWorktreeInstructionsVerified: true, loadedInstructionsVerified: true, modelInvocationRejected: true, modelToolResult: skillAdmissionResult },
  }, null, 2)}\n`);
  console.log(`composer worktree E2E passed; artifacts in ${output}`);
}

run().then(() => {
  clearTimeout(timeout);
  server.close();
  app.quit();
}).catch(async error => {
  fs.writeFileSync(path.join(output, 'provider-requests.json'), JSON.stringify(requests, null, 2));
  if (main && !main.isDestroyed()) {
    try {
      await capture(main, 'failure.png');
      const state = await evaluate(main, () => ({
        draft: document.querySelector('.composer textarea')?.value,
        flow: document.querySelector('.cached-conversation-pane[data-active="true"]')?.innerHTML,
        commands: [...document.querySelectorAll('.slash-command-item')].map(node => ({ id: node.id, text: node.textContent, disabled: node.disabled })),
      }));
      fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify(state, null, 2));
    } catch (captureError) { console.error('Could not capture failure state', captureError); }
  }
  console.error(error, 'FIXTURE', fixture);
  server.close();
  app.exit(1);
});
