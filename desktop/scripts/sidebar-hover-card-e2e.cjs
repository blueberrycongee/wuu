// Sidebar hover cards in the real product renderer, driven by native mouse
// input over a synthetic bridge (sidebar-hover-card-e2e-preload.cjs).
// Run after building: electron scripts/sidebar-hover-card-e2e.cjs
// Evidence (screenshots + results.json) is written to out/sidebar-hover-card-e2e-*.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
fs.mkdirSync(path.join(desktop, "out"), { recursive: true });
const output = fs.mkdtempSync(path.join(desktop, "out", "sidebar-hover-card-e2e-"));
app.setPath("userData", path.join(output, "profile"));
process.env.WUU_HOVER_E2E_CWD = path.resolve(desktop, "..");
const results = [];

app.whenReady().then(async () => {
  const timeout = setTimeout(() => { console.error("Sidebar hover card E2E timed out"); app.exit(1); }, 120000);
  const win = new BrowserWindow({
    width: 1200, height: 820, show: false,
    webPreferences: {
      preload: path.join(__dirname, "sidebar-hover-card-e2e-preload.cjs"),
      contextIsolation: true, sandbox: false, backgroundThrottling: false,
    },
  });
  win.webContents.on("console-message", ({ level, message }) => {
    if (level === "error") console.error(`Renderer: ${message}`);
  });
  const evaluate = (fn, value) => win.webContents.executeJavaScript(`(${fn})(${JSON.stringify(value ?? null)})`);
  const waitFor = async (fn, value, ms = 5000) => {
    const deadline = Date.now() + ms;
    while (Date.now() < deadline) {
      const result = await evaluate(fn, value);
      if (result) return result;
      await new Promise((resolve) => setTimeout(resolve, 16));
    }
    throw new Error(`Timed out: ${fn}`);
  };
  const capture = async (name) =>
    fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage()).toPNG());
  const move = (x, y) => win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(x), y: Math.round(y) });
  const cardShown = () => {
    const card = document.querySelector(".sidebar-hover-card");
    return card && getComputedStyle(card).visibility === "visible";
  };
  const rest = async () => {
    // Park the pointer over the conversation pane and let any card close.
    const bounds = win.getContentBounds();
    move(bounds.width - 40, bounds.height / 2);
    await waitFor(() => !document.querySelector(".sidebar-hover-card"));
    await new Promise((resolve) => setTimeout(resolve, 350)); // leave the skip-delay window
  };
  // Resolve an anchor to the viewport point the mouse should rest on.
  const anchorPoint = (selector) => evaluate(async (selector) => {
    const node = document.querySelector(selector);
    if (!node) return null;
    node.scrollIntoView({ block: "nearest" });
    // Let the scroll event dispatch before the pointer arrives; it would
    // otherwise retire the card this case is about to open.
    await new Promise(requestAnimationFrame);
    await new Promise(requestAnimationFrame);
    const rect = node.getBoundingClientRect();
    return { x: rect.left + Math.min(rect.width / 2, 120), y: rect.top + rect.height / 2 };
  }, selector);
  const measure = (selector) => evaluate(async (selector) => {
    await new Promise(requestAnimationFrame);
    const card = document.querySelector(".sidebar-hover-card");
    // Let the entrance run out rather than finish() it: an animation
    // finished on the main thread can still be mid-flight in the next
    // captured frame, which records the card half transparent.
    await Promise.all(card.getAnimations().map((a) => a.finished.catch(() => undefined)));
    await new Promise(requestAnimationFrame);
    const anchor = document.querySelector(selector);
    const rect = card.getBoundingClientRect();
    const row = anchor.getBoundingClientRect();
    const title = card.querySelector(".sidebar-hover-card-title");
    return {
      card: { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, width: rect.width },
      row: { top: row.top, right: row.right, center: row.top + row.height / 2 },
      sidebarRight: anchor.closest(".sidebar").getBoundingClientRect().right,
      titleCenter: title.getBoundingClientRect().top + parseFloat(getComputedStyle(title).lineHeight) / 2,
      viewport: { width: innerWidth, height: innerHeight },
      horizontalOverflow: card.scrollWidth - card.clientWidth,
      titleLines: Math.round(title.getBoundingClientRect().height / parseFloat(getComputedStyle(title).lineHeight)),
      titleClamped: title.scrollHeight > title.clientHeight + 1,
      status: card.querySelector(".sidebar-hover-card-status")?.dataset.tone ?? null,
      statusText: card.querySelector(".sidebar-hover-card-status")?.textContent ?? null,
      statusColor: card.querySelector(".sidebar-hover-card-status") &&
        getComputedStyle(card.querySelector(".sidebar-hover-card-status")).color,
      time: card.querySelector(".sidebar-hover-card-time")?.textContent ?? null,
      facts: [...card.querySelectorAll(".sidebar-hover-card-fact")].map((fact) => ({
        tone: fact.dataset.tone ?? null, text: fact.textContent,
      })),
      pointerEvents: getComputedStyle(card).pointerEvents,
      countSplit: [...card.querySelectorAll(".sidebar-hover-card-count")].some((node) => node.getClientRects().length > 1),
      nativeTitle: anchor.querySelector("[title]")?.closest(".project-row")?.getAttribute("title") ?? null,
    };
  }, selector);
  const assertPlacement = (label, m) => {
    assert.ok(m.card.left >= m.sidebarRight + 8, `${label}: card clears the sidebar edge`);
    assert.ok(m.card.left >= 0 && m.card.right <= m.viewport.width, `${label}: card fits horizontally`);
    assert.ok(m.card.top >= 0 && m.card.bottom <= m.viewport.height, `${label}: card fits vertically`);
    // The title's first line centers on the row unless that would push the
    // card off the bottom.
    const atBottom = Math.abs(m.card.bottom - (m.viewport.height - 8)) <= 1;
    assert.ok(atBottom || Math.abs(m.titleCenter - m.row.center) <= 1, `${label}: card aligns with the row`);
    assert.ok(m.horizontalOverflow <= 0, `${label}: nothing overflows the card`);
    assert.equal(m.pointerEvents, "none", `${label}: card never takes the pointer`);
    assert.equal(m.countSplit, false, `${label}: a count never breaks across lines`);
  };

  try {
    for (const [theme, size, width] of [["light", 14, 1200], ["dark", 14, 1200], ["light", 20, 1200], ["dark", 20, 900]]) {
      const label = `${theme}-${size}-${width}`;
      win.setContentSize(width, 820);
      await win.loadFile(path.join(desktop, "out/renderer/index.html"));
      await evaluate(({ theme, size }) => {
        document.documentElement.dataset.theme = theme;
        document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
        window.dispatchEvent(new Event("wuu-content-size-change"));
      }, { theme, size });
      await waitFor(() => [...document.querySelectorAll(".project-row-name")].some((node) => node.textContent === "wuu"));
      await evaluate(() => {
        const header = [...document.querySelectorAll(".sidebar-section-header-group")]
          .find((node) => node.querySelector(".project-row-name")?.textContent === "wuu");
        const toggle = header.querySelector(".project-row");
        if (toggle.getAttribute("aria-expanded") !== "true") toggle.click();
      });
      await waitFor(() => document.querySelectorAll(".thread-row .thread-row-title").length >= 5);
      await evaluate(async () => {
        while (document.querySelector(".thread-list-more")) {
          document.querySelector(".thread-list-more").click();
          await new Promise(requestAnimationFrame);
        }
      });
      // Tag fixture rows by title so the cases below read by intent.
      const tagged = await evaluate(() => {
        const titles = {
          "Review the changes in PR 499": "hover-running",
          "Release checklist": "hover-project",
          "Older conversation 10": "hover-filler-9",
          "Older conversation 9": "hover-filler-8",
        };
        let count = 0;
        for (const node of document.querySelectorAll(".thread-row")) {
          const text = node.querySelector(".thread-row-title")?.textContent ?? "";
          const id = text.startsWith("Refactor the extension host") ? "hover-failed" : titles[text];
          if (id) { node.dataset.e2eThread = id; count += 1; }
        }
        for (const node of document.querySelectorAll(".sidebar-section-header-group")) {
          const name = node.querySelector(".project-row-name")?.textContent;
          if (name) node.dataset.e2eHeader = name;
        }
        return count;
      });
      assert.equal(tagged, 5, `${label}: fixture rows are listed`);
      const threadRow = (id) => `[data-e2e-thread="${id}"]`;
      const header = (name) => `[data-e2e-header="${name}"]`;
      await evaluate(() => {
        window.__hoverE2EPointerOvers = [];
        for (const type of ["pointerover", "pointerout", "pointerdown", "scroll", "blur"]) {
          document.addEventListener(type, (event) => {
            window.__hoverE2EPointerOvers.push(`${type}:${event.target?.className ?? event.target?.nodeName}`.slice(0, 60));
          }, true);
        }
      });
      await rest();

      // Cold hover waits for intent; the card does not flash on a pass-through.
      const running = await anchorPoint(threadRow("hover-running"));
      move(running.x, running.y);
      const hoverStarted = Date.now();
      await new Promise((resolve) => setTimeout(resolve, 200));
      assert.equal(await evaluate(cardShown), null, `${label}: no card before the hover delay`);
      await waitFor(cardShown);
      const openDelay = Date.now() - hoverStarted;
      const runningCard = await measure(threadRow("hover-running"));
      assertPlacement(`${label} running`, runningCard);
      assert.equal(runningCard.status, "running", `${label}: running row explains its spinner`);
      assert.match(runningCard.statusText, /\d/, `${label}: running status carries its elapsed time`);
      assert.ok(runningCard.time, `${label}: last activity time is shown`);
      assert.ok(runningCard.facts.some((fact) => fact.text === "Codex · gpt-5.1-codex"),
        `${label}: the session's engine and model are listed`);
      assert.ok(runningCard.facts.some((fact) => fact.text === "wuu"), `${label}: the owning workspace is listed`);
      results.push({ label, case: "running", openDelay, ...runningCard });
      await capture(`${label}-running`);

      // Sweeping to a neighbour switches immediately (skip delay).
      const failed = await anchorPoint(threadRow("hover-failed"));
      move(failed.x, failed.y);
      const sweepStarted = Date.now();
      await waitFor(() => document.querySelector(".sidebar-hover-card-status")?.dataset.tone === "failed", null, 2000);
      const sweepDelay = Date.now() - sweepStarted;
      assert.ok(sweepDelay < 250, `${label}: sweeping between rows does not repay the delay (${sweepDelay}ms)`);
      const failedCard = await measure(threadRow("hover-failed"));
      assertPlacement(`${label} failed`, failedCard);
      assert.ok(failedCard.titleLines <= 3, `${label}: long titles clamp at three lines`);
      assert.ok(failedCard.facts.some((fact) => fact.tone === null), `${label}: worktree fact is listed`);
      results.push({ label, case: "failed-worktree-long-title", sweepDelay, ...failedCard });
      await capture(`${label}-failed-long-title`);

      // Project coordinator rows summarize the sessions the sidebar hides.
      const project = await anchorPoint(threadRow("hover-project"));
      move(project.x, project.y);
      await waitFor(() => document.querySelector(".sidebar-hover-card-title")?.textContent === "Release checklist", null, 2000);
      const projectCard = await measure(threadRow("hover-project"));
      assertPlacement(`${label} project`, projectCard);
      results.push({ label, case: "project", ...projectCard });

      // Pressing a row retires the card until the pointer leaves.
      win.webContents.sendInputEvent({ type: "mouseDown", x: Math.round(project.x), y: Math.round(project.y), button: "left", clickCount: 1 });
      await waitFor(() => !document.querySelector(".sidebar-hover-card"), null, 1000);
      win.webContents.sendInputEvent({ type: "mouseUp", x: Math.round(project.x), y: Math.round(project.y), button: "left", clickCount: 1 });
      await new Promise((resolve) => setTimeout(resolve, 700));
      assert.equal(await evaluate(() => document.querySelector(".sidebar-hover-card")), null,
        `${label}: a pressed row stays quiet while the pointer remains`);
      await rest();

      // Workspace headers: counts and folder; the native title hint is gone.
      const wuu = await anchorPoint(header("wuu"));
      move(wuu.x, wuu.y);
      await waitFor(cardShown);
      const workspaceCard = await measure(header("wuu"));
      assertPlacement(`${label} workspace`, workspaceCard);
      assert.equal(workspaceCard.nativeTitle, null, `${label}: no native title competes with the card`);
      assert.equal(workspaceCard.facts.length, 2, `${label}: workspace card lists counts and folder`);
      results.push({ label, case: "workspace", ...workspaceCard });
      await capture(`${label}-workspace`);

      const gone = await anchorPoint(header("old-prototype"));
      move(gone.x, gone.y);
      await waitFor(() => document.querySelector(".sidebar-hover-card-title")?.textContent === "old-prototype", null, 2000);
      const missingCard = await measure(header("old-prototype"));
      assertPlacement(`${label} missing`, missingCard);
      assert.ok(missingCard.facts.some((fact) => fact.tone === "warning"), `${label}: a missing folder is called out`);
      results.push({ label, case: "missing-workspace", ...missingCard });
      await capture(`${label}-missing-workspace`);
      await rest();

      // Scrolling the sidebar retires the card; its anchor geometry moved.
      const filler = await anchorPoint(threadRow("hover-filler-8"));
      move(filler.x, filler.y);
      await waitFor(cardShown);
      const fillerCard = await measure(threadRow("hover-filler-8"));
      assertPlacement(`${label} lower row`, fillerCard);
      results.push({ label, case: "lower-row", ...fillerCard });
      await capture(`${label}-lower-row`);
      const scrollable = await evaluate(() => {
        const list = document.querySelector(".sidebar-main");
        if (list.scrollHeight <= list.clientHeight) return false;
        list.scrollBy(0, list.scrollTop > 0 ? -40 : 40);
        return true;
      });
      if (scrollable) await waitFor(() => !document.querySelector(".sidebar-hover-card"), null, 1000);
      results.push({ label, case: "scroll-dismiss", scrollable });
      await rest();
    }
    console.log(`PASS: ${results.length} sidebar hover card cases; evidence: ${output}`);
  } catch (error) {
    console.error(error);
    console.error("Diagnostics:", await evaluate(() => ({
      pointerOvers: window.__hoverE2EPointerOvers,
      card: document.querySelector(".sidebar-hover-card")?.outerHTML.slice(0, 300) ?? null,
      focused: document.hasFocus(),
    })).catch(() => null));
    process.exitCode = 1;
    await capture("failure").catch(() => undefined);
  } finally {
    fs.writeFileSync(path.join(output, "results.json"), JSON.stringify(results, null, 2));
    clearTimeout(timeout);
    app.exit(process.exitCode || 0);
  }
});
