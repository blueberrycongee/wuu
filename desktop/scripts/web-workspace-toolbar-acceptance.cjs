// Toolbar panels must not replace a live website with a stale capture.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
module.exports = async function acceptToolbar(h) {
  const { win, view, output, report, evaluate, until, settle, click, capture, delay, nativeInput } = h;
  const samples = report.toolbar = [];
  const surfaces = root => root.children.flatMap(child => [child, ...surfaces(child)]);
  const popupContents = () => surfaces(win.contentView).find(candidate => candidate.webContents && candidate !== view && candidate.webContents.getURL() === 'about:blank')?.webContents;
  const nativeClick = async (selector, wc = win.webContents) => {
    const rect = await evaluate(s => { const r = document.querySelector(s).getBoundingClientRect(); return {x:r.x+r.width/2,y:r.y+r.height/2}; }, selector, wc);
    const bounds = win.getContentBounds(), zoom = win.webContents.getZoomFactor();
    await nativeInput(['mousemove', String(Math.round(bounds.x+rect.x*zoom)), String(Math.round(bounds.y+rect.y*zoom)), 'click', '1']);
    await settle();
  };
  report.windowRestore = [];
  for (const mode of ['hide', 'minimize']) for (let repeat = 0; repeat < 3; repeat++) {
    const events = [], original = view.setVisible;
    view.setVisible = function(visible) { events.push({visible, at:performance.now()}); return original.call(this, visible); };
    try {
      await view.webContents.executeJavaScript('document.querySelector("#draft").value="restore-state";void 0');
      if (mode === 'hide') win.hide(); else win.minimize();
      await until(() => mode === 'hide' ? !win.isVisible() : win.isMinimized(), 'window backgrounded');
      await delay(150);
      if (mode === 'hide') win.show(); else win.restore();
      await until(() => win.isVisible() && !win.isMinimized(), 'window restored');
      win.focus(); await settle();
      assert.equal(await view.webContents.executeJavaScript('document.querySelector("#draft").value'), 'restore-state');
      report.windowRestore.push({mode,repeat,events,visible:view.getVisible(),contentsID:view.webContents.id});
    } finally { view.setVisible = original; }
  }
  fs.writeFileSync(path.join(output, 'window-restore.json'), JSON.stringify(report.windowRestore, null, 2));
  for (let repeat = 0; repeat < 3; repeat++) {
    for (const [name, trigger, panel] of [
      ['downloads', '[data-web-workspace-downloads-trigger]', '[data-web-workspace-downloads]'],
      ['library', '[data-browser-library-trigger]', '[data-browser-library]'],
    ]) {
      await view.webContents.executeJavaScript('scrollTo(0,320);document.querySelector("#draft").value="retained";void 0');
      const events = [], original = view.setVisible;
      view.setVisible = function(visible) { events.push({ visible, at: performance.now(), stack: new Error().stack }); return original.call(this, visible); };
      try {
        console.log("TOOLBAR", repeat, name, "click");
        await click(trigger);
        console.log("TOOLBAR", repeat, name, "clicked");
        const surfaces = root => root.children.flatMap(child => [child, ...surfaces(child)]);
        let popup;
        await until(async () => {
          popup = surfaces(win.contentView).find(candidate => candidate.webContents && candidate !== view && candidate.webContents.getURL() === 'about:blank');
          return evaluate(selector => { const el = document.querySelector(selector); return !!el && getComputedStyle(el).visibility !== 'hidden'; }, panel, popup?.webContents || win.webContents);
        }, name + ' is open');
        await settle();
        console.log("TOOLBAR", repeat, name, "panel visible");
        const before = await view.webContents.executeJavaScript('window.ticks');
        await delay(250);
        const sample = { repeat, name, visible: view.getVisible(), events, ticks: (await view.webContents.executeJavaScript('window.ticks')) - before };
        samples.push(sample);
        await capture(`${name}-${repeat}`);
        await nativeInput(['key', 'Escape']);
        await until(() => evaluate(selector => document.querySelector(selector).getAttribute('aria-expanded') === 'false', trigger), name + ' closes with Escape');
        await settle();
        sample.state = await view.webContents.executeJavaScript('({draft:document.querySelector("#draft").value,scroll:scrollY})');
      } finally { view.setVisible = original; }
    }
  }
  fs.writeFileSync(path.join(output, 'toolbar.json'), JSON.stringify(samples, null, 2));
  if (process.env.WUU_TOOLBAR_BASELINE === '1') return;
  for (const restore of report.windowRestore) assert.ok(restore.visible && !restore.events.some(event => !event.visible), 'Parent occlusion preserves its selected page');
  for (const sample of samples) {
    assert.ok(sample.visible && !sample.events.some(event => !event.visible), 'Opening ' + sample.name + ' keeps the live page visible');
    assert.ok(sample.ticks > 0, 'Animation continues under ' + sample.name);
    assert.deepEqual(sample.state, { draft: 'retained', scroll: 320 });
  }
  // Native mouse/keyboard hit testing crosses the clipped overlay boundary.
  report.interactions = [];
  for (const [width, theme, size] of [[1440, 'light', 15], [720, 'dark', 20]]) {
    win.setSize(width, 900);
    await evaluate(({theme,size}) => { document.documentElement.dataset.theme=theme; document.documentElement.style.setProperty('--font-ui', size+'px'); }, {theme,size});
    await settle();
    await nativeClick('[data-web-workspace-downloads-trigger]');
    await until(() => evaluate(() => !!document.querySelector('[data-web-workspace-downloads]'), undefined, popupContents()), 'native download panel');
    await capture('downloads-'+theme);
    await nativeClick('[data-web-workspace-downloads] button', popupContents());
    await until(() => evaluate(() => document.querySelector('[data-web-workspace-downloads-trigger]').getAttribute('aria-expanded') === 'false'), 'native close button');
    await nativeClick('[data-browser-library-trigger]');
    await until(() => evaluate(() => document.activeElement?.matches('input[type="search"]'), undefined, popupContents()), 'library search focus');
    await nativeInput(['key','Tab']);
    await until(() => evaluate(() => getComputedStyle(document.activeElement).outlineStyle !== 'none', undefined, popupContents()), 'popup keyboard focus is visible');
    await nativeInput(['key','shift+Tab']);
    await nativeInput(['key','ctrl+a']); await nativeInput(['type', 'animated']);
    await until(() => evaluate(() => document.querySelector('input[type="search"]').value === 'animated' && document.querySelectorAll('[data-library-entry]').length === 1, undefined, popupContents()), 'native library search');
    await nativeClick('[data-library-clear-history]', popupContents());
    await nativeInput(['key','Escape']);
    await until(() => evaluate(() => !document.querySelector('[data-library-confirm-clear]') && !!document.querySelector('[data-browser-library]'), undefined, popupContents()), 'Escape cancels clear without closing library');
    await capture('library-'+theme);
    await nativeInput(['key','Escape']);
    await until(() => evaluate(() => document.querySelector('[data-browser-library-trigger]').getAttribute('aria-expanded') === 'false'), 'Escape closes library');
    await nativeClick('[data-web-workspace-downloads-trigger]');
    const draftBefore = await view.webContents.executeJavaScript('document.querySelector("#draft").value');
    // The left edge of the input remains outside even a large narrow-window panel.
    const pageRect = await evaluate(() => { const r=document.querySelector('#draft').getBoundingClientRect(); return {x:r.x+10,y:r.y+r.height/2}; }, undefined, view.webContents);
    const vb=view.getBounds(), wb=win.getContentBounds();
    await nativeInput(['mousemove', String(Math.round(wb.x+vb.x+pageRect.x)), String(Math.round(wb.y+vb.y+pageRect.y)), 'click','1']);
    await until(() => view.webContents.executeJavaScript('document.activeElement.id === "draft"'), 'outside click focuses website input');
    await nativeInput(['type','x']);
    assert.equal((await view.webContents.executeJavaScript('document.querySelector("#draft").value')).length, draftBefore.length+1);
    await until(() => evaluate(() => document.querySelector('[data-web-workspace-downloads-trigger]').getAttribute('aria-expanded') === 'false'), 'outside click closes download panel');
    assert.equal(view.getVisible(), true);
    report.interactions.push({width,theme,size,passed:true});
  }
  // Address keyboard selection and native pointer selection both retain their URL.
  win.setSize(1440,900); await settle();
  await nativeClick('.web-workspace-address input');
  await nativeInput(['type','Resize']);
  await until(() => evaluate(() => !!document.querySelector('[data-web-workspace-address-suggestions]'), undefined, popupContents()), 'address suggestions');
  await capture('address-suggestions');
  await nativeInput(['key','Down','Escape']);
  await until(() => evaluate(() => document.querySelector('.web-workspace-address input').getAttribute('aria-expanded') === 'false'), 'address Escape');
  await nativeInput(['key','ctrl+a']); await nativeInput(['type','static']);
  await until(() => evaluate(() => !!document.querySelector('[data-web-workspace-address-suggestions]'), undefined, popupContents()), 'address suggestions reopen');
  await nativeClick('[data-address-suggestion-url]', popupContents());
  await until(() => evaluate(() => document.querySelector('.web-workspace-address input').getAttribute('aria-expanded') === 'false'), 'pointer selects suggestion');
  await until(() => view.webContents.getURL().endsWith('/static') && !view.webContents.isLoading(), 'pointer suggestion navigates');
  const toolbarEvents=[], originalVisible=view.setVisible;
  view.setVisible=function(visible){toolbarEvents.push(visible);return originalVisible.call(this,visible)};
  try {
    for(let repeat=0;repeat<3;repeat++) {
      const before=await evaluate(()=>document.querySelector('[data-browser-bookmark-current]').getAttribute('aria-pressed'));
      await nativeClick('[data-browser-bookmark-current]');
      await until(()=>evaluate(value=>document.querySelector('[data-browser-bookmark-current]').getAttribute('aria-pressed')!==value,before),'bookmark toggles');
      await nativeClick('.web-workspace-actions button[aria-haspopup="menu"]');
      await until(()=>evaluate(()=>document.querySelector('.web-workspace-actions button[aria-haspopup="menu"]').getAttribute('aria-expanded')==='true'),'native options open');
      await nativeInput(['key','Escape']);
      await until(()=>evaluate(()=>document.querySelector('.web-workspace-actions button[aria-haspopup="menu"]').getAttribute('aria-expanded')==='false'),'native options close');
    }
    assert.equal(toolbarEvents.includes(false),false,'Bookmark and options retain native page');
  } finally {view.setVisible=originalVisible;}
  await nativeClick('[data-web-workspace-downloads-trigger]');
  await nativeInput(['key','ctrl+l']);
  await until(()=>evaluate(()=>document.activeElement?.matches('.web-workspace-address input') && document.querySelector('[data-web-workspace-downloads-trigger]').getAttribute('aria-expanded')==='false'),'address shortcut from popup');
  await nativeClick('[data-browser-library-trigger]');
  const closingContents = view.webContents;
  await nativeInput(['key','ctrl+w']);
  await until(()=>closingContents.isDestroyed(),'close-tab shortcut from popup');
  assert.equal(win.isDestroyed(),false,'Close-tab must retain the shell window');
  report.interactions.push({bookmarkOptionsRepeats:3,toolbarEvents,addressShortcut:true,closeTabShortcut:true});
  fs.writeFileSync(path.join(output, 'interactions.json'), JSON.stringify(report.interactions,null,2));
};
