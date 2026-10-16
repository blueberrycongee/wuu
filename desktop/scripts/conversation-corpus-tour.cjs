const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { execFileSync } = require('node:child_process');
const { validateCorpus, replayCalls } = require('./conversation-corpus.cjs');

// Extends the real UI tour: production main/preload/renderer + Go persistence.
// Only the provider and tool contents are scripted; no source command executes.
function createCorpusTour({ output, matrix, evaluate, waitFor, draft, setAppearance, audit, chunk, errors }) {
  const source = path.join(__dirname, 'fixtures/conversation-corpus/corpus.json');
  const bytes = fs.readFileSync(source);
  const corpus = validateCorpus(JSON.parse(bytes));
  const selected = process.env.WUU_TOUR_ONLY?.split(',');
  const samples = selected ? corpus.samples.filter(sample => selected.includes(sample.id)) : corpus.samples;
  assert.ok(samples.length && (!selected || samples.length === selected.length), 'Unknown or duplicate corpus sample selection');
  const report = {
    selectedSamples: samples.map(sample => sample.id), corpusVersion: corpus.version, corpusSha256: createHash('sha256').update(bytes).digest('hex'),
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: __dirname, encoding: 'utf8' }).trim(),
    dirty: Boolean(execFileSync('git', ['status', '--porcelain'], { cwd: __dirname, encoding: 'utf8' }).trim()),
    executable: process.execPath, checkout: path.resolve(__dirname, '../..'),
    electron: process.versions.electron, chromium: process.versions.chrome, platform: process.platform,
    timing: corpus.timing, tools: corpus.tools, matrix, checkpoints: [], samples: [], status: 'running',
    buildSha256: Object.fromEntries([
      ['core', process.env.WUU_DESKTOP_CORE],
      ['main', path.join(__dirname, '../out/main/index.js')],
      ['preload', path.join(__dirname, '../out/preload/index.cjs')],
      ['rendererEntry', path.join(__dirname, '../out/renderer/index.html')],
    ].map(([name, file]) => [name, createHash('sha256').update(fs.readFileSync(file)).digest('hex')])),
  };
  const save = () => { report.rendererErrors = [...errors]; fs.writeFileSync(path.join(output, 'corpus-report.json'), JSON.stringify(report, null, 2) + '\n'); };
  let active;
  let win;
  let stepIndex = 0;
  let providerError;
  const marker = text => {
    // Pick readable text, excluding Markdown delimiters, to acknowledge rendering.
    const readable = text.replace(/\]\([^)]*\)?/g, ']');
    const chinese = [...readable.matchAll(/[\p{Script=Han}]{2,}/gu)];
    const matches = chinese.length ? chinese : [...readable.matchAll(/[A-Za-z][A-Za-z ]{6,}/g)];
    assert.ok(matches.length, 'Checkpoint needs visible text');
    return matches.at(-1)[0];
  };
  const settle = () => evaluate(win, async () => {
    await document.fonts.ready;
    let previous = ''; let stable = 0;
    for (let frame = 0; frame < 120; frame++) {
      await new Promise(resolve => requestAnimationFrame(resolve));
      const geometry = JSON.stringify([...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')].map(node => {
        const r = node.getBoundingClientRect(); return [r.x, r.y, r.width, r.height];
      }));
      stable = geometry === previous ? stable + 1 : 0;
      if (stable >= 3) return;
      previous = geometry;
    }
    throw new Error('Reply layout did not stabilize');
  });
  async function capture(stage, text) {
    await waitFor(win, needle => [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')].some(node => node.textContent.includes(needle)), marker(text));
    for (const appearance of matrix) {
      await setAppearance(win, appearance);
      await settle();
      const geometry = await evaluate(win, () => {
        const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
        const contents = [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')];
        const content = contents.at(-1);
        content?.scrollIntoView({ block: 'end' });
        return {
          viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio },
          font: content ? getComputedStyle(content).fontFamily : null,
          blocks: contents.map(rect),
          markdown: content ? [...content.querySelectorAll('p,h1,h2,h3,li,pre,table')].map(node => ({ tag: node.tagName, ...rect(node) })) : [],
        };
      });
      await settle();
      const name = `${active.id}-${stage}-${appearance.theme}-${appearance.size}-${appearance.width}.png`;
      fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG());
      const result = await audit(win);
      const details = [];
      if (stage === 'completed') {
        for (const selector of ['table', 'pre']) {
          const found = await evaluate(win, selector => Boolean([...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')].at(-1)?.querySelector(selector)), selector);
          if (!found) continue;
          let bounds;
          // Use native wheel input so scroll ownership follows the same path as
          // a reader. Programmatic jumps can be restored by the reading anchor.
          for (let attempt = 0; attempt < 60; attempt++) {
            bounds = await evaluate(win, selector => {
              const node = [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')].at(-1).querySelector(selector);
              const r = node.getBoundingClientRect();
              const viewport = node.closest('.conversation-pane').querySelector(':scope > .scroll-region').getBoundingClientRect();
              return { top: r.top, bottom: r.bottom, viewportTop: viewport.top, viewportBottom: viewport.bottom,
                x: Math.round(viewport.left + viewport.width * 0.6), y: Math.round(viewport.top + viewport.height * 0.4) };
            }, selector);
            if (bounds.top >= bounds.viewportTop - 1 && bounds.top < bounds.viewportBottom - 80) break;
            const distance = bounds.top - (bounds.viewportTop + 40);
            win.webContents.sendInputEvent({ type: 'mouseWheel', x: bounds.x, y: bounds.y,
              deltaX: 0, deltaY: -Math.sign(distance) * Math.min(Math.abs(distance), 400), canScroll: true });
            await settle();
          }
          assert.ok(bounds.top >= bounds.viewportTop - 1 && bounds.top < bounds.viewportBottom - 80,
            `${name}: ${selector} capture is offscreen: ${JSON.stringify(bounds)}`);
          const detail = name.replace('.png', `-${selector}.png`);
          fs.writeFileSync(path.join(output, detail), (await win.webContents.capturePage()).toPNG());
          details.push(detail);
        }
      }
      report.checkpoints.push({ sample: active.id, stage, appearance, screenshot: name, details, geometry, audit: result });
      save();
      assert.ok(result.overflowX <= 1, `${name}: page overflow ${result.overflowX}px`);
      assert.ok(geometry.blocks.length > 0, `${name}: missing reply`);
    }
  }
  async function respond(res, sampleID) {
    try {
      assert.equal(sampleID, active.id, 'Unexpected provider request');
      const index = stepIndex++;
      const step = active.steps[index];
      assert.ok(step, 'Unexpected extra model request');
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
      const parts = Array.from(step.text);
      const partial = Math.max(1, Math.floor(parts.length / 2));
      const checkpoint = index === active.target_step;
      // Acknowledged checkpoints, not wall-clock sleeps, hold the provider while
      // the real renderer paints all appearances. Timing is deliberately simulated.
      for (let offset = 0; offset < parts.length;) {
        const end = Math.min(parts.length, offset < partial ? Math.min(offset + 48, partial) : offset + 48);
        chunk(res, { role: 'assistant', content: parts.slice(offset, end).join('') });
        offset = end;
        if (checkpoint && offset === partial) await capture('target-partial', parts.slice(0, partial).join(''));
      }
      if (checkpoint) await capture('target-full', step.text);
      if (index === active.steps.length - 1 && !checkpoint) await capture('answer-live', step.text);
      replayCalls(step).forEach((call, callIndex) => chunk(res, { tool_calls: [{ index: callIndex,
        id: `call_${active.id}_${index}_${callIndex}`, type: 'function',
        function: { name: call.name, arguments: JSON.stringify(call.args) } }] }));
      chunk(res, {}, step.tool_count ? 'tool_calls' : 'stop');
      res.end('data: [DONE]\n\n');
    } catch (error) {
      providerError = error;
      res.destroy();
      save();
    }
  }
  async function run(window) {
    win = window;
    win.webContents.setBackgroundThrottling(false);
    // Even Markdown images/links cannot fetch real services during acceptance.
    win.webContents.session.webRequest.onBeforeRequest({ urls: ['http://*/*', 'https://*/*'] }, (_details, callback) => callback({ cancel: true }));
    save();
    try {
      for (const sample of samples) {
        active = sample; stepIndex = 0;
        await setAppearance(win, { theme: 'light', size: 15, width: 1280 });
        await evaluate(win, () => [...document.querySelectorAll('button')].find(button => (button.getAttribute('aria-label') || button.title || button.textContent).trim() === '新对话')?.click());
        await waitFor(win, () => !!document.querySelector('.composer textarea') && !document.querySelector('.cached-conversation-pane[data-active="true"] .turn'));
        await draft(win, `[${sample.id}] ${sample.prompt}`);
        await waitFor(win, () => !document.querySelector('.composer-send-button')?.disabled);
        await evaluate(win, () => document.querySelector('.composer-send-button').click());
        const deadline = Date.now() + 180000;
        while (Date.now() < deadline) {
          if (providerError) throw providerError;
          if (stepIndex === sample.steps.length && await evaluate(win, () => !!document.querySelector('.composer-send-button') && !document.querySelector('.composer-stop-button'))) break;
          await new Promise(resolve => setTimeout(resolve, 50));
        }
        assert.equal(stepIndex, sample.steps.length, `${sample.id}: incomplete provider replay`);
        await waitFor(win, () => !!document.querySelector('.composer-send-button') && !document.querySelector('.composer-stop-button'));
        await capture('completed', sample.steps.at(-1).text);
        // Reload the production shell, forcing history to come back from Go.
        win.webContents.reload();
        await waitFor(win, () => !!document.querySelector('.composer textarea'));
        await capture('history', sample.steps.at(-1).text);
        const persisted = await evaluate(win, async id => {
          const { threads } = await window.wuu.listAllThreads();
          const thread = threads.find(item => item.preview.includes(`[${id}]`));
          if (!thread) throw new Error('Missing persisted corpus thread');
          const result = await window.wuu.resumeThread(thread.id);
          return result.thread.turns.flatMap(turn => turn.items);
        }, sample.id);
        assert.ok(persisted.some(item => item.type === 'agent_message' && item.text === sample.steps.at(-1).text),
          `${sample.id}: persisted answer differs from the corpus`);
        const processButtons = await evaluate(win, () => document.querySelectorAll('.cached-conversation-pane[data-active="true"] .turn-process-toggle').length);
        assert.ok(processButtons > 0, `${sample.id}: tool history was not restored`);
        const processText = sample.steps.find(step => step.tool_count && step.text.trim());
        if (processText) {
          await evaluate(win, () => document.querySelector('.cached-conversation-pane[data-active="true"] .turn-process-toggle').click());
          await capture('history-expanded', processText.text);
        }
        report.samples.push({ id: sample.id, family: sample.family, model: sample.model, steps: stepIndex,
          toolCalls: sample.steps.reduce((sum, step) => sum + step.tool_count, 0), historyRestored: true });
        console.log(`PASS corpus ${sample.id} (${sample.family}), ${stepIndex} response steps`);
        save();
      }
      assert.deepEqual(errors, [], 'Renderer errors');
      report.status = 'passed';
    } catch (error) {
      report.status = 'failed'; report.error = String(error.message || error); throw error;
    } finally { save(); }
  }
  return { respond, run };
}
module.exports = { createCorpusTour };
