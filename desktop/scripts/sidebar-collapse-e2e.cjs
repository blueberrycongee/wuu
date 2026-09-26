const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = path.join(desktop, "out");
fs.mkdirSync(output, { recursive: true });
const temp = fs.mkdtempSync(path.join(output, "sidebar-collapse-e2e-"));
app.setPath("userData", path.join(temp, "profile"));

app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Sidebar collapse E2E timed out"); app.exit(1); }, 60000);
  let win;
  try {
    const { build } = await import("vite");
    await build({
      configFile: false,
      root: path.join(desktop, "dev/sidebar-collapse"),
      base: "./",
      logLevel: "warn",
      build: { outDir: path.join(temp, "renderer") },
    });
    win = new BrowserWindow({
      width: 1050, height: 850, show: true,
      webPreferences: { backgroundThrottling: false },
    });
    await win.loadURL("about:blank");
    win.webContents.debugger.attach("1.3");
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "no-preference" }],
    });
    const evaluate = (fn) => win.webContents.executeJavaScript(`(${fn})()`);
    const load = async (query) => {
      await win.loadFile(path.join(temp, "renderer/index.html"), { query });
      await evaluate(async () => {
        while (!document.querySelector('[data-testid="toggle"], .project-group')) await new Promise(requestAnimationFrame);
        await document.fonts.ready;
        await new Promise(requestAnimationFrame);
      });
    };
    const results = [];
    for (const theme of ["light", "dark"]) {
      for (const size of ["14", "20"]) {
        win.setContentSize(size === "14" ? 1050 : 640, 850);
        await load({ theme, size, width: size === "14" ? "296" : "240", pinned: size === "20" ? "true" : "false" });
        const result = await evaluate(async () => {
          const frame = () => new Promise(requestAnimationFrame);
          const commit = async () => { await frame(); await frame(); };
          const find = (id) => document.querySelector(`[data-testid="${id}"]`);
          const height = (element) => element?.getBoundingClientRect().height ?? 0;
          const body = () => find("group").querySelector(".thread-list-collapse");
          const child = () => find("project")?.querySelector(".thread-list-collapse");
          const following = () => find("following").getBoundingClientRect().top - find("group").getBoundingClientRect().top;
          const initial = { height: height(body()), child: height(child()), following: following() };
          const animate = async (toggle) => {
            toggle.focus();
            toggle.click();
            await commit();
            if (toggle.getAttribute("aria-expanded") === "false") {
              find("row")?.focus();
              if (document.activeElement !== toggle) throw new Error("Closing rows must not receive focus");
            }
            const element = body();
            const animations = [...element.getAnimations(), ...find("following").getAnimations()];
            const heightAnimation = animations.find(animation => animation.transitionProperty === "height");
            if (!heightAnimation) throw new Error("Fold must animate height in both directions");
            animations.forEach(animation => animation.pause());
            const samples = [0, 0.1, 0.3, 0.6, 0.9].map(progress => {
              for (const animation of animations) animation.currentTime = animation.effect.getComputedTiming().endTime * progress;
              return { progress, height: height(element), child: height(child()), following: following() };
            });
            animations.forEach(animation => animation.finish());
            await commit();
            return { samples, height: height(body()), following: following() };
          };
          const closing = await animate(find("toggle"));
          const released = !find("row");
          const opening = await animate(find("toggle"));

          // Reverse an actual in-flight close without remounting the rows.
          const row = find("row");
          find("toggle").click();
          await commit();
          const closingAnimations = body().getAnimations();
          closingAnimations.forEach(animation => {
            animation.pause();
            animation.currentTime = animation.effect.getComputedTiming().endTime * 0.1;
          });
          const reversalStart = height(body());
          find("toggle").click();
          await commit();
          const reversalHeight = height(body());
          body().getAnimations().forEach(animation => animation.finish());
          await commit();
          const reversal = { start: reversalStart, resumed: reversalHeight, final: height(body()), sameRow: row === find("row") };

          // A parent closing must not fade or translate nested row text, and
          // a late row update must remain included in the intrinsic endpoint.
          await animate(find("toggle"));
          find("toggle").click();
          await commit();
          const beforeGrowth = height(body());
          find("add").click();
          await commit();
          body().getAnimations().forEach(animation => animation.finish());
          await commit();
          const growth = { before: beforeGrowth, final: height(body()), rows: document.querySelectorAll('[data-testid="row"]').length };
          const childFull = height(child());
          find("project").querySelector("button").click();
          await commit();
          const childMotion = child().getAnimations().find(animation => animation.transitionProperty === "height");
          const projectAnimated = Boolean(childMotion);
          child().getAnimations().forEach(animation => animation.finish());
          await commit();
          const projectClosed = height(child());
          find("project").querySelector("button").click();
          await commit();
          child().getAnimations().forEach(animation => animation.finish());
          await commit();
          const projectReopened = height(child());
          find("empty").click();
          await commit();
          const empty = { height: height(body()), rows: document.querySelectorAll('[data-testid="row"]').length };
          return { initial, closing, opening, released, reversal, growth, projectAnimated, childFull, projectClosed, projectReopened, empty };
        });
        assert.ok(result.released, "closed folds release their rows");
        for (const direction of ["closing", "opening"]) {
          const samples = result[direction].samples;
          assert.ok(samples.some(sample => sample.height > 1 && sample.height < result.initial.height - 1), `${direction}: intermediate heights exist`);
          for (let i = 1; i < samples.length; i++) {
            assert.ok(direction === "opening" ? samples[i].height >= samples[i - 1].height : samples[i].height <= samples[i - 1].height, `${direction}: monotonic reveal`);
          }
          for (const sample of samples) {
            assert.ok(Math.abs(sample.child - result.initial.child) < 1, `${direction}: nested rows keep their own size`);
          }
          const last = samples.at(-1);
          assert.ok(Math.abs(last.height - result[direction].height) < 2, `${direction}: no second height jump at completion`);
          assert.ok(Math.abs(last.following - result[direction].following) < 2, `${direction}: no late heading-gap jump`);
        }
        assert.ok(Math.abs(result.opening.height - result.initial.height) < 1, "reopening restores full content height");
        assert.ok(result.reversal.sameRow, "reversing a close preserves row identity");
        assert.ok(result.reversal.resumed >= result.reversal.start && result.reversal.resumed < result.initial.height, "reversal resumes from its current height");
        assert.ok(Math.abs(result.reversal.final - result.initial.height) < 1, "reversal reaches the original endpoint");
        assert.ok(result.growth.final > result.initial.height && result.growth.rows === 16, "new rows are not clipped by a stale measurement");
        assert.ok(result.projectAnimated && result.projectClosed === 0 && Math.abs(result.projectReopened - result.childFull) < 1, "nested projects fold independently");
        assert.ok(result.empty.rows === 0 && result.empty.height < result.initial.height, "empty content does not retain a stale height");
        results.push({ theme, size, ...result });
      }
    }
    await load({ rows: "40" });
    const scrolling = await evaluate(async () => {
      const frame = () => new Promise(requestAnimationFrame);
      const sidebar = document.querySelector(".sidebar");
      const body = document.querySelector(".thread-list-collapse");
      const toggle = document.querySelector('[data-testid="toggle"]');
      const finish = async () => {
        await frame(); await frame();
        body.getAnimations().forEach(animation => animation.finish());
        await frame(); await frame();
      };
      sidebar.scrollTop = sidebar.scrollHeight;
      const scrollable = sidebar.scrollTop > 0;
      toggle.click();
      await finish();
      const collapsedScroll = sidebar.scrollTop;
      toggle.click();
      await finish();
      sidebar.scrollTop = sidebar.scrollHeight;
      const lastRow = document.querySelectorAll('[data-testid="row"]').item(39);
      const row = lastRow.getBoundingClientRect();
      const viewport = sidebar.getBoundingClientRect();
      const lastRowVisible = row.top >= viewport.top && row.bottom <= viewport.bottom;
      toggle.focus();
      toggle.click();
      await finish();
      return { scrollable, collapsedScroll, lastRowVisible };
    });
    assert.deepEqual(scrolling, { scrollable: true, collapsedScroll: 0, lastRowVisible: true });
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
    assert.equal(await evaluate(async () => {
      await new Promise(requestAnimationFrame);
      return document.activeElement?.getAttribute("data-testid");
    }), "add", "keyboard navigation skips collapsed rows and reaches the next control");
    await win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-reduced-motion", value: "reduce" }],
    });
    await load({ theme: "light" });
    const reduced = await evaluate(async () => {
      const toggle = document.querySelector('[data-testid="toggle"]');
      toggle.click();
      await new Promise(requestAnimationFrame);
      await new Promise(requestAnimationFrame);
      const closed = !document.querySelector('[data-testid="row"]');
      toggle.click();
      await new Promise(requestAnimationFrame);
      const body = document.querySelector(".thread-list-collapse");
      return { closed, open: body.getBoundingClientRect().height > 0, animations: body.getAnimations().length };
    });
    assert.deepEqual(reduced, { closed: true, open: true, animations: 0 });
    // Drive the production project list with synthetic session updates. Measure
    // actual row rectangles so fixed-pixel caps or shrinking rows cannot pass.
    const historyResults = [];
    for (const theme of ["light", "dark"]) {
      for (const size of ["14", "20"]) {
        for (const width of ["296", "240"]) {
          win.setContentSize(width === "296" ? 1050 : 640, 850);
          await load({ mode: "history", theme, size, width });
          const history = await evaluate(async () => {
            const commit = async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); };
            const click = async selector => { document.querySelector(selector).click(); await commit(); };
            const scroller = () => document.querySelector(".project-thread-scroll");
            const rows = () => [...scroller().querySelectorAll(".thread-row")];
            const measure = () => {
              const list = scroller();
              const rects = rows().map(row => row.getBoundingClientRect());
              const gap = rects[1].top - rects[0].bottom;
              return {
                count: rects.length, height: list.getBoundingClientRect().height,
                eightRows: rects.slice(0, 8).reduce((sum, rect) => sum + rect.height, 0) + gap * 7,
                rowHeight: rects[0].height, following: document.querySelector('[data-testid="following"]').getBoundingClientRect().top,
                horizontalOverflow: list.scrollWidth > list.clientWidth,
                fitsSidebar: list.getBoundingClientRect().right <= document.querySelector(".sidebar").getBoundingClientRect().right,
              };
            };
            const initial = measure();
            const read = rows().find(row => row.querySelector(".thread-row-fork-icon"));
            read.querySelector(".thread-row-main").click();
            await commit();
            rows().find(row => row.textContent.includes("Conversation 1:")).querySelector(".thread-row-main").click();
            await commit();
            const readRetained = rows().includes(read) && !read.classList.contains("has-unread");
            await click(".thread-list-more");
            const expanded = measure();
            scroller().scrollTop = scroller().scrollHeight;
            await commit();
            const last = rows().at(-1).getBoundingClientRect();
            const viewport = scroller().getBoundingClientRect();
            const oldestVisible = last.top >= viewport.top && last.bottom <= viewport.bottom + 1;
            const footer = document.querySelector(".thread-list-collapse-btn");
            const footerOutside = !scroller().contains(footer) && footer.getBoundingClientRect().top >= viewport.bottom;
            await click(".thread-list-collapse-btn");
            const collapsed = measure();
            await click('[data-testid="grow"]');
            const growing = measure();
            await click('[data-testid="select-old"]');
            const active = scroller().querySelector(".active").getBoundingClientRect();
            const bounds = scroller().getBoundingClientRect();
            const selectedVisible = active.top >= bounds.top && active.bottom <= bounds.bottom + 1;
            // Context menus portal outside the bounded viewport.
            scroller().querySelector(".active").dispatchEvent(new MouseEvent("contextmenu", {
              bubbles: true, cancelable: true, clientX: active.left + 60, clientY: active.top + 15,
            }));
            await commit();
            const menu = document.querySelector(".thread-row-context-menu");
            const menuOutside = Boolean(menu && !scroller().contains(menu) && menu.getBoundingClientRect().height > 0);
            document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
            await commit();
            scroller().scrollTop = 0;
            return { initial, expanded, collapsed, growing, readRetained, oldestVisible, footerOutside, selectedVisible, menuOutside };
          });
          assert.equal(history.initial.count, 16, "five recent, five unread, four running, and two creating rows are candidates");
          assert.equal(history.expanded.count, 32, "one expansion includes every historical and creating row");
          assert.equal(history.collapsed.count, 16, "collapsing restores the recent range plus retained states");
          assert.equal(history.growing.count, 36, "running candidates remain reachable beyond the height cap");
          for (const state of ["initial", "expanded", "collapsed", "growing"]) {
            assert.ok(Math.abs(history[state].height - history[state].eightRows) < 1, `${state}: viewport is exactly eight actual rows`);
            assert.ok(Math.abs(history[state].following - history.initial.following) < 1, `${state}: following workspace does not move`);
            assert.equal(history[state].horizontalOverflow, false, `${state}: long titles fit without horizontal scrolling`);
            assert.equal(history[state].fitsSidebar, true, `${state}: list remains inside the sidebar`);
          }
          for (const check of ["readRetained", "oldestVisible", "footerOutside", "selectedVisible", "menuOutside"]) {
            assert.equal(history[check], true, check);
          }
          fs.writeFileSync(path.join(temp, `history-${theme}-${size}-${width}.png`), (await win.webContents.capturePage()).toPNG());
          historyResults.push({ theme, size, width, ...history });
        }
      }
    }
    await load({ mode: "history", theme: "dark", size: "20", width: "240" });
    const wheelTarget = await evaluate(() => {
      const rect = document.querySelector(".project-thread-scroll").getBoundingClientRect();
      return { x: Math.round(rect.left + rect.width / 2), y: Math.round(rect.top + rect.height / 2) };
    });
    win.focus();
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", ...wheelTarget });
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseWheel", ...wheelTarget, deltaY: 180, deltaX: 0 });
    const interaction = await evaluate(async () => {
      const list = document.querySelector(".project-thread-scroll");
      const waitFor = async (condition, message) => {
        for (let frame = 0; !condition(); frame++) {
          if (frame > 120) throw new Error(message);
          await new Promise(requestAnimationFrame);
        }
      };
      await waitFor(() => list.scrollTop > 0, "Native wheel did not scroll the project list");
      const afterWheel = list.scrollTop;
      const rows = [...list.querySelectorAll('[data-sortable="true"]')];
      const source = rows.at(-1);
      const target = rows[0];
      const dataTransfer = new DataTransfer();
      source.dispatchEvent(new DragEvent("dragstart", { bubbles: true, dataTransfer }));
      await waitFor(() => source.classList.contains("dragging"), "Drag did not start");
      target.dispatchEvent(new DragEvent("dragover", { bubbles: true, cancelable: true, dataTransfer, clientY: target.getBoundingClientRect().top }));
      await waitFor(() => target.dataset.sortIndicator === "before", "Drop target did not accept the row");
      target.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer, clientY: target.getBoundingClientRect().top }));
      source.dispatchEvent(new DragEvent("dragend", { bubbles: true, dataTransfer }));
      await waitFor(() => list.querySelector('[data-sortable="true"]') === source, "Drop did not reorder the row");
      const reordered = list.querySelector('[data-sortable="true"]') === source;
      const lastRow = [...list.querySelectorAll(".thread-row")].at(-1);
      lastRow.querySelector(".thread-row-main").focus();
      await new Promise(requestAnimationFrame);
      lastRow.querySelector(".thread-row-action.archive").focus();
      return { afterWheel, reordered };
    });
    assert.ok(interaction.afterWheel > 0 && interaction.reordered, "native wheel and row reordering work inside the viewport");
    win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
    win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
    const keyboard = await evaluate(async () => {
      await new Promise(requestAnimationFrame);
      const button = document.querySelector(".thread-list-more");
      return { reachedFooter: document.activeElement === button, outline: getComputedStyle(button).outlineWidth };
    });
    assert.ok(keyboard.reachedFooter && parseFloat(keyboard.outline) > 0, `keyboard navigation reaches the fixed footer with a visible focus ring: ${JSON.stringify(keyboard)}`);
    fs.writeFileSync(path.join(temp, "history-keyboard.png"), (await win.webContents.capturePage()).toPNG());
    await load({ mode: "history", busy: "false", count: "3", size: "14" });
    const shortHistory = await evaluate(async () => {
      const commit = async () => { await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); };
      const measure = () => {
        const list = document.querySelector(".project-thread-scroll");
        const rows = [...list.querySelectorAll(".thread-row")].map(row => row.getBoundingClientRect());
        return { height: list.getBoundingClientRect().height, content: rows.at(-1).bottom - rows[0].top };
      };
      const short = measure();
      document.querySelector('[data-testid="pending-only"]').click();
      await commit();
      const list = document.querySelector(".project-thread-scroll");
      const pending = { ...measure(), count: list.querySelectorAll(".thread-row").length };
      document.querySelector('[data-testid="font"]').click();
      await commit();
      const large = measure();
      document.querySelector('[data-testid="clear"]').click();
      await commit();
      return { short, pending, large, empty: !document.querySelector(".project-thread-scroll") };
    });
    assert.ok(Math.abs(shortHistory.short.height - shortHistory.short.content) < 1, "few rows use only their content height");
    assert.ok(shortHistory.pending.count === 12 && shortHistory.pending.height < shortHistory.pending.content, "creating-only lists share the scroll cap");
    assert.ok(shortHistory.large.height > shortHistory.pending.height, "changing the UI font resizes the live cap");
    assert.ok(shortHistory.empty, "empty projects reserve no list height");
    fs.writeFileSync(path.join(temp, "history-results.json"), JSON.stringify({ historyResults, shortHistory, interaction, keyboard }, null, 2));
    console.log("Project history E2E passed: eight-row cap, full history, read transitions, nested scrolling, creating rows, live font resize, menus, and short/empty lists.");
    console.log("Sidebar collapse E2E passed: light/dark, 14/20px, wide/narrow, nested folds, reversal, live/empty rows, scrolling, keyboard focus, reduced motion.");
    fs.writeFileSync(path.join(temp, "results.json"), JSON.stringify(results, null, 2));
    console.log(`Geometry results: ${path.join(temp, "results.json")}`);
    clearTimeout(timeout);
    win.destroy();
    app.exit(0);
  } catch (error) {
    console.error(error);
    win?.destroy();
    app.exit(1);
  }
});
