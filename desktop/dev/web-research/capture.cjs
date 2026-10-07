// Manual runtime review of the real turn shell and tool-call source circles.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../../artifacts/web-research-compact");
fs.mkdirSync(path.join(output, "frames"), { recursive: true });
const profile = fs.mkdtempSync(path.join(app.getPath("temp"), "wuu-web-research-"));
app.setPath("userData", profile);

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 900, height: 760,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  // The long-list fixture includes deliberately unavailable domains, exercising
  // letter fallbacks without depending on DNS failures or request timeouts.
  win.webContents.session.webRequest.onBeforeRequest({ urls: ["https://www.google.com/s2/favicons*"] }, (details, callback) => {
    callback({ cancel: new URL(details.url).searchParams.get("domain")?.endsWith("example.com") ?? false });
  });
  win.webContents.debugger.attach("1.3");
  const key = async (name, code) => {
    for (const type of ["keyDown", "keyUp"]) await win.webContents.debugger.sendCommand("Input.dispatchKeyEvent", {
      type, key: name, code: name, windowsVirtualKeyCode: code,
      text: type === "keyDown" && name === "Enter" ? "\r" : undefined,
    });
  };
  const run = code => win.webContents.executeJavaScript(code);
  const frames = (n = 2) => run(`new Promise(resolve => { let n = ${n}; function tick() { if (--n <= 0) resolve(); else requestAnimationFrame(tick); } requestAnimationFrame(tick); })`);
  const until = condition => run(`new Promise((resolve, reject) => { const end = performance.now() + 10000; function tick() { if (${condition}) resolve(); else if (performance.now() > end) reject(new Error('Condition not reached: ' + ${JSON.stringify(condition)})); else requestAnimationFrame(tick); } tick(); })`);
  const ready = async () => {
    await until("document.querySelector('.process-surface')");
    await run(`window.sourceArrivals = 0; document.addEventListener('animationstart', event => {
      if (event.animationName === 'web-source-arrive') window.sourceArrivals++;
    })`);
  };
  const resetArrivals = () => run("window.sourceArrivals = 0");
  const noArrivalReplay = async label => {
    await settle();
    assert.equal(await run("window.sourceArrivals"), 0, label);
  };
  const settle = async () => { await frames(); await run(`Promise.race([
    Promise.allSettled(document.getAnimations().filter(a => a.playState === 'running' && a.effect.getTiming().iterations !== Infinity).map(a => a.finished)),
    new Promise((_, reject) => setTimeout(() => reject(new Error('Motion failed to settle')), 3000))
  ])`); };
  const shot = async name => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  const point = async selector => run(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { x: Math.round(r.x+r.width/2), y: Math.round(r.y+r.height/2) }; })()`);
  const click = async selector => {
    await run(`document.querySelector(${JSON.stringify(selector)}).scrollIntoView({block:'nearest', behavior:'instant'})`);
    await frames();
    const p = await point(selector);
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...p });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...p });
    await frames();
  };
  const measure = () => run(`(() => {
    const region = document.querySelector('.turn-web-research');
    const circles = [...document.querySelectorAll('.web-source-link')].filter(node => !node.closest('[inert]'));
    const bounds = el => { const r = el.getBoundingClientRect(); return { x:r.x,y:r.y,width:r.width,height:r.height }; };
    return { running:region?.getAnimations({subtree:true}).filter(a => a.animationName === 'web-source-arrive' && a.playState === 'running').length ?? 0,
      pageWidth:document.documentElement.scrollWidth, width:innerWidth, circles:circles.map(bounds),
      region:region && bounds(region), count:circles.length, focus:document.activeElement?.className,
      toolOwned:region ? !!region.closest('.process-surface') : null,
      expanded:document.querySelector('.turn-process-toggle')?.getAttribute('aria-expanded'),
      more:document.querySelector('.web-research-more')?.getAttribute('aria-expanded'),
      loaded:circles.filter(node => node.querySelector('img[data-loaded=true]')).length };
  })()`);
  const results = [];
  for (const theme of ["light", "dark"]) for (const size of [14, 20]) for (const width of [900, 390]) {
    const name = `${theme}-${size}-${width}`;
    console.log("Review", name);
    win.setContentSize(width, 800);
    await win.loadURL(`http://127.0.0.1:5218/dev/web-research/?theme=${theme}&size=${size}${width === 390 ? "&long" : ""}`);
    await ready();
    await win.webContents.debugger.sendCommand("Emulation.setFocusEmulationEnabled", { enabled: true });
    assert.equal((await measure()).count, 0);
    await click("#results");
    const entranceCount = await run(`(() => {
      window.sourceEntranceClocks = document.querySelector('.turn-web-research').getAnimations({subtree:true})
        .filter(animation => animation.animationName === 'web-source-arrive')
        .map(animation => ({ animation, delay: animation.effect.getTiming().delay }));
      return window.sourceEntranceClocks.length;
    })()`);
    assert.equal(entranceCount, 3, "the live result batch starts three avatar entrances");
    // Opening a source updates the fixture's opened URL and rerenders its
    // real turn shell while the entrance is still active. Receipt tracking
    // must not change the clocks of avatars already in flight.
    await click(".web-source-link");
    assert.ok(await run(`window.sourceEntranceClocks.every(({ animation, delay }) =>
      animation.effect.getTiming().delay === delay)`), "streaming rerenders preserve entrance delays");
    // A click intentionally suppresses this source's tooltip until leave.
    win.webContents.sendInputEvent({ type: "mouseMove", x: 5, y: 5 });
    await frames();
    await settle();
    const arrival = await measure();
    assert.equal(arrival.count, 3);
    assert.equal(arrival.toolOwned, true);
    assert.ok(arrival.pageWidth <= width, name);
    assert.ok(arrival.region.height <= 36, "three sources stay on one compact row");
    assert.ok(arrival.circles.every(r => r.x >= 0 && r.x+r.width <= width && r.height >= 28 && r.height <= 36), name);
    await run("window.scrollTo({top:0,behavior:'instant'})");
    await frames();
    await shot(`${name}-results`);
    // Full titles/URLs are available without adding text to the message flow.
    win.webContents.sendInputEvent({ type: "mouseMove", ...(await point(".web-source-link")) });
    await until("document.querySelector('[role=tooltip]')?.textContent.includes('developer.mozilla.org')");
    await shot(`${name}-tooltip`);
    win.webContents.sendInputEvent({ type: "mouseMove", x: 5, y: 5 });
    await click("#more-results");
    await settle();
    assert.equal((await measure()).count, 6);
    await click(".web-research-more");
    await settle();
    assert.equal((await measure()).count, 8);
    // The disclosure may be revisited while the search is still running.
    // Already received sources must not masquerade as new results again.
    await click(".web-research-more");
    await settle();
    await resetArrivals();
    await click(".web-research-more");
    await noArrivalReplay("reopening does not replay source arrivals");
    const stableHeader = await run("[...document.querySelectorAll('.web-research-sources > button, .web-research-sources .web-source-circle')].map(el => ({x:el.getBoundingClientRect().x,y:el.getBoundingClientRect().y}))");
    await click(".web-research-more");
    await click(".web-research-more");
    await noArrivalReplay("rapid reversal does not replay arrivals");
    assert.equal((await measure()).count, 8);
    assert.deepEqual(await run("[...document.querySelectorAll('.web-research-sources > button, .web-research-sources .web-source-circle')].map(el => ({x:el.getBoundingClientRect().x,y:el.getBoundingClientRect().y}))"), stableHeader);
    await run("document.querySelectorAll('.web-source-link')[7].focus()");
    await key("Enter", 13);
    await frames();
    assert.equal(await run("document.querySelector('#opened').textContent"), "https://caniuse.com/css-animation");
    await key("Escape", 27);
    await settle();
    const collapsed = await measure();
    assert.equal(collapsed.more, "false");
    assert.equal(collapsed.count, 6);
    assert.equal(collapsed.focus, "web-research-more");
    // The owning process folds when the answer arrives; reopening restores
    // the same links without replaying a historical source arrival.
    await click("#finish");
    await shot(`${name}-handoff`);
    await until("document.querySelector('.turn-answer-body')");
    await until("!document.querySelector('.turn-web-research')");
    await resetArrivals();
    await click(".turn-process-toggle");
    await noArrivalReplay("history does not replay source arrivals");
    const done = await measure();
    assert.equal(done.count, 6);
    assert.equal(done.running, 0);
    await run("window.scrollTo({top:0,behavior:'instant'})");
    await frames();
    await shot(`${name}-done`);
    await click("#many");
    await click(".web-research-more");
    await settle();
    const many = await measure();
    assert.equal(many.count, 20);
    assert.ok(many.pageWidth <= width);
    await run("document.querySelectorAll('.web-source-link')[19].focus()");
    await frames();
    assert.ok(await run("document.activeElement.getBoundingClientRect().bottom <= innerHeight"));
    await shot(`${name}-many`);
    await click("#reset");
    await click("#stop");
    await settle();
    assert.equal((await measure()).count, 0);
    await click("#fail");
    await settle();
    assert.equal((await measure()).count, 0);
    await click("#reset");
    await click("#finish");
    await settle();
    assert.equal((await measure()).count, 0);
    await click("#reset");
    await click("#background");
    await click("#results");
    assert.equal((await measure()).running, 0);
    await click("#background");
    await click("#reset");
    await run("document.documentElement.dataset.appearanceMotion = 'reduce'");
    await click("#results");
    assert.equal((await measure()).running, 0);
    await shot(`${name}-reduced`);
    results.push({ name, arrival, collapsed, done, many });
  }
  win.setContentSize(900, 760);
  await win.loadURL("http://127.0.0.1:5218/dev/web-research/");
  await ready();
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await click("#results");
  assert.equal((await measure()).running, 0);
  await resetArrivals();
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [] });
  await noArrivalReplay("restoring motion does not replay received sources");
  win.webContents.debugger.detach();
  // Real renderer frames with elapsed timestamps preserve the live timing.
  await win.loadURL("http://127.0.0.1:5218/dev/web-research/");
  await ready();
  await run("document.querySelector('.research-preview-controls').style.visibility='hidden'");
  const actions = [
    [1000, "#results"], [2200, "#more-results"], [3100, ".web-research-more"],
    [4200, ".web-research-more"], [5300, "#finish"], [6800, ".turn-process-toggle"],
  ];
  const captured = [];
  const start = performance.now();
  let action = 0;
  let hovered = false;
  let unhovered = false;
  while (performance.now() - start < 9500) {
    const now = performance.now() - start;
    while (action < actions.length && now >= actions[action][0]) {
      await run(`document.querySelector(${JSON.stringify(actions[action][1])}).click()`);
      action++;
    }
    if (now >= 7500 && !hovered) {
      win.webContents.sendInputEvent({ type: "mouseMove", ...(await point(".web-source-link")) });
      hovered = true;
    }
    if (now >= 8800 && !unhovered) {
      win.webContents.sendInputEvent({ type: "mouseMove", x: 5, y: 5 });
      unhovered = true;
    }
    const frame = String(captured.length).padStart(4, "0");
    const image = (await win.webContents.capturePage()).resize({ width: 900 });
    fs.writeFileSync(path.join(output, "frames", `${frame}.png`), image.toPNG());
    captured.push({ file: `frames/${frame}.png`, ms: performance.now() - start });
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  const concat = captured.map((frame, i) => `file '${frame.file}'\nduration ${((captured[i+1]?.ms ?? frame.ms+80)-frame.ms)/1000}\n`).join("");
  fs.writeFileSync(path.join(output, "frames.txt"), concat + `file '${captured.at(-1).file}'\n`);
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ runtime: process.versions, results, capture: captured }, null, 2));
  console.log(JSON.stringify({ output, scenarios: results.length, recordedFrames: captured.length }));
  win.destroy();
  fs.rmSync(profile, { recursive: true, force: true });
  app.quit();
}).catch(error => { console.error(error); app.exit(1); });
