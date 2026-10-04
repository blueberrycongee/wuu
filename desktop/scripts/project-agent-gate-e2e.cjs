// The real renderer consumes the backend capability; Go behavioral tests cover
// creation, execution and recovery with both build-tag configurations.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const evidence = path.join(desktopRoot, "out", "e2e", "project-agent-gate");
fs.mkdirSync(evidence, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(evidence, "profile-")));
app.on("window-all-closed", () => {});
const evaluate = (win, fn) => win.webContents.executeJavaScript(`(${fn.toString()})()`, true);

async function waitFor(win, fn) {
  const end = Date.now() + 20000;
  while (Date.now() < end) {
    if (await evaluate(win, fn)) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out: ${fn}`);
}

async function click(win, selector) {
  await win.webContents.executeJavaScript(
    "document.querySelector(" + JSON.stringify(selector) + ").click()", true);
}

async function capture(win, name) {
  await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  fs.writeFileSync(path.join(evidence, name + '.png'), (await win.webContents.capturePage()).toPNG());
}

async function choose(win, testID, value) {
  await waitFor(win, () => document.querySelector('[data-testid="project-lead-model"]')?.disabled === false);
  await click(win, '[data-testid="' + testID + '"]');
  await waitFor(win, () => Boolean(document.querySelector('.select-menu-item')));
  await win.webContents.executeJavaScript("Array.from(document.querySelectorAll('.select-menu-item')).find(item => item.dataset.value === " + JSON.stringify(value) + ").click()", true);
}

async function presetBehavior(win, samples) {
  win.setContentSize(1180, 860);
  await click(win, '.sidebar-account-trigger');
  await waitFor(win, () => Boolean(document.querySelector('[data-settings-page="providers"]')));
  await click(win, '[data-settings-page="providers"]');
  await waitFor(win, () => Boolean(document.querySelector('.settings-shell')));
  await evaluate(win, () => Array.from(document.querySelectorAll('.settings-nav-item'))
    .find(item => /Built-in agent|内置 Agent/.test(item.textContent)).click());
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-lead-effort"]')));
  const before = await evaluate(win, () => window.projectPresetE2E.state().projectPresets);
  await choose(win, 'project-lead-effort', 'high');
  await waitFor(win, () => window.projectPresetE2E.state().projectPresets.low.lead.effort === 'high');
  let saved = await evaluate(win, () => window.projectPresetE2E.state().projectPresets);
  assert.deepEqual(saved.low.side, before.low.side);
  assert.deepEqual(saved.low.worker, before.low.worker);
  assert.deepEqual(saved.medium, before.medium);
  await evaluate(win, () => window.projectPresetE2E.failNextSave());
  await choose(win, 'project-worker-effort', '');
  await waitFor(win, () => document.querySelector('[data-testid="settings-project-presets"]')?.textContent.includes('Synthetic preset save failure'));
  assert.deepEqual(await evaluate(win, () => window.projectPresetE2E.state().projectPresets.low.worker), before.low.worker);
  await choose(win, 'project-worker-effort', '');
  await waitFor(win, () => window.projectPresetE2E.state().projectPresets.low.worker.variant === '');
  await choose(win, 'project-worker-effort', 'deliberate');
  await waitFor(win, () => window.projectPresetE2E.state().projectPresets.low.worker.variant === 'deliberate');
  await choose(win, 'project-worker-model', JSON.stringify(['e2e', 'mock-resize']));
  await waitFor(win, () => window.projectPresetE2E.state().projectPresets.low.worker.model === 'mock-resize');
  saved = await evaluate(win, () => window.projectPresetE2E.state().projectPresets);
  assert.equal(saved.low.worker.variant ?? '', '');
  assert.equal(saved.low.worker.effort ?? '', '');
  await choose(win, 'project-worker-model', JSON.stringify(['e2e', 'custom-model']));
  await choose(win, 'project-worker-effort', 'deliberate');
  for (const theme of ['light', 'dark']) for (const font of [14, 20]) for (const width of [1180, 640]) {
    win.setContentSize(width, 860);
    await win.webContents.executeJavaScript("document.documentElement.dataset.theme = " + JSON.stringify(theme) + "; document.documentElement.style.setProperty('--font-ui', '" + font + "px');");
    await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const name = ['preset-settings', theme, font, width].join('-');
    await capture(win, name);
    samples.push({ name, theme, font, width });
  }
  win.setContentSize(1180, 860);
  await click(win, '.settings-back-button');
  await waitFor(win, () => Boolean(document.querySelector('[data-functional-group-id="projects"] .sidebar-functional-action')));
  await click(win, '[data-functional-group-id="projects"] .sidebar-functional-action');
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-preset-picker"]')));
  for (const theme of ['light', 'dark']) for (const font of [14, 20]) for (const width of [1180, 640]) {
    win.setContentSize(width, 860);
    await win.webContents.executeJavaScript("document.documentElement.dataset.theme = " + JSON.stringify(theme) + "; document.documentElement.style.setProperty('--font-ui', '" + font + "px');");
    await evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await click(win, '[data-testid="project-preset-picker"]');
    await waitFor(win, () => document.querySelector('[data-testid="project-preset-panel"]')?.getBoundingClientRect().width > 0);
    const bounds = await evaluate(win, () => {
      const rect = document.querySelector('[data-testid="project-preset-panel"]').getBoundingClientRect();
      return { left: rect.left, right: rect.right, viewport: innerWidth };
    });
    assert.ok(bounds.left >= 0 && bounds.right <= bounds.viewport, JSON.stringify(bounds));
    const name = ['preset', theme, font, width].join('-');
    await capture(win, name);
    samples.push({ name, theme, font, width });
    await click(win, '[data-testid="project-preset-picker"]');
  }
  await click(win, '[data-testid="project-preset-picker"]');
  await click(win, '[data-testid="project-mode-ultra"]');
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-preset-incomplete"]')));
  await capture(win, 'preset-empty');
  await click(win, '[data-testid="project-mode-low"]');
  await waitFor(win, () => !document.querySelector('[data-testid="project-preset-incomplete"]'));
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-preset-panel"]')));
  win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Escape' });
  win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Escape' });
  await waitFor(win, () => !document.querySelector('[data-testid="project-preset-panel"]'));
  assert.equal(await evaluate(win, () => document.activeElement?.dataset.testid), 'project-preset-picker');
  await evaluate(win, () => {
    const input = document.querySelector('.composer textarea');
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(input, 'Verify the project preset snapshot.');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await evaluate(win, () => document.querySelector('.composer textarea').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true, cancelable: true })));
  await waitFor(win, () => Boolean(window.projectPresetE2E.state().createdProject));
  const start = await evaluate(win, () => window.projectPresetE2E.state().lastProjectStart);
  assert.equal(start.project.preset, 'low');
  for (const field of ['provider', 'model', 'effort', 'variant', 'speed']) assert.equal(start[field], undefined);
  await waitFor(win, () => document.querySelector('[data-testid="project-preset-picker"]')?.disabled === false);
  await click(win, '[data-testid="project-preset-picker"]');
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-preset-panel"]')));
  assert.equal(await evaluate(win, () => Boolean(document.querySelector('[data-testid="project-mode-low"]'))), false);
  const teamBefore = await evaluate(win, () => document.querySelector('.project-preset-team').textContent);
  await click(win, '.project-preset-configure');
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-lead-effort"]')));
  await choose(win, 'project-lead-effort', 'low');
  await waitFor(win, () => window.projectPresetE2E.state().projectPresets.low.lead.effort === 'low');
  await click(win, '.settings-back-button');
  await waitFor(win, () => Boolean(document.querySelector('[data-testid="project-preset-picker"]')));
  await click(win, '[data-testid="project-preset-picker"]');
  await waitFor(win, () => Boolean(document.querySelector('.project-preset-team')));
  assert.equal(await evaluate(win, () => document.querySelector('.project-preset-team').textContent), teamBefore);
  await capture(win, 'preset-saved-snapshot');
  samples.push({ name: 'preset-behavior', saves: 'passed', failureRecovery: 'passed', creation: 'passed', snapshot: 'passed', keyboardDismissal: 'passed' });
}

app.whenReady().then(async () => {
  const samples = [];
  for (const capability of ["absent", "disabled", "enabled"]) {
    const win = new BrowserWindow({
      width: 1180, height: 860, show: false,
      webPreferences: {
        contextIsolation: true, sandbox: false, backgroundThrottling: false,
        preload: path.join(__dirname, "resize-e2e-preload.cjs"),
        additionalArguments: [`--project-agent-e2e-${capability}`],
      },
    });
    await win.loadFile(path.join(desktopRoot, "out", "renderer", "index.html"));
    win.webContents.debugger.attach("1.3");
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "reduce" }],
    });
    await waitFor(win, () => Boolean(document.querySelector('[data-functional-group-id="workspace"]')));
    for (const theme of ["light", "dark"]) for (const font of [14, 20]) for (const width of [1180, 640]) {
      win.setContentSize(width, 860);
      await win.webContents.executeJavaScript(`document.documentElement.dataset.theme = '${theme}'; document.documentElement.style.setProperty('--font-ui', '${font}px');`);
      await evaluate(win, async () => {
        const toggle = document.querySelector('.conversation-pane [data-wuu-component="sidebar-toggle"]');
        if (toggle?.getAttribute("aria-pressed") === "false") toggle.click();
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      });
      await waitFor(win, () => document.getAnimations().every(animation =>
        animation.playState !== "running" || animation.effect?.getComputedTiming().iterations === Infinity,
      ));
      const projectEntry = await evaluate(win, () => {
        const group = document.querySelector('[data-functional-group-id="projects"]');
        return { section: Boolean(group), create: Boolean(group?.querySelector('.sidebar-functional-action')) };
      });
      assert.deepEqual(projectEntry, { section: capability === "enabled", create: capability === "enabled" });
      const name = `${capability}-${theme}-${font}-${width}`;
      fs.writeFileSync(path.join(evidence, `${name}.png`), (await win.webContents.capturePage()).toPNG());
      samples.push({ capability, theme, font, width, ...projectEntry });
      console.log(name);
    }
    if (capability === "enabled") await presetBehavior(win, samples);
    win.destroy();
  }
  fs.writeFileSync(path.join(evidence, "results.json"), JSON.stringify(samples, null, 2));
  console.log(`Project Agent capability acceptance passed: ${samples.length} samples in ${evidence}`);
  app.quit();
}).catch(error => {
  console.error(error);
  app.exit(1);
});
