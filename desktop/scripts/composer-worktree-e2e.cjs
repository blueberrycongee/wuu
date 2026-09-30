// Real Electron/main/preload/Go coverage for starting a conversation in its own
// Git worktree from the composer. Build core and desktop first;
// WUU_WORKTREE_CORE can override the binary. A local HTTP provider answers the
// turn, so no account or paid inference is used. Screenshots and the evidence
// record are written to artifacts/composer-worktree/ for visual review.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { spawnSync } = require('node:child_process');
const { pathToFileURL } = require('node:url');
const { app } = require('electron');

const desktop = path.resolve(__dirname, '..');
const output = process.env.WUU_WORKTREE_OUTPUT || path.resolve(desktop, '../artifacts/composer-worktree');
const fixture = fs.mkdtempSync(path.join(os.tmpdir(), 'wuu-composer-worktree-'));
const home = path.join(fixture, 'home');
const project = path.join(fixture, 'project');
const plain = path.join(fixture, 'plain');
for (const dir of [home, project, plain]) fs.mkdirSync(dir);
fs.rmSync(output, { recursive: true, force: true });
fs.mkdirSync(output, { recursive: true });
app.setPath('userData', path.join(fixture, 'profile'));
process.env.WUU_HOME = home;
process.env.WUU_DESKTOP_CORE = process.env.WUU_WORKTREE_CORE || path.join(desktop, 'build/bin/wuu-core');
delete process.env.ELECTRON_RENDERER_URL;
process.env.WUU_ENABLE_BROWSER = '0';
process.env.WUU_SAFE_MODE = '1';
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
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\n---\nWORKSPACE_SKILL_MARKER ${ARGUMENTS}\nResource base: ${CLAUDE_SKILL_DIR}\n');
fs.writeFileSync(path.join(project, path.dirname(skillPath), 'resource.txt'), 'WORKSPACE_RESOURCE_MARKER');
git(project, 'add', '.');
git(project, 'commit', '-q', '-m', 'init');
git(project, 'switch', '-q', '-c', 'feature');
fs.writeFileSync(path.join(project, 'feature.txt'), 'feature\n');
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\n---\nSELECTED_SKILL_MARKER ${ARGUMENTS}\nResource base: ${CLAUDE_SKILL_DIR}\n');
fs.writeFileSync(path.join(project, path.dirname(skillPath), 'resource.txt'), 'SELECTED_RESOURCE_MARKER');
git(project, 'add', '.');
git(project, 'commit', '-q', '-m', 'feature');
const featureHead = git(project, 'rev-parse', 'HEAD');
const longBranch = 'long-branch/an-unusually-long-branch-name-for-the-narrow-composer';
git(project, 'branch', longBranch);
git(project, 'switch', '-q', 'main');
// Uncommitted work in the shared checkout stays out of a new worktree.
fs.writeFileSync(path.join(project, 'README.md'), 'uncommitted edit\n');
fs.writeFileSync(path.join(plain, 'notes.txt'), 'not a repository\n');

const requests = [];
let rebindStage = 0;
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    requests.push({ path: req.url, body: JSON.parse(body) });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = delta => `data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`;
    const messages = requests.at(-1).body.messages || [];
    const latestUser = messages.findLast(message => message.role === 'user');
    if (typeof latestUser?.content === 'string' && latestUser.content.includes('Rebind workspace fixture') && rebindStage === 0) {
      rebindStage++;
      res.write(chunk({ role: 'assistant', content: 'Moving this session back to the project.' }));
      res.write(chunk({ tool_calls: [{ index: 0, id: 'workspace-fixture', type: 'function', function: { name: 'set_session_workspace', arguments: JSON.stringify({ root: fs.realpathSync(project) }) } }] }));
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
    if (await evaluate(win, fn, arg)) return;
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
const click = (win, selector) => evaluate(win, target => {
  const element = document.querySelector(target);
  if (!element) throw new Error(`missing ${target}`);
  element.click();
}, selector);
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
const gitControls = win => evaluate(win, () => {
  const group = document.querySelector('.composer-git-controls');
  const toggle = group?.querySelector('.composer-worktree-toggle');
  return group ? {
    branch: group.querySelector('.hero-project-pill-text')?.textContent,
    worktree: toggle?.getAttribute('aria-pressed'),
  } : null;
});

let main;
app.on('browser-window-created', (_event, win) => { main ||= win; });
const timeout = setTimeout(() => { console.error('E2E timeout', fixture); app.exit(1); }, 180000);

async function run() {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const engines = Object.fromEntries(['codex', 'claude', 'cursor', 'devin', 'grok', 'hermes', 'pi', 'opencode', 'antigravity'].map(id => [id, { enabled: false }]));
  fs.writeFileSync(path.join(home, 'config.json'), JSON.stringify({
    default_provider: 'fixture', engines,
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-only', model: 'fixture' } },
  }));
  const stamp = '2026-01-01T00:00:00Z';
  fs.writeFileSync(path.join(home, 'projects.json'), JSON.stringify({
    projects: [
      { id: 'repo', name: 'worktree-fixture', path: project, created_at: stamp, updated_at: stamp },
      { id: 'plain', name: 'plain-folder', path: plain, created_at: stamp, updated_at: stamp },
    ],
    active_context: { kind: 'project', project_id: 'repo', cwd: project },
  }));
  fs.writeFileSync(path.join(home, 'desktop-settings.json'), JSON.stringify({ onboarding_version: 100, language: 'en', theme: 'light' }));
  await import(pathToFileURL(path.join(desktop, 'out/main/index.js')).href);
  while (!main) await delay(25);
  main.webContents.setBackgroundThrottling(false);
  main.setSize(1280, 820);
  await waitFor(main, () => document.querySelector('.composer textarea') && document.querySelector('.composer-git-controls .hero-project-pill-text')?.textContent === 'main');

  // Off by default: branch choice keeps its checkout meaning.
  assert.deepEqual(await gitControls(main), { branch: 'main', worktree: 'false' });
  await capture(main, '01-draft-light-default.png');

  // On: the branch segment becomes the start point, and choosing a branch
  // checks nothing out in the shared project.
  await click(main, '.composer-worktree-toggle');
  await waitFor(main, () => document.querySelector('.composer-worktree-toggle')?.getAttribute('aria-pressed') === 'true');
  await click(main, '.composer-git-controls .hero-project-pill');
  await waitFor(main, () => document.querySelector('.composer-branch-menu [role="menuitemradio"][title="feature"]'));
  assert.equal(await evaluate(main, () => Boolean(document.querySelector('.composer-branch-menu form, .composer-branch-menu button[role="menuitem"]'))), false,
    'Creating and checking out a branch is not offered for a worktree start point.');
  await capture(main, '02-worktree-start-menu.png');
  await click(main, '.composer-branch-menu [role="menuitemradio"][title="feature"]');
  await waitFor(main, () => !document.querySelector('.composer-branch-menu') && document.querySelector('.composer-git-controls .hero-project-pill-text')?.textContent === 'feature');
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'main', 'Choosing a start point must not check out the shared project.');
  assert.deepEqual(await gitControls(main), { branch: 'feature', worktree: 'true' });
  await capture(main, '03-worktree-on-feature.png');

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
  await click(main, '.composer-send-button');
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
    const toggle = document.querySelector('.environment-toggle-button');
    if (!toggle.classList.contains('active')) toggle.click();
  });
  await waitFor(main, () => document.querySelector('.environment-panel.open .fork-worktree-card'));
  await click(main, '.environment-panel .fork-worktree-summary');
  assert(await evaluate(main, root => document.querySelector('.environment-panel .fork-worktree-meta')?.textContent.includes(root), thread.cwd));
  await capture(main, '04a-current-worktree-info.png');
  const inspectorGeometry = [];
  for (const theme of ['light', 'dark']) for (const fontSize of [14, 20]) for (const width of [1280, 760]) {
    // Close before resizing; the shell applies deferred panel-fit changes at
    // resize settlement, so reopening earlier can race its dismissal.
    await click(main, '.environment-panel-close-row button');
    await waitFor(main, () => !document.querySelector('.environment-panel'));
    await setAppearance(main, { theme, fontSize, width });
    await click(main, '.environment-toggle-button');
    await waitFor(main, () => document.querySelector('.environment-panel.open .fork-worktree-card'));
    for (const expanded of [false, true]) {
      await evaluate(main, expanded => {
        const card = document.querySelector('.fork-worktree-card');
        if (card.open !== expanded) card.querySelector('summary').click();
      }, expanded);
      await settle(main);
      await evaluate(main, () => Promise.all([...document.querySelectorAll('.environment-panel, .fork-worktree-chevron')]
        .flatMap(node => node.getAnimations()).map(animation => animation.finished.catch(() => {}))));
      const geometry = await evaluate(main, () => {
        const panel = document.querySelector('.environment-panel');
        const summary = panel.querySelector('.fork-worktree-summary').getBoundingClientRect();
        const close = panel.querySelector('.environment-panel-close-row button').getBoundingClientRect();
        const rect = panel.getBoundingClientRect();
        return {
          summaryRight: summary.right, closeLeft: close.left, closeWidth: close.width, closeHeight: close.height,
          closeCount: panel.querySelectorAll('.environment-panel-close-row button').length,
          inside: rect.left >= 0 && rect.right <= innerWidth && rect.top >= 0 && rect.bottom <= innerHeight,
          crop: { x: Math.floor(rect.x), y: Math.floor(rect.y), width: Math.ceil(rect.width), height: Math.ceil(rect.height) },
        };
      });
      assert.equal(geometry.closeCount, 1, 'The inspector has one dismissal control.');
      assert(geometry.summaryRight <= geometry.closeLeft, 'The worktree disclosure must not overlap the close button.');
      assert(geometry.closeWidth >= 28 && geometry.closeHeight >= 28 && geometry.inside, 'The close target stays usable and inside the panel.');
      inspectorGeometry.push({ theme, fontSize, width, expanded, ...geometry });
      // Closed metadata keeps temporary fixture paths out of the shareable crop.
      if (!expanded) fs.writeFileSync(path.join(output, `worktree-panel-${theme}-${fontSize}-${width}.png`),
        await captureCrop(main, geometry.crop));
    }
  }
  await click(main, '.environment-panel-close-row button');
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });

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
  main.setSize(760, 820);
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
  await click(main, '.composer-send-button');
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
  for (const theme of ['light', 'dark']) for (const fontSize of [14, 20]) for (const width of [1280, 760]) {
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
  await click(main, '.composer-send-button');
  await waitFor(main, () => [...document.querySelectorAll('.user-message-motion')].some(node => node.textContent.includes('Rebind workspace fixture'))
    && document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  await waitFor(main, async id => {
    const thread = (await window.wuu.listThreads()).threads.find(candidate => candidate.id === id);
    return thread && !thread.worktree && thread.status !== 'in_progress';
  }, thread.id);
  await waitFor(main, () => document.querySelector('.cached-conversation-pane[data-active="true"] .turn-process-toggle[aria-expanded="false"]'));
  await evaluate(main, () => [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .turn-process-toggle[aria-expanded="false"]')].at(-1)?.click());
  await waitFor(main, () => document.querySelector('.cached-conversation-pane[data-active="true"] .process-surface'));
  await evaluate(main, () => document.querySelectorAll('.cached-conversation-pane[data-active="true"] .process-surface-fold.has-details:not([open]) > summary').forEach(node => node.click()));
  await waitFor(main, () => document.querySelector('.workspace-tool-record[data-status="completed"]'));
  const rebound = await evaluate(main, async id => (await window.wuu.listThreads()).threads.find(candidate => candidate.id === id), thread.id);
  assert.equal(fs.realpathSync(rebound.cwd), fs.realpathSync(project));
  assert.ok(!rebound.worktree);
  assert(await evaluate(main, () => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    const record = pane.querySelector('.workspace-tool-record');
    const message = [...pane.querySelectorAll('.user-message-motion')].find(node => node.textContent.includes('Rebind workspace fixture'));
    return message && Boolean(message.compareDocumentPosition(record) & Node.DOCUMENT_POSITION_FOLLOWING)
      && !pane.querySelector('.fork-worktree-notice');
  }), 'The workspace operation must follow its initiating message.');
  await click(main, '.workspace-tool-record > summary');
  assert(await evaluate(main, root => document.querySelector('.workspace-tool-details')?.textContent.includes(root), fs.realpathSync(project)));
  await capture(main, '10-workspace-tool-light.png');
  main.setSize(760, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'dark';
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
    document.querySelector('.workspace-tool-record > summary').focus();
  });
  await capture(main, '11-workspace-tool-dark-large-narrow.png');
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.dataset.theme = 'light';
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });

  // The next draft starts shared again, and with worktree off a branch choice
  // is a checkout of the shared project.
  await click(main, '.session-tab-new');
  // Git status refreshes for the draft's project after leaving the worktree.
  await waitFor(main, () => document.querySelector('.composer-git-controls .hero-project-pill-text')?.textContent === 'main');
  assert.deepEqual(await gitControls(main), { branch: 'main', worktree: 'false' });
  await click(main, '.composer-git-controls .hero-project-pill');
  await waitFor(main, () => document.querySelector('.composer-branch-menu [role="menuitemradio"][title="feature"]:not(:disabled)'));
  await click(main, '.composer-branch-menu [role="menuitemradio"][title="feature"]');
  await waitFor(main, () => document.querySelector('.composer-git-controls .hero-project-pill-text')?.textContent === 'feature');
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'feature', 'With worktree off, choosing a branch checks it out.');

  // Dark, large text, narrow window with worktree on.
  await click(main, '.composer-worktree-toggle');
  main.setSize(760, 820);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'dark');
    document.documentElement.style.setProperty('--conversation-message-font-size', '20px');
    document.documentElement.style.setProperty('--appearance-scale', String(20 / 14));
  });
  await waitFor(main, () => document.querySelector('.composer-worktree-toggle')?.getAttribute('aria-pressed') === 'true');
  await click(main, '.composer-git-controls .hero-project-pill');
  await waitFor(main, branch => document.querySelector(`.composer-branch-menu [role="menuitemradio"][title="${branch}"]`), longBranch);
  await evaluate(main, branch => document.querySelector(`.composer-branch-menu [role="menuitemradio"][title="${branch}"]`).click(), longBranch);
  await waitFor(main, branch => !document.querySelector('.composer-branch-menu') && document.querySelector('.composer-git-controls .hero-project-pill-text')?.textContent === branch, longBranch);
  const narrow = await evaluate(main, () => {
    const bar = document.querySelector('.composer-workspace-bar').getBoundingClientRect();
    const groups = [...document.querySelectorAll('.composer-workspace-group')].map(group => group.getBoundingClientRect());
    const text = [...document.querySelectorAll('.composer-workspace-bar .hero-project-pill-text, .composer-worktree-toggle span')]
      .map(node => ({ text: node.textContent, clipped: node.scrollWidth > node.clientWidth + 1 }));
    return { barRight: bar.right, groups: groups.map(rect => ({ left: rect.left, right: rect.right, height: rect.height })), text };
  });
  assert.equal(narrow.groups.length, 2);
  assert.ok(narrow.groups.every(rect => rect.right <= narrow.barRight + 0.5), 'Both groups stay inside the bar.');
  assert.ok(Math.abs(narrow.groups[0].height - narrow.groups[1].height) < 0.5, 'Both groups share one height.');
  assert.deepEqual(narrow.text.filter(item => item.clipped).map(item => item.text), [longBranch],
    'Only the long branch name truncates; the project and toggle labels stay whole.');
  await capture(main, '05-draft-dark-large-narrow.png');

  // A folder outside Git keeps only the project group.
  main.setSize(1280, 820);
  await evaluate(main, () => {
    document.documentElement.setAttribute('data-theme', 'light');
    document.documentElement.style.removeProperty('--conversation-message-font-size');
    document.documentElement.style.removeProperty('--appearance-scale');
  });
  await click(main, '.composer-project-control .hero-project-pill');
  await waitFor(main, () => document.querySelector('.composer-project-menu button[title="plain-folder"]'));
  await click(main, '.composer-project-menu button[title="plain-folder"]');
  await waitFor(main, () => document.querySelector('.composer-project-control .hero-project-pill-text')?.textContent === 'plain-folder' && !document.querySelector('.composer-git-controls'));
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
    narrow,
    forkDismissals,
    inspectorGeometry,
    providerRequests: requests.length,
    workspaceRebind: { cwd: rebound.cwd, chronologyVerified: true },
    crossProject,
    selectedSkill: { initialDraft: initialSkillDraft, draft: selectedDraft, initialWorktreeInstructionsVerified: true, loadedInstructionsVerified: true },
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
