// Start the fixture server, then run node_modules/.bin/electron dev/empty-home/capture.cjs.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const output = path.resolve(__dirname, "../../../artifacts/empty-home");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "browser-data"));

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1100, height: 850, show: false, webPreferences: { backgroundThrottling: false } });
  const errors = [];
  win.webContents.on("console-message", (event) => { if (event.level === "error") errors.push(event.message); });
  const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map((arg) => JSON.stringify(arg)).join(",")})`);
  const ready = () => evaluate(() => new Promise((resolve, reject) => {
    let frames = 0;
    const check = () => {
      frames++;
      const card = document.querySelector(".empty-home-overview");
      // Scenes measure settled cells, and idle play arms only after the
      // entrance. Two more frames let it arm and take its first resize
      // callback on the real clock, before the idle checks switch to virtual time.
      const armed = () => requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      if (card && frames > 5) void Promise.allSettled(card.getAnimations({ subtree: true }).map((animation) => animation.finished)).then(armed);
      else if (frames > 120) reject(new Error("Overview did not mount"));
      else requestAnimationFrame(check);
    };
    check();
  }));
  const reports = [];
  for (const width of [1100, 390]) {
    win.setContentSize(width, 850);
    for (const theme of ["light", "dark"]) {
      for (const font of [14, 20]) {
        const query = new URLSearchParams({ theme, font });
        if (font === 20) { query.set("empty", ""); query.set("long", ""); }
        await win.loadURL(`http://127.0.0.1:5188/?${query}`);
        await ready();
        assert.equal(await evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--font-ui").trim()), `${font}px`);
        const original = await evaluate(() => document.querySelector(".empty-home-heatmap").innerHTML);
        for (const kind of ["bounce", "snake", "breakout"]) {
          await evaluate((kind) => {
            window.beforePlay = new Set(document.getAnimations());
            [...document.querySelectorAll("nav button")].find((button) => button.textContent === kind).click();
            window.sceneAnimations = document.getAnimations().filter((animation) => !window.beforePlay.has(animation));
            window.sceneEnd = Math.max(...window.sceneAnimations.map((animation) => animation.effect.getComputedTiming().endTime));
          }, kind);
          assert.equal(await evaluate(() => Boolean(document.querySelector(".empty-home-play"))), true, `Scene did not start: ${width}/${kind}`);
          // Scenes differ in length; sample the middle, the ending, and its last beat.
          for (const share of [0.3, 0.6, 0.9]) {
            const geometry = await evaluate((share) => {
              for (const animation of window.sceneAnimations) {
                animation.pause();
                animation.currentTime = share * window.sceneEnd;
              }
              const ball = document.querySelector(".empty-home-play-ball > span").getBoundingClientRect();
              return { left: ball.left, right: ball.right, top: ball.top, bottom: ball.bottom, viewport: innerWidth, page: document.documentElement.scrollWidth };
            }, share);
            assert.ok(geometry.left >= 0 && geometry.right <= width && geometry.top >= 0 && geometry.bottom <= 850, JSON.stringify({ kind, width, theme, font, share, geometry }));
            assert.ok(geometry.page <= width, "Horizontal overflow");
            if (share > 0.5) {
              await evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
              fs.writeFileSync(path.join(output, `${width}-${theme}-${font}-${kind}-${share * 100}.png`), (await win.webContents.capturePage()).toPNG());
            }
            if (kind === "snake" && share === 0.6) {
              const eaten = await evaluate(() => [...document.querySelectorAll(".empty-home-heatmap i")]
                .filter((cell) => getComputedStyle(cell).opacity === "0").map((cell) => Number(cell.dataset.level)));
              assert.ok(eaten.every((level) => level > 0), "Snake ate empty ground");
              if (query.has("empty")) assert.equal(eaten.length, 0);
              else assert.ok(eaten.length > 0, "Snake did not eat any activity cells");
            }
          }
          await evaluate(async () => {
            for (const animation of window.sceneAnimations) animation.finish();
            await Promise.resolve();
          });
          assert.equal(await evaluate(() => document.querySelector(".empty-home-play")), null);
          assert.equal(await evaluate(() => document.querySelector(".empty-home-heatmap").innerHTML), original, "Scene changed usage markup");
          assert.equal(await evaluate(() => [...document.querySelectorAll(".empty-home-heatmap i")].every((cell) => getComputedStyle(cell).opacity === "1" && getComputedStyle(cell).scale === "none")), true, "Cells did not recover");
          reports.push({ width, theme, font, kind, restored: true });
        }
      }
    }
  }
  // Drive idle timers with Chromium's virtual clock. Compositor animations
  // use a separate timeline in a hidden window, so finish those explicitly.
  win.setContentSize(1100, 850);
  await win.loadURL("http://127.0.0.1:5188/");
  await ready();
  win.webContents.debugger.attach("1.3");
  const advance = (budget) => new Promise((resolve, reject) => {
    const onMessage = (_, method) => {
      if (method !== "Emulation.virtualTimeBudgetExpired") return;
      win.webContents.debugger.removeListener("message", onMessage);
      win.webContents.capturePage().then(resolve, reject);
    };
    win.webContents.debugger.on("message", onMessage);
    win.webContents.debugger.sendCommand("Emulation.setVirtualTimePolicy", { policy: "advance", budget }).catch(reject);
  });
  const currentScene = () => evaluate(() => document.querySelector(".empty-home-play")?.dataset.play ?? null);
  const finishFade = () => evaluate(async () => {
    for (const animation of document.getAnimations()) {
      if (animation.effect?.target?.matches(".empty-home-play")) animation.finish();
    }
    await Promise.resolve();
  });
  await advance(20_100);
  assert.ok(await currentScene(), "Idle did not start a scene");
  for (const event of ["pointermove", "keydown", "wheel", "scroll", "resize"]) {
    await evaluate(() => {
      for (const animation of document.getAnimations()) {
        if (animation.constructor.name === "Animation" && animation.effect?.target?.closest(".empty-home")) {
          animation.pause();
          animation.currentTime = 4_000;
        }
      }
    });
    await evaluate((event) => window.dispatchEvent(new Event(event)), event);
    await advance(200);
    await finishFade();
    assert.equal(await currentScene(), null, `${event} did not dismiss the scene`);
    assert.equal(await evaluate(() => [...document.querySelectorAll(".empty-home-heatmap i")].every((cell) => getComputedStyle(cell).opacity === "1" && getComputedStyle(cell).scale === "none")), true);
    await advance(19_850);
    assert.ok(await currentScene(), `Idle did not recover after ${event}`);
  }
  const rotation = [];
  for (let index = 0; index < 6; index++) {
    rotation.push(await currentScene());
    await evaluate(async () => {
      for (const animation of document.getAnimations()) {
        if (animation.constructor.name === "Animation" && animation.effect?.target?.closest(".empty-home")) animation.finish();
      }
      await Promise.resolve();
    });
    assert.equal(await currentScene(), null, "Scene did not clean up on completion");
    await advance(60_050);
    assert.ok(await currentScene(), "Idle scene did not repeat");
  }
  assert.equal(new Set(rotation).size, 3);
  assert.ok(rotation.every((kind, index) => !index || kind !== rotation[index - 1]), "Consecutive scenes repeated");
  const setReducedMotion = async (value) => {
    await evaluate(() => {
      window.mediaChanged = new Promise((resolve) => {
        matchMedia("(prefers-reduced-motion: reduce)").addEventListener("change", () => resolve(), { once: true });
      });
    });
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", { features: [{ name: "prefers-reduced-motion", value }] });
    await advance(100);
    await evaluate(() => window.mediaChanged);
  };
  await setReducedMotion("reduce");
  await advance(70_000);
  await finishFade();
  assert.equal(await currentScene(), null, "Reduced motion did not suppress idle scenes");
  await setReducedMotion("no-preference");
  await advance(20_100);
  assert.ok(await currentScene(), "Idle did not recover after reduced motion was disabled");
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(output, "checks.json"), JSON.stringify({ reports, rotation, interruption: "passed", reducedMotion: "passed", errors }, null, 2));
  console.log(`Passed ${reports.length} rendered scenes; screenshots: ${output}`);
  app.quit();
}).catch((error) => { console.error(error); app.exit(1); });
