// Production browser -> OpenSSH -> headless host -> encrypted relay -> Go core.
// Only model responses and disposable loopback SSH credentials are fixtures.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs/promises';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const repo = fileURLToPath(new URL('../../..', import.meta.url));
const binary = path.resolve(process.env.WUU_E2E_BINARY || path.join(repo, 'desktop/build/bin/wuu-core'));
const hostBundle = path.join(repo, 'desktop/out-remote/host.mjs');
const webRoot = path.join(repo, 'desktop/build/mobile-web');
const output = path.resolve(process.env.WUU_HEADLESS_E2E_OUTPUT || path.join(repo, '.tmp/e2e/headless-workspace'));
const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'wuu-headless-e2e-'));
const workspace = path.join(temp, 'workspace');
const secondWorkspace = path.join(temp, 'second-workspace');
const state = path.join(temp, 'state');
const home = path.join(temp, 'home');
const env = { ...process.env, HOME: home, WUU_HOME: state };
const runs = [], checks = [], pageErrors = [];
let browser, page, provider, occupied, releaseCompletion;
let completionRequested = false, completionDisconnected = false;
const completionGate = new Promise(resolve => { releaseCompletion = resolve; });
const finalText = 'The remote task completed while the SSH tunnel was disconnected.';
const fileText = 'export const lifecycle = "remote execution survived";\n';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const redact = text => text.replace(/wuu:\/\/pair\?[^\s]+/g, '[pairing URI]')
  .replace(/http:\/\/127\.0\.0\.1:\d+\/#pair=[^\s]+/g, '[browser pairing URL]')
  .replace(/"token":"[^"]+"/g, '"token":"[redacted]"');

async function until(check, label, timeout = 30_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { if (await check()) return; await pause(50); }
  throw new Error(`Timed out: ${label}`);
}
async function waitForPID(file, label) {
  let pid;
  await until(async () => {
    try { pid = Number((await fs.readFile(file, 'utf8')).trim()); }
    catch { return false; }
    return Number.isInteger(pid) && pid > 1;
  }, label);
  return pid;
}
function pass(label) { checks.push(label); console.log(`PASS: ${label}`); }
function start(name, command, args, options = {}) {
  const child = spawn(command, args, { cwd: repo, env, ...options, stdio: ['pipe', 'pipe', 'pipe'] });
  const run = { name, child, output: '', exited: false, code: null, signal: null };
  child.stdout.on('data', data => { run.output += data; });
  child.stderr.on('data', data => { run.output += data; });
  child.on('exit', (code, signal) => { Object.assign(run, { exited: true, code, signal }); });
  child.on('error', error => { run.output += error.message; run.exited = true; });
  runs.push(run);
  return run;
}
async function stop(run, signal = 'SIGTERM') {
  if (!run.exited) run.child.kill(signal);
  await until(() => run.exited, `${run.name} exits`, 10_000);
}
async function listen(server) {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  return server.address().port;
}
async function freePort() {
  const server = net.createServer(); const port = await listen(server);
  await new Promise(resolve => server.close(resolve)); return port;
}
async function accepts(port) {
  return new Promise(resolve => {
    const socket = net.connect({ host: '127.0.0.1', port });
    const finish = result => { socket.destroy(); resolve(result); };
    socket.once('connect', () => finish(true)); socket.once('error', () => finish(false));
    socket.setTimeout(500, () => finish(false));
  });
}
function live(pid) {
  try {
    process.kill(pid, 0);
    return !/^Z/.test(execFileSync('ps', ['-o', 'stat=', '-p', String(pid)], { encoding: 'utf8' }).trim());
  } catch { return false; }
}
function descendants(pid) {
  const rows = execFileSync('ps', ['-eo', 'pid=,ppid='], { encoding: 'utf8' }).trim().split('\n')
    .map(line => line.trim().split(/\s+/).map(Number));
  const found = new Set([pid]);
  for (let previous = -1; previous !== found.size;) {
    previous = found.size;
    for (const [child, parent] of rows) if (found.has(parent)) found.add(child);
  }
  found.delete(pid); return [...found];
}
function startHost(name, port, browserPort = port, extra = [], options = {}) {
  return start(name, process.execPath, [hostBundle, '--core', binary, '--web-root', webRoot,
    '--workdir', workspace, '--port', String(port), '--browser-port', String(browserPort), ...extra], options);
}
async function ready(run) {
  await until(() => {
    if (run.exited) throw new Error(`${run.name} exited before ready: ${redact(run.output)}`);
    return run.output.includes('Wuu workspace ready at');
  }, `${run.name} ready`);
}
async function failStartup(name, port, args, options) {
  const run = startHost(name, port, port, args, options);
  await until(() => run.exited, `${name} rejects startup`);
  assert.notEqual(run.code, 0, `${name} has a failing exit status`);
  assert(!run.output.includes('Wuu workspace ready at'), `${name} never announces readiness`);
  if (name !== 'occupied-listener') assert.equal(await accepts(port), false, `${name} leaves no listener behind`);
  return run;
}
async function connected() {
  await page.locator('.app-shell').waitFor();
  await page.locator('.web-connection-status').waitFor({ state: 'hidden', timeout: 45_000 });
  // Snapshot reads are allowed during restoration; a regular read must also
  // succeed before the fixture can exercise terminal ownership or other actions.
  await until(async () => {
    const rpcReady = await page.evaluate(() => window.wuu.listWorkspaceFiles().then(() => true, () => false));
    return rpcReady && await page.locator('.web-connection-status').isHidden();
  }, 'browser restoration and RPC ready');
}
async function terminal(pidFile) {
  return page.evaluate(async pidFile => {
    window.terminalEvents ??= [];
    window.wuu.onTerminalEvent(event => window.terminalEvents.push(event));
    const session = await window.wuu.startTerminalSession({ cols: 80, rows: 24 });
    await window.wuu.writeTerminalSession(session.id, `printf '%s' $$ > '${pidFile}'\n`);
    return session;
  }, pidFile);
}

await fs.mkdir(output, { recursive: true });
try {
  for (const dir of [workspace, secondWorkspace, state, home]) await fs.mkdir(dir);
  for (const artifact of [binary, hostBundle, path.join(webRoot, 'index.html')]) await fs.access(artifact);
  provider = http.createServer(async (req, res) => {
    try {
      let raw = ''; for await (const chunk of req) raw += chunk;
      if (req.method === 'GET') { res.end(JSON.stringify({ object: 'list', data: [{ id: 'headless-fixture', object: 'model' }] })); return; }
      const body = JSON.parse(raw);
      const task = body.tools?.some(tool => tool.function?.name === 'write_file')
        && body.messages?.some(message => message.role === 'user' && JSON.stringify(message.content).includes('Create the SSH lifecycle fixture'));
      const hasReply = body.messages?.some(message => message.role === 'tool');
      const message = task && !hasReply
        ? { role: 'assistant', tool_calls: [{ id: 'write-lifecycle', type: 'function', function: { name: 'write_file', arguments: JSON.stringify({ path: 'lifecycle.ts', content: fileText }) } }] }
        : { role: 'assistant', content: task ? finalText : 'SSH lifecycle fixture' };
      if (task && hasReply) {
        completionRequested = true;
        res.once('close', () => { if (!res.writableEnded) completionDisconnected = true; });
        await completionGate;
      }
      if (body.stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        for (const choice of [{ index: 0, delta: message, finish_reason: null },
          { index: 0, delta: {}, finish_reason: message.tool_calls ? 'tool_calls' : 'stop' }]) {
          res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', choices: [choice] })}\n\n`);
        }
        res.end('data: [DONE]\n\n');
      } else res.end(JSON.stringify({ id: 'fixture', choices: [{ index: 0, message, finish_reason: 'stop' }] }));
    } catch (error) { res.writeHead(500); res.end(String(error)); }
  });
  const modelPort = await listen(provider);
  await fs.writeFile(path.join(state, 'config.json'), JSON.stringify({ default_provider: 'fixture',
    providers: { fixture: { type: 'openai-compatible', base_url: `http://127.0.0.1:${modelPort}/v1`, api_key: 'local-test', model: 'headless-fixture' } },
    agent: { permission_mode: 'standard' } }));

  await failStartup('invalid-port', await freePort(), ['--port', '0']);
  await failStartup('missing-core', await freePort(), ['--core', path.join(temp, 'missing-core')]);
  await failStartup('missing-web-root', await freePort(), ['--web-root', temp]);
  await failStartup('missing-workspace', await freePort(), ['--workdir', path.join(temp, 'missing-workspace')]);
  occupied = net.createServer(); const occupiedPort = await listen(occupied);
  await failStartup('occupied-listener', occupiedPort, []);
  await new Promise(resolve => occupied.close(resolve)); occupied = undefined;
  pass('invalid ports, missing artifacts, and occupied listeners fail without announcing readiness');

  const accountState = path.join(temp, 'account-state'); await fs.mkdir(accountState);
  const accountEnv = { ...env, WUU_HOME: accountState };
  execFileSync(binary, ['remote', 'init'], { env: accountEnv });
  const accountFile = path.join(accountState, 'remote.json');
  const accountData = JSON.parse(await fs.readFile(accountFile, 'utf8'));
  accountData.account = { server: 'http://127.0.0.1:1', token: 'synthetic-test-token', username: 'fixture' };
  const accountBytes = JSON.stringify(accountData); await fs.writeFile(accountFile, accountBytes);
  for (const pair of [false, true]) {
    await failStartup(`account-home-${pair ? 'pair' : 'resume'}`, await freePort(), pair ? ['--pair'] : [], { env: accountEnv });
    assert.equal(await fs.readFile(accountFile, 'utf8'), accountBytes, 'account guard preserves existing credentials');
  }
  pass('account-linked homes reject local pairing and resume without altering account state');

  const delayedCore = path.join(temp, 'delayed-core');
  const delayedPID = path.join(temp, 'delayed-core.pid');
  await fs.writeFile(delayedCore, `#!/bin/sh\nif [ "$1" = remote ] && [ "$2" = status ]; then\n printf '%s' $$ > '${delayedPID}'\n exec sleep 120\nfi\nexec '${binary}' "$@"\n`, { mode: 0o700 });
  const statusPort = await freePort();
  const pendingStatus = startHost('cancel-initial-status', statusPort, statusPort, ['--core', delayedCore]);
  const statusPID = await waitForPID(delayedPID, 'initial status reaches controlled delay');
  await stop(pendingStatus);
  assert.equal(pendingStatus.code, 0);
  assert(!pendingStatus.output.includes('Wuu workspace ready at'));
  await until(async () => !live(statusPID) && !await accepts(statusPort), 'cancelled initial status releases its child');
  await fs.writeFile(delayedCore, `#!/bin/sh\nif [ "$1" = remote ] && [ "$2" = host ]; then\n printf '%s' $$ > '${delayedPID}'\n exec sleep 120\nfi\nexec '${binary}' "$@"\n`, { mode: 0o700 });
  for (const pair of [false, true]) {
    await fs.rm(delayedPID, { force: true });
    const port = await freePort();
    const run = startHost(`cancel-startup-${pair ? 'pair' : 'resume'}`, port, port, ['--core', delayedCore, ...(pair ? ['--pair'] : [])]);
    const pid = await waitForPID(delayedPID, 'host startup reaches controlled delay');
    await stop(run);
    assert.equal(run.code, 0, 'startup cancellation exits cleanly');
    assert(!run.output.includes('Wuu workspace ready at'), 'cancelled startup never announces readiness');
    await until(async () => !live(pid) && !await accepts(port), 'cancelled startup cleans owned host and relay');
  }
  pass('cancelling initial status or host registration closes children and never reports late readiness');

  const remotePort = await freePort(), localPort = await freePort(), sshPort = await freePort();
  let host = startHost('headless-host', remotePort, localPort, ['--pair']);
  await ready(host);
  const pairURL = host.output.match(/http:\/\/127\.0\.0\.1:\d+\/#pair=[^\s]+/)?.[0];
  assert(pairURL, 'production host prints a browser pairing URL');
  assert.equal(new URL(pairURL).port, String(localPort), 'pair URL uses the browser-side port');
  const pairing = new URL(decodeURIComponent(new URL(pairURL).hash.slice('#pair='.length)));
  assert.equal(new URL(pairing.searchParams.get('r')).port, String(localPort), 'pairing relay uses the forwarded browser port');
  const duplicate = await failStartup('duplicate-owner', await freePort(), []);
  assert(duplicate.output.includes('already running'), 'existing host ownership is reported');
  assert(live(host.child.pid), 'duplicate startup does not stop the existing owner');
  pass('ready follows actual host registration and duplicate ownership cannot produce a false ready result');

  const sshDir = path.join(temp, 'ssh'); await fs.mkdir(sshDir, { mode: 0o700 });
  for (const name of ['host', 'client']) execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-f', path.join(sshDir, name)]);
  const hostKey = (await fs.readFile(path.join(sshDir, 'host.pub'), 'utf8')).trim().split(/\s+/).slice(0, 2).join(' ');
  await fs.writeFile(path.join(sshDir, 'known_hosts'), `[127.0.0.1]:${sshPort} ${hostKey}\n`);
  const sshConfig = path.join(sshDir, 'config');
  await fs.writeFile(sshConfig, `Host fixture\n HostName 127.0.0.1\n User ${os.userInfo().username}\n Port ${sshPort}\n IdentityFile ${sshDir}/client\n IdentitiesOnly yes\n UserKnownHostsFile ${sshDir}/known_hosts\n StrictHostKeyChecking yes\n BatchMode yes\n ControlMaster auto\n ControlPath ${sshDir}/master\n ControlPersist yes\n ForkAfterAuthentication yes\n`);
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    const proxyPIDFile = path.join(sshDir, `${signal}.pid`);
    const proxyScript = path.join(sshDir, `${signal}.sh`);
    const proxyConfig = path.join(sshDir, `${signal}.conf`);
    await fs.writeFile(proxyScript, `printf '%s' $$ > '${proxyPIDFile}'\nexec sleep 120\n`);
    await fs.writeFile(proxyConfig, await fs.readFile(sshConfig, 'utf8') + ` ProxyCommand sh ${proxyScript}\n`);
    const proxied = start(`ssh-proxy-${signal}`, binary, ['remote', 'ssh', '--port', String(localPort), '--ssh-config', proxyConfig, 'fixture'], { detached: true });
    const proxyPID = await waitForPID(proxyPIDFile, 'ProxyCommand reaches the pre-authentication wait');
    const proxyChildren = descendants(proxied.child.pid);
    try {
      await stop(proxied, signal);
      assert.equal(proxied.code, 0, 'cancelling SSH exits cleanly');
      await until(() => !live(proxyPID) && proxyChildren.every(pid => !live(pid)), 'cancellation removes the pre-authentication ProxyCommand', 5000);
    } finally {
      for (const pid of proxyChildren) if (live(pid)) { try { process.kill(pid, 'SIGKILL'); } catch {} }
    }
  }
  const failedProxyPID = path.join(sshDir, 'failed-proxy.pid');
  const failedProxyScript = path.join(sshDir, 'failed-proxy.sh');
  const failedProxyConfig = path.join(sshDir, 'failed-proxy.conf');
  await fs.writeFile(failedProxyScript, `sleep 120 </dev/null >/dev/null 2>&1 &\nprintf '%s' $! > '${failedProxyPID}'\nexit 1\n`);
  await fs.writeFile(failedProxyConfig, await fs.readFile(sshConfig, 'utf8') + ` ProxyCommand sh ${failedProxyScript}\n`);
  const failedProxy = start('ssh-proxy-failure', binary, ['remote', 'ssh', '--port', String(localPort), '--ssh-config', failedProxyConfig, 'fixture'], { detached: true });
  const failedPID = await waitForPID(failedProxyPID, 'failing ProxyCommand creates its child');
  try {
    await until(() => failedProxy.exited, 'SSH reports the ProxyCommand failure');
    assert.notEqual(failedProxy.code, 0);
    await until(() => !live(failedPID), 'SSH failure removes ProxyCommand descendants', 5000);
  } finally {
    if (live(failedPID)) { try { process.kill(failedPID, 'SIGKILL'); } catch {} }
  }
  pass('noninteractive SSH cancellation and failure remove pre-authentication ProxyCommand descendants');
  const daemonConfig = path.join(sshDir, 'sshd_config');
  await fs.writeFile(daemonConfig, `Port ${sshPort}\nListenAddress 127.0.0.1\nHostKey ${sshDir}/host\nAuthorizedKeysFile ${sshDir}/client.pub\nStrictModes no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nPidFile ${sshDir}/pid\nAllowUsers ${os.userInfo().username}\nAllowTcpForwarding local\nPermitOpen 127.0.0.1:${remotePort}\nPermitTTY no\n`);
  const sshd = start('isolated-sshd', process.env.WUU_E2E_SSHD || '/usr/sbin/sshd', ['-D', '-e', '-f', daemonConfig]);
  await until(() => { if (sshd.exited) throw new Error(sshd.output); return sshd.output.includes('Server listening'); }, 'isolated sshd');
  const refused = start('ssh-refused', binary, ['remote', 'ssh', '--port', String(localPort), '--remote-port', String(remotePort),
    '--ssh-port', String(await freePort()), '--ssh-config', sshConfig, 'fixture']);
  await until(() => refused.exited, 'SSH connection refusal');
  assert.notEqual(refused.code, 0); assert.equal(await accepts(localPort), false);
  occupied = net.createServer(); const occupiedLocalPort = await listen(occupied);
  const collision = start('ssh-bind-collision', binary, ['remote', 'ssh', '--port', String(occupiedLocalPort),
    '--remote-port', String(remotePort), '--ssh-config', sshConfig, 'fixture']);
  await until(() => collision.exited, 'SSH bind failure');
  assert.notEqual(collision.code, 0, 'an unrelated listening socket cannot count as a successful tunnel');
  assert.equal(await accepts(occupiedLocalPort), true, 'failed SSH does not close the unrelated listener');
  await new Promise(resolve => occupied.close(resolve)); occupied = undefined;
  assert(live(host.child.pid), 'failed SSH connections do not affect the remote service');
  pass('SSH connection refusal and local bind collisions return failure without disturbing other listeners');
  const tunnel = name => start(name, binary, ['remote', 'ssh', '--port', String(localPort), '--remote-port', String(remotePort), '--ssh-config', sshConfig, 'fixture']);
  let ssh = tunnel('ssh-tunnel');
  await until(async () => { if (ssh.exited) throw new Error(ssh.output); return accepts(localPort); }, 'OpenSSH tunnel listening');
  browser = await chromium.launch({ channel: process.env.WUU_BROWSER_CHANNEL || undefined,
    executablePath: process.env.WUU_BROWSER_EXECUTABLE || undefined });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: 'zh-CN' });
  page = await context.newPage();
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.addInitScript(() => { window.documentIdentity = Math.random(); });
  await page.goto(pairURL); await connected();
  const pairingIdentity = await page.evaluate(() => localStorage.getItem('wuu.web.paired'));
  assert(pairingIdentity, 'browser stores the paired identity');
  const documentIdentity = await page.evaluate(() => window.documentIdentity);
  const input = page.locator('textarea:visible').first();
  await input.fill('Create the SSH lifecycle fixture using write_file.');
  await page.getByRole('button', { name: '发送', exact: true }).filter({ visible: true }).click();
  await until(() => completionRequested, 'real Go agent writes the fixture and waits for completion');
  assert.equal(await fs.readFile(path.join(workspace, 'lifecycle.ts'), 'utf8'), fileText);
  const threads = await page.evaluate(() => window.wuu.listThreads());
  const threadID = threads.threads.find(thread => thread.cwd === workspace)?.id;
  assert(threadID, 'active thread is rooted in the SSH workspace');
  const termPIDFile = path.join(temp, 'terminal.pid'), terminalRelease = path.join(temp, 'terminal-release');
  const term = await terminal(termPIDFile);
  const termPID = await waitForPID(termPIDFile, 'terminal PID');
  await page.evaluate(({ id, terminalRelease }) => window.wuu.writeTerminalSession(id,
    `printf 'terminal-%s\\n' before; while [ ! -f '${terminalRelease}' ]; do sleep 0.05; done; printf 'terminal-%s\\n' offline\n`), { id: term.id, terminalRelease });
  await until(() => page.evaluate(() => window.terminalEvents.some(event => event.text?.includes('terminal-before'))), 'terminal output before detach');
  await input.fill('Draft retained across the SSH disconnect');
  const sshChildren = descendants(ssh.child.pid);
  await stop(ssh, 'SIGINT');
  await until(async () => !await accepts(localPort) && sshChildren.every(pid => !live(pid)), 'foreground tunnel owns and removes its SSH child');
  await page.getByRole('status').filter({ hasText: '连接已断开' }).waitFor();
  assert(live(host.child.pid) && live(termPID), 'disconnect keeps remote host and terminal alive');
  assert.equal(completionDisconnected, false, 'disconnect does not cancel the model request');
  releaseCompletion(); await fs.writeFile(terminalRelease, 'release');
  await until(() => host.output.includes('push agent_done'), 'remote task completes while SSH is disconnected');
  ssh = tunnel('ssh-reconnect');
  await until(() => accepts(localPort), 'reconnected tunnel'); await connected();
  await page.getByText(finalText, { exact: true }).filter({ visible: true }).waitFor();
  assert.equal(await input.inputValue(), 'Draft retained across the SSH disconnect');
  assert.equal(await page.evaluate(() => window.documentIdentity), documentIdentity, 'reconnect does not replace the document');
  assert.equal(await page.evaluate(() => localStorage.getItem('wuu.web.paired')), pairingIdentity);
  await until(() => page.evaluate(() => window.terminalEvents.some(event => event.text?.includes('terminal-offline'))), 'terminal output replays');
  assert.equal((await page.evaluate(id => window.wuu.writeTerminalSession(id, "printf 'terminal-%s\\n' resumed\n"), term.id)).ok, true);
  await until(() => page.evaluate(() => window.terminalEvents.some(event => event.text?.includes('terminal-resumed'))), 'same terminal accepts input after resume');
  await page.screenshot({ path: path.join(output, 'reconnected.png') });
  pass('real SSH disconnect preserves active execution, document, pairing, draft, and resumed terminal ownership');

  const owned = descendants(host.child.pid);
  await stop(host);
  await until(async () => !await accepts(remotePort) && owned.every(pid => !live(pid)) && !live(termPID), 'service stop closes relay, app-server pool, and terminals');
  host = startHost('headless-restart', remotePort, localPort);
  await ready(host); await connected();
  const restored = await page.evaluate(id => window.wuu.resumeThread(id), threadID);
  assert(JSON.stringify(restored).includes(finalText), 'saved conversation survives full service restart');
  assert.equal(await input.inputValue(), 'Draft retained across the SSH disconnect');
  assert.equal((await page.evaluate(id => window.wuu.writeTerminalSession(id, 'ignored\n'), term.id)).ok, false, 'restart rejects the old terminal');
  assert(!host.output.includes('#pair='), 'restart does not reopen enrollment');
  pass('full service stop cleans owned processes and restart restores saved history without reopening pairing');

  const freshPIDFile = path.join(temp, 'fresh-terminal.pid');
  const freshTerminal = await terminal(freshPIDFile);
  const freshPID = await waitForPID(freshPIDFile, 'fresh terminal PID');
  await page.reload(); await connected();
  await until(() => !live(freshPID), 'fresh attachment releases previous terminal owner');
  assert.equal((await page.evaluate(id => window.wuu.writeTerminalSession(id, 'ignored\n'), freshTerminal.id)).ok, false);
  pass('a fresh browser attachment cannot keep or control a terminal from the replaced owner');

  await page.evaluate(() => { window.folderSelection = window.wuu.chooseProjectFolder(); });
  const dialog = page.getByRole('dialog', { name: '选择电脑文件夹', exact: true });
  await until(() => dialog.getByRole('button', { name: '前往', exact: true }).isEnabled(), 'initial folder listing');
  await dialog.getByRole('textbox', { name: '电脑路径' }).fill(secondWorkspace);
  await dialog.getByRole('button', { name: '前往', exact: true }).click();
  await until(async () => await dialog.getByRole('textbox', { name: '电脑路径' }).inputValue() === secondWorkspace
    && await dialog.getByRole('button', { name: '使用此文件夹', exact: true }).isEnabled(), 'second workspace folder');
  await dialog.getByRole('button', { name: '使用此文件夹', exact: true }).click();
  const selected = await page.evaluate(() => window.folderSelection);
  const second = selected.projects.find(project => project.path === secondWorkspace);
  assert(second, 'remote folder picker registers the second workspace');
  await page.evaluate(id => window.wuu.selectProject(id), second.id);
  const otherThread = await page.evaluate(() => window.wuu.startThread());
  assert.equal(otherThread.thread.cwd, secondWorkspace);
  const original = await page.evaluate(id => window.wuu.resumeThread(id), threadID);
  assert.equal(original.thread.cwd, workspace, 'resuming an existing thread keeps its workspace after switching projects');
  assert(JSON.stringify(original).includes(finalText));
  const first = selected.projects.find(project => project.path === workspace);
  await page.evaluate(id => window.wuu.removeProject(id), first.id);
  await page.reload(); await connected();
  const discovered = await page.evaluate(() => window.wuu.listProjects());
  assert(discovered.projects.some(project => project.path === secondWorkspace));
  assert(!discovered.projects.some(project => project.path === workspace));
  await page.evaluate(id => window.wuu.selectProject(id), second.id);
  assert.equal((await page.evaluate(() => window.wuu.startThread())).thread.cwd, secondWorkspace);
  await page.screenshot({ path: path.join(output, 'workspace-recovery.png') });
  pass('cross-workspace resume stays scoped and discovery recovers after the startup workspace is removed');
  assert.deepEqual(pageErrors, []);
  const finalOwned = descendants(host.child.pid);
  await stop(ssh); await stop(host, 'SIGHUP'); await stop(sshd);
  await until(async () => !await accepts(localPort) && !await accepts(remotePort) && finalOwned.every(pid => !live(pid)), 'signal cleanup');
  pass('SIGTERM closes the foreground tunnel and SIGHUP cleans the headless service');
  await fs.writeFile(path.join(output, 'result.json'), JSON.stringify({ ok: true, checks,
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim(),
    workingTree: execFileSync('git', ['status', '--porcelain'], { cwd: repo, encoding: 'utf8' }).trim(),
    binarySHA256: createHash('sha256').update(await fs.readFile(binary)).digest('hex'),
    hostSHA256: createHash('sha256').update(await fs.readFile(hostBundle)).digest('hex'),
    webIndexSHA256: createHash('sha256').update(await fs.readFile(path.join(webRoot, 'index.html'))).digest('hex'),
    platform: `${process.platform}/${process.arch}`, finishedAt: new Date().toISOString() }, null, 2));
  console.log(`Evidence: ${output}`);
} catch (error) {
  if (page && !page.isClosed()) {
    await page.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {});
    await fs.writeFile(path.join(output, 'failure-page.txt'), await page.locator('body').innerText().catch(() => ''));
  }
  await fs.writeFile(path.join(output, 'result.json'), JSON.stringify({ ok: false, checks, error: String(error), stack: error.stack }, null, 2));
  throw error;
} finally {
  releaseCompletion();
  await browser?.close();
  for (const run of runs) {
    const children = run.exited ? [] : descendants(run.child.pid);
    if (!run.exited) await stop(run).catch(() => run.child.kill('SIGKILL'));
    for (const pid of children) if (live(pid)) { try { process.kill(pid, 'SIGKILL'); } catch {} }
    await fs.writeFile(path.join(output, `${run.name}.log`), redact(run.output));
  }
  provider?.closeAllConnections(); provider?.close(); occupied?.close();
  await fs.rm(temp, { recursive: true, force: true });
}
