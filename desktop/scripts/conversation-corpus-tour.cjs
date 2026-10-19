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
  const selectedSamples = selected ? corpus.samples.filter(sample => selected.includes(sample.id)) : corpus.samples;
  const textOnly = process.env.WUU_CORPUS_TEXT_ONLY === '1';
  const samples = textOnly ? selectedSamples.map(sample => ({ ...sample, target_step: 0, steps: [sample.steps.at(-1)] })) : selectedSamples;
  assert.ok(samples.length && (!selected || samples.length === selected.length), 'Unknown or duplicate corpus sample selection');
  const report = {
    selectedSamples: samples.map(sample => sample.id), corpusVersion: corpus.version, corpusSha256: createHash('sha256').update(bytes).digest('hex'),
    commit: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: __dirname, encoding: 'utf8' }).trim(),
    dirty: Boolean(execFileSync('git', ['status', '--porcelain'], { cwd: __dirname, encoding: 'utf8' }).trim()),
    executable: path.basename(process.execPath), replayMode: textOnly ? 'final-answer-only' : 'original-steps',
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
  const marker = (text, fromStart = false) => {
    // Pick readable text, excluding Markdown delimiters, to acknowledge rendering.
    const readable = text.replace(/\]\([^)]*\)?/g, ']');
    const chinese = [...readable.matchAll(/[\p{Script=Han}]{2,}/gu)];
    const matches = chinese.length ? chinese : [...readable.matchAll(/[A-Za-z][A-Za-z ]{6,}/g)];
    assert.ok(matches.length, 'Checkpoint needs visible text');
    return fromStart ? matches[0][0].slice(0, 28) : matches.at(-1)[0].slice(-28);
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
  async function capture(stage, text, index, restored) {
    const ordinal = active.steps.slice(0, index).filter(step => step.text.trim()).length;
    // Bind each checkpoint to its protocol item, not any DOM block containing a
    // common phrase. Exact source text also acknowledges the held stream chunk.
    await waitFor(win, ({ ordinal, text, restored }) => {
      const reply = (restored || window.corpusReplies).filter(item => item.text?.trim())[ordinal];
      return reply?.text === text;
    }, { ordinal, text, restored });
    const reply = await evaluate(win, ({ ordinal, restored }) =>
      (restored || window.corpusReplies).filter(item => item.text?.trim())[ordinal], { ordinal, restored });
    const selector = `.cached-conversation-pane[data-active="true"][data-thread-id="${reply.threadID}"] [data-response-turn-id="${reply.turnID}"][data-response-item-id="${reply.id}"] .agent-text`;
    const fromStart = stage === 'history-expanded';
    const needle = marker(text, fromStart);
    await waitFor(win, ({ selector, needle }) => document.querySelector(selector)?.textContent.includes(needle), { selector, needle });
    // Keep the selected conversation visible so these whole-window captures
    // cover sidebar, reply and composer together, including after reloads.
    await evaluate(win, () => document.querySelectorAll('.project-row[aria-expanded="false"]').forEach(row => row.click()));
    await waitFor(win, () => Boolean(document.querySelector('.sidebar-content .thread-row.active')));
    for (const appearance of matrix) {
      await setAppearance(win, appearance);
      await evaluate(win, () => document.querySelector('.sidebar-content .thread-row.active').scrollIntoView({ block: 'nearest' }));
      await settle();
      let geometry;
      // Wheel input preserves reader scroll ownership; a programmatic jump can
      // be undone by the conversation's reading anchor on the next frame.
      for (let attempt = 0; attempt < 60; attempt++) {
        geometry = await evaluate(win, ({ selector, needle, fromStart }) => {
          const content = document.querySelector(selector);
          const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
          const offset = fromStart ? content.textContent.indexOf(needle) : content.textContent.lastIndexOf(needle);
          if (offset < 0) throw new Error('Target marker disappeared');
          const walker = document.createTreeWalker(content, NodeFilter.SHOW_TEXT);
          const range = document.createRange();
          let position = 0; let started = false;
          for (let node = walker.nextNode(); node; node = walker.nextNode()) {
            const end = position + node.textContent.length;
            if (!started && offset < end) { range.setStart(node, offset - position); started = true; }
            if (started && offset + needle.length <= end) { range.setEnd(node, offset + needle.length - position); break; }
            position = end;
          }
          const markerRect = rect(range);
          const scroll = content.closest('.conversation-pane').querySelector(':scope > .scroll-region');
          const scrollRect = scroll.getBoundingClientRect();
          const clip = { left: 0, top: 0, right: innerWidth, bottom: innerHeight };
          let painted = true;
          for (let node = range.startContainer.parentElement; node; node = node.parentElement) {
            const style = getComputedStyle(node);
            painted &&= style.visibility === 'visible' && style.display !== 'none' && Number(style.opacity) > 0;
            const r = node.getBoundingClientRect();
            if (/(auto|scroll|hidden|clip)/.test(style.overflowY)) { clip.top = Math.max(clip.top, r.top); clip.bottom = Math.min(clip.bottom, r.bottom); }
            if (/(auto|scroll|hidden|clip)/.test(style.overflowX)) { clip.left = Math.max(clip.left, r.left); clip.right = Math.min(clip.right, r.right); }
          }
          const verticallyVisible = markerRect.y >= clip.top - 1 && markerRect.y + markerRect.height <= clip.bottom + 1;
          const visible = painted && markerRect.width > 0 && markerRect.height > 0 && verticallyVisible
            && markerRect.x >= clip.left - 1 && markerRect.x + markerRect.width <= clip.right + 1;
          const selectedRow = document.querySelector('.sidebar-content .thread-row.active');
          const selectedRect = rect(selectedRow);
          const selectedStyle = getComputedStyle(selectedRow);
          return {
            viewport: { width: innerWidth, height: innerHeight, dpr: devicePixelRatio },
            font: getComputedStyle(content).fontFamily,
            blocks: [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-text')].map(rect),
            markdown: [...content.querySelectorAll('p,h1,h2,h3,li,pre,table')].map(node => ({ tag: node.tagName, ...rect(node) })),
            target: { itemID: content.closest('[data-response-item-id]').dataset.responseItemId, ...rect(content) },
            marker: { text: needle, ...markerRect, visible, verticallyVisible }, clip,
            sidebarSelection: { ...selectedRect, label: selectedRow.querySelector('.thread-row-main')?.getAttribute('aria-label'),
              radius: selectedStyle.borderRadius, background: selectedStyle.backgroundColor,
              visible: selectedRect.width > 0 && selectedRect.x >= 0 && selectedRect.y >= 0
                && selectedRect.x + selectedRect.width <= innerWidth && selectedRect.y + selectedRect.height <= innerHeight },
            scroll: { x: Math.round(scrollRect.left + scrollRect.width * 0.6), y: Math.round(scrollRect.top + scrollRect.height * 0.4), viewportTop: scrollRect.top },
          };
        }, { selector, needle, fromStart });
        if (geometry.marker.visible) break;
        const horizontal = geometry.marker.verticallyVisible;
        const distance = horizontal
          ? (geometry.marker.x < geometry.clip.left ? geometry.marker.x - geometry.clip.left - 8
            : geometry.marker.x + geometry.marker.width - geometry.clip.right + 8)
          : geometry.marker.y - (geometry.scroll.viewportTop + 40);
        const delta = -Math.sign(distance) * Math.min(Math.abs(distance), 400);
        // Table and code panes own horizontal scrolling. Aim inside the visible
        // target row so wheel input reaches that pane instead of the outer view.
        win.webContents.sendInputEvent({ type: 'mouseWheel',
          x: horizontal ? Math.round((geometry.clip.left + geometry.clip.right) / 2) : geometry.scroll.x,
          y: horizontal ? Math.round(geometry.marker.y + geometry.marker.height / 2) : geometry.scroll.y,
          deltaX: horizontal ? delta : 0, deltaY: horizontal ? 0 : delta, canScroll: true });
        await settle();
      }
      const name = `${active.id}-${stage}-${appearance.theme}-${appearance.size}-${appearance.width}.png`;
      assert.ok(geometry.marker.visible, `${name}: target marker is offscreen: ${JSON.stringify(geometry)}`);
      assert.ok(geometry.sidebarSelection.visible, `${name}: selected sidebar row is offscreen`);
      fs.writeFileSync(path.join(output, name), (await win.webContents.capturePage()).toPNG());
      const result = await audit(win);
      const details = [];
      if (stage === 'completed') {
        for (const detailSelector of ['table', 'pre']) {
          const found = await evaluate(win, ({ selector, detailSelector }) => Boolean(document.querySelector(selector)?.querySelector(detailSelector)), { selector, detailSelector });
          if (!found) continue;
          let bounds;
          // Use native wheel input so scroll ownership follows the same path as
          // a reader. Programmatic jumps can be restored by the reading anchor.
          for (let attempt = 0; attempt < 60; attempt++) {
            bounds = await evaluate(win, ({ selector, detailSelector }) => {
              const node = document.querySelector(selector).querySelector(detailSelector);
              const r = node.getBoundingClientRect();
              const viewport = node.closest('.conversation-pane').querySelector(':scope > .scroll-region').getBoundingClientRect();
              return { top: r.top, bottom: r.bottom, viewportTop: viewport.top, viewportBottom: viewport.bottom,
                x: Math.round(viewport.left + viewport.width * 0.6), y: Math.round(viewport.top + viewport.height * 0.4) };
            }, { selector, detailSelector });
            if (bounds.top >= bounds.viewportTop - 1 && bounds.top < bounds.viewportBottom - 80) break;
            const distance = bounds.top - (bounds.viewportTop + 40);
            win.webContents.sendInputEvent({ type: 'mouseWheel', x: bounds.x, y: bounds.y,
              deltaX: 0, deltaY: -Math.sign(distance) * Math.min(Math.abs(distance), 400), canScroll: true });
            await settle();
          }
          assert.ok(bounds.top >= bounds.viewportTop - 1 && bounds.top < bounds.viewportBottom - 80,
            `${name}: ${detailSelector} capture is offscreen: ${JSON.stringify(bounds)}`);
          const detail = name.replace('.png', `-${detailSelector}.png`);
          fs.writeFileSync(path.join(output, detail), (await win.webContents.capturePage()).toPNG());
          details.push(detail);
        }
      }
      report.checkpoints.push({ sample: active.id, step: index, stage, appearance, screenshot: name, details, geometry, audit: result });
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
        if (checkpoint && offset === partial) await capture('target-partial', parts.slice(0, partial).join(''), index);
      }
      if (checkpoint) await capture('target-full', step.text, index);
      if (index === active.steps.length - 1 && !checkpoint) await capture('answer-live', step.text, index);
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
        await evaluate(win, () => {
          window.corpusReplies = [];
          window.stopCorpusReplies?.();
          window.stopCorpusReplies = window.wuu.onServerEvent(event => {
            if (event.kind !== 'notification') return;
            const { method, params } = event.message;
            if ((method === 'item/started' || method === 'item/completed') && params.item.type === 'agent_message') {
              let item = window.corpusReplies.find(item => item.id === params.item.id);
              if (!item) { item = { id: params.item.id, threadID: params.thread_id, turnID: params.turn_id }; window.corpusReplies.push(item); }
              item.text = params.item.text || '';
            } else if (method === 'item/agentMessage/delta') {
              const item = window.corpusReplies.find(item => item.id === params.item_id);
              if (item) item.text += params.delta;
            }
          });
        });
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
        await capture('completed', sample.steps.at(-1).text, sample.steps.length - 1);
        // Reload the production shell, forcing history to come back from Go.
        win.webContents.reload();
        await waitFor(win, () => !!document.querySelector('.composer textarea'));
        const persisted = await evaluate(win, async id => {
          const { threads } = await window.wuu.listAllThreads();
          const thread = threads.find(item => item.preview.includes(`[${id}]`));
          if (!thread) throw new Error('Missing persisted corpus thread');
          const result = await window.wuu.resumeThread(thread.id);
          return result.thread.turns.flatMap(turn => turn.items.filter(item => item.type === 'agent_message').map(item => ({ ...item, threadID: thread.id, turnID: turn.id })));
        }, sample.id);
        assert.ok(persisted.some(item => item.type === 'agent_message' && item.text === sample.steps.at(-1).text),
          `${sample.id}: persisted answer differs from the corpus`);
        await capture('history', sample.steps.at(-1).text, sample.steps.length - 1, persisted);
        const hasProcess = sample.steps.some(step => step.tool_count > 0);
        const processIndex = sample.steps.findIndex(step => step.tool_count && step.text.trim());
        if (hasProcess) {
          const toggle = '.cached-conversation-pane[data-active="true"] .turn-process-toggle[aria-expanded]';
          await waitFor(win, selector => Boolean(document.querySelector(selector)), toggle);
          await evaluate(win, selector => {
            const button = document.querySelector(selector);
            if (button.getAttribute('aria-expanded') !== 'true') button.click();
          }, toggle);
          await waitFor(win, selector => document.querySelector(selector)?.getAttribute('aria-expanded') === 'true', toggle);
          if (processIndex >= 0) await capture('history-expanded', sample.steps[processIndex].text, processIndex, persisted);
        }
        report.samples.push({ id: sample.id, family: sample.family, model: sample.model, steps: stepIndex,
          toolCalls: sample.steps.reduce((sum, step) => sum + step.tool_count, 0), historyRestored: true, processExpansion: hasProcess ? (processIndex >= 0 ? 'captured' : 'expanded-tools-only') : 'not-applicable' });
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
