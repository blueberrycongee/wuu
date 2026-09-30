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
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\n---\nWORKSPACE_SKILL_MARKER ${ARGUMENTS}\n');
git(project, 'add', '.');
git(project, 'commit', '-q', '-m', 'init');
git(project, 'switch', '-q', '-c', 'feature');
fs.writeFileSync(path.join(project, 'feature.txt'), 'feature\n');
fs.writeFileSync(path.join(project, skillPath), '---\nname: compact\ndescription: Explicit skill identity fixture\n---\nSELECTED_SKILL_MARKER ${ARGUMENTS}\n');
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
const server = http.createServer((req, res) => {
  let body = '';
  req.on('data', chunk => { body += chunk; });
  req.on('end', () => {
    requests.push({ path: req.url, body: JSON.parse(body) });
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = delta => `data: ${JSON.stringify({ id: 'fixture', model: 'fixture', choices: [{ index: 0, delta, finish_reason: null }] })}\n\n`;
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
  fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG());
}
const click = (win, selector) => evaluate(win, target => {
  const element = document.querySelector(target);
  if (!element) throw new Error(`missing ${target}`);
  element.click();
}, selector);
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

  // Send: the conversation runs in a new worktree at the start branch.
  await evaluate(main, text => {
    const textarea = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(textarea, text);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  }, 'Summarize this repository');
  await click(main, '.composer-send-button');
  await waitFor(main, () => document.querySelector('.fork-worktree-notice'));
  assert.equal(await evaluate(main, () => {
    const notice = document.querySelector('.fork-worktree-notice');
    const message = notice?.closest('[data-thread-id]')?.querySelector('.user-message-motion');
    return Boolean(message && (notice.compareDocumentPosition(message) & Node.DOCUMENT_POSITION_FOLLOWING));
  }), true, 'A conversation started in a worktree opens with its notice.');
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
  assert.equal(git(project, 'rev-parse', '--abbrev-ref', 'HEAD'), 'main');
  assert.equal(fs.readFileSync(path.join(project, 'README.md'), 'utf8'), 'uncommitted edit\n');
  assert.ok(requests.some(request => JSON.stringify(request.body.messages).includes('Summarize this repository')), 'The turn reached the provider.');
  await evaluate(main, () => document.querySelector('.fork-worktree-card')?.setAttribute('open', ''));
  await capture(main, '04-conversation-worktree-notice.png');

  // Explicit selection must beat the built-in control command and use this checkout.
  await waitFor(main, () => document.querySelector('.composer-send-button[data-wuu-state="send"]'));
  await evaluate(main, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, '/compact');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await waitFor(main, () => document.querySelector('.slash-command-item[id$="-skill:compact"]'));
  await capture(main, '07-explicit-skill-menu.png');
  await evaluate(main, () => document.querySelector('.slash-command-item[id$="-skill:compact"]').click());
  await waitFor(main, () => document.querySelector('.composer textarea').value.startsWith('/skill '));
  const selectedDraft = await evaluate(main, () => document.querySelector('.composer textarea').value);
  assert.deepEqual(JSON.parse(selectedDraft.split('\n')[0].slice('/skill '.length)), {
    name: 'compact', source: 'project', path: path.join(thread.cwd, skillPath),
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

  fs.writeFileSync(path.join(output, 'evidence.json'), `${JSON.stringify({
    fixture,
    featureHead,
    thread: { id: thread.id, cwd: thread.cwd, worktree: thread.worktree },
    narrow,
    providerRequests: requests.length,
    selectedSkill: { draft: selectedDraft, loadedInstructionsVerified: true },
  }, null, 2)}\n`);
  console.log(`composer worktree E2E passed; artifacts in ${output}`);
}

run().then(() => {
  clearTimeout(timeout);
  server.close();
  app.quit();
}).catch(async error => {
  if (main && !main.isDestroyed()) {
    try {
      await capture(main, 'failure.png');
      const state = await evaluate(main, () => ({
        draft: document.querySelector('.composer textarea')?.value,
        commands: [...document.querySelectorAll('.slash-command-item')].map(node => ({ id: node.id, text: node.textContent, disabled: node.disabled })),
      }));
      fs.writeFileSync(path.join(output, 'failure.json'), JSON.stringify(state, null, 2));
    } catch (captureError) { console.error('Could not capture failure state', captureError); }
  }
  console.error(error, 'FIXTURE', fixture);
  server.close();
  app.exit(1);
});
