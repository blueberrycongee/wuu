// Real pointer input and composed screenshots for native browser chrome.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { contentTracing } = require('electron');

module.exports = async function acceptChrome(h, view) {
  const { win, output, report, evaluate, until, settle, nativeInput, capture, delay, fixtureTabs } = h;
  const wc = view.webContents;
  const samples = report.browserChrome = [];
  for (const [width, height, zoom, theme] of [[1280, 800, 1, 'light'], [600, 700, 1, 'light'], [1280, 800, 1.25, 'dark']]) {
    win.setBounds({ x: 30, y: 30, width, height }); win.webContents.setZoomFactor(zoom);
    await evaluate(theme => { document.documentElement.dataset.theme = theme; }, theme);
    const title = theme === 'dark' ? '飞书 | 字节跳动旗下 AI 工作平台 - 飞书官网' : 'Resize animated';
    await wc.executeJavaScript('document.title=' + JSON.stringify(title));
    await until(() => evaluate(({ id, title }) => document.querySelector(`[data-ribbon-id="${id}"]`)?.getAttribute('aria-label') === title, { id: fixtureTabs.animated.id, title }), 'ribbon title updates');
    await settle();
    const anchor = await evaluate(id => {
      const r = document.querySelector(`[data-ribbon-id="${id}"]`).getBoundingClientRect();
      return { x: r.x, y: r.y, width: r.width, height: r.height };
    }, fixtureTabs.animated.id);
    const b = win.getContentBounds();
    const focusBeforeHover = wc.isFocused(), boundsBeforeHover = view.getBounds();
    await nativeInput(['mousemove', String(Math.round(b.x + (anchor.x + anchor.width / 2) * zoom)), String(Math.round(b.y + (anchor.y + anchor.height / 2) * zoom))]);
    // Chromium's native tooltip has its own hover delay, outside renderer rAF.
    await delay(1300);
    let hint = await evaluate(() => {
      const el = document.querySelector('[role="tooltip"]'); if (!el || getComputedStyle(el).visibility === 'hidden') return null;
      const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width, height: r.height };
    });
    const nativeHint = win.getChildWindows().find(window => window.isVisible() && !window.isFocusable());
    if (nativeHint) {
      const r = nativeHint.getBounds();
      hint = { x: (r.x - b.x) / zoom, y: (r.y - b.y) / zoom, width: r.width / zoom, height: r.height / zoom,
        native: true, text: await nativeHint.webContents.executeJavaScript('document.body.innerText') };
      const textBox = await nativeHint.webContents.executeJavaScript(`(()=>{const hint=document.getElementById('hint'),r=hint.getBoundingClientRect();return{height:r.height,viewport:innerHeight}})()`);
      assert.ok(textBox.height <= textBox.viewport + 1, 'The native hint includes every wrapped line');
      if (!await evaluate(() => document.querySelector('.sidebar-ribbon')?.dataset.expanded === 'true')) assert.equal(wc.isFocused(), focusBeforeHover, 'A native hint does not take page focus');
    }
    const label = `${width}-${zoom}-${theme}`;
    fs.writeFileSync(path.join(output, `hover-windows-${label}.txt`), spawnSync('xwininfo', ['-root', '-tree'], { encoding: 'utf8' }).stdout || '');
    await capture('hover-' + label);
    const screen = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-video_size', '1920x1200', '-i', process.env.DISPLAY, '-frames:v', '1', '-y', path.join(output, 'hover-screen-' + label + '.png')]);
    assert.equal(screen.status, 0, String(screen.stderr));
    const compact = await evaluate(() => document.querySelector('.sidebar-ribbon').getBoundingClientRect().width);
    assert.equal(compact, 52, 'Hover keeps the rail compact');
    assert.deepEqual(view.getBounds(), boundsBeforeHover, 'Hover keeps the native page viewport');
    assert.equal(view.getVisible(), true, 'The page stays live beside the ribbon');
    assert.equal(wc.isFocused(), focusBeforeHover, 'Pointer hover never steals page focus');
    const sample = { width, height, zoom, theme, anchor, hint, visibleDuringHover: view.getVisible(), menu: [] };
    samples.push(sample);

    // The account panel is a second bounded view over the same live page.
    const accountPoint = await evaluate(() => { const r = document.querySelector('.sidebar-account-trigger').getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
    await nativeInput(['mousemove', String(Math.round(b.x + accountPoint.x * zoom)), String(Math.round(b.y + accountPoint.y * zoom)), 'click', '1']);
    const account = win.contentView.children.find(child => child.webContents?.getURL() === 'wuu-chrome://ui/chrome-overlay.html?surface=account');
    await until(() => account?.getVisible(), 'Account panel has painted');
    const accountBefore = await wc.executeJavaScript('window.ticks'); await delay(150);
    sample.account = { bounds: account.getBounds(), beforeTicks: accountBefore, afterTicks: await wc.executeJavaScript('window.ticks'), visible: view.getVisible(),
      authority: await account.webContents.executeJavaScript('({wuu:typeof window.wuu,require:typeof require,bridge:Object.keys(window.wuuChromeOverlay).sort()})') };
    assert.ok(sample.account.visible && sample.account.afterTicks > accountBefore, 'Account menu keeps the webpage animating');
    assert.deepEqual(sample.account.authority, { wuu: 'undefined', require: 'undefined', bridge: ['command','onState','painted','ready'] }, 'Native chrome has only its narrow bridge');
    // Stale messages and commands for another surface must have no effect.
    await account.webContents.executeJavaScript(`(async()=>{const state=await window.wuuChromeOverlay.ready();window.wuuChromeOverlay.command({token:state.token,generation:state.generation-1,command:{type:'account-action',action:'providers'}});window.wuuChromeOverlay.command({token:state.token,generation:state.generation,command:{type:'new-tab'}})})()`);
    await settle();
    assert.equal(view.getVisible(), true, 'Stale and cross-surface commands do not navigate the shell');
    await capture('account-' + label);
    // Test the first click while the account is still open, rather than after
    // explicitly closing it: transparent native views must not eat page input.
    const outsideInput = await wc.executeJavaScript(`(()=>{scrollTo(0,0);const r=document.querySelector('#draft').getBoundingClientRect();return{x:r.right-8,y:r.y+r.height/2}})()`);
    const liveBounds = view.getBounds();
    await nativeInput(['mousemove', String(Math.round(b.x + liveBounds.x + outsideInput.x)), String(Math.round(b.y + liveBounds.y + outsideInput.y)), 'click', '1']);
    assert.equal(await wc.executeJavaScript('document.activeElement.id'), 'draft', 'The first click while account is open reaches the live webpage');
    await until(() => !account.getVisible(), 'Outside click dismisses account');
    await nativeInput(['mousemove', String(Math.round(b.x + (anchor.x + anchor.width / 2) * zoom)), String(Math.round(b.y + (anchor.y + anchor.height / 2) * zoom))]);
    await nativeInput(['mousemove', String(Math.round(b.x + accountPoint.x * zoom)), String(Math.round(b.y + accountPoint.y * zoom)), 'click', '1']);
    await until(() => account.getVisible(), 'Account reopens for keyboard dismissal');
    await nativeInput(['key', 'Escape']);
    await until(() => !account.getVisible(), 'Escape dismisses account');
    await nativeInput(['mousemove', String(b.x + width - 80), String(b.y + 40)]);
    assert.equal(view.getVisible(), true, 'Leaving chrome never hides the webpage');
    assert.deepEqual(view.getBounds(), boundsBeforeHover, 'Leaving the rail keeps native page bounds unchanged');

    // Real pointer input outside the panel reaches the original page without
    // a page-sized transparent overlay eating the first click.
    const draftPoint = await wc.executeJavaScript(`(()=>{scrollTo(0,0);const r=document.querySelector('#draft').getBoundingClientRect();return{x:r.x+Math.min(r.width-4,200),y:r.y+r.height/2}})()`);
    const pb = view.getBounds();
    await nativeInput(['mousemove', String(Math.round(b.x + pb.x + draftPoint.x)), String(Math.round(b.y + pb.y + draftPoint.y)), 'click', '1']);
    assert.equal(await wc.executeJavaScript('document.activeElement.id'), 'draft', 'The first click outside native chrome reaches the webpage');
    for (let cycle = 0; cycle < 3; cycle++) {
      await wc.executeJavaScript(`scrollTo(0,${300 + cycle * 180});document.documentElement.style.background='${['#dc5572', '#45a688', '#6766c1'][cycle]}';void 0;`);
      const before = await wc.executeJavaScript('({ticks:window.ticks,scroll:scrollY})');
      const events = [], original = view.setVisible;
      view.setVisible = function (visible) { events.push(visible); return original.call(this, visible); };
      try {
        const button = await evaluate(() => { const r = document.querySelector('button[aria-label="Browser options"]').getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 }; });
        await nativeInput(['mousemove', String(Math.round(b.x + button.x * zoom)), String(Math.round(b.y + button.y * zoom)), 'click', '1']);
        await settle();
        await until(() => evaluate(() => document.querySelector('button[aria-label="Browser options"]').getAttribute('aria-expanded') === 'true'), 'options opens');
        await delay(150);
        const open = { visible: view.getVisible(), ticks: await wc.executeJavaScript('window.ticks') };
        await capture(`options-${label}-${cycle}`);
        if (cycle === 0) {
          const screen = spawnSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'x11grab', '-video_size', '1920x1200', '-i', process.env.DISPLAY, '-frames:v', '1', '-y', path.join(output, 'options-screen-' + label + '.png')]);
          assert.equal(screen.status, 0, String(screen.stderr));
        }
        await nativeInput(['key', 'Escape']); await settle();
        await until(() => evaluate(() => document.querySelector('button[aria-label="Browser options"]').getAttribute('aria-expanded') === 'false'), 'Escape dismisses options');
        sample.menu.push({ cycle, before, open, events, after: { visible: view.getVisible(), scroll: await wc.executeJavaScript('scrollY') } });
      } finally { view.setVisible = original; }
    }
  }
  report.linkNavigation = [];
  if (process.env.WUU_CHROME_TRACE === '1') await contentTracing.startRecording({ included_categories: ['devtools.timeline', 'blink.user_timing', 'cc', 'viz'] });
  win.webContents.setZoomFactor(1); win.setBounds({ x: 30, y: 30, width: 1280, height: 800 }); await settle();
  for (let cycle = 0; cycle < 6; cycle++) {
    const sameDocument = cycle % 2 === 0;
    const url = sameDocument ? wc.getURL().split('#')[0] + '#route-' + cycle : h.origin + '/navigation-' + cycle;
    await wc.executeJavaScript(`scrollTo(0,0);document.querySelector('#draft').value='navigation-state';document.querySelector('header').insertAdjacentHTML('beforeend', '<a id="navigate" href="${url}">Local link</a>');void 0;`);
    const point = await wc.executeJavaScript(`(()=>{const r=document.querySelector('#navigate').getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
    const outer = win.getContentBounds(), inner = view.getBounds(), events = [], original = view.setVisible;
    view.setVisible = function (visible) { events.push({ visible, at: performance.now(), stack: new Error().stack }); return original.call(this, visible); };
    try {
      await nativeInput(['mousemove', String(Math.round(outer.x + inner.x + point.x)), String(Math.round(outer.y + inner.y + point.y)), 'click', '1']);
      await until(() => wc.getURL() === url && !wc.isLoading(), 'same-tab link navigation'); await settle();
      const input = await wc.executeJavaScript('document.querySelector("#draft").value');
      report.linkNavigation.push({ cycle, sameDocument, url, events, visible: view.getVisible(), contentsID: wc.id, input });
      await capture('link-navigation-' + cycle);
      if (sameDocument) {
        assert.equal(input, 'navigation-state', 'Same-document links retain form state');
        await wc.executeJavaScript("document.querySelector('#navigate').remove();void 0;");
      }
    } finally { view.setVisible = original; }
  }
  if (process.env.WUU_CHROME_TRACE === '1') await contentTracing.stopRecording(path.join(output, 'link-navigation-trace.json'));
  fs.writeFileSync(path.join(output, 'link-navigation.json'), JSON.stringify(report.linkNavigation, null, 2));
  for (const navigation of report.linkNavigation) assert.ok(navigation.visible && !navigation.events.some(event => !event.visible), 'Following a link must not hide the native website');
  fs.writeFileSync(path.join(output, 'browser-chrome.json'), JSON.stringify(samples, null, 2));
  for (const sample of samples) {
    assert.equal(sample.visibleDuringHover, true, 'Hover chrome preserves the live native website');
    for (const menu of sample.menu) {
      assert.ok(menu.open.visible && !menu.events.includes(false), 'Opening options keeps the live webpage visible');
      assert.ok(menu.open.ticks > menu.before.ticks, 'The webpage keeps animating under options');
      assert.equal(menu.after.scroll, menu.before.scroll, 'Options retains the page scroll position');
    }
  }
  if (process.env.WUU_CHROME_REAL_LINK === '1') {
    const events = [], original = view.setVisible;
    view.setVisible = function (visible) { events.push({ visible, at: performance.now() }); return original.call(this, visible); };
    try {
      let loadError;
      void wc.loadURL('https://example.com').catch(error => { loadError = String(error); });
      await until(() => !wc.isLoading() && wc.getURL().startsWith('https://example.com'), 'public website ready', 20000);
      if (loadError) throw new Error(loadError);
      await capture('public-link-before');
      const point = await wc.executeJavaScript(`(async()=>{const link=document.querySelector('a[href*="iana.org"]');link.scrollIntoView({block:'center'});await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));const r=link.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()`);
      events.length = 0;
      const outer = win.getContentBounds(), inner = view.getBounds();
      await nativeInput(['mousemove', String(Math.round(outer.x + inner.x + point.x)), String(Math.round(outer.y + inner.y + point.y)), 'click', '1']);
      await until(() => !wc.isLoading() && new URL(wc.getURL()).hostname.endsWith('iana.org'), 'public native link navigates', 20000);
      await capture('public-link-after');
      report.publicLink = { url: wc.getURL(), visible: view.getVisible(), events };
    } catch (error) { report.publicLink = { error: String(error), url: wc.getURL(), events }; }
    finally { view.setVisible = original; wc.stop(); }
    fs.writeFileSync(path.join(output, 'public-link.json'), JSON.stringify(report.publicLink, null, 2));
  }
  assert.ok(win.getChildWindows().length <= 1, 'Repeated hover reuses a bounded native hint window');
  win.webContents.setZoomFactor(1); await evaluate(() => { document.documentElement.dataset.theme = 'light'; }); await settle();
};
