const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");
const desktop = path.resolve(__dirname, "..");
fs.mkdirSync(path.join(desktop, "out"), { recursive: true });
const temp = fs.mkdtempSync(path.join(desktop, "out/sidebar-status-e2e-"));
app.setPath("userData", path.join(temp, "profile"));
app.whenReady().then(async () => {
  const timeout = setTimeout(() => app.exit(1), 120000);
  const results = [];
  try {
    const { build } = await import("vite");
    await build({ configFile: false, root: path.join(desktop, "dev/sidebar-collapse"),
      base: "./", logLevel: "warn", build: { outDir: path.join(temp, "renderer") } });
    const win = new BrowserWindow({ width: 900, height: 850, show: false });
    for (const theme of ["light", "dark"]) {
      for (const size of ["14", "20"]) {
        for (const switching of ["thread-1", "thread-5"]) {
          await win.loadFile(path.join(temp, "renderer/index.html"), { query: {
            mode: "history", count: "6", busy: "false", theme, size,
            width: size === "14" ? "296" : "240", switching,
          } });
          const result = await win.webContents.executeJavaScript(`(async () => {
            while (!document.querySelector('.thread-row.pending-switch')) await new Promise(requestAnimationFrame);
            await document.fonts.ready;
            const settle = async () => {
              await new Promise(requestAnimationFrame);
              await new Promise(requestAnimationFrame);
              document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).forEach(a => a.finish());
            };
            await settle();
            const row = document.querySelector('.thread-row.pending-switch');
            const title = row.querySelector('.thread-row-title');
            const dot = getComputedStyle(row, '::before');
            const bounds = row.getBoundingClientRect();
            const dotLeft = bounds.right - parseFloat(dot.right) - parseFloat(dot.width);
            const titleRight = () => title.getBoundingClientRect().right - parseFloat(getComputedStyle(title).paddingRight);
            const forkBounds = row.querySelector('.thread-row-fork-icon')?.getBoundingClientRect();
            const passive = { titleRight: titleRight(), dotLeft, color: dot.backgroundColor,
              opacity: dot.opacity, forkLeft: forkBounds?.left, forkRight: forkBounds?.right };
            row.querySelector('.thread-row-main').focus();
            await settle();
            const focused = { titleRight: titleRight(), actionsLeft: row.querySelector('.thread-row-actions').getBoundingClientRect().left,
              opacity: getComputedStyle(row, '::before').opacity };
            return { passive, focused };
          })()`);
          results.push({ theme, size, switching, ...result });
          fs.writeFileSync(path.join(temp, `${theme}-${size}-${switching}.png`), (await win.webContents.capturePage()).toPNG());
          await win.webContents.executeJavaScript(`(async () => {
            document.activeElement.blur();
            await new Promise(requestAnimationFrame);
            await new Promise(requestAnimationFrame);
            document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).forEach(a => a.finish());
          })()`);
          fs.writeFileSync(path.join(temp, `${theme}-${size}-${switching}-passive.png`), (await win.webContents.capturePage()).toPNG());
          const { passive, focused } = result;
          assert.equal(passive.opacity, "1");
          assert.ok(passive.titleRight + 2 <= (passive.forkLeft ?? passive.dotLeft),
            `Title overlaps switching indicator: ${JSON.stringify(results.at(-1))}`);
          if (passive.forkRight !== undefined) assert.ok(passive.forkRight + 2 <= passive.dotLeft, "Fork overlaps switching indicator");
          assert.equal(focused.opacity, "0", "Keyboard actions must hide the switching dot");
          assert.ok(focused.titleRight + 2 <= focused.actionsLeft, "Title overlaps keyboard actions");
        }
      }
    }
    // Row ages sit between the title and every accessory combination, and give
    // their place to the actions on keyboard focus.
    await build({ configFile: false, root: path.join(desktop, "dev/sidebar-accessories"),
      base: "./", logLevel: "warn", build: { outDir: path.join(temp, "accessories") } });
    for (const theme of ["light", "dark"]) {
      for (const size of ["14", "20"]) {
        await win.loadFile(path.join(temp, "accessories/index.html"), { query: { theme, size } });
        const rows = await win.webContents.executeJavaScript(`(async () => {
          while (!document.querySelector('.thread-row-age')) await new Promise(requestAnimationFrame);
          await document.fonts.ready;
          const settle = async () => {
            await new Promise(requestAnimationFrame);
            await new Promise(requestAnimationFrame);
            document.getAnimations().filter(a => a.effect.getComputedTiming().iterations !== Infinity).forEach(a => a.finish());
          };
          await settle();
          const rows = [];
          for (const row of document.querySelectorAll('.thread-row')) {
            const age = row.querySelector('.thread-row-age');
            const title = row.querySelector('.thread-row-title').getBoundingClientRect();
            const dot = getComputedStyle(row, '::before');
            const bounds = row.getBoundingClientRect();
            const limits = [bounds.right];
            if (row.matches('.running, .has-unread, .pending-switch')) limits.push(bounds.right - parseFloat(dot.right) - parseFloat(dot.width));
            const fork = row.querySelector('.thread-row-fork-icon');
            if (fork) limits.push(fork.getBoundingClientRect().left);
            const ageBounds = age?.getBoundingClientRect();
            row.querySelector('.thread-row-main').focus();
            await settle();
            rows.push({ title: row.querySelector('.thread-row-title').textContent, running: row.classList.contains('running'),
              titleRight: title.right, ageLeft: ageBounds?.left, ageRight: ageBounds?.right, limit: Math.min(...limits),
              focusedAgeDisplay: age && getComputedStyle(age).display });
            document.activeElement.blur();
          }
          await settle();
          return rows;
        })()`);
        results.push({ theme, size, fixture: "accessories", rows });
        fs.writeFileSync(path.join(temp, `accessories-${theme}-${size}.png`), (await win.webContents.capturePage()).toPNG());
        for (const row of rows) {
          if (row.running) {
            assert.equal(row.ageRight, undefined, `Running row shows an age: ${JSON.stringify(row)}`);
            continue;
          }
          assert.ok(row.ageRight > row.ageLeft, `Missing row age: ${JSON.stringify(row)}`);
          assert.ok(row.titleRight <= row.ageLeft, `Title overlaps row age: ${JSON.stringify(row)}`);
          assert.ok(row.ageRight + 2 <= row.limit, `Row age overlaps an accessory: ${JSON.stringify(row)}`);
          assert.equal(row.focusedAgeDisplay, "none", `Keyboard focus must hand the age's place to the actions: ${JSON.stringify(row)}`);
        }
      }
    }
    console.log(`PASS: ${results.length} sidebar status cases; evidence: ${temp}`);
    win.destroy();
  } catch (error) { console.error(error); process.exitCode = 1; }
  finally {
    fs.writeFileSync(path.join(temp, "results.json"), JSON.stringify(results, null, 2));
    clearTimeout(timeout);
    app.exit(process.exitCode || 0);
  }
});
