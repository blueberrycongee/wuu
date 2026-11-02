// Actual selected product shell, preload and Go graph; native clicks and synthetic
// sessions supplied by session-switch-e2e.cjs. No external inference or accounts.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { waitForNativeBlankPage } = require('./session-switch-native-page.cjs');
module.exports = async ({ win, evaluate, waitFor, output, getResumeCount }) => {
  const viewport = await evaluate(win, () => ({ width: innerWidth, height: innerHeight, scale: devicePixelRatio }));
  const narrow = process.env.WUU_SWITCH_VARIANT === 'narrow';
  assert((narrow ? viewport.width >= 800 && viewport.width < 1000 : viewport.width >= 1300) && viewport.height >= 800, 'Presentation acceptance requires its declared wide/narrow product window');
  const stablePresentation = process.env.WUU_SWITCH_STABLE_PRESENTATION !== '0';
  const exceptions = [];
  win.webContents.debugger.on('message', (_event, method, params) => {
    if (method !== 'Runtime.exceptionThrown') return;
    exceptions.push(params.exceptionDetails);
    fs.writeFileSync(path.join(output, 'renderer-exceptions.json'), JSON.stringify(exceptions, null, 2));
    console.error('PRESENTATION_EXCEPTION', JSON.stringify(params.exceptionDetails));
  });
  await win.webContents.debugger.sendCommand('Runtime.enable');
  const results = [];
  const nativeFrames = [];
  const inputTargets = [];
  const frameFiles = new Map();
  let scenario;
  win.webContents.debugger.on('message', (_event, method, params) => {
    if (method !== 'Page.screencastFrame') return;
    void win.webContents.debugger.sendCommand('Page.screencastFrameAck', { sessionId: params.sessionId });
    if (!scenario) return;
    const pixels = Buffer.from(params.data, 'base64');
    const hash = createHash('sha256').update(pixels).digest('hex');
    const file = frameFiles.get(hash) || `${scenario}-frame-${String(nativeFrames.length).padStart(4, '0')}.png`;
    if (!frameFiles.has(hash)) { fs.writeFileSync(path.join(output, file), pixels); frameFiles.set(hash, file); }
    nativeFrames.push({ scenario, file, metadata: params.metadata });
  });
  await evaluate(win, () => {
    window.__presentationCompositorProfile = [];
    window.__presentationCaptureReleases = [];
    window.__presentationCaptureCompletions = [];
    const start = document.startViewTransition.bind(document);
    document.startViewTransition = update => {
      const record = { started: performance.now() }; window.__presentationCompositorProfile.push(record);
      const invoke = () => { record.callback = performance.now(); return update(); };
      const transition = start(() => window.__presentationCaptureBlocked
        ? new Promise(resolve => { window.__presentationCaptureReleases.push(() => { invoke(); resolve(); }); }) : invoke());
      const skip = transition.skipTransition.bind(transition);
      transition.skipTransition = () => { record.released = performance.now(); record.releaseStack = new Error().stack; skip(); };
      transition.ready.then(() => { record.ready = performance.now(); }, () => { record.skipped = performance.now(); });
      window.__presentationCaptureCompletions.push(Promise.all([transition.updateCallbackDone.catch(() => {}), transition.finished.catch(() => {})]).then(() => { record.finished = performance.now(); }));
      return transition;
    };
  });
  const frames = () => evaluate(win, () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  async function click(selector, text) {
    await waitFor(win, ({ selector, text }) => [...document.querySelectorAll(selector)].some(node =>
      (!text || node.textContent.includes(text) || node.getAttribute('aria-label')?.includes(text)) && node.getBoundingClientRect().width && !node.disabled), { selector, text });
    const target = await evaluate(win, async ({ selector, text }) => {
      const node = [...document.querySelectorAll(selector)].find(node => (!text || node.textContent.includes(text) || node.getAttribute('aria-label')?.includes(text)) && node.getBoundingClientRect().width && !node.disabled);
      node.scrollIntoView({ block: 'nearest', behavior: 'instant' });
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const rect = node.getBoundingClientRect();
      const point = { x: Math.round(rect.x + rect.width / 2), y: Math.round(rect.y + rect.height / 2) };
      node.addEventListener('click', event => { if (window.__presentationProbe) window.__presentationProbe.trusted.push(event.isTrusted); }, { once: true });
      const style = getComputedStyle(node);
      return { point, beforeHover: { hit: node.contains(document.elementFromPoint(point.x, point.y)),
        pointerEvents: style.pointerEvents, opacity: style.opacity, inert: !!node.closest('[inert]'),
        top: document.elementFromPoint(point.x, point.y)?.outerHTML.slice(0, 500) } };
    }, { selector, text });
    const point = target.point;
    inputTargets.push({ scenario, selector, text, ...target });
    fs.writeFileSync(path.join(output, 'native-input-targets.json'), JSON.stringify(inputTargets, null, 2));
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseMoved', ...point });
    // Sidebar add controls become hit-testable only after a real pointer hover.
    // Do not bypass their product visibility rules with a DOM click.
    await waitFor(win, ({ selector, text, point }) => [...document.querySelectorAll(selector)].some(node =>
      (!text || node.textContent.includes(text) || node.getAttribute('aria-label')?.includes(text))
      && node.contains(document.elementFromPoint(point.x, point.y))), { selector, text, point });
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', clickCount: 1, ...point });
    await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', clickCount: 1, ...point });
  }
  const thread = async index => {
    await click('.thread-row-main', `Switch session ${index}`);
    await waitFor(win, index => document.querySelector('.cached-conversation-pane[data-active="true"]')?.getAttribute('data-thread-id') === `switch-thread-${index}`, index);
  };
  async function openRelatedSplit() {
    const selector = '.cached-conversation-pane[data-active="true"] .session-message-source';
    for (let attempt = 0; attempt < 20; attempt++) {
      const position = await evaluate(win, selector => {
        const node = document.querySelector(selector), viewport = node?.closest('.scroll-region');
        if (!node || !viewport) throw new Error('Related-session message viewport is missing');
        const rect = node.getBoundingClientRect(), view = viewport.getBoundingClientRect();
        const dock = document.querySelector('[data-main-conversation-composer="dock"]')?.getBoundingClientRect();
        const top = view.top + 16, bottom = Math.min(view.bottom, dock?.top ?? innerHeight) - 16;
        return { visible: rect.top >= top && rect.bottom <= bottom, x: Math.round(view.x + view.width / 2),
          y: Math.round((top + bottom) / 2), deltaY: Math.max(-(bottom - top) * .8, Math.min((bottom - top) * .8, rect.y + rect.height / 2 - (top + bottom) / 2)) };
      }, selector);
      if (position.visible) { await click(selector); return; }
      // Large text can put the related-session control far below the dock.
      // Real wheel input participates in the product's scroll-owner bookkeeping.
      await win.webContents.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseWheel', x: position.x, y: position.y, deltaX: 0, deltaY: position.deltaY });
      await frames();
    }
    throw new Error('Related-session control did not become visible through native scrolling');
  }
  const draftReady = index => waitFor(win, index => {
    // Workspace identity remains in the visible titlebar when the responsive
    // tool rail intentionally removes the composer's workspace footer.
    const title = [...document.querySelectorAll('[data-wuu-component="conversation-titlebar"]')].find(node => node.getBoundingClientRect().height)?.textContent;
    const input = [...document.querySelectorAll('.composer textarea')].find(node => node.getBoundingClientRect().height && !node.closest('[inert]'));
    return !document.querySelector('.cached-conversation-pane[data-active="true"], .view-switch-loading-conversation, .view-switch-loading')
      && title?.includes(`Switch project ${index}`) && input && !input.disabled && !input.readOnly;
  }, index);
  const draft = async index => {
    await click('.project-row-new-thread', `Switch project ${index}`);
    await draftReady(index);
  };
  const ready = async () => {
    try {
      await waitFor(win, () => {
        const input = document.querySelector('.composer textarea');
        return input && !input.disabled && !input.readOnly && !input.closest('[inert]') && !document.querySelector('[data-wuu-presentation-frame], [data-wuu-compositor-frame]');
      });
    } catch (error) {
      const state = await evaluate(win, () => ({ probe: window.__presentationProbe, compositorProfile: window.__presentationCompositorProfile,
        input: document.querySelector('.composer textarea')?.outerHTML,
        inert: [...document.querySelectorAll('[inert]')].map(node => node.outerHTML.slice(0, 500)),
        frame: document.documentElement.getAttribute('data-wuu-compositor-frame') }));
      fs.writeFileSync(path.join(output, 'presentation-pending.json'), JSON.stringify({ ...state, nativeFrames }, null, 2));
      throw error;
    }
  };
  async function begin(name) {
    console.log('PRESENTATION_BEGIN', name);
    scenario = name;
    await win.webContents.debugger.sendCommand('Page.startScreencast', { format: 'png', everyNthFrame: 1 });
    const settling = await evaluate(win, () => document.getAnimations().map(animation => ({
      state: animation.playState, name: animation.animationName || animation.transitionProperty || animation.id,
      target: animation.effect?.target instanceof Element ? animation.effect.target.className : undefined,
      end: animation.effect?.getComputedTiming().endTime,
    })));
    fs.writeFileSync(path.join(output, `${name}-settling.json`), JSON.stringify(settling, null, 2));
    await evaluate(win, async name => {
      await document.fonts.ready;
      // Paused decorative/snapshot animations do not finish; they are not a
      // paint-settling deadline and must not deadlock the next native gesture.
      await Promise.all(document.getAnimations().filter(animation => animation.playState !== 'paused' && Number.isFinite(animation.effect.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => {})));
      const probe = window.__presentationProbe = { name, frames: [], trusted: [], active: true, started: performance.now() };
      const instances = new WeakMap(); let nextInstance = 0;
      const selectors = { shell: '.app-shell', sidebar: '.sidebar', titlebar: '.conversation-pane > .titlebar', composer: '[data-main-conversation-composer="dock"]', composerFooter: '[data-main-conversation-composer="dock"] .composer-footer-shell', splitPrimaryComposer: '.conversation-split-pane:first-of-type .composer', splitSecondaryComposer: '.conversation-split-pane:last-of-type .composer', status: '.conversation-status-cluster', toolRail: '[data-wuu-component="workspace-tool-rail"]' };
      const bounds = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
      const tick = () => {
        if (!probe.active) return;
        const nodes = Object.fromEntries(Object.entries(selectors).map(([key, selector]) => [key, document.querySelector(selector)]));
        const identities = Object.fromEntries(Object.entries(nodes).filter(([, node]) => node).map(([key, node]) => {
          if (!instances.has(node)) instances.set(node, ++nextInstance);
          return [key, instances.get(node)];
        }));
        const chrome = Object.fromEntries(Object.entries(nodes).filter(([, node]) => node && node.getBoundingClientRect().width && node.getBoundingClientRect().height).map(([key, node]) => {
          const style = getComputedStyle(node);
          if (!instances.has(node)) instances.set(node, ++nextInstance);
          return [key, { ...bounds(node), instance: instances.get(node), opacity: style.opacity, transform: style.transform }];
        }));
        const loading = [...document.querySelectorAll('.view-switch-loading-conversation')].map(bounds);
        const native = document.documentElement.hasAttribute('data-wuu-compositor-frame');
        const image = document.querySelector('[data-wuu-presentation-frame]');
        const imageStyle = native ? getComputedStyle(document.documentElement, '::view-transition-old(wuu-presentation)') : image && getComputedStyle(image);
        const animations = document.getAnimations().filter(animation => {
          if (animation.playState === 'paused' || animation.playState !== 'running' && !animation.pending) return false;
          const target = animation.effect?.target;
          return target instanceof Element && Object.values(nodes).some(node => node && (node === target || target.contains(node) || node === nodes.toolRail && node.contains(target)));
        }).map(animation => ({ target: animation.effect.target.className, name: animation.animationName || animation.transitionProperty || 'waapi' }));
        const rasterChrome = [];
        if (imageStyle?.clipPath.startsWith('path(')) {
          const match = imageStyle.clipPath.match(/^path\("(.*)"\)$/);
          const context = document.createElement('canvas').getContext('2d');
          if (!match || !context) throw new Error('Cannot inspect presentation clip');
          const clip = new Path2D(match[1]);
          for (const [key, rect] of Object.entries(chrome).filter(([key]) => key !== 'shell')) {
            for (const [dx, dy] of [[.05, .05], [.5, .5], [.95, .95]]) if (context.isPointInPath(clip, rect.x + rect.width * dx, rect.y + rect.height * dy)) rasterChrome.push(key);
          }
        }
        const railControls = [...(nodes.toolRail?.querySelectorAll('button') || [])].map(node => {
          const style = getComputedStyle(node);
          return { className: node.className, label: node.getAttribute('aria-label'), disabled: node.disabled, ariaDisabled: node.getAttribute('aria-disabled'),
            opacity: style.opacity, color: style.color, background: style.backgroundColor };
        });
        const layout = document.querySelector('.app-shell[data-web-workspace-open]') ? 'browser'
          : document.querySelector('.conversation-split') ? 'split' : 'conversation';
        // Keep inner editor evidence beside the outer dock sample: an upward
        // growth offset and an actual textarea resize are different failures.
        const input = nodes.composer?.querySelector('textarea');
        const stack = input?.closest('.composer-stack');
        const inputStyle = input && getComputedStyle(input);
        const composerGeometry = input && {
          input: bounds(input), inputInlineStyle: input.getAttribute('style'),
          inputScrollHeight: input.scrollHeight, inputOffsetHeight: input.offsetHeight,
          inputClientWidth: input.clientWidth, valueLength: input.value.length,
          valueLines: input.value.split('\n').length, fontStatus: document.fonts.status,
          inputFont: inputStyle.font, inputLineHeight: inputStyle.lineHeight,
          inputMinHeight: inputStyle.minHeight, inputMaxHeight: inputStyle.maxHeight,
          inputPadding: inputStyle.padding, inputBoxSizing: inputStyle.boxSizing,
          stack: stack && bounds(stack), stackInlineStyle: stack?.getAttribute('style'),
          multiline: stack?.dataset.multiline,
          growth: stack && getComputedStyle(stack).getPropertyValue('--composer-input-growth'),
          expandedOffset: stack && getComputedStyle(stack).getPropertyValue('--composer-expanded-offset'),
          parts: Object.fromEntries(['.composer', '.composer-frame', '.composer-frame-shell', '.composer-editor', '.composer-footer-shell']
            .map(selector => [selector, nodes.composer.querySelector(selector)])
            .filter(([, node]) => node).map(([selector, node]) => [selector, {
              ...bounds(node), inlineStyle: node.getAttribute('style'),
              transform: getComputedStyle(node).transform, marginBottom: getComputedStyle(node).marginBottom,
            }])),
        };
        probe.frames.push({ at: performance.now() - probe.started, layout, identities, chrome, composerGeometry, railControls, loading, animations, rasterChrome,
          frame: native ? document.documentElement.getAttribute('data-wuu-compositor-frame') : image?.getAttribute('data-wuu-presentation-frame'), rootInert: !!document.querySelector('.app-shell')?.closest('[inert]'),
          imageAnimations: image?.getAnimations().length || 0, imageOpacity: imageStyle?.opacity, imageTransform: imageStyle?.transform, imageWidth: imageStyle?.width, imageHeight: imageStyle?.height,
          fullscreenLoading: !!document.querySelector('.view-switch-loading'), recovery: !!document.querySelector('[data-wuu-component="launch-view"]') });
        requestAnimationFrame(tick);
      };
      tick();
    }, name);
  }
  async function end(name, allowRecovery = false, expectedDraft = '') {
    await ready(); await frames();
    await win.webContents.debugger.sendCommand('Page.stopScreencast'); scenario = undefined;
    const result = await evaluate(win, () => { const probe = window.__presentationProbe; probe.active = false; return { name: probe.name, started: probe.started, timeOrigin: performance.timeOrigin, frames: probe.frames, trusted: probe.trusted }; });
    result.allowRecovery = allowRecovery;
    result.footer = await evaluate(win, () => {
      const composer = document.querySelector('[data-main-conversation-composer="dock"]');
      const footer = composer?.querySelector('.composer-footer');
      const rect = node => { const r = node.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height }; };
      return footer && { bounds: rect(composer), overflow: footer.scrollWidth - footer.clientWidth,
        controls: [...footer.querySelectorAll('button')].filter(node => node.getBoundingClientRect().width).map(rect),
        fontSize: getComputedStyle(composer.querySelector('textarea')).fontSize };
    });
    results.push(result);
    fs.writeFileSync(path.join(output, 'presentation-results.json'), JSON.stringify({ viewport, stablePresentation, nativeFrames, compositorProfile: await evaluate(win, () => window.__presentationCompositorProfile), scope: 'Real selected product shell/preload/core, native input, per-rAF geometry/animation samples and compositor PNG frames. Synthetic local data; not physical-display proof.', results }, null, 2));
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
    assert(result.frames.length >= 2, `${name}: no frame observations`);
    assert(result.footer, `${name}: current product footer is missing`);
    assert(result.footer.overflow <= 1, `${name}: footer controls overflow`);
    if (narrow) assert.equal(parseFloat(result.footer.fontSize), 20, `${name}: narrow case must use the saved large text preference`);
    for (const control of result.footer.controls) {
      const b = result.footer.bounds;
      assert(control.x >= b.x - 1 && control.x + control.width <= b.x + b.width + 1
        && control.y >= b.y - 1 && control.y + control.height <= b.y + b.height + 1, `${name}: footer control escaped composer bounds`);
    }
    assert(result.trusted.length && result.trusted.every(Boolean), `${name}: requires native input`);
    if (stablePresentation) {
      const stableKeys = ['shell', 'sidebar'];
      // Split and browser routes intentionally change their composer topology.
      if (/(?:^|-)session-[01]$|sidebar-new-draft|empty-composer-workspace|capture-timeout/.test(name)) stableKeys.push('composer', 'composerFooter', 'toolRail');
      for (const key of stableKeys) {
        assert(result.frames.every(frame => frame.identities[key]), `${name}: ${key} disappeared during a business rebind`);
        assert.equal(new Set(result.frames.map(frame => frame.identities[key])).size, 1, `${name}: ${key} remounted during a business rebind`);
        // Expanded rails deliberately collapse, rather than remove, their footer.
        // Preserve that initial visibility too; a transient hide is still a bug.
        const visible = Boolean(result.frames[0].chrome[key]);
        assert(result.frames.every(frame => Boolean(frame.chrome[key]) === visible), `${name}: ${key} visibility changed during a business rebind`);
      }
      if (/(?:^|-)session-[01]$/.test(name)) {
        const visual = frame => frame.railControls.map(({ label, className, ...appearance }) => appearance);
        assert.deepEqual(visual(result.frames.at(-1)), visual(result.frames[0]), `${name}: fixture runtime controls must share configuration`);
        for (const frame of result.frames) assert.deepEqual(visual(frame), visual(result.frames[0]), `${name}: tool rail controls flashed at ${frame.at}ms`);
      }
      assert(result.frames.every(frame => frame.frame !== 'full' && !frame.recovery && !frame.fullscreenLoading), `${name}: stable presentation used whole-window replacement`);
    }
    const geometry = {};
    for (const frame of result.frames) {
      // A full retained frame covers a genuinely inert successor while its
      // selected surfaces finish their first layout. Track chrome only from
      // its first live frame; an unmasked or interactive owner gets no grace.
      const liveChrome = frame.frame !== 'full' || !frame.rootInert;
      for (const [key, rect] of Object.entries(liveChrome ? frame.chrome : {})) {
        // A successor may legitimately use different chrome geometry, but
        // it must stay fixed from the first frame in which it is live.
        // Split/browser routes intentionally add or remove composer-owned rail
        // controls. Geometry remains fixed within each declared layout, not each
        // arbitrary control mutation. Ordinary session cases get no such change.
        const layout = key === 'toolRail' && /-split-session$|-browser-via-conversations-workspace$/.test(name) ? frame.layout : '';
        const identity = `${key}:${rect.instance}:${layout}`; geometry[identity] ||= rect;
        for (const axis of ['x', 'y', 'width', 'height']) assert(Math.abs(rect[axis] - geometry[identity][axis]) <= 1,
          `${name}: ${key} ${axis} shifted within its live owner (${geometry[identity][axis]} → ${rect[axis]})`);
        assert.equal(rect.opacity, '1', `${name}: ${key} faded`);
        assert.equal(rect.transform, 'none', `${name}: ${key} transformed`);
      }
      if (liveChrome) assert.deepEqual(frame.animations, [], `${name}: shell chrome animated at ${frame.at}ms`);
      assert.deepEqual(frame.rasterChrome, [], `${name}: message raster includes live chrome`);
      if (!allowRecovery && frame.frame !== 'full') assert.equal(frame.fullscreenLoading || frame.recovery, false, `${name}: ordinary navigation replaced the whole window with loading`);
      if (frame.frame === 'full') {
        assert(!frame.chrome.shell || frame.rootInert, `${name}: full raster covered live interactive chrome`);
        assert.equal(frame.imageAnimations, 0, `${name}: static retired frame animated`);
        assert.equal(frame.imageOpacity, '1'); assert.equal(frame.imageTransform, 'none');
        assert(Math.abs(parseFloat(frame.imageWidth) - viewport.width) <= 1 && Math.abs(parseFloat(frame.imageHeight) - viewport.height) <= 1, `${name}: retired frame did not cover the viewport`);
      }
      for (const region of frame.loading) for (const [key, control] of Object.entries(frame.chrome).filter(([key]) => key !== 'shell')) {
        const width = Math.min(region.x + region.width, control.x + control.width) - Math.max(region.x, control.x);
        const height = Math.min(region.y + region.height, control.y + control.height) - Math.max(region.y, control.y);
        assert(!(width > 1 && height > 1), `${name}: loading animation overlaps ${key}`);
      }
    }
    // Once the current provider is live again, its actual input accepts native text.
    await click('.composer textarea');
    assert.equal(await evaluate(win, () => document.querySelector('.composer textarea')?.value), expectedDraft, `${name}: workspace navigation lost or replayed the local draft`);
    win.webContents.selectAll();
    await waitFor(win, () => { const input = document.querySelector('.composer textarea'); return document.activeElement === input && input.selectionStart === 0 && input.selectionEnd === input.value.length; });
    await win.webContents.insertText('Presentation input probe');
    await waitFor(win, () => document.querySelector('.composer textarea')?.value === 'Presentation input probe');
    win.webContents.selectAll();
    await waitFor(win, () => { const input = document.querySelector('.composer textarea'); return document.activeElement === input && input.selectionStart === 0 && input.selectionEnd === input.value.length; });
    win.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Backspace' }); win.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Backspace' });
    await waitFor(win, () => document.querySelector('.composer textarea')?.value === '');
    console.log('PRESENTATION_PASS', name);
  }
  for (const motion of ['no-preference', 'reduce']) {
    await win.webContents.debugger.sendCommand('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: motion }] });
    for (const index of [1, 0]) {
      const name = `${motion}-session-${index}`; await begin(name); await thread(index); await end(name);
    }
    await openRelatedSplit();
    await waitFor(win, () => document.querySelectorAll('.conversation-split-body').length === 2
      && [...document.querySelectorAll('.conversation-split-body')].some(node => node.textContent.includes('Related split answer'))
      && [...document.querySelectorAll('.conversation-split-pane textarea')].length === 2
      && [...document.querySelectorAll('.conversation-split-pane textarea')].every(node => !node.disabled && !node.readOnly && !node.closest('[inert]')));
    const split = `${motion}-split-session`; await begin(split); await thread(1); await end(split);
    const newDraft = `${motion}-sidebar-new-draft`; await begin(newDraft); await draft(0); await end(newDraft);
    const carriedDraft = 'Preserved workspace note\nSecond line stays editable\nThird line retains the taller footer';
    await click('.composer textarea'); await win.webContents.insertText(carriedDraft);
    await waitFor(win, value => document.querySelector('.composer textarea')?.value === value, carriedDraft);
    // Exercise the actual composer project menu. Its controls move to the
    // tool rail when expanded; collapse that rail through native input first.
    if (await evaluate(win, () => document.querySelector('.workspace-tool-rail-toggle')?.getAttribute('aria-expanded') === 'true')) {
      await click('.workspace-tool-rail-toggle');
      await waitFor(win, () => document.querySelector('.workspace-tool-rail-toggle')?.getAttribute('aria-expanded') === 'false'
        && document.querySelector('[data-main-conversation-composer="dock"] .composer-project-control button[aria-haspopup="menu"]')?.getBoundingClientRect().height);
    }
    await click('[data-main-conversation-composer="dock"] .composer-project-control button[aria-haspopup="menu"]');
    const empty = `${motion}-empty-composer-workspace`; await begin(empty);
    await click('.composer-project-menu button[role="menuitemradio"][aria-label="Switch project 1"]');
    await draftReady(1); await end(empty, false, carriedDraft);
    const { ipcMain, webContents } = require('electron');
    const existingContents = new Set(webContents.getAllWebContents().map(contents => contents.id));
    await click('.sidebar-ribbon-add');
    await waitFor(win, () => !!document.querySelector('.web-workspace-stage:not([hidden])'));
    const nativePageWaitStarted = performance.now();
    const page = await waitForNativeBlankPage(win, existingContents);
    console.log('PRESENTATION_NATIVE_PAGE_READY', JSON.stringify({ motion, ownerId: win.id, contentsId: page.id,
      url: page.getURL(), loading: page.isLoadingMainFrame(), waitedMs: performance.now() - nativePageWaitStarted }));
    const preferences = page.getLastWebPreferences();
    assert.equal(preferences.contextIsolation, true);
    assert.equal(preferences.nodeIntegration, false);
    assert.equal(preferences.sandbox, true);
    await waitFor({ webContents: page }, () => document.readyState === 'complete');
    // Exercise the shipped page preload, not a fixture-defined __dirname or
    // merely an existing path: native editing must reach its isolated IPC hook.
    let markEdited;
    const edited = new Promise(resolve => { markEdited = resolve; });
    const observeEdit = event => { if (event.sender === page) markEdited(); };
    ipcMain.on('wuu:web-workspace-page-edited', observeEdit);
    let editTimeout;
    try {
      await page.executeJavaScript(`document.body.innerHTML = '<input id="preload-probe">'; document.querySelector('input').focus(); undefined`);
      await page.insertText('Browser preload probe');
      await Promise.race([edited, new Promise((_, reject) => {
        editTimeout = setTimeout(() => reject(new Error('The ESM browser page did not load its shipped edit-protection preload')), 5000);
      })]);
      await page.executeJavaScript('document.body.replaceChildren(); undefined');
    } finally { clearTimeout(editTimeout); ipcMain.removeListener('wuu:web-workspace-page-edited', observeEdit); }
    const browser = `${motion}-browser-via-conversations-workspace`; await begin(browser);
    // The browser intentionally hides contextual workspace navigation. Follow
    // its real ribbon entry rather than clicking an invisible sidebar control.
    await click('.sidebar-ribbon-button[data-ribbon-id="conversations"]');
    await waitFor(win, () => !document.querySelector('.app-shell[data-web-workspace-open]'));
    // The workspace shortcut reopens its existing draft. Retargeting copies the
    // local draft to the destination without destroying the source workspace's.
    await draft(0); await end(browser, false, carriedDraft);
  }
  // The capture budget must not block business navigation or replay a late result.
  const timeout = 'capture-timeout-recovery';
  await evaluate(win, () => { window.__presentationCaptureBlocked = true; });
  const offset = await evaluate(win, () => window.__presentationCompositorProfile.length);
  const resumeOffset = getResumeCount();
  await begin(timeout); await thread(1); await ready();
  const stalled = await evaluate(win, offset => window.__presentationCompositorProfile.slice(offset), offset);
  // Stable presentation needs only the message action capture. Legacy baseline
  // comparison explicitly keeps its separate pre-retirement whole-window capture.
  assert.equal(stalled.length, stablePresentation ? 1 : 2,
    stablePresentation ? 'Stable presentation must not capture retired whole-window chrome' : 'Expected action and fresh retirement capture attempts');
  for (const capture of stalled) {
    assert.equal(capture.callback, undefined, 'Business navigation waited for a stalled capture callback');
    assert(Number.isFinite(capture.released), 'A stalled capture remained held after navigation completed');
  }
  const resumeCalls = getResumeCount() - resumeOffset;
  assert(resumeCalls <= 1, 'Capture recovery replayed the business thread resume');
  fs.writeFileSync(path.join(output, 'capture-timeout.json'), JSON.stringify({
    configuredCaptureBudgetMs: 120, observedReleaseMs: stalled.map(capture => capture.released - capture.started),
    targetReadyWhileCaptureBlocked: true, resumeCalls,
    timingScope: 'Timer observation includes host scheduling; deterministic boundary and lifecycle tests enforce the exact 120 ms deadlines.',
  }, null, 2));
  await evaluate(win, async offset => {
    if (!window.__presentationCaptureReleases.length) throw new Error('Capture fault did not start');
    window.__presentationCaptureBlocked = false;
    for (const release of window.__presentationCaptureReleases.splice(0)) release();
    await Promise.all(window.__presentationCaptureCompletions.slice(offset));
  }, offset);
  await end(timeout, !stablePresentation);
  assert.equal(await evaluate(win, () => window.__presentationCompositorProfile.length) - offset, stalled.length, 'Late capture started another navigation capture');
  assert.equal(getResumeCount() - resumeOffset, resumeCalls, 'Late capture replayed the business thread resume');
  assert.deepEqual(exceptions, [], 'Navigation raised an uncaught renderer exception');
  console.log('PRESENTATION_ACCEPTANCE', JSON.stringify({ status: 'passed', scenarios: results.length, output }));
};
