// Manual Electron rendering review, not a stylesheet-source or image merge gate.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../../artifacts/plan-motion");
fs.mkdirSync(output, { recursive: true });
const profile = fs.mkdtempSync(path.join(app.getPath("temp"), "wuu-plan-motion-"));
app.setPath("userData", profile);

const measure = () => {
  const capsule = document.querySelector(".conversation-status-todo-trigger");
  const card = document.querySelector(".conversation-status-todo-card");
  const arc = document.querySelector(".todo-progress-value");
  const running = document.querySelector("li.is-in_progress .todo-step-ring");
  const rect = node => node ? { x: node.getBoundingClientRect().x, y: node.getBoundingClientRect().y,
    width: node.getBoundingClientRect().width, height: node.getBoundingClientRect().height } : null;
  return {
    capsule: rect(capsule), card: rect(card), label: capsule?.getAttribute("aria-label"),
    offset: arc && getComputedStyle(arc).strokeDashoffset,
    cardVisible: card && getComputedStyle(card).visibility,
    overflow: card && card.scrollHeight > card.clientHeight,
    running: running && { name: getComputedStyle(running).animationName, state: getComputedStyle(running).animationPlayState },
    completed: [...document.querySelectorAll("li.is-completed .todo-step-check")].map(node => getComputedStyle(node).strokeDashoffset),
    focus: document.activeElement?.className,
    labelClipped: capsule && capsule.querySelector('.conversation-status-label').scrollWidth > capsule.querySelector('.conversation-status-label').clientWidth,
    width: innerWidth, scrollWidth: document.documentElement.scrollWidth,
    moving: document.getAnimations().filter(animation => animation.playState === "running").length,
  };
};

app.whenReady().then(async () => {
  const win = new BrowserWindow({ show: false, width: 900, height: 780,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", event => { if (event.level === "error") errors.push(event.message); });
  const run = source => win.webContents.executeJavaScript(source);
  const frames = count => run(`new Promise(resolve => { let n = ${count}; function frame() { if (--n <= 0) resolve(); else requestAnimationFrame(frame); } requestAnimationFrame(frame); })`);
  const settle = () => run(`Promise.allSettled(document.getAnimations().filter(a => a.effect.getTiming().iterations !== Infinity).map(a => a.finished))`);
  const shot = async name => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  const click = async selector => {
    const point = await run(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { x: Math.round(r.x+r.width/2), y: Math.round(r.y+r.height/2) }; })()`);
    win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...point });
    win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...point });
    await frames(2);
  };
  const results = {};
  for (const theme of ["light", "dark"]) for (const size of [14, 20]) for (const width of [900, 390]) {
    const name = `${theme}-${size}-${width}`;
    win.setContentSize(width, 800);
    await win.loadURL(`http://127.0.0.1:5216/dev/plan-motion/?theme=${theme}&size=${size}${width === 390 ? "&long" : ""}`);
    await run(`new Promise((resolve, reject) => { const deadline = performance.now() + 10000; function ready() { if (document.querySelector('.conversation-status-todo-trigger')) resolve(); else if (performance.now() > deadline) reject(new Error('Preview did not mount')); else requestAnimationFrame(ready); } ready(); })`);
    await frames(2);
    const closed = await run(`(${measure})()`);
    assert.equal(closed.running.state, "paused", name);
    // The TODO capsule is first in the tab order; exercise real keyboard input.
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
    await frames(2);
    await settle();
    const before = await run(`(${measure})()`);
    assert.equal(before.cardVisible, "visible", name);
    assert.equal(before.running.name, "wuu-spin", name);
    assert.equal(before.running.state, "running", name);
    assert.ok(before.focus.includes("todo-trigger"), name);
    assert.ok(before.card.x >= 0 && before.card.x + before.card.width <= width, name);
    assert.ok(before.scrollWidth <= width, name);
    assert.equal(before.labelClipped, false, name);
    await shot(`${name}-open`);
    if (width === 390) {
      assert.equal(before.overflow, true, name);
      win.webContents.sendInputEvent({ type: "mouseWheel", x: Math.round(before.card.x + before.card.width / 2),
        y: Math.round(before.card.y + before.card.height / 2), deltaY: -240, deltaX: 0 });
      await frames(6);
      assert.ok(await run("document.querySelector('.conversation-status-todo-card').scrollTop > 0"), name);
      await shot(`${name}-scrolled`);
      await run("document.querySelector('.conversation-status-todo-card').scrollTop = 0");
    }
    // Keep focus in the production card while changing its data from the fixture.
    await run("document.querySelector('#advance').click()");
    await frames(2);
    await settle();
    const after = await run(`(${measure})()`);
    assert.deepEqual(after.capsule, before.capsule, name);
    assert.ok(parseFloat(after.offset) < parseFloat(before.offset), name);
    assert.ok(after.completed.every(offset => parseFloat(offset) === 0), name);
    await shot(`${name}-advanced`);
    // Reverse during the transition; the final state must settle to real data.
    await run("document.querySelector('#advance').click()");
    await frames(2);
    await run("document.querySelector('#reverse').click()");
    await frames(2);
    await settle();
    const reversed = await run(`(${measure})()`);
    assert.equal(reversed.offset, after.offset, name);
    // Reduced motion must stop an already-running marker as well as transitions.
    await run("document.documentElement.dataset.appearanceMotion = 'reduce'");
    await frames(2);
    await run("document.querySelector('#reverse').click()");
    await frames(2);
    const reduced = await run(`(${measure})()`);
    assert.equal(reduced.running.name, "none", name);
    assert.equal(reduced.moving, 0, name);
    await shot(`${name}-reduced`);
    // Real pointer input exercises empty and fully completed plans.
    await click("#empty");
    assert.equal(await run("Boolean(document.querySelector('.conversation-status-todo'))"), false);
    await click("#reset");
    await run("for (let i = 0; i < 16; i++) document.querySelector('#advance').click()");
    await frames(2);
    assert.equal(await run("Boolean(document.querySelector('.conversation-status-todo'))"), false);
    results[name] = { closed, before, after, reversed, reduced };
    console.log(`Verified ${name}`);
  }
  // Keep the plan usable alongside the other host controls at a narrow width.
  await win.loadURL("http://127.0.0.1:5216/dev/plan-motion/?size=20&crowded");
  await frames(4);
  await run("document.querySelector('.conversation-status-todo-trigger').focus()");
  await frames(2);
  await settle();
  results.crowded = await run(`(${measure})()`);
  assert.ok(results.crowded.card.x >= 0 && results.crowded.card.x + results.crowded.card.width <= 390);
  assert.ok(results.crowded.scrollWidth <= 390);
  await shot("crowded-20-390");
  await run("document.activeElement.blur()");
  const capsule = results.crowded.capsule;
  const pointerX = Math.round(capsule.x + capsule.width / 2);
  win.webContents.sendInputEvent({ type: "mouseMove", x: pointerX, y: Math.round(capsule.y + capsule.height / 2) });
  await frames(2);
  await settle();
  win.webContents.sendInputEvent({ type: "mouseMove", x: pointerX, y: Math.round(capsule.y - 4) });
  await frames(2);
  await settle();
  assert.equal((await run(`(${measure})()`)).cardVisible, "visible");
  win.webContents.sendInputEvent({ type: "mouseMove", x: pointerX, y: Math.round(capsule.y - 20) });
  await frames(2);
  assert.equal((await run(`(${measure})()`)).cardVisible, "visible");
  win.webContents.sendInputEvent({ type: "mouseMove", x: 0, y: 0 });
  await frames(2);
  await settle();
  assert.equal((await run(`(${measure})()`)).running.state, "paused");
  // Check the OS preference independently of the in-app setting.
  await win.loadURL("http://127.0.0.1:5216/dev/plan-motion/");
  await frames(4);
  win.webContents.debugger.attach("1.3");
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  await run("document.querySelector('.conversation-status-todo-trigger').focus(); document.querySelector('#advance').click()");
  await frames(3);
  results.osReduced = await run(`(${measure})()`);
  assert.equal(results.osReduced.running.name, "none");
  assert.equal(results.osReduced.moving, 0);
  await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [] });
  win.webContents.debugger.detach();
  // Record the actual renderer at native size. No offline animation recreation.
  win.setContentSize(760, 760);
  await win.loadURL("http://127.0.0.1:5216/dev/plan-motion/");
  await frames(4);
  await run("document.querySelector('.conversation-status-todo-trigger').focus()");
  await frames(2);
  await settle();
  fs.mkdirSync(path.join(output, "frames"), { recursive: true });
  for (let index = 0; index < 90; index++) {
    if (index === 20 || index === 50) await run("document.querySelector('#advance').click()");
    if (index === 75) await run("document.querySelector('#reverse').click()");
    await frames(2);
    fs.writeFileSync(path.join(output, "frames", `${String(index).padStart(3, "0")}.png`), (await win.webContents.capturePage()).toPNG());
  }
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ runtime: process.versions, results, errors }, null, 2));
  win.destroy();
  console.log(`Evidence: ${output}`);
}).catch(error => { console.error(error); process.exitCode = 1; }).finally(() => {
  app.quit();
  fs.rmSync(profile, { recursive: true, force: true });
});
