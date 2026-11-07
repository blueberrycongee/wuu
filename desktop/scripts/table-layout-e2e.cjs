// Real Electron layout capture for message-stream Markdown tables: a seeded
// conversation renders five assistant tables plus one user-bubble table across
// a theme/font/pane-width matrix. No model requests are made. Screenshots and
// a metrics.json land in WUU_TABLE_LAYOUT_OUTPUT (default out/table-layout-e2e).
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktop = path.resolve(__dirname, "..");
const output = process.env.WUU_TABLE_LAYOUT_OUTPUT || path.join(desktop, "out/table-layout-e2e");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "profile-")));
app.on("window-all-closed", () => {});
process.env.WUU_STREAM_E2E_CWD = output;
const viewportSelector = ".conversation-pane > .scroll-region";
const activeSelector = '.cached-conversation-pane[data-active="true"]';
const HEIGHT = 900;
const WIDTHS = [1680, 1280, 960, 720];
const THEMES = ["light", "dark"];
const FONTS = [15, 19];
const metrics = [];
let win;

const userTableText = [
  "Here are the environments I collected — which region should the new service use?",
  "",
  "| Env | Region | Tier |",
  "| --- | --- | --- |",
  "| prod-us | us-east | standard |",
  "| prod-eu | eu-west | standard |",
  "| staging | us-east | free |",
].join("\n");

const answerText = [
  "Here is the consolidated comparison. The five tables below cover the saved settings, the hosting trade-offs, the module metrics, the localization notes, and the wide export.",
  "",
  "| Setting | Value |",
  "| --- | --- |",
  "| Theme | Dark |",
  "| Message font | 15px |",
  "| Sidebar | Docked |",
  "| Locale | English (US) |",
  "",
  "Each row above stays short; this paragraph separates the key/value table from the wider comparison that follows.",
  "",
  "| Plan | Latency | Cost model | Best for |",
  "| --- | --- | --- | --- |",
  "| Single region | Low for nearby users, but degrades across oceans and long-haul routes. | One bill and one failover story to reason about. | Small teams that want the simplest operations story. |",
  "| Multi region active | Consistent reads near each user with replication lag on writes. | Higher steady spend plus duplicated services. | Products with a global audience and strict latency budgets. |",
  "| Region failover | Fast for the primary region; the secondary stays cold until promoted. | Cheaper standby capacity at the cost of slower recovery. | Teams that need resilience without paying for dual writes. |",
  "| Edge cached | Fastest for static content, but cache misses fall back to the origin. | Egress dominates while the origin stays small. | Read-heavy workloads that can tolerate some staleness. |",
  "",
  "The comparison above wraps across several lines per cell, which is where column sizing matters most.",
  "",
  "| Module | Path | Size (KB) | Status | Note | Updated |",
  "| --- | --- | ---: | :---: | --- | --- |",
  "| Table renderer | `src/renderer/RichTable.tsx` | 1,234.5 | stable | wraps long cells | 2026-11-03 |",
  "| Stream parser | `src/renderer/StreamingMarkdown.ts` | 98.0 | beta | tails token batches | 2026-10-28 |",
  "| Session store | `internal/session/store.go` | 12 | stable | sqlite-backed | 2026-09-30 |",
  "| Relay client | `clients/core/src/relay.ts` | 456.75 | deprecated | pending removal | 2026-08-12 |",
  "",
  "Numbers and dates should line up on their own edges when alignment reaches the cells; this paragraph sits between the technical table and the localized one.",
  "",
  "| 功能 | 说明 | 示例 | 备注 |",
  "| --- | --- | --- | --- |",
  "| 表格渲染 | 中文列宽与英文混排时的折行表现 | `npm run build` | 需要检查窄窗口 |",
  "| 内联代码 | 单元格内的代码不应挤压整列宽度 | `rich-table-scroll` | 保持整词 |",
  "| 长文本 | 超过可读宽度的段落要正常换行 | 这是一个比较长的中文句子用来测试折行效果 | 覆盖四种宽度 |",
  "| 对齐 | 表头与单元格在中文语境下的对齐一致性 | 见上表 | 观察差异 |",
  "",
  "The Chinese table exercises CJK wrapping and inline code together; the final table below intentionally overflows the column.",
  "",
  "| Col A | Col B | Col C | Col D | Col E | Col F | Col G | Col H | Col I | Col J |",
  "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
  "| alpha baseline row | second column content | third cell here | fourth column text | fifth cell value | sixth data point | seventh col item | eighth cell text | ninth col entry | tenth column end |",
  "| beta second row | another medium cell | values keep going | more medium text | additional details | further cell data | ongoing content | extra column info | penultimate cell | final cell value |",
  "| gamma third row | medium length text | consistent cells | readable column | steady content | uniform data | similar cell text | matching values | parallel entries | closing cell |",
  "| delta fourth row | last medium row | near the end | final stretch | ending columns | wrapping up text | concluding data | finishing cell | almost done | terminal value |",
  "",
  "That covers the overflow case. This closing paragraph verifies prose returns to the column measure after the wide table.",
].join("\n");

function threads() {
  const now = new Date().toISOString();
  return [{
    id: "tables", preview: "tables", cwd: output, model_provider: "e2e", model: "mock-stream",
    status: "idle", created_at: now, updated_at: now,
    turns: [
      {
        id: "tables-0", status: "completed", items_view: "full", started_at: now, completed_at: now,
        items: [
          { id: "tables-user-0", type: "user_message", status: "completed", text: userTableText },
          { id: "tables-ack-0", type: "agent_message", status: "completed", terminal: true, text: "Got it — the expanded comparison follows in the next answer." },
        ],
      },
      {
        id: "tables-1", status: "completed", items_view: "full", started_at: now, completed_at: now,
        items: [
          { id: "tables-user-1", type: "user_message", status: "completed", text: "Please expand these into the full breakdown." },
          { id: "tables-answer-1", type: "agent_message", status: "completed", terminal: true, text: answerText },
        ],
      },
    ],
  }];
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const evaluate = (fn, ...args) => win.webContents.executeJavaScript(`(${fn})(${args.map(arg => JSON.stringify(arg)).join(",")})`, true);
async function until(fn, ...args) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await evaluate(fn, ...args)) return;
    await sleep(20);
  }
  throw new Error(`Timed out: ${fn}`);
}
const frames = (count = 4) => evaluate(count => new Promise(resolve => {
  const tick = () => --count ? requestAnimationFrame(tick) : resolve();
  requestAnimationFrame(tick);
}), count);
function layoutMotionSettled() {
  return !document.querySelector(".window-resizing, .layout-motion-active, .sidebar-animating, .environment-panel.closing")
    && !document.getAnimations().some(animation => animation instanceof CSSTransition && animation.playState === "running");
}
async function select(id) {
  await evaluate(id => {
    const row = [...document.querySelectorAll(".project-thread-list .thread-row")]
      .find(row => row.querySelector(".thread-row-title").textContent === id);
    if (!row) throw new Error(`Missing conversation ${id}`);
    row.querySelector(".thread-row-main").click();
  }, id);
  await until((selector, id) => document.querySelector(`${selector} [data-turn-id]`)?.dataset.turnId === `${id}-0`, activeSelector, id);
  await frames();
}
async function capture(name, crop) {
  const zoom = win.webContents.getZoomFactor();
  const rect = crop && Object.fromEntries(Object.entries(crop).map(([key, value]) => [key, Math.round(value * zoom)]));
  fs.writeFileSync(path.join(output, `${name}.png`), (await win.webContents.capturePage(rect ?? undefined)).toPNG());
}

// Converge the window width so the conversation pane column itself lands on
// the requested width; the sidebar and pane insets are absorbed automatically.
async function fitPaneWidth(target) {
  for (let attempt = 0; attempt < 8; attempt++) {
    const actual = await evaluate(() => document.querySelector(".conversation-pane").getBoundingClientRect().width);
    if (Math.abs(actual - target) <= 2) return actual;
    const next = Math.round(win.getContentSize()[0] + (target - actual));
    await evaluate(width => {
      window.tableLayoutResizeReady = window.innerWidth === width;
      if (!window.tableLayoutResizeReady) {
        const resized = () => {
          if (window.innerWidth !== width) return;
          window.removeEventListener("resize", resized);
          requestAnimationFrame(() => requestAnimationFrame(() => {
            window.tableLayoutResizeReady = true;
          }));
        };
        window.addEventListener("resize", resized);
      }
    }, next);
    win.setContentSize(next, HEIGHT);
    await until(() => window.tableLayoutResizeReady);
    await until(layoutMotionSettled);
  }
  return evaluate(() => document.querySelector(".conversation-pane").getBoundingClientRect().width);
}

function collectMetrics() {
  const box = element => {
    if (!element) return null;
    const rect = element.getBoundingClientRect();
    return { x: +rect.x.toFixed(2), y: +rect.y.toFixed(2), width: +rect.width.toFixed(2), height: +rect.height.toFixed(2) };
  };
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const conversationPane = document.querySelector(".conversation-pane");
  const viewport = document.querySelector(".conversation-pane > .scroll-region");
  const wraps = [...pane.querySelectorAll(".agent-block .rich-table-wrap")];
  const block = wraps[0]?.closest(".agent-block") ?? null;
  const describe = wrap => {
    const scroll = wrap.querySelector(".rich-table-scroll");
    if (scroll) scroll.scrollLeft = 0;
    const table = scroll?.querySelector("table") ?? null;
    const firstTd = table?.querySelector("td") ?? null;
    const style = firstTd ? getComputedStyle(firstTd) : null;
    const cells = table ? [...table.querySelectorAll("tbody td")] : [];
    const cellLines = cells.map(cell => {
      const cellStyle = getComputedStyle(cell);
      const padding = parseFloat(cellStyle.paddingTop) + parseFloat(cellStyle.paddingBottom);
      const lineHeight = parseFloat(cellStyle.lineHeight) || 1;
      return Math.max(1, Math.round((cell.clientHeight - padding) / lineHeight));
    });
    const headers = table ? [...table.querySelectorAll("th")] : [];
    const rows = table ? [...table.querySelectorAll("tr")] : [];
    return {
      wrap: box(wrap),
      scrollClientWidth: scroll?.clientWidth ?? null,
      scrollWidth: scroll?.scrollWidth ?? null,
      table: box(table),
      tdFontSize: style?.fontSize ?? null,
      tdLineHeight: style?.lineHeight ?? null,
      thTextAlign: headers.map(th => getComputedStyle(th).textAlign),
      thStyleAttr: headers.map(th => th.getAttribute("style")),
      firstCell: box(headers[0] ?? null),
      alignRows: rows.map(row => [...row.querySelectorAll("th, td")].map(cell => getComputedStyle(cell).textAlign)),
      cellLines,
      maxCellLines: cellLines.length ? Math.max(...cellLines) : 0,
    };
  };
  const userWrap = pane.querySelector(".user-message .rich-table-wrap");
  const rootStyle = getComputedStyle(document.documentElement);
  const shell = document.querySelector(".app-shell");
  // The wrap's flex-column parent is the reading column; a breaking-out
  // wrap is centered on it, so its content box is the outset's origin.
  const columnParent = wraps[0]?.parentElement ?? null;
  const parentStyle = columnParent ? getComputedStyle(columnParent) : null;
  const parentRect = columnParent ? columnParent.getBoundingClientRect() : null;
  const paddingX = parentStyle ? parseFloat(parentStyle.paddingLeft) + parseFloat(parentStyle.paddingRight) : 0;
  // Custom properties serialize unresolved (var()/calc()); measure the token
  // through a probe element instead.
  const probe = document.createElement("div");
  probe.style.cssText = "position:absolute;width:var(--section-gap);visibility:hidden";
  document.body.appendChild(probe);
  const sectionGap = probe.offsetWidth;
  probe.remove();
  return {
    innerWidth: window.innerWidth,
    innerHeight: window.innerHeight,
    devicePixelRatio: window.devicePixelRatio,
    theme: document.documentElement.dataset.theme ?? null,
    fontUI: rootStyle.getPropertyValue("--font-ui").trim(),
    messageFontSize: rootStyle.getPropertyValue("--conversation-message-font-size").trim(),
    sidebarCollapsed: Boolean(shell?.classList.contains("sidebar-collapsed")),
    pane: box(conversationPane),
    scrollRegion: viewport ? {
      rect: box(viewport),
      clientLeft: viewport.clientLeft,
      clientWidth: viewport.clientWidth,
      scrollWidth: viewport.scrollWidth,
      clientHeight: viewport.clientHeight,
      scrollHeight: viewport.scrollHeight,
      scrollTop: viewport.scrollTop,
    } : null,
    agentBlock: box(block),
    firstParagraph: box(block?.querySelector(".rich-paragraph")),
    railRect: box(document.querySelector(".workspace-tool-rail") ?? document.querySelector(".environment-side-stack")),
    floats: [...document.querySelectorAll(".conversation-pane *")].flatMap(el => {
      if (el.closest(".rich-table-wrap")) return [];
      const position = getComputedStyle(el).position;
      if (position !== "absolute" && position !== "fixed") return [];
      const rect = el.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) return [];
      const columnRect = columnParent?.getBoundingClientRect();
      // Only margin-anchored controls constrain the outset; overlays that sit
      // on the column (jump-to-latest, message actions) are unaffected.
      if (!columnRect || (rect.x < columnRect.right - 1 && rect.x + rect.width > columnRect.x + 1)) return [];
      return [{ cls: String(el.className).slice(0, 60), x: rect.x, y: rect.y, width: rect.width, height: rect.height }];
    }),
    column: parentRect ? { x: parentRect.x + (parentStyle ? parseFloat(parentStyle.paddingLeft) : 0), width: columnParent.clientWidth - paddingX } : null,
    sectionGap,
    tableCount: wraps.length,
    tables: wraps.map((wrap, index) => ({ id: `T${index + 1}`, ...describe(wrap) })),
    userTable: userWrap ? { id: "U1", bubble: box(userWrap.closest(".user-message")), ...describe(userWrap) } : null,
  };
}

// Element crops keep the wrap plus ~40px of prose above and below, clamped to
// the visible conversation viewport.
function tableCrop(index, user) {
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const wrap = user
    ? pane.querySelector(".user-message .rich-table-wrap")
    : pane.querySelectorAll(".agent-block .rich-table-wrap")[index];
  wrap.scrollIntoView({ block: "center" });
  const viewport = document.querySelector(".conversation-pane > .scroll-region").getBoundingClientRect();
  const rect = wrap.getBoundingClientRect();
  const x = Math.max(viewport.left, rect.left - 24);
  const right = Math.min(viewport.right, rect.right + 24);
  const y = Math.max(viewport.top, rect.top - 40);
  const bottom = Math.min(viewport.bottom, rect.bottom + 40);
  return { x, y, width: right - x, height: bottom - y };
}

// The full-viewport shot anchors the comparison table (T2) near the top so the
// technical table (T3) and the prose edges stay in frame.
function scrollToComparison() {
  const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
  const wrap = pane.querySelectorAll(".agent-block .rich-table-wrap")[1];
  wrap.scrollIntoView({ block: "start" });
  const viewport = document.querySelector(".conversation-pane > .scroll-region");
  viewport.scrollTop -= 72;
}

function assertCombo(name, width, sample) {
  const { column, scrollRegion, tables, userTable, floats } = sample;
  assert.ok(column, `${name}: column geometry missing`);
  assert.ok(scrollRegion.scrollWidth <= scrollRegion.clientWidth + 1,
    `${name}: conversation scrolls horizontally (${scrollRegion.scrollWidth} > ${scrollRegion.clientWidth})`);
  const colRight = column.x + column.width;
  // The wrap must lie inside the scroll region's visible padding box —
  // content painted past it (a leftward outset is not scrollable) is
  // clipped even though the pane itself still has room.
  const boxLeft = scrollRegion.rect.x + scrollRegion.clientLeft;
  const boxRight = boxLeft + scrollRegion.clientWidth;
  const rail = sample.railRect;
  for (const table of tables) {
    assert.ok(table.wrap.width >= column.width - 1,
      `${name} ${table.id}: wrap ${table.wrap.width} narrower than column ${column.width}`);
    assert.ok(table.wrap.width <= column.width + 240 + 1,
      `${name} ${table.id}: wrap ${table.wrap.width} exceeds the 120px-per-side cap`);
    const outsetLeft = column.x - table.wrap.x;
    const outsetRight = table.wrap.x + table.wrap.width - colRight;
    assert.ok(Math.abs(outsetLeft - outsetRight) <= 1.5,
      `${name} ${table.id}: asymmetric outset ${outsetLeft}px left vs ${outsetRight}px right`);
    assert.ok(table.wrap.x >= boxLeft - 1.5,
      `${name} ${table.id}: left edge ${table.wrap.x} clipped by scroll region (visible box left ${boxLeft})`);
    assert.ok(table.wrap.x + table.wrap.width <= boxRight + 1.5,
      `${name} ${table.id}: right edge ${table.wrap.x + table.wrap.width} clipped by scroll region (visible box right ${boxRight})`);
    if (rail) {
      assert.ok(table.wrap.x + table.wrap.width <= rail.x - 1 || table.wrap.x >= rail.x + rail.width + 1,
        `${name} ${table.id}: wrap overlaps the right rail (${JSON.stringify(rail)})`);
    }
    for (const f of floats ?? []) {
      const wrapRight = table.wrap.x + table.wrap.width;
      const wrapBottom = table.wrap.y + table.wrap.height;
      const overlaps = wrapRight > f.x + 1 && table.wrap.x < f.x + f.width - 1
        && wrapBottom > f.y + 1 && table.wrap.y < f.y + f.height - 1;
      assert.ok(!overlaps,
        `${name} ${table.id}: wrap overlaps floating control ${f.cls} (${JSON.stringify(f)})`);
    }
    // First header cell must be fully inside the visible box so its text
    // is never hard-clipped mid-glyph.
    if (table.firstCell) {
      assert.ok(table.firstCell.x >= boxLeft - 1.5,
        `${name} ${table.id}: first header cell clipped at the left edge`);
    }
  }
  assert.ok(Math.abs(tables[0].wrap.width - column.width) <= 1,
    `${name} T1: a short table must still fill the column (${tables[0].wrap.width} vs ${column.width})`);
  if (width === 1680) {
    assert.ok(tables[2].wrap.width > column.width + 1, `${name} T3: technical table must break out at pane 1680`);
    assert.ok(tables[4].wrap.width > column.width + 1, `${name} T5: wide table must break out at pane 1680`);
    assert.ok(tables[4].scrollWidth > tables[4].scrollClientWidth,
      `${name} T5: wrap must still scroll horizontally after the cap`);
  }
  assert.ok(userTable.wrap.width <= userTable.bubble.width + 1,
    `${name} U1: bubble table ${userTable.wrap.width} exceeds its bubble ${userTable.bubble.width}`);
  // T3: `---:` column right, `:---:` center, everything else left.
  for (const [rowIndex, row] of tables[2].alignRows.entries()) {
    for (const [cellIndex, align] of row.entries()) {
      const expected = cellIndex === 2 ? "right" : cellIndex === 3 ? "center" : "left";
      assert.ok(align === expected || (expected === "left" && align === "start"),
        `${name} T3 r${rowIndex}c${cellIndex}: text-align "${align}", expected "${expected}"`);
    }
  }
}

// While window-resizing is set, rendered turns fall back to
// content-visibility:auto and their paint containment would clip the table
// outset. overflow-clip-margin must keep it painted — check a strip inside
// the left outset that crosses the T3 header hairline.
async function resizePaintCheck(name) {
  const info = await evaluate(() => {
    const pane = document.querySelector('.cached-conversation-pane[data-active="true"]');
    const wrap = pane.querySelectorAll(".agent-block .rich-table-wrap")[2];
    wrap.scrollIntoView({ block: "center" });
    const wrapRect = wrap.getBoundingClientRect();
    const theadRect = wrap.querySelector("thead").getBoundingClientRect();
    const turnRect = wrap.closest(".turn").getBoundingClientRect();
    return { wrapX: wrapRect.x, theadBottom: theadRect.bottom, turnLeft: turnRect.x };
  });
  assert.ok(info.wrapX < info.turnLeft - 1,
    `${name}: T3 outset must reach outside the turn box (wrap ${info.wrapX} vs turn ${info.turnLeft})`);
  const strip = { x: Math.round(info.wrapX + 4), y: Math.round(info.theadBottom - 4), width: 30, height: 7 };
  const colors = async tag => {
    const image = await win.webContents.capturePage(strip);
    fs.writeFileSync(path.join(output, `${name}-strip${tag}.png`), image.toPNG());
    const bitmap = image.toBitmap();
    return new Set([...Array(Math.floor(bitmap.length / 4))].map((_, i) => bitmap.readUInt32LE(i * 4))).size;
  };
  // scrollIntoView only forces layout; a content-visibility:auto turn that
  // was skipped paints on the next frames. Sample after it renders.
  await frames(3);
  const before = await colors("-control");
  assert.ok(before >= 2, `${name}: control sample must show painted outset content (${before} colors)`);
  await evaluate(() => document.documentElement.classList.add("window-resizing"));
  await frames(4);
  const during = await colors("-resize");
  const crop = await evaluate(tableCrop, 2, false);
  await capture(`${name}-resize-paint`, crop);
  await evaluate(() => document.documentElement.classList.remove("window-resizing"));
  await frames(4);
  assert.ok(during >= 2,
    `${name}: window-resizing clipped the T3 outset despite overflow-clip-margin (${during} colors vs ${before} control)`);
}

app.whenReady().then(async () => {
  const fixture = path.join(output, "threads.json");
  fs.writeFileSync(fixture, JSON.stringify(threads()));
  process.env.WUU_TABLE_LAYOUT_FIXTURE = fixture;
  win = new BrowserWindow({ width: 1500, height: HEIGHT, show: process.env.WUU_E2E_HIDDEN !== "true", enableLargerThanScreen: true, webPreferences: {
    preload: path.join(__dirname, "table-layout-e2e-preload.cjs"), contextIsolation: true, sandbox: false, backgroundThrottling: false,
  } });
  await win.loadFile(path.join(desktop, "out/renderer/index.html"));
  await until(() => Boolean(document.querySelector(".project-row")));
  await evaluate(() => {
    const workspace = document.querySelector(".project-row");
    if (workspace.getAttribute("aria-expanded") !== "true") workspace.click();
  });
  await until(() => document.querySelectorAll(".project-thread-list .thread-row").length >= 1);
  await select("tables");
  await until(selector => document.querySelectorAll(`${selector} .agent-block .rich-table-wrap`).length === 5
    && Boolean(document.querySelector(`${selector} .user-message .rich-table-wrap`)), activeSelector);
  await until(layoutMotionSettled);
  await evaluate(() => document.fonts.ready.then(() => undefined));
  for (const width of WIDTHS) {
    const paneWidth = await fitPaneWidth(width);
    for (const font of FONTS) {
      for (const theme of THEMES) {
        await evaluate((theme, font) => {
          document.documentElement.dataset.theme = theme;
          document.documentElement.style.setProperty("--font-ui", `${font}px`);
          document.documentElement.style.setProperty("--conversation-message-font-size", `${font}px`);
          window.dispatchEvent(new Event("wuu-content-size-change"));
        }, theme, font);
        await frames(6);
        await evaluate(() => document.fonts.ready.then(() => undefined));
        // Collect geometry at a deterministic scroll anchor; element rects are
        // viewport-relative, so the baseline must not depend on the previous
        // combination's scroll position.
        await evaluate(() => { document.querySelector(".conversation-pane > .scroll-region").scrollTop = 0; });
        await frames(2);
        const name = `${theme}-${font}-${width}`;
        const sample = await evaluate(collectMetrics);
        await evaluate(scrollToComparison);
        await frames(4);
        await capture(`${name}-full`);
        for (let index = 0; index < sample.tableCount; index++) {
          const crop = await evaluate(tableCrop, index, false);
          await frames(2);
          await capture(`${name}-T${index + 1}`, crop);
        }
        const userCrop = await evaluate(tableCrop, 0, true);
        await frames(2);
        await capture(`${name}-U1`, userCrop);
        if (!process.env.WUU_TABLE_LAYOUT_SKIP_ASSERT && theme === "light" && font === 15 && width === 1680) {
          await resizePaintCheck(name);
        }
        // The environment panel floats in the breakout margin; with it
        // visible every answer table stays at column width.
        await evaluate(() => document.querySelector(".conversation-pane").classList.add("environment-panel-visible"));
        await frames(4);
        const envWidths = await evaluate(() =>
          [...document.querySelectorAll('.cached-conversation-pane[data-active="true"] .agent-block .rich-table-wrap')]
            .map(wrap => wrap.getBoundingClientRect().width));
        await evaluate(() => document.querySelector(".conversation-pane").classList.remove("environment-panel-visible"));
        await frames(2);
        envWidths.forEach((envWidth, index) => assert.ok(process.env.WUU_TABLE_LAYOUT_SKIP_ASSERT || Math.abs(envWidth - sample.column.width) <= 1,
          `${name} T${index + 1}: environment panel must keep the column width (${envWidth} vs ${sample.column.width})`));
        metrics.push({ name, theme, font, targetWidth: width, paneWidth, ...sample });
        fs.writeFileSync(path.join(output, "metrics.json"), JSON.stringify(metrics, null, 2));
        if (!process.env.WUU_TABLE_LAYOUT_SKIP_ASSERT) assertCombo(name, width, sample);
        console.log(`${name}: pane=${paneWidth.toFixed(0)}px tables=${sample.tableCount}`);
      }
    }
  }
  win.destroy();
  app.exit(0);
}).catch(async error => {
  console.error(error);
  if (win && !win.isDestroyed()) await capture("failure");
  fs.writeFileSync(path.join(output, "failure.txt"), String(error.stack));
  app.exit(1);
});
