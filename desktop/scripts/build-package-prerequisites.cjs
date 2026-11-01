const { spawn: nodeSpawn } = require('node:child_process');
const { resolve } = require('node:path');

function scriptsForPlatform(platform) {
  // Core/plugins, mobile web, and Electron have disjoint outputs. Start core
  // and web first, then let Electron use the first free slot instead of waiting
  // for web; helper compilation and downloads fill the remaining slot.
  if (platform === 'darwin') return ['build:core', 'build:web', 'build:renderer', 'build:cua-mac', 'build:app-context-mac', 'build:speech-mac', 'build:ripgrep'];
  if (platform === 'linux') return ['build:core', 'build:web', 'build:renderer', 'build:cua-linux', 'build:ripgrep'];
  throw new Error(`Unsupported package build platform: ${platform}`);
}

async function runBuilds(scripts, { spawn = nodeSpawn, cwd = resolve(__dirname, '..') } = {}) {
  let next = 0;
  let failure;
  async function worker() {
    while (!failure && next < scripts.length) {
      const script = scripts[next++];
      try {
        await new Promise((resolveBuild, rejectBuild) => {
          // Two Vite graphs otherwise grow to their default heap limits together.
          // Bound only these build workers; preserve other caller Node options.
          const env = ['build:web', 'build:renderer'].includes(script)
            ? { ...process.env, NODE_OPTIONS: `${process.env.NODE_OPTIONS || ''} --max-old-space-size=1536`.trim() }
            : process.env;
          const child = spawn('npm', ['run', script], { cwd, env, stdio: 'inherit' });
          child.once('error', error => rejectBuild(new Error(`${script}: ${error.message}`)));
          child.once('close', (code, signal) => {
            if (code === 0) resolveBuild();
            else rejectBuild(new Error(`${script} failed (${signal || code})`));
          });
        });
      } catch (error) {
        failure ||= error;
      }
    }
  }
  // Two heavy builds fit standard hosted runners without unbounded compiler
  // fan-out. Join active children even on failure before packaging may proceed.
  await Promise.all([worker(), worker()]);
  if (failure) throw failure;
}

module.exports = { runBuilds, scriptsForPlatform };
if (require.main === module) {
  runBuilds(scriptsForPlatform(process.platform)).catch(error => { console.error(error); process.exitCode = 1; });
}
