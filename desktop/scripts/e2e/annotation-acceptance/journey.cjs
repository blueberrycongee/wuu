// Real production-window interactions. This module has no substitute UI or preload.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');

module.exports = async function annotationJourney({
  main, output, evaluate, waitFor, focusWindow, nativeText, dragFirstPage,
  capture, snapshot, fittedPdf, firstQuote, originalSHA, checks,
}) {
  const directory = path.join(output, 'annotation-acceptance');
  fs.mkdirSync(directory, { recursive: true });
  const matrix = [];
  const recordings = [];
  let recordingActive = false;
  const checkpoint = async (name, state) => {
    await capture(`annotation-acceptance/${name}-${state}`);
    if (recordingActive) await new Promise(resolve => setTimeout(resolve, 420));
    return evaluate(() => {
      const geometry = (selector) => {
        const element = [...document.querySelectorAll(selector)].find(item => !item.closest('[inert]') && item.getBoundingClientRect().width > 0);
        return element ? { rect: element.getBoundingClientRect().toJSON(),
          font: getComputedStyle(element).fontSize, scrollWidth: element.scrollWidth,
          clientWidth: element.clientWidth } : null;
      };
      return { viewport: { width: innerWidth, height: innerHeight },
        menu: geometry('.pdf-selection-action-menu'),
        comment: geometry('.pdf-selection-comment__input'),
        card: geometry('.composer-file-selection-card'),
        preview: geometry('.composer-file-selection-card-popover'),
        composer: geometry('.workspace-document-composer'),
        document: geometry('[data-workspace-pdf-preview]'),
        focus: { tag: document.activeElement?.tagName, classes: document.activeElement?.className,
          label: document.activeElement?.getAttribute('aria-label') } };
    });
  };
  const nativeClick = async (selector) => {
    await focusWindow();
    const point = await evaluate(selector => {
      const element = [...document.querySelectorAll(selector)].find(item => !item.closest('[inert]') && item.getBoundingClientRect().width > 0);
      if (!element) throw new Error(`Missing ${selector}`);
      const rect = element.getBoundingClientRect();
      return { x: Math.round((rect.left + rect.right) / 2), y: Math.round((rect.top + rect.bottom) / 2) };
    }, selector);
    main.webContents.sendInputEvent({ type: 'mouseMove', ...point });
    main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...point });
    main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...point });
  };
  const sourceHighlights = () => evaluate(() => {
    const host = document.querySelector('[data-workspace-pdf-preview]');
    const root = host?.shadowRoot;
    return [...(root?.querySelectorAll('style[data-pdf-selection-highlight]') ?? [])].map(style => {
      const key = style.dataset.pdfSelectionHighlight;
      const ranges = [...(CSS.highlights?.get(key) ?? [])];
      return { key, quotes: ranges.map(range => range.toString()),
        rects: ranges.flatMap(range => [...range.getClientRects()].map(rect => rect.toJSON())) };
    });
  });
  const assertHighlightCleared = async (name) => {
    const highlights = await sourceHighlights();
    assert.deepEqual(highlights, [], `${name}: inactive source highlight is removed`);
    assert.deepEqual(await evaluate(() => [...(CSS.highlights?.keys() ?? [])]
      .filter(key => key.startsWith('pdf-selection-'))), [], `${name}: no stale source highlight remains registered`);
  };
  const escape = () => {
    main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'ESC' });
    main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'ESC' });
  };
  const assertVisible = (target, viewport, label) => {
    assert.ok(target && target.rect.width > 0 && target.rect.height > 0, `${label} is visible`);
    const rect = target.rect;
    assert.ok(rect.left >= -1 && rect.top >= -1 && rect.right <= viewport.width + 1
      && rect.bottom <= viewport.height + 1, `${label} fits viewport: ${JSON.stringify(rect)}`);
  };
  const startRecording = (name) => {
    recordingActive = true;
    const framesDir = path.join(directory, `${name}-frames`);
    fs.mkdirSync(framesDir, { recursive: true });
    const frames = [];
    const started = performance.now();
    let stopped = false;
    // Each frame is an actual capturePage result; timestamps retain variable cadence.
    const pending = (async () => {
      while (!stopped) {
        const timestamp = performance.now() - started;
        const bytes = (await main.webContents.capturePage()).toPNG();
        const file = `${String(frames.length).padStart(5, '0')}.png`;
        fs.writeFileSync(path.join(framesDir, file), bytes);
        frames.push({ file, timestamp, sha256: createHash('sha256').update(bytes).digest('hex') });
        await new Promise(resolve => setTimeout(resolve, 80));
      }
    })();
    return async () => {
      stopped = true;
      recordingActive = false;
      await pending;
      const recording = { name, directory: path.basename(framesDir), frames,
        source: 'Electron webContents.capturePage, no generated frames or interpolation' };
      recordings.push(recording);
      fs.writeFileSync(path.join(framesDir, 'frames.json'), JSON.stringify(recording, null, 2));
    };
  };
  const write = () => fs.writeFileSync(path.join(directory, 'manifest.json'), JSON.stringify({
    presentation: 'production', sourceCommit: process.env.WUU_DOCUMENT_COMMIT || null,
    sourceHeadCommit: process.env.WUU_DOCUMENT_HEAD_COMMIT || null,
    originalSHA,
    boundary: 'Full Wuu Electron main/preload/renderer and Go core; local synthetic provider, disposable profile. The production electron.vite.config.ts output is used without import substitutions or a presentation switch. Screenshots are real rendered app evidence; they are not user acceptance.',
    matrix, recordings: recordings.map(({ frames, ...recording }) => ({ ...recording, frameCount: frames.length })),
  }, null, 2));

  try {
    for (const theme of ['light', 'dark']) {
      for (const width of [1380, 820]) {
        for (const size of [14, 18]) {
          const name = `${theme}-${width}-${size}`;
          const variant = { theme, width, height: 1000, size, name, states: {} };
          matrix.push(variant);
          main.setSize(width, 1000);
          await evaluate(({ theme, size }) => {
            document.documentElement.dataset.theme = theme;
            document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
            document.documentElement.style.setProperty('--appearance-scale', String(size / 14));
            window.dispatchEvent(new Event('wuu-content-size-change'));
            document.querySelector('[data-workspace-pdf-preview]').shadowRoot
              .querySelector('.page[data-page-number="1"]').scrollIntoView();
          }, variant);
          await fittedPdf();
          await focusWindow();
          const before = (await snapshot()).turns.length;
          const record = (theme === 'light' && width === 1380 && size === 14)
            || (theme === 'dark' && width === 820 && size === 18);
          const stop = record ? startRecording(name) : null;
          try {
            variant.states.source = await checkpoint(name, '01-source');
            assert.equal(await dragFirstPage(), firstQuote, `${name}: native drag retains exact PDF text`);
            await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
            variant.states.selection = await checkpoint(name, '02-selection');
            assertVisible(variant.states.selection.menu, variant.states.selection.viewport, `${name} menu`);
            await nativeClick('.pdf-selection-action-menu .pdf-selection-menu__comment-toggle');
            await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
            variant.states.commentFocus = await checkpoint(name, '03-comment-focus');
            {
              variant.focusedSourceHighlight = await sourceHighlights();
              assert.equal(variant.focusedSourceHighlight.length, 1, `${name}: focused comment keeps one source highlight`);
              assert.deepEqual(variant.focusedSourceHighlight[0].quotes, [firstQuote], `${name}: source highlight retains the exact captured quote`);
              assert.ok(variant.focusedSourceHighlight[0].rects.length > 0, `${name}: highlighted range has visible text geometry`);
            }
            let comment = size === 18
              ? 'Keep the saved version intact. Explain the original passage before suggesting a clearer alternative, and preserve the author’s intended meaning.'
              : 'Keep the saved version intact.';
            await main.webContents.insertText(comment);
            // A composing Escape/Enter must stay in the editor. Synthetic composing
            // events test the actual DOM handlers, not an operating-system IME.
            await evaluate(() => {
              const input = document.querySelector('.pdf-selection-action-menu textarea');
              for (const key of ['Escape', 'Enter']) input.dispatchEvent(new KeyboardEvent('keydown',
                { key, code: key, isComposing: true, bubbles: true, cancelable: true }));
            });
            assert.equal(await evaluate(() => document.querySelector('.pdf-selection-action-menu textarea')?.value), comment,
              `${name}: composing keys preserve the comment`);
            escape();
            await waitFor(() => !document.querySelector('.pdf-selection-action-menu textarea'));
            await nativeClick('.pdf-selection-action-menu .pdf-selection-menu__comment-toggle');
            await waitFor(() => document.activeElement?.matches('.pdf-selection-action-menu textarea'));
            assert.equal(await evaluate(() => document.activeElement.value), comment,
              `${name}: Escape collapse and reopen retain the draft`);
            if (size === 18) {
              await evaluate(() => {
                const input = document.querySelector('.pdf-selection-action-menu textarea');
                input.setSelectionRange(input.value.length, input.value.length);
              });
              main.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Enter', modifiers: ['shift'] });
              main.webContents.sendInputEvent({ type: 'char', keyCode: '\r', modifiers: ['shift'] });
              main.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Enter', modifiers: ['shift'] });
              await waitFor(expected => document.querySelector('.pdf-selection-action-menu textarea')?.value === expected, `${comment}\n`);
              await main.webContents.insertText('Keep the page reference with the draft.');
              comment += '\nKeep the page reference with the draft.';
              assert.equal(await evaluate(() => document.querySelector('.pdf-selection-action-menu textarea')?.value), comment,
                `${name}: Shift+Enter adds a newline without submitting`);
            }
            variant.states.comment = await checkpoint(name, '04-comment');
            assertVisible(variant.states.comment.menu, variant.states.comment.viewport, `${name} comment`);
            assert.ok(variant.states.comment.menu.rect.bottom <= variant.states.comment.composer.rect.top + 1,
              `${name}: comment stays clear of the bottom composer`);
            await nativeClick('.pdf-selection-comment__submit');
            await waitFor(() => !document.querySelector('.pdf-selection-action-menu')
              && document.querySelector('.composer-file-selection-card'));
            assert.equal((await snapshot()).turns.length, before, `${name}: comment remains unsent`);
            await assertHighlightCleared(name);
            variant.states.draft = await checkpoint(name, '05-draft');
            await nativeClick('.composer-file-selection-card .pdf-quote-tile-main');
            await waitFor(() => document.querySelector('.composer-file-selection-card-popover blockquote'));
            variant.states.expanded = await checkpoint(name, '06-expanded');
            assertVisible(variant.states.expanded.preview, variant.states.expanded.viewport, `${name} quote preview`);
            assert.equal(await evaluate(() => document.querySelector('.composer-file-selection-card-popover blockquote').textContent), firstQuote);
            assert.equal(await evaluate(() => document.querySelector('.pdf-quote-comment-input').value), comment);
            if (record) {
              const points = await evaluate(() => {
                document.querySelector('.pdf-quote-comment-input').focus();
                const quote = document.querySelector('.composer-file-selection-card-popover blockquote');
                const node = document.createTreeWalker(quote, NodeFilter.SHOW_TEXT).nextNode();
                const range = document.createRange();
                range.setStart(node, 0); range.setEnd(node, 1);
                const start = range.getBoundingClientRect();
                range.setStart(node, node.length - 1); range.setEnd(node, node.length);
                const end = range.getBoundingClientRect();
                return { start: { x: Math.ceil(start.left), y: Math.round((start.top + start.bottom) / 2) },
                  end: { x: Math.floor(end.right), y: Math.round((end.top + end.bottom) / 2) } };
              });
              main.webContents.sendInputEvent({ type: 'mouseMove', ...points.start });
              main.webContents.sendInputEvent({ type: 'mouseDown', button: 'left', clickCount: 1, ...points.start });
              for (let step = 1; step <= 12; step++) {
                main.webContents.sendInputEvent({ type: 'mouseMove', button: 'left', modifiers: ['leftbuttondown'],
                  x: Math.round(points.start.x + (points.end.x - points.start.x) * step / 12),
                  y: Math.round(points.start.y + (points.end.y - points.start.y) * step / 12) });
                await evaluate(() => new Promise(resolve => requestAnimationFrame(resolve)));
              }
              main.webContents.sendInputEvent({ type: 'mouseUp', button: 'left', clickCount: 1, ...points.end });
              assert.equal(await evaluate(() => Boolean(document.querySelector('.composer-file-selection-card-popover'))), true,
                `${name}: textarea blur must not dismiss selecting the quoted excerpt`);
              const selected = await evaluate(() => window.getSelection().toString());
              const selectable = selected.trim().length > 5 && firstQuote.includes(selected.trim());
              variant.excerptSelection = { selected, selectable, userSelect: await evaluate(() =>
                getComputedStyle(document.querySelector('.composer-file-selection-card-popover blockquote')).userSelect) };
              assert.ok(selectable, `${name}: quoted excerpt is selectable`);
              variant.states.excerptSelected = await checkpoint(name, '06-excerpt-selected');
            }
            await nativeClick('.pdf-quote-comment-input');
            await evaluate(() => {
              const input = document.querySelector('.pdf-quote-comment-input');
              input.setSelectionRange(input.value.length, input.value.length);
            });
            comment += ' Recheck this saved quotation.';
            await main.webContents.insertText(' Recheck this saved quotation.');
            assert.equal(await evaluate(() => document.querySelector('.pdf-quote-comment-input').value), comment,
              `${name}: editing the draft comment updates the controlled value`);
            assert.equal(await evaluate(() => document.querySelector('.composer-file-selection-card-popover blockquote').textContent), firstQuote,
              `${name}: editing a comment never mutates the quoted source`);
            await nativeClick('.pdf-quote-source-action');
            await waitFor(() => !document.querySelector('.composer-file-selection-card-popover'));
            variant.states.sourceReturn = await checkpoint(name, '07-source-return');
            assert.equal(await evaluate(() => document.querySelector('[data-workspace-pdf-preview]').shadowRoot
              .querySelector('.page[data-page-number="1"] .textLayer span').textContent), firstQuote);
            await nativeClick('.composer-file-selection-card .pdf-quote-tile-main');
            await waitFor(() => document.querySelector('.composer-file-selection-card-popover')?.contains(document.activeElement));
            assert.equal(await evaluate(() => document.querySelector('.pdf-quote-comment-input').value), comment,
              `${name}: source return and reopening retain the edited draft comment`);
            variant.commentEdit = { retained: true, quoteUnchanged: true };
            escape();
            await waitFor(() => !document.querySelector('.composer-file-selection-card-popover'));
            assert.equal(await evaluate(() => document.activeElement?.matches('.composer-file-selection-card .pdf-quote-tile-main')), true,
              `${name}: Escape returns keyboard focus to the quote card`);
            await nativeClick('.composer-file-selection-card .pdf-quote-tile-remove');
            await waitFor(() => !document.querySelector('.composer-file-selection-card'));
            assert.equal((await snapshot()).turns.length, before, `${name}: removing a draft reference does not send`);
            variant.states.removed = await checkpoint(name, '08-removed');
            assert.equal(await dragFirstPage(), firstQuote);
            await waitFor(() => document.querySelector('.pdf-selection-action-menu'));
            await nativeClick('.pdf-selection-action-menu button[aria-label="Close"]');
            await waitFor(() => !document.querySelector('.pdf-selection-action-menu'));
            await assertHighlightCleared(`${name} cancellation`);
            variant.passed = true;
          } finally {
            if (stop) await stop();
            write();
          }
        }
      }
    }
    checks.push('production annotation matrix: 8 viewports/themes/font sizes, native selection → comment → unsent draft → source return, removal and Escape focus');
  } finally {
    main.setSize(1380, 1000);
    await evaluate(() => {
      document.documentElement.dataset.theme = 'light';
      document.documentElement.style.setProperty('--conversation-message-font-size', '14px');
      document.documentElement.style.setProperty('--appearance-scale', '1');
      window.dispatchEvent(new Event('wuu-content-size-change'));
    });
    write();
  }
};
