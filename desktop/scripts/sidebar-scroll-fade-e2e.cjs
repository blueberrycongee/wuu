// Real product renderer and Chromium scroll timelines with synthetic IPC data.
// Run after building: electron scripts/sidebar-scroll-fade-e2e.cjs
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_SCROLL_FADE_OUTPUT || fs.mkdtempSync(path.join(desktop, "out", "sidebar-scroll-fade-e2e-"));
fs.mkdirSync(output, { recursive: true });
const onboarding = process.env.WUU_SCROLL_FADE_ONBOARDING === "1";
app.setPath("userData", path.join(output, "profile"));
process.env.WUU_STREAM_E2E_CWD = path.resolve(desktop, "..");
process.env.WUU_STREAM_E2E_SIDEBAR_THREADS = "48";
const results = [];

app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Scroll fade E2E timed out"); app.exit(1); }, 60000);
  const win = new BrowserWindow({
    width: 1100, height: 820, show: true,
    webPreferences: {
      ...(onboarding ? {} : { preload: path.join(__dirname, "streaming-e2e-preload.cjs") }),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  let focusWindow;
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error") console.error(`Renderer: ${message}`);
  });
  const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value)})`);
  const waitFor = async (fn, value) => {
    const deadline = Date.now() + 5000;
    while (Date.now() < deadline) {
      if (await evaluate(fn, value)) return;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`Timed out: ${fn}`);
  };
  const sample = async (label, selector = ".sidebar-main") => {
    const state = await evaluate(async (selector) => {
      await new Promise(requestAnimationFrame);
      await new Promise(requestAnimationFrame);
      const node = document.querySelector(selector);
      const style = getComputedStyle(node);
      return {
        following: document.documentElement.hasAttribute("data-stream-following"),
        focused: document.hasFocus(),
        scrollTop: node.scrollTop, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight,
        mask: style.maskImage,
        bounds: node.getBoundingClientRect().toJSON(),
        animations: node.getAnimations().map(animation => ({
          name: animation.animationName, playState: animation.playState,
          currentTime: String(animation.currentTime), timelineTime: String(animation.timeline?.currentTime),
          progress: animation.effect.getComputedTiming().progress,
        })),
        top: parseFloat(style.getPropertyValue("--scroll-fade-top")),
        bottom: parseFloat(style.getPropertyValue("--scroll-fade-bottom")),
      };
    }, selector);
    results.push({ label, selector, ...state });
    return state;
  };
  const unchanged = (before, after, label) => {
    for (const key of ["scrollTop", "scrollHeight", "clientHeight", "mask", "top", "bottom"]) {
      assert.equal(after[key], before[key], `${label}: sidebar ${key} stays independent`);
    }
  };
  const emit = (method, params) => win.webContents.send("test:server-event", {
    workdir: process.env.WUU_STREAM_E2E_CWD, kind: "notification", message: { method, params },
  });
  const capture = async (name) => fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  try {
    await win.loadURL("about:blank");
    win.webContents.debugger.attach("1.3");
    const media = (features = [], media = "screen") => win.webContents.debugger.sendCommand("Emulation.setEmulatedMedia", {
      media, features: [{ name: "prefers-reduced-motion", value: "no-preference" }, ...features],
    });
    await media();
    if (onboarding) {
      const body = ".onboarding-stage-plugins .onboarding-body";
      const click = async (selector) => {
        const point = await evaluate((selector) => {
          const node = document.querySelector(selector);
          const box = node.getBoundingClientRect();
          const x = Math.round(box.x + box.width / 2), y = Math.round(box.y + box.height / 2);
          if (!node.contains(document.elementFromPoint(x, y))) throw new Error(`Obscured control: ${selector}`);
          return { x, y };
        }, selector);
        win.webContents.sendInputEvent({ type: "mouseDown", ...point, button: "left", clickCount: 1 });
        win.webContents.sendInputEvent({ type: "mouseUp", ...point, button: "left", clickCount: 1 });
      };
      await win.loadFile(path.join(desktop, "out-dev/onboarding/renderer/dev/onboarding/index.html"), { query: { lang: "en" } });
      await waitFor(() => Boolean(document.querySelector(".onboarding-stage-welcome")));
      await click(".onboarding-actions .settings-button-primary");
      await waitFor(() => document.querySelectorAll(".onboarding-plugin").length === 9);
      await evaluate(async () => {
        await document.fonts.ready;
        await Promise.all(document.querySelector(".onboarding-heading").getAnimations().map(animation => animation.finished));
      });
      for (const theme of ["light", "dark"]) {
        for (const size of [15, 20]) {
          for (const width of [920, 620]) {
            const label = `onboarding-${theme}-${size}-${width}`;
            win.setContentSize(width, 600);
            await waitFor(width => innerWidth === width && innerHeight === 600, width);
            await evaluate(({ theme, size }) => {
              document.documentElement.dataset.theme = theme;
              document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
            }, { theme, size });
            for (const position of ["top", "middle", "bottom"]) {
              await evaluate((position) => {
                const node = document.querySelector(".onboarding-body");
                node.scrollTop = position === "top" ? 0 : position === "bottom" ? node.scrollHeight : (node.scrollHeight - node.clientHeight) / 2;
              }, position);
              const state = await sample(`${label}-${position}`, body);
              assert.ok(state.scrollHeight > state.clientHeight, `${label}: list overflows`);
              assert.ok(position === "top" ? state.top === 0 && state.bottom > 0
                : position === "bottom" ? state.top > 0 && state.bottom === 0
                  : state.top > 0 && state.bottom > 0, `${label}-${position}: fades follow remaining content`);
              const layout = await evaluate(() => {
                const body = document.querySelector(".onboarding-body").getBoundingClientRect();
                const heading = document.querySelector(".onboarding-heading");
                const actions = document.querySelector(".onboarding-actions");
                const mascot = document.querySelector(".onboarding-plugin-mascot-stage");
                return { body: body.toJSON(), heading: heading.getBoundingClientRect().toJSON(), actions: actions.getBoundingClientRect().toJSON(),
                  masks: [heading, actions, mascot].map(node => getComputedStyle(node).maskImage),
                  pageWidth: document.documentElement.scrollWidth, viewportWidth: innerWidth };
              });
              assert.ok(layout.heading.bottom <= layout.body.top && layout.body.bottom <= layout.actions.top, `${label}: fixed regions remain outside list`);
              assert.ok(layout.pageWidth <= layout.viewportWidth, `${label}: no horizontal overflow`);
              assert.deepEqual(layout.masks, ["none", "none", "none"]);
              results.push({ label: `${label}-${position}-layout`, ...layout });
              await capture(`${label}-${position}`);
            }
          }
        }
        win.setContentSize(1100, 1600);
        await waitFor(() => {
          const node = document.querySelector(".onboarding-body");
          const style = getComputedStyle(node);
          return innerWidth === 1100 && innerHeight === 1600 && node.scrollHeight === node.clientHeight
            && parseFloat(style.getPropertyValue("--scroll-fade-top")) === 0
            && parseFloat(style.getPropertyValue("--scroll-fade-bottom")) === 0;
        });
        const fitting = await sample(`onboarding-${theme}-fitting`, body);
        assert.ok(fitting.scrollHeight === fitting.clientHeight && fitting.top === 0 && fitting.bottom === 0, "fitting onboarding has no fades");
        await capture(`onboarding-${theme}-fitting`);
      }
      win.setContentSize(920, 720);
      await waitFor(() => innerWidth === 920 && innerHeight === 720);
      await evaluate(() => { document.documentElement.style.setProperty("--conversation-message-font-size", "15px"); document.querySelector(".onboarding-body").scrollTop = 0; });
      await sample("onboarding-controls", body);
      const firstSwitch = '.onboarding-plugin:first-child [role="switch"]';
      const selected = await evaluate(selector => document.querySelector(selector).getAttribute("aria-checked"), firstSwitch);
      await click(firstSwitch);
      await waitFor(selected => document.querySelector('.onboarding-plugin:first-child [role="switch"]').getAttribute("aria-checked") !== selected, selected);
      assert.notEqual(await evaluate(selector => document.querySelector(selector).getAttribute("aria-checked"), firstSwitch), selected, "switch remains clickable");
      win.focus();
      win.webContents.focus();
      win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Tab" });
      win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Tab" });
      await waitFor(() => document.activeElement === document.querySelector('.onboarding-plugin:nth-child(2) [role="switch"]') && document.activeElement.matches(":focus-visible"));
      const focus = await evaluate(() => ({ role: document.activeElement.getAttribute("role"), visible: document.activeElement.matches(":focus-visible"), outline: getComputedStyle(document.activeElement, "::before").outlineStyle }));
      assert.ok(focus.role === "switch" && focus.visible && focus.outline !== "none", "keyboard switch focus remains visible");
      results.push({ label: "onboarding-keyboard-focus", ...focus });
      await capture("onboarding-keyboard-focus");
      // Exercise the same selection controls that drive the mascot equipment.
      const selectedCapabilities = () => evaluate(() => ({
        selected: [...document.querySelectorAll('.onboarding-plugin')].filter(node => node.querySelector('[role="switch"]').getAttribute('aria-checked') === 'true').map(node => node.dataset.plugin).sort(),
        worn: [...document.querySelectorAll('[data-onboarding-capability]')].map(node => node.dataset.onboardingCapability).sort(),
        split: document.querySelector('[data-testid="onboarding-mascot-stage"]').hasAttribute('data-onboarding-split'),
      }));
      const checkOutfit = async (label, expected) => {
        await waitFor(expected => JSON.stringify([...document.querySelectorAll('.onboarding-plugin')]
          .filter(node => node.querySelector('[role="switch"]').getAttribute('aria-checked') === 'true')
          .map(node => node.dataset.plugin).sort()) === JSON.stringify(expected), [...expected].sort());
        await evaluate(async () => {
          await new Promise(requestAnimationFrame);
          await Promise.all(document.querySelector('.onboarding-plugin-mascot-stage').getAnimations({ subtree: true })
            .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
            .map(animation => animation.finished.catch(() => {})));
        });
        const state = await selectedCapabilities();
        assert.deepEqual(state.worn, state.selected.filter(id => id !== 'subagent'), `${label}: selected plugins match equipment`);
        assert.equal(state.split, state.selected.includes('subagent'), `${label}: Subagent controls companions`);
        results.push({ label, ...state });
      };
      const preset = async (index) => {
        await evaluate(() => { document.querySelector('.onboarding-body').scrollTop = 0; });
        await click(`.onboarding-presets button:nth-child(${index})`);
        await checkOutfit(`preset-${index}`, index === 1 ? [] : index === 2 ? ["side", "automation"] : pluginIDs);
      };
      const pluginIDs = await evaluate(() => [...document.querySelectorAll('.onboarding-plugin')].map(node => node.dataset.plugin));
      win.setContentSize(920, 720);
      await preset(1);
      for (const id of pluginIDs) {
        const selector = `.onboarding-plugin[data-plugin="${id}"] [role="switch"]`;
        await evaluate(selector => document.querySelector(selector).scrollIntoView({ block: 'center' }), selector);
        await click(selector);
        await checkOutfit(`single-${id}`, [id]);
        await capture(`mascot-single-${id}`);
        await click(selector);
        await checkOutfit(`removed-${id}`, []);
        assert.deepEqual((await selectedCapabilities()).selected, []);
      }
      for (const theme of ['light', 'dark']) {
        for (const size of [15, 20]) {
          for (const width of [920, 620]) {
            win.setContentSize(width, 600);
            await waitFor(width => innerWidth === width && innerHeight === 600, width);
            await evaluate(({ theme, size }) => {
              document.documentElement.dataset.theme = theme;
              document.documentElement.style.setProperty('--conversation-message-font-size', `${size}px`);
            }, { theme, size });
            for (const [name, index] of [['minimal', 1], ['recommended', 2], ['all', 3]]) {
              await preset(index);
              await evaluate(async () => {
                await document.fonts.ready;
                await Promise.all(document.querySelector('.onboarding-plugin-mascot-stage').getAnimations({ subtree: true })
                  .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
                  .map(animation => animation.finished.catch(() => {})));
              });
              await capture(`mascot-${theme}-${size}-${width}-${name}`);
            }
          }
        }
      }
      await preset(2);
      await click(".onboarding-actions .settings-button-ghost");
      await waitFor(() => Boolean(document.querySelector(".onboarding-stage-welcome")));
      await click(".onboarding-actions .settings-button-primary");
      await waitFor(() => Boolean(document.querySelector(".onboarding-stage-plugins")));
      await click(".onboarding-actions .settings-button-primary");
      await waitFor(() => Boolean(document.querySelector(".onboarding-stage-runtime")));
      console.log("PASS: onboarding fades, fitting content, fixed regions, switches, keyboard focus, and Back/Continue across light/dark, 15/20px, wide/narrow.");
    } else if (process.env.WUU_SIDEBAR_FULL_CAPTURE === "1") {
      // Linux-hosted preview of macOS chrome, not native Mac acceptance.
      // The fallback platform stamp follows this preview-only user agent.
      win.webContents.setUserAgent(win.webContents.getUserAgent().replace(/\([^)]*\)/, "(Macintosh; Intel Mac OS X 10_15_7)"));
      results.push({ previewChrome: "simulated macOS traffic lights on Linux Electron", position: { x: 18, y: 17 }, diameter: 14 });
      // Full product shell with the existing synthetic IPC bridge; no user data.
      for (const theme of ["light", "dark"]) {
        await win.loadFile(path.join(desktop, "out/renderer/index.html"));
        await waitFor(() => Boolean(document.querySelector(".thread-list-more")));
        await evaluate(() => document.querySelector(".thread-row-main").click());
        await waitFor(() => Boolean(document.querySelector(".thread-row.active")));
        await evaluate(theme => {
          document.documentElement.dataset.theme = theme;
          document.documentElement.style.setProperty("--conversation-message-font-size", "15px");
          window.dispatchEvent(new Event("wuu-content-size-change"));
        }, theme);
        await evaluate(() => {
          // Match MAC_TRAFFIC_LIGHT_POSITION and the 48px titlebar in main/index.ts.
          const lights = document.createElement("div");
          lights.setAttribute("aria-hidden", "true");
          Object.assign(lights.style, { position: "fixed", left: "18px", top: "17px", display: "flex", gap: "6px", zIndex: "9999", pointerEvents: "none" });
          for (const color of ["#ff5f57", "#febc2e", "#28c840"]) {
            const light = document.createElement("span");
            Object.assign(light.style, { width: "14px", height: "14px", borderRadius: "50%", background: color, boxShadow: "inset 0 0 0 0.5px #0002" });
            lights.append(light);
          }
          document.body.append(lights);
        });
        const turn = {
          id: "sidebar-surface-preview", status: "in_progress", started_at: new Date().toISOString(), items_view: "full",
          items: [
            { id: "surface-user", type: "user_message", status: "completed", text: "把侧边栏的选中底色收窄一些，保留文字和图标的对齐。" },
            { id: "surface-answer", type: "agent_message", status: "completed", terminal: true, text: "侧边栏视觉调整\n\n选中底色向内收窄，文字、图标和右侧操作仍然保持对齐。\n\n- 文件夹与会话的层级保持清楚\n- 长标题继续在操作按钮前省略\n- 浅色与深色模式使用相同的间距\n\n这段对话是用于界面验收的示例内容。" },
          ],
        };
        emit("turn/started", { thread_id: "sidebar-fade-0", turn });
        await waitFor(() => document.querySelector(".agent-text")?.textContent.includes("侧边栏视觉调整"));
        emit("turn/completed", { thread_id: "sidebar-fade-0", turn: { ...turn, status: "completed", completed_at: new Date().toISOString() } });
        await waitFor(() => !document.querySelector(".composer-stop-button"));
        const geometry = () => evaluate(async () => {
          await document.fonts.ready;
          await new Promise(requestAnimationFrame);
          await new Promise(requestAnimationFrame);
          await Promise.all(document.getAnimations()
            .filter(animation => animation.timeline instanceof DocumentTimeline && animation.effect?.getComputedTiming().iterations !== Infinity)
            .map(animation => animation.finished.catch(() => {})));
          const row = document.querySelector(".thread-row.active");
          return { row: row.getBoundingClientRect().toJSON(), title: row.querySelector(".thread-row-title").getBoundingClientRect().toJSON() };
        });
        const baselineCSS = await win.webContents.insertCSS(require("../dev/sidebar-accessories/baseline.cjs"));
        const before = await geometry();
        await capture(`full-${theme}-before`);
        await win.webContents.removeInsertedCSS(baselineCSS);
        const after = await geometry();
        await capture(`full-${theme}-after`);
        assert.ok(after.row.width < before.row.width, "selection surface narrows");
        assert.equal(after.title.x, before.title.x, "title axis stays fixed");
        assert.equal(after.title.width, before.title.width, "title space stays fixed");
        const sidebarBounds = await evaluate(() => document.querySelector(".sidebar").getBoundingClientRect().toJSON());
        fs.writeFileSync(path.join(output, `sidebar-${theme}-after.png`), (await win.webContents.capturePage({ x: 0, y: 0, width: Math.ceil(sidebarBounds.right), height: 820 })).toPNG());
        results.push({ label: `full-${theme}`, before, after });

        // Compare the previous corners with the approved production radius.
        // Only the before capture injects CSS into this disposable window.
        const radiusBaseline = {};
        const hoverPoint = await evaluate(() => {
          const bounds = document.querySelector(".thread-row:not(.active)").getBoundingClientRect();
          return { x: Math.round(bounds.x + bounds.width / 2), y: Math.round(bounds.y + bounds.height / 2) };
        });
        for (const radius of [6, null]) {
          const variant = radius === null ? "after" : "before";
          const baselineRadiusCSS = radius === null ? null : await win.webContents.insertCSS(
            `.sidebar-content :is(.nav-item, .project-row, .thread-row) { border-radius: ${radius}px !important; }`,
          );
          for (const state of ["idle", "hover"]) {
            win.webContents.sendInputEvent({ type: "mouseMove", ...(state === "hover" ? hoverPoint : { x: 1000, y: 20 }) });
            await geometry();
            const measurement = await evaluate(() => {
              const row = document.querySelector(".thread-row.active");
              const style = getComputedStyle(row);
              return {
                radius: style.borderRadius,
                layout: [...document.querySelectorAll(".sidebar :is(.nav-item, .project-row, .thread-row, .thread-row-title, .thread-row-action, svg)")]
                  .map(node => ({ tag: node.tagName, class: node.getAttribute("class"), bounds: node.getBoundingClientRect().toJSON() })),
                appearance: { background: style.backgroundColor, shadow: style.boxShadow, color: style.color, fontSize: style.fontSize },
              };
            });
            if (radius !== null) radiusBaseline[state] = measurement;
            else {
              assert.deepEqual(measurement.layout, radiusBaseline[state].layout, `${theme}/${state}: radius preserves all sidebar axes`);
              assert.deepEqual(measurement.appearance, radiusBaseline[state].appearance, `${theme}/${state}: radius preserves tone and type`);
            }
            const label = `radius-${theme}-${variant}-${state}`;
            if (state === "idle") {
              await capture(`${label}-full`);
              fs.writeFileSync(path.join(output, `${label}-sidebar.png`), (await win.webContents.capturePage({ x: 0, y: 0, width: Math.ceil(sidebarBounds.right), height: 820 })).toPNG());
            }
            fs.writeFileSync(path.join(output, `${label}-detail.png`), (await win.webContents.capturePage({ x: 0, y: Math.max(0, Math.floor(after.row.y - 72)), width: Math.ceil(sidebarBounds.right), height: 200 })).toPNG());
            results.push({ label, baselineCSSOnly: radius !== null, ...measurement });
          }
          if (baselineRadiusCSS) await win.webContents.removeInsertedCSS(baselineRadiusCSS);
        }
      }
      console.log("PASS: full production shell before/after with synthetic conversation content.");
    } else {
    const threadID = "sidebar-fade-0";
    const now = new Date().toISOString();
    const turn = {
      id: "fade-turn", status: "in_progress", started_at: now, items_view: "full",
      items: [
        { id: "fade-user", type: "user_message", status: "completed", text: "Inspect scroll boundaries." },
        { id: "fade-reasoning", type: "reasoning", status: "in_progress", text: "Reasoning line.\n\n".repeat(70) },
        { id: "fade-answer", type: "agent_message", status: "in_progress", text: "Streaming answer paragraph.\n\n".repeat(45) },
      ],
    };

    for (const theme of ["light", "dark"]) {
      for (const size of [14, 20]) {
        const label = `${theme}-${size}`;
        win.setContentSize(size === 14 ? 1100 : 940, 820);
        await win.loadFile(path.join(desktop, "out/renderer/index.html"));
        await waitFor(() => Boolean(document.querySelector(".thread-list-more")));
        const fitting = await sample(`${label}-initial-fitting-list`);
        assert.ok(fitting.scrollHeight === fitting.clientHeight && fitting.top === 0 && fitting.bottom === 0, "an initially fitting list has no faded edges");
        await evaluate(async () => {
          while (document.querySelector(".thread-list-more")) {
            document.querySelector(".thread-list-more").click();
            await new Promise(requestAnimationFrame);
          }
        });
        await waitFor(() => document.querySelectorAll(".thread-row").length === 48);
        await evaluate(() => document.querySelector(".thread-row-main").click());
        // Active content appears before the reveal finishes. Wait for the
        // pending row to clear before choosing an independent reading position.
        await waitFor(() => Boolean(document.querySelector(".thread-row.active"))
          && !document.querySelector(".thread-row.pending-switch"));
        app.focus({ steal: true });
        win.focus();
        await waitFor(() => document.hasFocus());
        await evaluate(({ theme, size }) => {
          document.documentElement.dataset.theme = theme;
          document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
          window.dispatchEvent(new Event("wuu-content-size-change"));
        }, { theme, size });
        assert.ok(await evaluate(() => document.querySelector(".sidebar").getBoundingClientRect().right > 200), "sidebar remains visible at the tested width");
        await evaluate(() => { const node = document.querySelector(".sidebar-main"); node.scrollTop = node.scrollHeight / 2; });
        const idle = await sample(`${label}-idle`);
        assert.ok(idle.scrollTop > 0 && idle.top > 0 && idle.bottom > 0 && idle.mask !== "none", "overflow fades both edges");

        await evaluate(() => {
          const sidebar = document.querySelector(".sidebar-main");
          const bounds = sidebar.getBoundingClientRect();
          const row = [...sidebar.querySelectorAll(".thread-row")].find(node => {
            const rect = node.getBoundingClientRect();
            return rect.top > bounds.top + 60 && rect.bottom < bounds.bottom - 60;
          });
          row.querySelector(".thread-row-main").focus({ preventScroll: true });
        });
        unchanged(idle, await sample(`${label}-sidebar-focus`), "sidebar focus");
        await evaluate(() => document.querySelector(".composer textarea").focus({ preventScroll: true }));
        unchanged(idle, await sample(`${label}-composer-focus`), "composer focus");
        if (!focusWindow) {
          focusWindow = new BrowserWindow({ width: 220, height: 120, show: true });
          await focusWindow.loadURL("about:blank");
        }
        focusWindow.show();
        win.blur();
        focusWindow.focus();
        await waitFor(() => !document.hasFocus());
        unchanged(idle, await sample(`${label}-window-blur`), "window blur");
        win.focus();
        await waitFor(() => document.hasFocus());
        focusWindow.hide();
        unchanged(idle, await sample(`${label}-window-focus`), "window focus");

        await capture(`${label}-idle`);
        emit("turn/started", { thread_id: threadID, turn });
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        await waitFor(() => Boolean(document.querySelector(".turn-reasoning-scroll")));
        await evaluate(() => document.querySelector(".turn-reasoning-summary").click());
        await waitFor(() => document.querySelector(".turn-reasoning-scroll").clientHeight > 0);
        const following = await sample(`${label}-following`);
        await capture(`${label}-following`);
        unchanged(idle, following, "stream following");
        const reasoningFollowing = await sample(`${label}-reasoning-following`, ".turn-reasoning-scroll");
        assert.notEqual(reasoningFollowing.mask, "none", "live inspection keeps its scroll fade");
        assert.ok(reasoningFollowing.top > 0 && reasoningFollowing.bottom < 1, "following the live edge leaves only the top fade");
        emit("item/agentMessage/delta", { thread_id: threadID, turn_id: turn.id, item_id: "fade-answer", delta: `${turn.items[2].text}Fresh streamed content.` });
        await waitFor(() => document.querySelector(".agent-text")?.textContent.includes("Fresh streamed content."));
        unchanged(idle, await sample(`${label}-delta`), "streamed content");

        await evaluate(() => document.querySelector('[data-pip-anchor-host="conversation"]').dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true })));
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-paused`), "paused following");
        await evaluate(() => {
          const node = document.querySelector(".turn-reasoning-scroll");
          node.dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true }));
          node.scrollTop = (node.scrollHeight - node.clientHeight) / 2;
        });
        const reasoningPaused = await sample(`${label}-reasoning-paused`, ".turn-reasoning-scroll");
        assert.notEqual(reasoningPaused.mask, "none", "inspection fade remains while reading");
        assert.ok(reasoningPaused.top > 0 && reasoningPaused.bottom > 0, "reading mid-panel keeps both fades");

        await evaluate(() => { document.querySelector('[data-pip-anchor-host="conversation"]').scrollTop = 0; });
        await waitFor(() => Boolean(document.querySelector("button.jump-to-latest-pill")));
        await evaluate(() => document.querySelector("button.jump-to-latest-pill").click());
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        unchanged(idle, await sample(`${label}-resumed`), "resumed following");

        // Retain the streamed snapshot in the IPC fixture so normal resume
        // requests can switch away and back without losing the running turn.
        const snapshot = await evaluate(async () => (await window.wuu.resumeThread("sidebar-fade-0")).thread);
        emit("thread/updated", { thread: { ...snapshot, status: "in_progress", turns: [turn] } });
        // Selecting an offscreen session intentionally reveals its row. Keep
        // both switch targets visible so this checks stream-state isolation.
        await evaluate(() => { document.querySelector(".sidebar-main").scrollTop = 0; });
        const switchBaseline = await sample(`${label}-before-switch`);
        await evaluate(() => document.querySelectorAll(".sidebar-main .thread-row-main")[1].click());
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(switchBaseline, await sample(`${label}-switch-idle`), "switch to idle conversation");
        await evaluate(() => document.querySelector(".sidebar-main .thread-row-main").click());
        await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
        unchanged(switchBaseline, await sample(`${label}-switch-running`), "switch to running conversation");

        emit("turn/completed", { thread_id: threadID, turn: { ...turn, status: "completed", completed_at: now } });
        await waitFor(() => !document.querySelector(".composer-stop-button"));
        await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
        unchanged(switchBaseline, await sample(`${label}-completed`), "turn completion");

        await evaluate(() => { document.querySelector(".sidebar-main").scrollTop = 0; });
        const top = await sample(`${label}-top`);
        assert.ok(top.top === 0 && top.bottom > 0, "top boundary leaves only the bottom fade");
        await evaluate(() => { const node = document.querySelector(".sidebar-main"); node.scrollTop = node.scrollHeight; });
        const bottom = await sample(`${label}-bottom`);
        assert.ok(bottom.top > 0 && bottom.bottom === 0, "bottom boundary leaves only the top fade");
      }
    }

    const unreadTurn = { ...turn, id: "fade-unread-turn", items: turn.items.map(item => ({ ...item, id: `${item.id}-unread` })) };
    for (let index = 1; index < 48; index++) {
      emit("turn/started", { thread_id: `sidebar-fade-${index}`, turn: { id: `background-${index}`, status: "in_progress", started_at: now, items: [] } });
    }
    emit("turn/started", { thread_id: threadID, turn: unreadTurn });
    await waitFor(() => document.documentElement.hasAttribute("data-stream-following"));
    await evaluate(() => document.querySelector(".sidebar-notifications-button").click());
    await waitFor(() => document.querySelectorAll(".sidebar-unread-row").length === 48);
    await evaluate(() => { const node = document.querySelector(".sidebar-unread-view"); node.scrollTop = node.scrollHeight / 2; });
    const unread = await sample("unread-following", ".sidebar-unread-view");
    assert.ok(unread.top > 0 && unread.bottom > 0 && unread.mask !== "none", "unread overflow keeps both fades during streaming");
    await evaluate(() => document.querySelector('[data-pip-anchor-host="conversation"]').dispatchEvent(new WheelEvent("wheel", { deltaY: -200, bubbles: true })));
    await waitFor(() => !document.documentElement.hasAttribute("data-stream-following"));
    unchanged(unread, await sample("unread-paused", ".sidebar-unread-view"), "unread pause");
    await capture("unread-paused");
    await evaluate(() => document.querySelector(".sidebar-notifications-button").click());
    await waitFor(() => Boolean(document.querySelector(".sidebar-main")));

    for (const [name, features] of [
      ["reduced-motion", [{ name: "prefers-reduced-motion", value: "reduce" }]],
      ["forced-colors", [{ name: "forced-colors", value: "active" }]],
      ["print", []],
    ]) {
      await media(features, name === "print" ? "print" : "screen");
      assert.equal((await sample(name)).mask, "none", `${name} remains unmasked`);
    }
    await media();
    await evaluate(() => { document.documentElement.dataset.appearanceMotion = "reduce"; });
    assert.equal((await sample("app-reduced-motion")).mask, "none");
    console.log("PASS: sidebar fades track scroll bounds across stream, control/window focus, light/dark, 14/20px, wide/narrow, and accessibility states.");
    }
  } catch (error) {
    await capture("failure");
    throw error;
  } finally {
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify({ versions: process.versions, results }, null, 2));
    console.log(`Evidence: ${output}`);
    clearTimeout(timeout);
    focusWindow?.destroy();
    win.destroy();
  }
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
